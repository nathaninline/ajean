package ajean

// jean_consolidate_guard.go — le garde-fou de la consolidation : RIEN de ce que
// l'utilisateur a appris à Jean ne doit se perdre pendant le ménage.
//
// La consolidation a le droit de déplacer, fusionner, reformuler. Elle n'a pas
// le droit de perdre. Après son passage, chaque ligne disparue (une leçon, une
// ligne du profil, une étape ou un piège de fiche) doit se retrouver ailleurs
// dans la mémoire, au moins par ses mots importants. Sinon elle est restaurée
// telle quelle. Vérification déterministe, sans IA : c'est ce qui permet de
// faire confiance au ménage sans jamais aller le relire.
//
// Exception voulue : une ligne que la mémoire CONTREDIT désormais (remplacée
// par une version plus récente sous la même clé de profil) n'est pas restaurée.
// L'avant-dernier état complet reste aussi copié dans consolidation-avant/,
// pour un retour arrière à la main.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	jeanGuardDir      = "consolidation-avant"
	jeanGuardCoverage = 0.6 // part des mots importants à retrouver ailleurs
)

type jeanMemSnap map[string]string // fichier → contenu déchiffré

// jeanSnapMemory lit profil, leçons et fiches (et en garde copie sur disque).
func jeanSnapMemory() jeanMemSnap {
	jeanMu.Lock()
	defer jeanMu.Unlock()
	snap := jeanMemSnap{}
	ents, _ := os.ReadDir(jeanDir())
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !(n == jeanProfileFile || n == jeanLessonsFile || strings.HasPrefix(n, jeanFichePrefix) && strings.HasSuffix(n, ".md")) {
			continue
		}
		if s, err := jeanRead(n); err == nil {
			snap[n] = s
		}
	}
	dir := filepath.Join(jeanDir(), jeanGuardDir)
	_ = os.RemoveAll(dir)
	if os.MkdirAll(dir, 0o755) == nil {
		for n, s := range snap {
			if out, err := encodeMemContent([]byte(s)); err == nil {
				_ = os.WriteFile(filepath.Join(dir, n), out, 0o600)
			}
		}
	}
	return snap
}

// jeanUnits : les lignes porteuses d'information d'un fichier de mémoire.
func jeanUnits(name, s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "when: ") || t == "```" {
			continue
		}
		if len([]rune(t)) < 12 {
			continue // « - Lait », séparateurs : trop court pour juger
		}
		out = append(out, t)
	}
	return out
}

// jeanKeyTerms : mots importants d'une ligne (4 lettres et plus, sans clé de profil).
func jeanKeyTerms(line string) []string {
	if m := jeanLineRe.FindStringSubmatch(line); m != nil {
		line = m[2]
	}
	if i := strings.Index(line, "] "); strings.HasPrefix(line, "- [lecon-") && i > 0 {
		line = line[i+2:]
	}
	var out []string
	for _, t := range uniqueTerms(foldSearch(line)) {
		if len([]rune(t)) >= 4 {
			out = append(out, t)
		}
	}
	return out
}

// jeanGuardCheck compare la mémoire avant/après et restaure ce qui s'est perdu.
// Renvoie la liste lisible des restaurations.
func jeanGuardCheck(before jeanMemSnap) []string {
	after := jeanSnapCurrent()
	var all strings.Builder
	for _, s := range after {
		all.WriteString(foldSearch(s) + "\n")
	}
	hay := all.String()
	newFacts := map[string]bool{}
	for _, f := range parseJeanProfile(after[jeanProfileFile]) {
		newFacts[f.Key] = true
	}
	var restored []string
	for name, old := range before {
		cur := after[name]
		var lost []string
		for _, u := range jeanUnits(name, old) {
			if strings.Contains(cur, u) {
				continue
			}
			terms := jeanKeyTerms(u)
			if len(terms) == 0 {
				continue
			}
			found := 0
			for _, t := range terms {
				if strings.Contains(hay, t) {
					found++
				}
			}
			if float64(found)/float64(len(terms)) >= jeanGuardCoverage {
				continue // déplacé ou reformulé : rien de perdu
			}
			// Clé de profil réécrite avec une autre valeur : remplacement voulu.
			if m := jeanLineRe.FindStringSubmatch(u); m != nil && name == jeanProfileFile && newFacts[m[1]] {
				continue
			}
			lost = append(lost, u)
		}
		if len(lost) == 0 {
			continue
		}
		switch {
		case name == jeanProfileFile:
			for _, u := range lost {
				if m := jeanLineRe.FindStringSubmatch(u); m != nil {
					if _, err := JeanRemember(m[1], m[2]); err == nil {
						restored = append(restored, "profil "+m[1])
					}
				}
			}
		case name == jeanLessonsFile:
			for _, u := range lost {
				text := strings.TrimPrefix(u, "- ")
				if i := strings.Index(text, "] "); strings.HasPrefix(text, "[lecon-") && i > 0 {
					text = text[i+2:]
				}
				if _, err := JeanLesson(text, ""); err == nil {
					restored = append(restored, "leçon « "+clipRunes(text, 60)+" »")
				}
			}
		default:
			// Fiche supprimée : on la remet entière. Fiche modifiée : on y remet
			// seulement les lignes perdues, dans une section que la prochaine
			// consolidation intégrera proprement (revenir à l'ancienne version
			// annulerait aussi ce que la consolidation y a fusionné à raison).
			content := old
			if cur != "" {
				content = strings.TrimRight(cur, "\n") + "\n\n" + jeanRestoredHead + "\n"
				for _, u := range lost {
					if !strings.HasPrefix(u, "- ") && !startsWithDigitDot(u) {
						u = "- " + u
					}
					content += u + "\n"
				}
			}
			jeanMu.Lock()
			err := jeanWrite(name, content)
			jeanMu.Unlock()
			if err == nil {
				restored = append(restored, "fiche "+strings.TrimSuffix(strings.TrimPrefix(name, jeanFichePrefix), ".md"))
			}
		}
	}
	return restored
}

func jeanSnapCurrent() jeanMemSnap {
	jeanMu.Lock()
	defer jeanMu.Unlock()
	snap := jeanMemSnap{}
	ents, _ := os.ReadDir(jeanDir())
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !(n == jeanProfileFile || n == jeanLessonsFile || (strings.HasPrefix(n, jeanFichePrefix) || strings.HasPrefix(n, jeanArchivePrefix)) && strings.HasSuffix(n, ".md")) {
			continue
		}
		if s, err := jeanRead(n); err == nil {
			snap[n] = s
		}
	}
	return snap
}

func jeanGuardReport(restored []string) string {
	if len(restored) == 0 {
		return ""
	}
	return fmt.Sprintf("Garde-fou : %d élément(s) que la consolidation avait perdus, restaurés : %s", len(restored), strings.Join(restored, " · "))
}

// jeanRestoredHead : section où le garde-fou remet les lignes perdues d'une fiche.
const jeanRestoredHead = "## Restauré (à réintégrer)"

func startsWithDigitDot(s string) bool {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i > 0 && i < len(s) && s[i] == '.'
}
