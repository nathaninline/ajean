package ajean

import (
	"fmt"
	"strings"
)

// backend_external.go — modèles « externes » : au lieu d'un serveur local, un
// tel modèle route le chat vers une API OpenAI-compatible distante
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
	External bool   // true = API distante (pas un modèle de cette machine)
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

// usesRemoteEndpoint : la config pointe vers une API (service ou distante)
// plutôt que vers l'ancien moteur local (0.18 et avant).
func usesRemoteEndpoint(cfg map[string]string) bool { return isExternalConfig(cfg) }

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
// externe si le modèle du chat en est une, sinon le modèle local.
func resolveChatEndpoint() chatEndpoint {
	cfg := ReadConfig()
	if isExternalConfig(cfg) {
		return chatEndpoint{
			URL:      completionsURL(cfg[extKeyURL]),
			Model:    strings.TrimSpace(cfg[extKeyModel]),
			Key:      strings.TrimSpace(cfg[extKeyToken]),
			External: cfg[cfgKeyServiceLocal] != "1",
		}
	}
	return chatEndpoint{
		URL:      fmt.Sprintf("http://localhost:%d/v1/chat/completions", LLMPort()),
		Model:    "ajean",
		Key:      readAPIKey(),
		External: false,
	}
}
