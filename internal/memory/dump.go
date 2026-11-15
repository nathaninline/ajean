package memory

import (
	"fmt"
	"sort"
	"strings"

	"ajean/internal/config"
)

// La matière du sommeil : toute la mémoire, mise à plat pour le modèle.

const (
	groupBodyWords = 20  // en dessous, une fiche est trop courte pour juger par son contenu
	groupBodyShare = 0.5 // part des mots de la petite fiche présents dans la grande
	dumpTitlesRoom = 300 // place gardée pour les titres de sections

	// RelatedFichesHeading : le titre, dans Dump, de la section des fiches qui
	// se ressemblent. Exporté pour que la consigne du sommeil cite la section
	// par le nom qu'elle porte vraiment.
	RelatedFichesHeading = "FICHES THAT LOOK ALIKE"
)

// Mots de nom trop généraux pour rapprocher deux fiches à eux seuls.
var vagueNameWords = map[string]bool{"fiche": true, "ajean": true, "suivi": true, "etat": true, "note": true, "notes": true, "liste": true, "guide": true, "faire": true}

// Dump : toute la mémoire pour le modèle. Profil, leçons, règles, puis les
// fiches avec leur usage. Si tout tient dans maxChars (0 = sans limite), tout
// est donné en entier. Sinon viennent en entier d'abord les groupes de fiches
// qui se ressemblent (c'est là que sont les doublons : sans leur contenu, le
// modèle n'en repère aucun), puis les plus récemment modifiées ; le reste en
// liste.
func Dump(maxChars int) string {
	mu.Lock()
	defer mu.Unlock()
	var b strings.Builder
	b.WriteString("## PROFILE\n")
	// Un fichier illisible n'arrive pas jusqu'ici pendant un sommeil : BeginSleep
	// a déjà refusé de commencer.
	facts, _ := profileLocked()
	for _, f := range facts {
		b.WriteString("- " + f.Key + ": " + f.Value + "\n")
	}
	lessons, _ := lessonsLocked()
	b.WriteString("\n## LESSONS\n" + numberedLessons(lessons) + "\n")
	b.WriteString("\n## WRITING RULES (enforced by the program)\n")
	rules, _ := rulesLocked()
	for _, r := range rules {
		b.WriteString(r.describe() + "\n")
	}

	fs := fichesLocked(false)
	usage, _ := usageLocked()
	loc := config.Location()
	head := func(f fiche) string {
		return fmt.Sprintf("%s (when: %s) · changed %s · %s", f.Name, f.When, f.mod.In(loc).Format("2006-01-02"), usageLine(usage[f.Name]))
	}
	block := func(f fiche) string { return "\n### fiche " + head(f) + "\n" + f.Content + "\n" }
	line := func(f fiche) string { return fmt.Sprintf("- %s · %d chars\n", head(f), runeLen(f.Content)) }

	everything, index := 0, 0
	for _, f := range fs {
		everything += len(block(f))
		index += len(line(f))
	}
	if maxChars <= 0 || b.Len()+dumpTitlesRoom+everything <= maxChars {
		b.WriteString("\n## FICHES (in full, most recently changed first)\n")
		for _, f := range fs {
			b.WriteString(block(f))
		}
		return b.String()
	}

	// La place de la liste est réservée d'abord : aucune fiche ne doit manquer.
	budget := maxChars - b.Len() - dumpTitlesRoom - index
	full := map[string]bool{}
	groups := 0
	for _, group := range ficheGroups(fs) {
		size := 0
		for _, f := range group {
			size += len(block(f))
		}
		if size > budget {
			continue
		}
		budget -= size
		if groups++; groups == 1 {
			b.WriteString("\n## " + RelatedFichesHeading + " (in full: this is where duplicates hide)\n")
		}
		fmt.Fprintf(&b, "\n### GROUP %d: %s\n", groups, strings.Join(ficheNames(group), ", "))
		for _, f := range group {
			full[f.Name] = true
			b.WriteString(strings.Replace(block(f), "\n### ", "\n#### ", 1))
		}
	}
	recent := false
	for _, f := range fs {
		if full[f.Name] || len(block(f)) > budget {
			continue
		}
		if !recent {
			b.WriteString("\n## RECENTLY CHANGED FICHES (in full)\n")
			recent = true
		}
		budget -= len(block(f))
		full[f.Name] = true
		b.WriteString(block(f))
	}
	b.WriteString("\n## OTHER FICHES (index only, fiche_read to open)\n")
	for _, f := range fs {
		if !full[f.Name] {
			b.WriteString(line(f))
		}
	}
	return b.String()
}

// ficheGroups : les fiches qui se ressemblent (un mot de nom en commun, ou le
// contenu de la plus petite largement présent dans l'autre), par groupes de
// deux ou plus. Les plus petits groupes d'abord : ils tiennent dans le budget
// et se fusionnent sûrement.
func ficheGroups(fs []fiche) [][]fiche {
	parent := make([]int, len(fs))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	names := make([]map[string]bool, len(fs))
	bodies := make([]map[string]bool, len(fs))
	for i, f := range fs {
		names[i] = map[string]bool{}
		for w := range words(strings.ReplaceAll(f.Name, "-", " ")) {
			if !vagueNameWords[w] {
				names[i][w] = true
			}
		}
		bodies[i] = words(f.When + " " + f.Content)
	}
	for i := range fs {
		for j := i + 1; j < len(fs); j++ {
			small, big := bodies[i], bodies[j]
			if len(small) > len(big) {
				small, big = big, small
			}
			if overlap(names[i], names[j]) > 0 || len(small) >= groupBodyWords && overlap(small, big) >= groupBodyShare {
				parent[find(i)] = find(j)
			}
		}
	}
	byRoot := map[int][]fiche{}
	for i, f := range fs {
		byRoot[find(i)] = append(byRoot[find(i)], f)
	}
	var out [][]fiche
	for _, group := range byRoot {
		if len(group) >= 2 {
			out = append(out, group)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if len(out[a]) != len(out[b]) {
			return len(out[a]) < len(out[b])
		}
		return out[a][0].Name < out[b][0].Name
	})
	return out
}
