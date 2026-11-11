// web_sysprompt.go — prompt système personnalisé de l'utilisateur, persisté
// CÔTÉ SERVEUR (en base) et partagé entre appareils, comme la conversation
// elle-même. Historique : avant la conversation serveur (v0.4.x),
// l'UI envoyait son prompt système dans chaque requête /api/chat ; depuis,
// /api/chat/send ne porte que le message → le champ de l'UI n'avait plus aucun
// effet. Il est maintenant lu ici par la génération (chat_conversation.go), et
// InjectSkills (llm_client.go) le fusionne avec le préambule agent.
package ajean

import (
	"strings"
)

// Le prompt système est désormais rattaché au PRESET actif (un prompt par preset,
// réglé dans le modal de preset). readSysPrompt renvoie celui du preset actif ; à
// défaut, l'ancien prompt global (compat) sert de repli.
func readSysPrompt() string {
	if id := activePresetID(); id != "" {
		if sp := presetSysPrompt(id); sp != "" {
			return sp
		}
	}
	return getStr(bkState, "sysprompt")
}

// activePresetID renvoie l'id du preset actif ("" si aucun / indéterminé).
func activePresetID() string {
	if s := explicitChatService(); s != nil {
		return s.promptKey()
	}
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
	if s := explicitChatService(); s != nil {
		return s.Name
	}
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

// presetSysPrompt / setPresetSysPrompt : prompt système propre à un preset, stocké
// en base (clé sysprompt/<id>), hors du fichier .env (qui ne gère pas le multi-ligne).
func presetSysPrompt(id string) string {
	if id == "" {
		return ""
	}
	return getStr(bkState, "sysprompt/"+id)
}

func setPresetSysPrompt(id, text string) error {
	if id == "" {
		return nil
	}
	return putStr(bkState, "sysprompt/"+id, strings.TrimSpace(text))
}

func deletePresetSysPrompt(id string) {
	if id != "" {
		_ = putStr(bkState, "sysprompt/"+id, "")
	}
}
