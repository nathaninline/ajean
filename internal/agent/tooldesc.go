package agent

import (
	"context"
	"strings"

	"ajean/internal/config"
	"ajean/internal/llm"
)

// ContextWindow : la fenêtre de contexte du modèle actif, en tokens (0 : l'API
// ne la donne pas). Lue une fois sur /models, puis gardée en mémoire.
func ContextWindow() int {
	return llm.ContextWindow(context.Background(), config.Get().Chat())
}

// Chaque outil d'une conversation reçoit un champ « description » : une phrase
// que le modèle écrit pour l'utilisateur, qui dit exactement ce qu'il fait avec
// cet appel. Elle s'affiche dans la ligne d'activité. Le champ est retiré des
// arguments avant l'exécution : les outils ne le voient jamais.
const toolDescParam = "description"

const toolDescHelp = "Une phrase courte destinée à l'utilisateur, dans sa langue, qui dit précisément ce que fait cet appel, " +
	"sans « je » ni nom de commande. Exemple : « Recherche dans les notes de la date du rendez-vous chez le dentiste »."

// withToolDesc renvoie une copie des specs avec le champ description ajouté en
// premier paramètre requis, et l'ensemble des outils qui l'ont reçu.
func withToolDesc(specs []llm.Tool) ([]llm.Tool, map[string]bool) {
	out := make([]llm.Tool, len(specs))
	set := map[string]bool{}
	for i, s := range specs {
		out[i] = s
		p := s.Function.Parameters
		props := map[string]any{}
		if old, ok := p["properties"].(map[string]any); ok {
			if _, taken := old[toolDescParam]; taken {
				continue // l'outil a déjà son propre champ description
			}
			for k, v := range old {
				props[k] = v
			}
		}
		props[toolDescParam] = map[string]any{"type": "string", "description": toolDescHelp}
		np := make(map[string]any, len(p)+2)
		for k, v := range p {
			np[k] = v
		}
		if np["type"] == nil {
			np["type"] = "object"
		}
		np["properties"] = props
		req := []string{toolDescParam}
		switch r := p["required"].(type) {
		case []string:
			req = append(req, r...)
		case []any:
			for _, x := range r {
				if v, ok := x.(string); ok {
					req = append(req, v)
				}
			}
		}
		np["required"] = req
		out[i].Function.Parameters = np
		set[s.Function.Name] = true
	}
	return out, set
}

// takeToolDesc retire la description des arguments et la renvoie, seulement
// pour un outil qui l'a reçue.
func takeToolDesc(set map[string]bool, c *call) string {
	if c.args == nil || !set[c.name] {
		return ""
	}
	d, _ := c.args[toolDescParam].(string)
	delete(c.args, toolDescParam)
	d = strings.Join(strings.Fields(d), " ")
	if r := []rune(d); len(r) > 140 {
		d = string(r[:140]) + "…"
	}
	return d
}
