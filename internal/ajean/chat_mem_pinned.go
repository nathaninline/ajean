package ajean

import (
	"encoding/json"
	"fmt"
	"strings"
)

// chat_mem_pinned.go — RAPPEL des pages mémoire lues au compactage.
//
// Problème corrigé : quand le contexte est compacté, le torse (dont les résultats
// de mem_read) est résumé. Une page mémoire qui portait, par exemple, TOUTES LES
// RÈGLES à respecter pour une tâche était donc réduite à quelques mots dans le
// résumé — et le modèle, après compactage, « oubliait » les règles et partait en
// vrille.
//
// Choix retenu (le plus léger pour le contexte) : on ne garde RIEN verbatim. On
// injecte simplement, en tête de la conversation compactée, un petit rappel qui
// LISTE les pages que le modèle a lues (mem_read) et l'invite à en relire une si
// elle est pertinente, pour être sûr d'avoir toutes les infos avant de répondre. Le
// rappel ne pèse que les NOMS des pages (borné), le contenu complet reste à un
// mem_read de distance. Le comportement de compactage est par ailleurs inchangé.
//
// Le rappel ACCUMULE les noms à travers les compactages successifs : le rappel du
// compactage précédent est relu comme source, donc une page lue tôt (dont le
// mem_read d'origine a depuis été résumé) reste listée.

// memReminderPrefix ouvre le message de rappel. Sert aussi à le reconnaître (pour le
// relire comme source au compactage suivant, et pour le retirer avant reconstruction).
const memReminderPrefix = "[MEMORY PAGES REMINDER]"

// memReminderMaxNames borne le nombre de noms listés : au-delà, une conversation qui
// lit énormément de pages ne ferait pas enfler le rappel. On garde les plus RÉCENTES
// (les plus susceptibles de concerner la tâche en cours).
const memReminderMaxNames = 24

// collectReadPageNames rassemble les noms des pages mémoire LUES au fil de la
// conversation, dans l'ordre (première lecture d'abord). Deux sources, toutes deux
// balayées pour survivre aux compactages successifs :
//   - les résultats de mem_read encore présents (résultat `tool` relié à sa page via
//     l'argument `file` du tool_call correspondant), en ignorant les lectures ratées ;
//   - le rappel injecté à un compactage PRÉCÉDENT (une fois le mem_read d'origine
//     résumé, c'est la seule trace restante des noms).
func collectReadPageNames(msgs []Message) []string {
	seen := map[string]bool{}
	var order []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		order = append(order, name)
	}
	// tool_call_id → nom de page pour les appels mem_read (le résultat `tool` ne
	// porte que l'id ; le nom vit dans l'argument `file` de l'appel de l'assistant).
	readCall := map[string]string{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if tc.Function.Name != "mem_read" || tc.ID == "" {
				continue
			}
			var a map[string]any
			if json.Unmarshal([]byte(tc.Function.Arguments), &a) == nil {
				if f, _ := a["file"].(string); f != "" {
					readCall[tc.ID] = f
				}
			}
		}
	}
	for _, m := range msgs {
		switch m.Role {
		case "system":
			for _, n := range parseReminderNames(msgText(m)) {
				add(n)
			}
		case "tool":
			name, ok := readCall[m.ToolCallID]
			if !ok {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(msgText(m)), "[erreur]") {
				continue // lecture ratée : rien à rappeler
			}
			add(name)
		}
	}
	return order
}

// parseReminderNames relit la liste de noms d'un message de rappel produit par
// buildReminderMessage. Renvoie nil si ce n'est pas un rappel.
func parseReminderNames(text string) []string {
	if !strings.HasPrefix(text, memReminderPrefix) {
		return nil
	}
	// Format : 1re ligne `[MEMORY PAGES REMINDER] … read earlier: a.md, b.md, c.md`
	// (les noms après le dernier `: `, sans ponctuation finale) ; la consigne de
	// relecture est sur la ou les lignes SUIVANTES, donc hors de la liste.
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	i := strings.LastIndex(line, ": ")
	if i < 0 {
		return nil
	}
	list := line[i+2:]
	var out []string
	for _, part := range strings.Split(list, ",") {
		if n := strings.TrimSpace(part); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// buildReminderMessage construit le message `system` de rappel. Renvoie ok=false si
// aucune page n'a été lue. Borné à memReminderMaxNames (on garde les plus récentes).
func buildReminderMessage(names []string) (Message, bool) {
	if len(names) == 0 {
		return Message{}, false
	}
	if len(names) > memReminderMaxNames {
		names = names[len(names)-memReminderMaxNames:] // les plus récentes
	}
	content := fmt.Sprintf("%s Memory pages you read earlier: %s\nTheir full content is no longer inline after context compaction — if any of them is relevant to what you're doing, mem_read it again so you have all the rules and info you need before answering.",
		memReminderPrefix, strings.Join(names, ", "))
	return Message{Role: "system", Content: content}, true
}

// stripReminder retire le(s) message(s) de rappel d'une séquence (avant reconstruction).
func stripReminder(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "system" {
			if s, ok := m.Content.(string); ok && strings.HasPrefix(s, memReminderPrefix) {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

// remindReadMemPages reconstruit, à partir de l'historique AVANT compactage (source),
// le rappel des pages mémoire lues, et l'injecte en tête de la séquence COMPACTÉE. On
// retire d'abord un éventuel rappel hérité (porté par compacted depuis la tête
// protégée) pour repartir d'un rappel propre et à jour.
//
// Appelé uniquement en mode agent (les outils mem_* existent) et hors mémoire coupée.
// Sans page lue, no-op.
func remindReadMemPages(compacted, source []Message) []Message {
	if memMode() == MemOff {
		return compacted
	}
	names := collectReadPageNames(source)
	compacted = stripReminder(compacted)
	if m, ok := buildReminderMessage(names); ok {
		return append([]Message{m}, compacted...)
	}
	return compacted
}
