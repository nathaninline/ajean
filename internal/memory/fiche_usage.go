package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// L'usage des fiches : chaque ouverture est comptée. C'est ce qui distingue ce
// qui sert de ce qui dort, et ce qui permet de garder une liste injectée à
// budget fixe : mille fiches coûtent autant au contexte qu'une trentaine.

const (
	ficheIndexBudget = 3000 // caractères de liste injectée (~750 tokens), à vie
	usageBonusMax    = 30
)

type ficheUse struct {
	N    int   `json:"n"`
	Last int64 `json:"last"` // dernière ouverture, en millisecondes depuis l'époque
}

func usagePath() string { return statePath("fiche-usage.json") }

// usageLocked : l'usage de chaque fiche. L'erreur signale des compteurs
// illisibles : bons à ignorer pour lire, à ne surtout pas réécrire.
func usageLocked() (map[string]ficheUse, error) {
	u := map[string]ficheUse{}
	err := readState(usagePath(), &u)
	return u, err
}

func noteUseLocked(name string) {
	u, err := usageLocked()
	if err != nil {
		return
	}
	u[name] = ficheUse{N: u[name].N + 1, Last: time.Now().UnixMilli()}
	_ = writeState(usagePath(), u)
}

// renameUseLocked : une fiche renommée garde son usage, sinon elle retomberait
// en bas de la liste comme une fiche jamais ouverte.
func renameUseLocked(old, name string) {
	u, err := usageLocked()
	if use, ok := u[old]; ok && err == nil {
		delete(u, old)
		u[name] = use
		_ = writeState(usagePath(), u)
	}
}

// usefulness : l'ordre de la liste injectée. La dernière fois que la fiche a
// servi (ou a été écrite), plus un jour d'avance par ouverture.
func usefulness(f fiche, u ficheUse) int64 {
	last := max(f.mod.UnixMilli(), u.Last)
	return last + int64(min(u.N, usageBonusMax))*24*3600*1000
}

// ficheIndexLocked : la liste des fiches placée dans le bloc mémoire. Les plus
// utiles d'abord ; ce qui dépasse le budget reste trouvable par la recherche.
func ficheIndexLocked() string {
	fs := fichesLocked(false)
	if len(fs) == 0 {
		return ""
	}
	u, _ := usageLocked()
	sort.SliceStable(fs, func(i, j int) bool {
		return usefulness(fs[i], u[fs[i].Name]) > usefulness(fs[j], u[fs[j].Name])
	})
	var b strings.Builder
	b.WriteString("\nYour fiches (procedures, how-tos, guides): when one matches the request, fiche_read it FIRST and follow it.\n")
	used := 0
	for i, f := range fs {
		line := "- " + f.Name + ": " + f.When + "\n"
		if used+len(line) > ficheIndexBudget {
			fmt.Fprintf(&b, "(+%d less used fiches not listed: memory_search finds them)\n", len(fs)-i)
			break
		}
		b.WriteString(line)
		used += len(line)
	}
	return b.String()
}

// usageLine : l'usage d'une fiche, dit au modèle pendant le sommeil.
func usageLine(u ficheUse) string {
	if u.N == 0 {
		return "never opened"
	}
	days := int(time.Since(time.UnixMilli(u.Last)).Hours() / 24)
	return fmt.Sprintf("opened %d times, last %d days ago", u.N, days)
}
