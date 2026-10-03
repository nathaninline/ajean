package ajean

// jean_fiche_life.go — la vie des fiches : ce qui garde la mémoire de Jean
// propre et son contexte léger, à vie, sans que personne n'ait à trier.
//
//   - USAGE : chaque ouverture d'une fiche est comptée (usage-fiches.json). C'est
//     ce qui distingue ce qui sert de ce qui dort.
//   - INDEX BORNÉ : la liste injectée en tête de conversation a un budget fixe
//     en caractères. Les fiches qui servent passent devant ; les autres restent
//     trouvables par jean_search. Mille fiches coûtent autant qu'une trentaine.
//   - ARCHIVE : oublier une fiche (jean_forget, typiquement à la consolidation)
//     l'archive au lieu de l'effacer : elle sort de la liste, jean_search la
//     trouve encore, et la rouvrir la ramène d'elle-même. Rien ne se perd, et
//     ce qui ne sert plus ne fait plus de bruit.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	jeanArchivePrefix    = "archive-fiche-"
	jeanUsageFile        = "usage-fiches.json"
	jeanFicheIndexBudget = 3000 // caractères de liste injectée (~750 tokens), à vie
)

type jeanFicheUse struct {
	N         int   `json:"n"`
	Last      int64 `json:"last"`                // epoch ms de la dernière ouverture
	Corrected int   `json:"corrected,omitempty"` // corrections reçues juste après l'avoir suivie
}

func archiveFile(name string) string { return jeanArchivePrefix + name + ".md" }

// jeanUsage lit les compteurs (jeanMu tenu).
func jeanUsageLocked() map[string]*jeanFicheUse {
	u := map[string]*jeanFicheUse{}
	if s, err := jeanRead(jeanUsageFile); err == nil && s != "" {
		_ = json.Unmarshal([]byte(s), &u)
	}
	return u
}

func jeanNoteFicheUse(name string) {
	jeanMu.Lock()
	defer jeanMu.Unlock()
	u := jeanUsageLocked()
	e := u[name]
	if e == nil {
		e = &jeanFicheUse{}
		u[name] = e
	}
	e.N++
	e.Last = time.Now().UnixMilli()
	if raw, err := json.Marshal(u); err == nil {
		_ = jeanWrite(jeanUsageFile, string(raw))
	}
}

// JeanRead renvoie une fiche entière. Une fiche archivée qu'on rouvre revient
// dans la liste : si on en a de nouveau besoin, c'est qu'elle sert.
func JeanRead(name string) (string, error) { return jeanReadOpt(name, false) }

// jeanReadSilent : lecture pendant une révision ou un ménage. Elle ne compte
// pas comme un usage (les compteurs décident des fiches à archiver ; vu en
// test, le ménage gonflait le compteur de chaque fiche qu'il relisait) et ne
// ressort pas une fiche des archives.
func jeanReadSilent(name string) (string, error) { return jeanReadOpt(name, true) }

func jeanReadOpt(name string, silent bool) (string, error) {
	name = normFicheName(name)
	jeanMu.Lock()
	s, err := jeanRead(ficheFile(name))
	revived := false
	if err == nil && s == "" && !silent {
		if a, aerr := jeanRead(archiveFile(name)); aerr == nil && a != "" {
			if os.Rename(filepath.Join(jeanDir(), archiveFile(name)), filepath.Join(jeanDir(), ficheFile(name))) == nil {
				s, revived = a, true
				jeanMemDirty.Store(true)
				_ = jeanAppendLocked("fiche", fmt.Sprintf("Fiche « %s » ressortie des archives", name))
			}
		}
	}
	jeanMu.Unlock()
	if err != nil {
		return "", err
	}
	if s == "" {
		var names []string
		for _, f := range jeanFiches(false) {
			names = append(names, f.Name)
		}
		if len(names) == 0 {
			return "", fmt.Errorf("fiche « %s » introuvable : aucune fiche enregistrée", name)
		}
		return "", fmt.Errorf("fiche « %s » introuvable (jean_search peut la retrouver sous un autre nom). Fiches : %s", name, strings.Join(names, ", "))
	}
	if !silent {
		jeanNoteFicheUse(name)
	}
	f := parseFiche(name, s)
	out := "# " + f.Name + "\n(when: " + f.When + ")\n\n" + f.Content
	if revived {
		out = "(this fiche was archived as unused; it is back in your list now)\n" + out
	}
	return out, nil
}

// jeanArchiveFiche sort une fiche de la liste sans la perdre.
func jeanArchiveFiche(name string) error {
	name = normFicheName(name)
	jeanMu.Lock()
	defer jeanMu.Unlock()
	p := filepath.Join(jeanDir(), ficheFile(name))
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("fiche « %s » introuvable", name)
	}
	if err := os.Rename(p, filepath.Join(jeanDir(), archiveFile(name))); err != nil {
		return err
	}
	_ = os.Remove(p + ".bak")
	jeanMemDirty.Store(true)
	_ = jeanAppendLocked("fiche", fmt.Sprintf("Fiche « %s » archivée (plus utilisée)", name))
	return nil
}

// jeanArchivedFiches : les fiches archivées, contenu compris.
func jeanArchivedFiches() []jeanFiche {
	ents, _ := os.ReadDir(jeanDir())
	var out []jeanFiche
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, jeanArchivePrefix) || !strings.HasSuffix(n, ".md") {
			continue
		}
		if s, err := jeanRead(n); err == nil {
			out = append(out, parseFiche(strings.TrimSuffix(strings.TrimPrefix(n, jeanArchivePrefix), ".md"), s))
		}
	}
	return out
}

// jeanFicheScore : ordre de la liste injectée. La dernière fois qu'une fiche a
// servi (ou a été écrite), plus un bonus pour celles qui servent souvent.
func jeanFicheScore(f jeanFiche, u *jeanFicheUse) int64 {
	last := f.ModTime
	n := 0
	if u != nil {
		n = u.N
		if u.Last > last {
			last = u.Last
		}
	}
	bonus := int64(n)
	if bonus > 30 {
		bonus = 30
	}
	return last + bonus*24*3600*1000 // une ouverture ≈ un jour d'avance
}

// jeanFicheIndex : la liste injectée dans le bloc Jean memory, à budget fixe.
func jeanFicheIndex() string {
	fs := jeanFiches(false)
	if len(fs) == 0 {
		return ""
	}
	jeanMu.Lock()
	u := jeanUsageLocked()
	jeanMu.Unlock()
	sort.SliceStable(fs, func(i, j int) bool {
		return jeanFicheScore(fs[i], u[fs[i].Name]) > jeanFicheScore(fs[j], u[fs[j].Name])
	})
	var b strings.Builder
	b.WriteString("\nYour fiches (procedures, how-tos, guides): when one matches the request, jean_read it FIRST and follow it.\n")
	used := 0
	for i, f := range fs {
		line := "- " + f.Name + ": " + f.When + "\n"
		if used+len(line) > jeanFicheIndexBudget || i >= jeanFicheIndexLimit {
			fmt.Fprintf(&b, "(+%d less used fiches not listed: jean_search finds them)\n", len(fs)-i)
			break
		}
		b.WriteString(line)
		used += len(line)
	}
	return b.String()
}

// jeanFicheHits : fiches (archivées comprises) qui contiennent au moins la
// moitié des termes (pour jean_search).
func jeanFicheHits(terms []string) []string {
	var out []string
	match := func(f jeanFiche) bool {
		hay := foldSearch(f.Name + " " + f.When + " " + f.Content)
		matched := 0
		for _, t := range terms {
			if strings.Contains(hay, t) {
				matched++
			}
		}
		// Au moins la moitié des mots : une requête bavarde ne doit pas tout rater.
		return matched > 0 && matched*2 >= len(terms)
	}
	for _, f := range jeanFiches(true) {
		if match(f) {
			out = append(out, "- [fiche] "+f.Name+": "+f.When+" (jean_read to open)")
		}
	}
	for _, f := range jeanArchivedFiches() {
		if match(f) {
			out = append(out, "- [fiche archivée] "+f.Name+": "+f.When+" (jean_read brings it back)")
		}
	}
	return out
}

// jeanFicheUsageLine : usage d'une fiche, lisible par le modèle (consolidation).
func jeanFicheUsageLine(name string, u map[string]*jeanFicheUse) string {
	e := u[name]
	if e == nil || e.N == 0 {
		return "never opened"
	}
	days := int(time.Since(time.UnixMilli(e.Last)).Hours() / 24)
	line := fmt.Sprintf("opened %d times, last %d days ago", e.N, days)
	if e.Corrected > 0 {
		line += fmt.Sprintf(", the user corrected you %d times right after following it (check it carefully)", e.Corrected)
	}
	return line
}

// jeanFichesRead : fiches ouvertes (jean_read) pendant un tour.
func jeanFichesRead(extra []Message) []string {
	var out []string
	for _, m := range extra {
		for _, tc := range m.ToolCalls {
			if tc.Function.Name != "jean_read" {
				continue
			}
			var a struct {
				Name string `json:"name"`
			}
			if json.Unmarshal([]byte(tc.Function.Arguments), &a) == nil && a.Name != "" {
				out = append(out, normFicheName(a.Name))
			}
		}
	}
	return out
}

// jeanNoteFicheCorrected : ces fiches ont été suivies juste avant une correction.
func jeanNoteFicheCorrected(names []string) {
	jeanMu.Lock()
	defer jeanMu.Unlock()
	u := jeanUsageLocked()
	for _, n := range names {
		e := u[n]
		if e == nil {
			e = &jeanFicheUse{}
			u[n] = e
		}
		e.Corrected++
	}
	if raw, err := json.Marshal(u); err == nil {
		_ = jeanWrite(jeanUsageFile, string(raw))
	}
}

// jeanToolCallCount : nombre d'appels d'outils d'un tour.
func jeanToolCallCount(extra []Message) int {
	n := 0
	for _, m := range extra {
		n += len(m.ToolCalls)
	}
	return n
}

// jeanSimilarFiche : une fiche existante qui couvre déjà la même demande ?
// Compare les mots importants du nom et du « quand ». Les mots présents dans
// la plupart des fiches (« quand », « demande », le prénom de l'utilisateur…)
// ne comptent pas : ils ne distinguent rien.
func jeanSimilarFiche(name, when string) (jeanFiche, bool) {
	fs := jeanFiches(false)
	terms := func(s string) map[string]bool {
		m := map[string]bool{}
		words := strings.FieldsFunc(foldSearch(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		for _, t := range words {
			if len([]rune(t)) >= 4 {
				m[t] = true
			}
		}
		return m
	}
	df := map[string]int{}
	sets := make([]map[string]bool, len(fs))
	for i, f := range fs {
		sets[i] = terms(f.Name + " " + f.When)
		for t := range sets[i] {
			df[t]++
		}
	}
	common := func(t string) bool { return len(fs) >= 3 && df[t]*2 > len(fs) }
	mine := terms(name + " " + when)
	for i, f := range fs {
		if f.Name == name {
			continue
		}
		shared, a, b := 0, 0, 0
		for t := range mine {
			if common(t) {
				continue
			}
			a++
			if sets[i][t] {
				shared++
			}
		}
		for t := range sets[i] {
			if !common(t) {
				b++
			}
		}
		if lo := min(a, b); lo >= 2 && float64(shared)/float64(lo) >= 0.6 {
			return f, true
		}
	}
	return jeanFiche{}, false
}
