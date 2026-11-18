package memory

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ajean/internal/tool"
)

// Tools : les outils de la mémoire donnés au modèle. Descriptions courtes : il
// les relit à chaque message. Un résultat ne porte « [ok] » que si la mémoire a
// changé : « déjà dit », « rien changé » sortent sans préfixe, pour que
// l'appelant ne les compte pas comme des écritures.
func Tools() []tool.Def {
	return []tool.Def{
		{
			Name:        "memory_search",
			Description: "Search your journal (all past conversations and notes) and your fiches. Write the query as one natural sentence describing what you look for AND include the distinctive words (names, exact codes, file names). Try other words if nothing comes back.",
			Params: tool.Params(map[string]any{
				"query": tool.String("A natural sentence plus the distinctive words, e.g. \"what gift did we choose for Marie's birthday, necklace\""),
				"limit": tool.Integer("Number of journal excerpts, default 6"),
			}, "query"),
			Run: func(c tool.Call) string {
				limit, _ := c.Int("limit")
				return search(c.Str("query"), limit)
			},
		},
		{
			Name:        "memory_remember",
			Description: "Save or REPLACE a lasting fact in the user's profile (always visible to you). Same key = replaces the old value. Never a password, key or token.",
			Params: tool.Params(map[string]any{
				"key":   tool.String("Short dotted key, e.g. family.mother.birthday"),
				"value": tool.String("One short line"),
			}, "key", "value"),
			Run: func(c tool.Call) string { return tool.Result(setFact(c.Str("key"), c.Str("value"))) },
		},
		{
			Name:        "memory_forget",
			Description: "Remove a profile line, a lesson (lecon-N) or a writing rule (regle-N); they stay findable in the journal. Or archive a fiche no longer useful (still findable, reading it brings it back).",
			Params: tool.Params(map[string]any{
				"key": tool.String("Profile key, lecon-N, regle-N or fiche name"),
			}, "key"),
			Run: func(c tool.Call) string { return tool.Result(forget(c.Str("key"))) },
		},
		{
			Name:        "memory_note",
			Description: "Write a note in your journal: details or events to find later (not changing lists, those live in a fiche).",
			Params:      tool.Params(map[string]any{"text": tool.String("The note")}, "text"),
			Run:         func(c tool.Call) string { return tool.Done(note(c.Text("text")), "noté dans le journal") },
		},
		{
			Name:        "memory_lesson",
			Description: "Save a lesson: a correction from the user, or what failed before what worked. One short rule (a full procedure goes in a fiche). Give fiche to add it to that fiche's pitfalls instead.",
			Params: tool.Params(map[string]any{
				"text":  tool.String("One sentence: the rule to follow and why"),
				"fiche": tool.String("Optional fiche name"),
			}, "text"),
			Run: func(c tool.Call) string { return tool.Result(addLesson(c.Str("text"), c.Str("fiche"))) },
		},
		{
			Name:        "memory_rule",
			Description: "Add or update a writing rule enforced by the program: an exact text you must never write again is replaced in everything you write (e.g. a character or a word the user does not want). Same from = updates the rule.",
			Params: tool.Params(map[string]any{
				"from": tool.String("Exact text to never write"),
				"to":   tool.String("What is written instead (may be empty)"),
				"why":  tool.String("Why, in a few words"),
			}, "from", "to"),
			Run: func(c tool.Call) string {
				return tool.Result(setRule(c.Text("from"), c.Text("to"), c.Str("why")))
			},
		},
		{
			Name:        "fiche_save",
			Description: "Save or REPLACE a fiche: a procedure, how-to, recipe or guide to keep in full. Same name = new version.",
			Params: tool.Params(map[string]any{
				"name":    tool.String("Fiche name, short, e.g. deploy-server"),
				"when":    tool.String("One line: when to use it"),
				"content": tool.String("Full content, Markdown"),
			}, "name", "when", "content"),
			Run: func(c tool.Call) string {
				return tool.Result(saveFiche(ficheArg(c), c.Str("when"), c.Text("content")))
			},
		},
		{
			Name:        "fiche_read",
			Description: "Open a fiche in full.",
			Params:      tool.Params(map[string]any{"name": tool.String("Fiche name")}, "name"),
			Run:         func(c tool.Call) string { return tool.Result(readFiche(ficheArg(c))) },
		},
		{
			Name:        "fiche_patch",
			Description: "Fix part of a fiche without rewriting it: replace an exact passage.",
			Params: tool.Params(map[string]any{
				"name": tool.String("Fiche name"),
				"old":  tool.String("Exact passage to replace (must appear once)"),
				"new":  tool.String("Replacement (empty = delete)"),
			}, "name", "old", "new"),
			Run: func(c tool.Call) string {
				name := ficheArg(c)
				return tool.Done(patchFiche(name, c.Text("old"), c.Text("new")), "fiche « %s » modifiée", name)
			},
		},
		{
			Name:        "fiche_restore",
			Description: "Undo a bad rewrite of a fiche: without version, lists its kept versions; with version, puts that one back.",
			Params: tool.Params(map[string]any{
				"name":    tool.String("Fiche name"),
				"version": tool.String("Version to put back"),
			}, "name"),
			Run: func(c tool.Call) string { return tool.Result(restoreFiche(ficheArg(c), c.Str("version"))) },
		},
		{
			Name:        "fiche_merge",
			Description: "Merge a whole fiche into another one covering the same task: its full text becomes a new section of the reference fiche, then it is archived. Nothing is lost.",
			Params: tool.Params(map[string]any{
				"from": tool.String("Fiche absorbed (archived after)"),
				"into": tool.String("Reference fiche that receives it"),
			}, "from", "into"),
			Run: func(c tool.Call) string {
				from, into := normFicheName(c.Str("from")), normFicheName(c.Str("into"))
				return tool.Done(mergeFiches(from, into), "« %s » versée en entier dans « %s » (nouvelle section), puis archivée. Resserre la section avec fiche_patch si des lignes y répètent la fiche", from, into)
			},
		},
	}
}

// ficheArg : le nom de fiche d'un appel, normalisé. Les modèles le passent
// parfois sous « fiche », le nom que porte ce paramètre dans memory_lesson.
func ficheArg(c tool.Call) string {
	name := c.Str("name")
	if name == "" {
		name = c.Str("fiche")
	}
	return normFicheName(name)
}

// numbered lit « lecon-3 », « leçon 3 », « regle-3 »… : le numéro, si key
// commence par ce genre de préfixe.
func numbered(key, prefix string) (n int, ok bool) {
	rest, ok := strings.CutPrefix(fold(key), strings.TrimSuffix(prefix, "-"))
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimLeft(rest, "- "))
	return n, err == nil
}

// forget (outil memory_forget) : une leçon, une règle, une ligne du profil, ou
// à défaut la fiche de ce nom, qui est archivée plutôt qu'effacée.
func forget(key string) (string, error) {
	if n, ok := numbered(key, lessonPrefix); ok {
		return tool.OK("%s%d retirée", lessonPrefix, n), ForgetLesson(n)
	}
	if n, ok := numbered(key, rulePrefix); ok {
		return tool.OK("%s%d retirée", rulePrefix, n), ForgetRule(n)
	}
	err := ForgetFact(key)
	if err == nil {
		return tool.OK("« %s » retiré du profil (toujours retrouvable par memory_search)", normKey(key)), nil
	}
	if !errors.Is(err, errNoFact) {
		return "", err
	}
	if archiveFiche(key) == nil {
		return tool.OK("fiche « %s » archivée : hors de ta liste, memory_search la retrouve et la relire la ramène", normFicheName(key)), nil
	}
	return "", fmt.Errorf("« %s » n'est ni une clé du profil, ni une leçon (%sN), ni une règle (%sN), ni une fiche", key, lessonPrefix, rulePrefix)
}
