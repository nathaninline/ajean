// web_sysprompt.go — prompt système personnalisé de l'utilisateur, persisté
// CÔTÉ SERVEUR (en base) et partagé entre appareils, comme la conversation
// elle-même. Historique : avant la conversation serveur (v0.4.x),
// l'UI envoyait son prompt système dans chaque requête /api/chat ; depuis,
// /api/chat/send ne porte que le message → le champ de l'UI n'avait plus aucun
// effet. Il est maintenant lu ici par la génération (chat_conversation.go), et
// InjectSkills (llm_client.go) le fusionne avec le préambule agent.
package ajean

import (
	"encoding/json"
	"net/http"
	"strings"
)

// readSysPrompt : le prompt système global, posé par /api/sysprompt (aucun réglage
// dans l'interface). Jusqu'à la 0.19 chaque preset pouvait porter le sien ; ce
// réglage est retiré : un projet a sa description, et Jean apprend seul.
func readSysPrompt() string {
	return getStr(bkState, "sysprompt")
}

// activePresetID renvoie l'id du preset actif ("" si aucun / indéterminé).
func activePresetID() string {
	list, err := ListPresets()
	if err != nil {
		return ""
	}
	for _, p := range list {
		if p.Active {
			return p.ID
		}
	}
	return ""
}

// activePresetName renvoie le NOM affiché du preset actif ("" si aucun). Journalisé
// par tour (turn_done) pour que la ligne d'état sous chaque réponse indique le preset
// qui a RÉELLEMENT répondu, même après un changement de preset ou au rechargement.
func activePresetName() string {
	list, err := ListPresets()
	if err != nil {
		return ""
	}
	for _, p := range list {
		if p.Active {
			return p.Name
		}
	}
	return ""
}

// deletePresetSysPrompt efface le prompt système qu'un preset portait avant la
// 0.20 (clé sysprompt/<id>).
func deletePresetSysPrompt(id string) {
	if id != "" {
		_ = putStr(bkState, "sysprompt/"+id, "")
	}
}

func saveSysPrompt(text string) error {
	return putStr(bkState, "sysprompt", strings.TrimSpace(text))
}

// handleSysPrompt :
//
//	GET  → {ok, text}
//	POST {text} → enregistre ("" = efface) puis renvoie {ok, text}
func handleSysPrompt(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sendJSON(w, 200, map[string]any{"ok": true, "text": readSysPrompt()})
	case http.MethodPost:
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if err := saveSysPrompt(body.Text); err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true, "text": readSysPrompt()})
	default:
		sendJSON(w, 405, map[string]any{"ok": false, "error": "méthode non autorisée"})
	}
}
