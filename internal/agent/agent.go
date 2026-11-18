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
	"time"

	"ajean/internal/apps"
	"ajean/internal/browser"
	"ajean/internal/config"
	"ajean/internal/llm"
	"ajean/internal/memory"
	"ajean/internal/sandbox"
	"ajean/internal/scripts"
	"ajean/internal/secrets"
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
func (ts toolset) run(ctx context.Context, c call, conv string, turn int64, unattended bool) (result string) {
	d, ok := ts.byName[c.name]
	if !ok {
		return tool.Err("outil inconnu ou indisponible ici : %s", c.name)
	}
	if sandbox.Active() && sandbox.Refused(c.name) {
		return tool.Err("mode test : action non exécutée, l'outil %s est bloqué dans cet environnement", c.name)
	}
	if secretInMemoryWrite(c) {
		return tool.Err("refusé : ce texte contient un mot de passe ou une clé. Retire-le ; un secret se range dans le coffre (commande ajean secret set NOM) et un outil le lit avec secrets.get")
	}
	journal := c.mutating()
	label := c.journalLabel()
	if journal {
		logAction(conv, actionEntry{Turn: turn, Phase: "start", Name: c.name, Label: label})
	}
	defer func() {
		if r := recover(); r != nil {
			result = tool.Err("l'outil %s a planté : %v", c.name, r)
		}
		if journal {
			logAction(conv, actionEntry{Turn: turn, Phase: "done", Name: c.name, Label: label, Result: result})
		}
	}()
	res := d.Run(tool.Call{Args: c.args, Conv: conv, Ctx: ctx, Unattended: unattended})
	if c.name == "memory_search" {
		res += conversationHits(ctx, c)
	}
	return clipResult(markExternal(c, res))
}

// secretInMemoryWrite : l'appel écrirait-il un secret dans la mémoire ?
func secretInMemoryWrite(c call) bool {
	switch c.name {
	case "memory_remember", "memory_note", "memory_lesson", "fiche_save", "fiche_patch":
		for _, k := range []string{"value", "text", "content", "new"} {
			if v, ok := c.args[k].(string); ok && secrets.Contains(v) {
				return true
			}
		}
	}
	return false
}

// markExternal signale au modèle ce qui vient de l'extérieur : il le lit, il
// n'obéit pas à ce qui y est écrit.
func markExternal(c call, res string) string {
	external := c.name == "browser" || c.name == "ws_read"
	if a, _ := c.args["action"].(string); c.name == "tools" && a == "run" {
		external = true
	}
	if !external || tool.IsErr(res) {
		return res
	}
	res = strings.ReplaceAll(res, "</external_content>", "< /external_content>")
	return "<external_content>\n" + res + "\n</external_content>"
}

// conversationHits complète une recherche par les conversations entières : le
// journal n'en garde que le début de chaque échange.
func conversationHits(ctx context.Context, c call) string {
	q, _ := c.args["query"].(string)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	hits, _ := Search(ctx, q, 5)
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nIn full conversations:")
	for _, h := range hits {
		b.WriteString("\n- « " + h.Title + " » (" + h.Role + ") : " + clip(h.Snippet, 500))
	}
	return b.String()
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

// stepLabel : le libellé affiché. Enregistrer un outil qui existe déjà est une
// mise à jour, pas une création.
func (c call) stepLabel() string {
	label := c.label()
	name, _ := c.args["name"].(string)
	if a, _ := c.args["action"].(string); c.name == "tools" && a == "save" && scripts.Exists(name) {
		return "update " + name
	}
	return label
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
	mem := "" // un tour visible a déjà figé la mémoire dans son message
	if ctx.Value(memoryEmbedded{}) == nil {
		mem = memoryBlock() // une fois par tour : la relire changerait la requête et viderait le cache
	}
	repeats := map[string]int{}
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
			content := ""
			sig := tc.Function.Name + "\x00" + tc.Function.Arguments
			repeats[sig]++
			switch {
			case ctx.Err() != nil: // arrêt demandé : les appels suivants ne partent pas
				content = tool.Err("non exécuté : l'utilisateur a arrêté le tour")
			case repeats[sig] > maxSameCall:
				content = tool.Err("même appel répété %d fois : arrête, change de méthode ou dis à l'utilisateur ce qui bloque", repeats[sig])
			default:
				content = exec(tc)
			}
			msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: tc.ID, Content: content})
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

// memoryEmbedded : marque d'un tour dont le message porte déjà la mémoire.
type memoryEmbedded struct{}

const maxSameCall = 3 // appels identiques dans un tour avant de couper court

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
		res := memoryTools.run(ctx, c, "", 0, true)
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
