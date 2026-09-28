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

// externalPresetWithMachine ajoute au corps d'un preset externe les réglages de
// MACHINE (BIN, HOST, PORT), que porte un preset normal (newPresetSeed) mais pas
// un preset externe. Sans eux, basculer vers un preset distant écrasait la config
// vive — applyPresetFile remplace TOUT, et preservedKeys ne contient ni BIN ni
// PORT. Sur une installation neuve, où presets/ est créé vide (sys_datadir.go),
// plus rien sur le disque ne contenait BIN : la seule issue était de réinstaller
// llama.cpp.
//
// Le détour par parseEnv/formatEnv évite le doublon quand la clé est déjà
// présente (ré-édition d'un preset existant). Une valeur vide n'écrase rien.
func externalPresetWithMachine(content string, machine map[string]string) string {
	if len(machine) == 0 {
		return content
	}
	m := parseEnv(content)
	for k, v := range machine {
		if s := strings.TrimSpace(v); s != "" {
			m[k] = s
		}
	}
	return formatEnv(m)
}

// externalMachineKeys renvoie les réglages de machine à reprendre dans un preset
// externe. À la CRÉATION (id vide) : ceux de la config vive, comme le formulaire
// de preset normal. À l'ÉDITION : ceux que le preset porte DÉJÀ, complétés par la
// config vive — sinon changer l'URL d'un preset existant effacerait le BIN qu'il
// vient d'apprendre à conserver.
func externalMachineKeys(id string) map[string]string {
	m := newPresetSeed()
	if id == "" {
		return m
	}
	old, err := ReadPreset(id)
	if err != nil {
		return m
	}
	have := parseEnv(old)
	for _, k := range newPresetSeedKeys {
		if v := strings.TrimSpace(have[k]); v != "" {
			m[k] = v
		}
	}
	return m
}
