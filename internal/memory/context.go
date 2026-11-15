package memory

import "strings"

// ContextBlock : ce qui est toujours placé dans le message système (profil,
// leçons, règles, liste bornée des fiches), relu à chaque appel donc toujours à
// jour. Renvoyé même vide : le modèle doit savoir que le profil existe pour
// penser à le remplir.
func ContextBlock() string {
	mu.Lock()
	defer mu.Unlock()
	var b strings.Builder
	b.WriteString("AJEAN memory — what you know about the user for sure (profile, kept short; edit with memory_remember / memory_forget). Everything else is in your journal: memory_search.\n\n")
	facts, err := profileLocked()
	switch {
	case err != nil:
		b.WriteString("(profile unavailable: " + err.Error() + ")\n")
	case len(facts) == 0:
		b.WriteString("(empty profile: learn about the user and save the lasting facts)\n")
	default:
		for _, f := range facts {
			b.WriteString("- " + f.Key + ": " + f.Value + "\n")
		}
	}
	if ls, err := lessonsLocked(); err != nil {
		b.WriteString("\n(lessons unavailable: " + err.Error() + ")\n")
	} else if len(ls) > 0 {
		b.WriteString("\nLessons you learned (follow them; edit with memory_lesson / memory_forget lecon-N):\n" + numberedLessons(ls) + "\n")
	}
	if rs, err := rulesLocked(); err != nil {
		b.WriteString("\n(writing rules unavailable: " + err.Error() + ")\n")
	} else if len(rs) > 0 {
		b.WriteString("\nWriting rules enforced automatically on everything you write (edit with memory_rule / memory_forget regle-N):\n")
		for _, r := range rs {
			b.WriteString(r.describe() + "\n")
		}
	}
	b.WriteString(ficheIndexLocked())
	return strings.TrimRight(b.String(), "\n")
}
