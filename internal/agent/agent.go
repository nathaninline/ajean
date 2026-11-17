// Package agent est le cœur d'AJEAN : il fait travailler le modèle.
//
//	agent.go         les outils rassemblés et la boucle modèle ↔ outils
//	prompt.go        le message système
//	conversation.go  les conversations (un fichier Markdown chacune) et leur contexte
//	turn.go          un tour visible : message de l'utilisateur, puis réponse
//	tasks.go         une tâche : travail isolé, puis compte rendu dans une conversation
//	review.go        la révision : après un échange, ce qui mérite d'être retenu
//	sleep.go         le sommeil : la consolidation nocturne de la mémoire
//	events.go        la diffusion en direct vers les interfaces ouvertes
//
// Quatre façons de faire travailler le modèle, une seule boucle : un tour de
// conversation (visible, tous les outils), une tâche (isolée, tous les outils),
// une révision et un sommeil (silencieux, outils de mémoire seulement).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ajean/internal/apps"
	"ajean/internal/browser"
	"ajean/internal/config"
	"ajean/internal/llm"
	"ajean/internal/memory"
	"ajean/internal/scripts"
	"ajean/internal/tasks"
	"ajean/internal/tool"
	"ajean/internal/workspace"
)

// toolset : un jeu d'outils, par nom et sous la forme attendue par l'API.
type toolset struct {
	byName map[string]tool.Def
	specs  []llm.Tool
}

func newToolset(groups ...[]tool.Def) toolset {
	ts := toolset{byName: map[string]tool.Def{}}
	for _, g := range groups {
		for _, d := range g {
			ts.byName[d.Name] = d
			ts.specs = append(ts.specs, d.Spec())
		}
	}
	return ts
}

var (
	// allTools : ce dont l'assistant dispose dans une conversation.
	allTools = newToolset(memory.Tools(), tasks.Tools(), workspace.Tools(), scripts.Tools(), apps.Tools(), browser.Tools())
	// memoryTools : les seuls outils d'un travail de fond (révision, sommeil).
	memoryTools = newToolset(memory.Tools())
)

// call : un appel d'outil décodé, prêt à être exécuté.
type call struct {
	name string
	args map[string]any
}

// decode lit les arguments d'un appel du modèle (ok=false s'ils sont illisibles).
func decode(tc llm.ToolCall) (call, bool) {
	c := call{name: tc.Function.Name, args: map[string]any{}}
	err := json.Unmarshal([]byte(tc.Function.Arguments), &c.args)
	return c, err == nil
}

// run exécute l'appel avec les outils de ts, pour la conversation conv. Une
// panique dans un outil (un script, une page web inattendue) devient une erreur
// rendue au modèle : elle ne doit pas emporter le programme en plein tour.
func (ts toolset) run(c call, conv string) (result string) {
	d, ok := ts.byName[c.name]
	if !ok {
		return tool.Err("outil inconnu ou indisponible ici : %s", c.name)
	}
	defer func() {
		if r := recover(); r != nil {
			result = tool.Err("l'outil %s a planté : %v", c.name, r)
		}
	}()
	return clipResult(d.Run(tool.Call{Args: c.args, Conv: conv}))
}

// label : la cible d'un appel, en quelques mots, pour l'afficher ou le consigner.
func (c call) label() string {
	var parts []string
	for _, k := range []string{"action", "name", "url", "query", "key", "path", "from", "prompt", "text"} {
		if v, ok := c.args[k].(string); ok && v != "" {
			parts = append(parts, v)
			if k != "action" {
				break
			}
		}
	}
	s := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80]) + "…"
	}
	return s
}

// loop envoie msgs au modèle et exécute ses appels d'outils jusqu'à ce qu'il
// réponde sans en demander (ou que maxIter soit atteint). onText reçoit le
// texte au fil de l'eau, règles d'écriture appliquées (end=true à la fin de
// chaque message du modèle) ; nil pour un travail silencieux. Renvoie le fil
// complété et les textes écrits par le modèle.
func loop(ctx context.Context, msgs []llm.Message, specs []llm.Tool, maxIter int,
	onText func(delta string, end bool), exec func(llm.ToolCall) string) ([]llm.Message, []string, error) {
	conf := config.Get()
	ep, budget := conf.Chat(), conf.MaxContextChars
	var texts []string
	mem := memoryBlock() // une fois par tour : la relire changerait la requête et viderait le cache
	for i := 0; i < maxIter; i++ {
		if size(msgs) > budget {
			lighten(msgs, budget/2) // un tour qui s'allonge : les vieux résultats d'outils laissent la place ; franchement, car chaque allègement vide le cache du serveur
		}
		filter := memory.NewRuleFilter()
		req := append([]llm.Message{{Role: "system", Content: systemPrompt()}}, withMemory(msgs, mem)...)
		stream := func(d string) {
			if onText != nil {
				if s := filter.Push(d); s != "" {
					onText(s, false)
				}
			}
		}
		m, err := llm.Chat(ctx, ep, req, specs, stream)
		if tooLong(err) {
			// Le modèle a un contexte plus petit que prévu : on allège franchement et on redemande, une fois.
			lighten(msgs, size(msgs)/2)
			req = append(req[:1], withMemory(msgs, mem)...)
			m, err = llm.Chat(ctx, ep, req, specs, stream)
			if tooLong(err) {
				err = fmt.Errorf("%w (%s)", err, shrinkBudget(size(msgs)))
			}
		}
		if err != nil {
			return msgs, texts, err
		}
		if onText != nil {
			onText(filter.Flush(), true)
		}
		m.Content = memory.ApplyRules(m.Content) // le fil garde ce que l'utilisateur a lu
		msgs = append(msgs, m)
		if t := strings.TrimSpace(m.Content); t != "" {
			texts = append(texts, t)
		}
		if len(m.ToolCalls) == 0 {
			return msgs, texts, nil
		}
		for _, tc := range m.ToolCalls {
			msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: tc.ID, Content: exec(tc)})
		}
		if err := ctx.Err(); err != nil {
			return msgs, texts, err
		}
	}
	// Limite atteinte : plutôt que de couper sans un mot, on demande une dernière
	// réponse, sans outils, qui dit où on en est. La consigne ne reste pas dans
	// le fil : seule la réponse y entre.
	note := llm.Message{Role: "user", Content: "[System: you have used all your tool steps for this request. Do not call any tool. " +
		"Answer the user now, in their language: what you found or did, what is still blocking and why, and what you would need or try next.]"}
	req := append([]llm.Message{{Role: "system", Content: systemPrompt()}}, append(withMemory(msgs, mem), note)...)
	filter := memory.NewRuleFilter()
	m, err := llm.Chat(ctx, ep, req, nil, func(d string) {
		if onText != nil {
			if s := filter.Push(d); s != "" {
				onText(s, false)
			}
		}
	})
	if err != nil || strings.TrimSpace(m.Content) == "" || len(m.ToolCalls) > 0 {
		return msgs, texts, errTooManySteps
	}
	if onText != nil {
		onText(filter.Flush(), true)
	}
	m.Content = memory.ApplyRules(m.Content)
	msgs = append(msgs, m)
	texts = append(texts, strings.TrimSpace(m.Content))
	return msgs, texts, nil
}

// errTooManySteps : le modèle enchaîne les outils sans jamais conclure.
var errTooManySteps = errors.New("trop d'étapes sans réponse finale : la tâche a été arrêtée")

// silent fait travailler le modèle sur sa mémoire sans rien afficher. Renvoie
// les écritures réussies (« fiche_save deploy-server ») et sa dernière phrase.
func silent(ctx context.Context, msgs []llm.Message, maxIter int) (writes []string, last string, err error) {
	exec := func(tc llm.ToolCall) string {
		c, ok := decode(tc)
		if !ok {
			return tool.Err("arguments illisibles (JSON invalide)")
		}
		if c.name == "fiche_read" { // une relecture de ménage ne compte pas comme un usage
			return tool.Result(memory.ReadFicheQuietly(tool.Call{Args: c.args}.Str("name")))
		}
		res := memoryTools.run(c, "")
		if tool.IsOK(res) {
			writes = append(writes, strings.TrimSpace(c.name+" "+c.label()))
		}
		return res
	}
	_, texts, err := loop(ctx, msgs, memoryTools.specs, maxIter, nil, exec)
	if len(texts) > 0 {
		last = texts[len(texts)-1]
	}
	return writes, last, err
}
