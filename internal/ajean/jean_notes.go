package ajean

// jean_notes.go — prévenir Jean de ce que son arrière-plan a appris.
//
// Le bloc mémoire (profil, leçons, liste des fiches) est injecté une fois, en
// début de conversation, et n'est plus réécrit ensuite : c'est ce qui garde le
// cache du modèle. Mais la révision, la consolidation ou une tâche peuvent créer
// une fiche ou une leçon EN COURS de conversation : Jean ne la voyait pas et
// refaisait tout (vu en vrai : fiche youtube-vues créée en arrière-plan, puis
// même question, quinze appels et une 2ᵉ fiche en double).
//
// Ces changements sont donc mis en file et collés en tête du prochain message
// de l'utilisateur, comme la date. Ajout en FIN de conversation : le cache reste
// intact. Inutile sur un contexte neuf (le bloc mémoire est alors à jour).

import (
	"strings"
	"sync"
)

const jeanNotesMax = 800

var jeanNotes struct {
	mu    sync.Mutex
	items []string
}

func jeanQueueNotes(items []string) {
	if len(items) == 0 {
		return
	}
	jeanNotes.mu.Lock()
	defer jeanNotes.mu.Unlock()
	jeanNotes.items = append(jeanNotes.items, items...)
	if len(jeanNotes.items) > 20 {
		jeanNotes.items = jeanNotes.items[len(jeanNotes.items)-20:]
	}
}

// jeanTakeNotes vide la file. fresh = contexte neuf : rien à signaler.
func jeanTakeNotes(fresh bool) string {
	jeanNotes.mu.Lock()
	items := jeanNotes.items
	jeanNotes.items = nil
	jeanNotes.mu.Unlock()
	if fresh || len(items) == 0 {
		return ""
	}
	s := "[Memory updated in the background since your last answer (newer than your memory block): " + strings.Join(items, " · ")
	if r := []rune(s); len(r) > jeanNotesMax {
		s = string(r[:jeanNotesMax]) + "…"
	}
	return s + "]\n"
}

// jeanNoteFor : phrase courte pour une écriture d'arrière-plan réussie.
func jeanNoteFor(tool, label string) string {
	switch tool {
	case "jean_save":
		if f := jeanFicheWhen(label); f != "" {
			return "fiche " + normFicheName(label) + " saved (when: " + f + ")"
		}
		return "fiche " + normFicheName(label) + " saved"
	case "jean_patch":
		return "fiche " + normFicheName(label) + " updated"
	case "jean_lesson":
		return "new lesson: " + clipRunes(label, 160)
	case "jean_remember":
		return "profile " + label + " updated"
	case "jean_rule":
		return "enforced rule changed"
	case "jean_forget":
		return label + " removed or archived"
	}
	return ""
}

func jeanFicheWhen(name string) string {
	jeanMu.Lock()
	s, err := jeanRead(ficheFile(normFicheName(name)))
	jeanMu.Unlock()
	if err != nil || s == "" {
		return ""
	}
	return parseFiche(normFicheName(name), s).When
}
