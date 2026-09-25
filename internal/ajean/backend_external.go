package ajean

import (
	"fmt"
	"strings"
)

// backend_external.go — presets « externes » : au lieu de lancer un llama-server
// local, un tel preset route le chat vers une API OpenAI-compatible distante
// (OpenAI, Groq, OpenRouter, un autre serveur…). Un preset externe se reconnaît
// à sa clé EXTERNAL=1 dans la config ; il porte l'URL, le modèle et la clé.
//
// Choix d'archi : l'externe est un PRESET comme un autre (fichier .env dans
// presetsDir), pour réutiliser toute la mécanique existante — liste, ordre,
// bascule, prompt système par preset. La seule différence vit au moment de
// l'inférence (resolveChatEndpoint) et de la bascule (pas de redémarrage moteur).

const (
	extKeyFlag   = "EXTERNAL"        // marqueur : "1" = preset externe
	extKeyURL    = "EXTERNAL_URL"    // base ou URL complète des complétions
	extKeyModel  = "EXTERNAL_MODEL"  // nom du modèle envoyé dans le payload
	extKeyToken  = "EXTERNAL_KEY"    // clé API (Bearer), peut être vide
	extKeyVision = "EXTERNAL_VISION" // "1" = le modèle distant accepte les images
)

// chatEndpoint : où partent les appels /v1/chat/completions de ce tour.
type chatEndpoint struct {
	URL      string // URL complète des complétions
	Model    string // nom de modèle envoyé dans le payload
	Key      string // Bearer ("" = aucun)
	External bool   // true = API distante (pas le llama-server local)
	Cloud    bool   // true = GPU cloud Modal (attente du réveil sur 503)
}

// isExternalConfig indique si une configuration décrit un endpoint externe.
func isExternalConfig(cfg map[string]string) bool {
	return strings.TrimSpace(cfg[extKeyFlag]) == "1"
}

// externalActive : le preset actif est-il un endpoint externe ? Relu en direct
// (ReadConfig passe par le cache), donc sensible à une bascule de preset sans
// redémarrage.
func externalActive() bool { return usesRemoteEndpoint(ReadConfig()) }

// externalVisionActive : le preset externe actif déclare-t-il accepter les
// images (EXTERNAL_VISION=1) ? C'est ce qui remplace la détection du projecteur
// MMPROJ (absent en externe) pour visionEnabled : sans ça, un modèle distant
// multimodal se voyait refuser les images et l'outil see_image.
func externalVisionActive() bool {
	cfg := ReadConfig()
	return isExternalConfig(cfg) && strings.TrimSpace(cfg[extKeyVision]) == "1"
}

// completionsURL normalise l'URL saisie par l'utilisateur en une URL de
// complétions complète. Accepte :
//   - une URL déjà complète (…/chat/completions) — laissée telle quelle ;
//   - une base OpenAI (…/v1, …/openai/v1, …/api/v1) — on ajoute /chat/completions ;
//   - autre chose — on suppose une racine et on ajoute /v1/chat/completions.
func completionsURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if u == "" {
		return ""
	}
	if strings.HasSuffix(u, "/chat/completions") {
		return u
	}
	if strings.HasSuffix(u, "/v1") {
		return u + "/chat/completions"
	}
	return u + "/v1/chat/completions"
}

// resolveChatEndpoint décide où envoyer les complétions pour ce tour : l'API
// externe si le preset actif en est un, sinon le llama-server local.
func resolveChatEndpoint() chatEndpoint {
	cfg := ReadConfig()
	if isCloudConfig(cfg) {
		return cloudEndpoint(cfg)
	}
	if isExternalConfig(cfg) {
		return chatEndpoint{
			URL:      completionsURL(cfg[extKeyURL]),
			Model:    strings.TrimSpace(cfg[extKeyModel]),
			Key:      strings.TrimSpace(cfg[extKeyToken]),
			External: true,
		}
	}
	return chatEndpoint{
		URL:      fmt.Sprintf("http://localhost:%d/v1/chat/completions", LLMPort()),
		Model:    "ajean",
		Key:      readAPIKey(),
		External: false,
	}
}

// externalPresetContent construit le corps .env d'un preset externe (hors ligne
// # NAME=, ajoutée par SavePreset). Une clé vide n'est pas écrite. `ctx` (taille
// de contexte) est stocké dans la clé CTX standard — elle pilote la jauge de
// contexte et le seuil de compaction, comme pour un preset local.
func externalPresetContent(url, model, key, ctx string, vision bool) string {
	m := map[string]string{
		extKeyFlag:  "1",
		extKeyURL:   strings.TrimSpace(url),
		extKeyModel: strings.TrimSpace(model),
	}
	if k := strings.TrimSpace(key); k != "" {
		m[extKeyToken] = k
	}
	if c := strings.TrimSpace(ctx); c != "" {
		m["CTX"] = c
	}
	if vision {
		m[extKeyVision] = "1"
	}
	return formatEnv(m)
}
