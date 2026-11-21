package ajean

// jean_activity.go : ce que Jean fait de sa mémoire EN ARRIÈRE-PLAN (révision
// après un échange, révision de tâche, consolidation), pour l'utilisateur.
// Sans ça, Jean rangeait sa mémoire sans que rien ne s'affiche.
//
// jeanSilentRun (jean_reflect.go) marque le début et la fin ; chaque outil de
// mémoire réussi ajoute une ligne lisible. L'état part avec /api/status
// (« jean_mem ») ; l'UI affiche « mise à jour de la mémoire » pendant le
// travail, puis ce qui a changé.

import (
	"strings"
	"sync"
	"time"
)

type jeanMemActivity struct {
	Running bool     `json:"running"`
	Kind    string   `json:"kind,omitempty"`  // révision, révision de tâche, consolidation
	Since   int64    `json:"since,omitempty"` // epoch ms
	Last    int64    `json:"last,omitempty"`  // fin du dernier travail qui a changé quelque chose (epoch ms)
	Changes []string `json:"changes,omitempty"`
}

var jeanActivity struct {
	mu sync.Mutex
	st jeanMemActivity
	// changements du travail en cours (publiés à la fin)
	cur []string
}

func jeanActivityStart(kind string) {
	jeanActivity.mu.Lock()
	defer jeanActivity.mu.Unlock()
	jeanActivity.st.Running = true
	jeanActivity.st.Kind = kind
	jeanActivity.st.Since = time.Now().UnixMilli()
	jeanActivity.cur = nil
}

func jeanActivityChange(tool, label string) {
	s := jeanChangeLabel(tool, label)
	if s == "" {
		return
	}
	jeanActivity.mu.Lock()
	defer jeanActivity.mu.Unlock()
	for _, c := range jeanActivity.cur {
		if c == s {
			return
		}
	}
	if len(jeanActivity.cur) < 12 {
		jeanActivity.cur = append(jeanActivity.cur, s)
	}
}

func jeanActivityEnd() {
	jeanActivity.mu.Lock()
	defer jeanActivity.mu.Unlock()
	jeanActivity.st.Running = false
	jeanActivity.st.Since = 0
	if len(jeanActivity.cur) > 0 {
		jeanActivity.st.Changes = jeanActivity.cur
		jeanActivity.st.Last = time.Now().UnixMilli()
	}
	jeanActivity.cur = nil
}

func jeanActivityGet() jeanMemActivity {
	jeanActivity.mu.Lock()
	defer jeanActivity.mu.Unlock()
	st := jeanActivity.st
	if st.Running {
		st.Changes = append([]string(nil), jeanActivity.cur...)
	}
	return st
}

// jeanChangeLabel : une modification de mémoire, en clair pour l'utilisateur.
func jeanChangeLabel(tool, label string) string {
	label = strings.TrimSpace(label)
	switch tool {
	case "jean_save":
		return "fiche « " + normFicheName(label) + " » enregistrée"
	case "jean_patch":
		return "fiche « " + normFicheName(label) + " » mise à jour"
	case "jean_merge":
		return "fiches fusionnées : " + clipRunes(label, 80)
	case "jean_lesson":
		return "nouvelle leçon : " + clipRunes(label, 100)
	case "jean_remember":
		return "profil mis à jour : " + clipRunes(label, 80)
	case "jean_rule":
		return "règle de réponse modifiée"
	case "jean_forget":
		return clipRunes(label, 80) + " retiré ou archivé"
	case "jean_note":
		return "journal complété"
	case "jean_scripts", "write", "edit":
		if label != "" {
			return "fichier « " + clipRunes(label, 60) + " » écrit"
		}
	}
	return ""
}
