package ajean

import "strings"

// jeanCorrectionBrief : tête de la consigne de révision quand l'utilisateur a
// corrigé Jean. La correction passe AVANT tout le reste ; une erreur répétée
// exige de durcir la règle existante, pas d'en ajouter une de plus.
func jeanCorrectionBrief(corr int, msgs []string) string {
	if corr == jeanCorrNone || len(msgs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[PRIORITY] The user corrected you in these messages:\n")
	for _, m := range msgs {
		b.WriteString("> " + strings.ReplaceAll(m, "\n", " ") + "\n")
	}
	if corr == jeanCorrRepeat {
		b.WriteString("The user says you ALREADY made this mistake before: the rule you had was not enough. " +
			"Find it (lessons in your memory block, the related fiche, or jean_search). Then make it impossible to miss: " +
			"one strict lesson with jean_lesson (always in front of you), in the user's own words, saying what to do and what never to do; " +
			"fix the fiche too with jean_patch if one is involved. Remove the weaker duplicate. " +
			"If the rule is mechanical (a banned character or word, list numbering, a maximum of list items) and not yet in your enforced rules, add it with jean_rule: then your harness makes it impossible to break.\n\n")
	} else {
		b.WriteString("Make sure the rule is saved exactly as the user meant it (re-read their words, do not reinterpret), " +
			"where you will see it next time: in the fiche of that task (jean_patch or jean_lesson fiche=<name>), else as a lesson. " +
			"If you already saved it during the conversation, check it matches their words and fix it if not. " +
			"If it is mechanical (a banned character or word, list numbering, a maximum of list items), also add it with jean_rule.\n\n")
	}
	return b.String()
}

// jeanStripNow retire le préfixe « [date heure] » des messages du mode Jean.
// Retire aussi la note « [Memory updated…] » qui peut suivre (jean_notes.go) :
// ce n'est pas l'utilisateur qui parle.
func jeanStripNow(s string) string {
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "] "); i > 0 && i < 80 {
			s = s[i+2:]
		}
	}
	if strings.HasPrefix(s, "[Memory updated") {
		if i := strings.Index(s, "]\n"); i > 0 {
			s = s[i+2:]
		}
	}
	return s
}
