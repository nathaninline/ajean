// web_sysprompt.go — prompt système personnalisé de l'utilisateur, persisté
// CÔTÉ SERVEUR ($JEAN_HOME/sysprompt.txt) et partagé entre appareils, comme la
// conversation elle-même. Historique : avant la conversation serveur (v0.4.x),
// l'UI envoyait son prompt système dans chaque requête /api/chat ; depuis,
// /api/chat/send ne porte que le message → le champ de l'UI n'avait plus aucun
// effet. Il est maintenant lu ici par la génération (chat_conversation.go), et
// InjectSkills (llm_client.go) le fusionne avec le préambule agent.
package jean

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func sysPromptPath() string { return filepath.Join(JeanHome(), "sysprompt.txt") }

// readSysPrompt renvoie le prompt système personnalisé ("" si absent).
func readSysPrompt() string {
	b, err := os.ReadFile(sysPromptPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func saveSysPrompt(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		err := os.Remove(sysPromptPath())
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(sysPromptPath(), []byte(text+"\n"), 0o644)
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
