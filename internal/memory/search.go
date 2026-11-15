package memory

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"ajean/internal/config"
	"ajean/internal/tool"
)

// La recherche dans le journal et les fiches. Trois signaux, fusionnés par
// leurs rangs :
//
//   - les MOTS : couverture des termes, puis rareté ; la fraîcheur ne fait que
//     départager (rien ne s'oublie par l'âge) ;
//   - les JETONS EXACTS : codes d'erreur, noms de fichiers, adresses. Par le
//     sens, « 0x80070005 » et « 0x80004005 » se ressemblent : seule une
//     correspondance exacte retrouve la bonne ligne ;
//   - le SENS, quand un modèle d'embeddings est configuré.

const (
	searchDefault  = 6
	searchMax      = 15
	searchExcerpt  = 600
	searchWait     = 5 * time.Second
	semanticFloor  = 0.2 // en dessous, deux textes n'ont rien à voir
	semanticKeep   = 30
	rrfK           = 60
	exactBonus     = 1.0 / rrfK // un jeton exact vaut une première place
	promoteRecalls = 3          // rappels, sur promoteDays jours distincts, avant de suggérer le profil
	promoteDays    = 2
	recallDaysKept = 10
)

var (
	// Un nombre et son unité ou son rang (20km, 10h30, 2ème) : écrit pareil par
	// beaucoup d'entrées, il n'en distingue aucune.
	measureRe = regexp.MustCompile(`^\d+(?:[.,]\d+)?\p{L}{1,4}\d{0,2}$`)
	hexRe     = regexp.MustCompile(`^0[xX][0-9a-fA-F]{2,}$`)
)

// exactTokens : les jetons de q que seule une correspondance exacte retrouve, repliés.
func exactTokens(q string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(q, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("\"'`«»“”‘’()[]{}<>,;!?", r)
	}) {
		w = strings.TrimRight(w, ".:") // la fin d'une phrase
		if f := fold(w); isExactToken(w) && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func isExactToken(w string) bool {
	if runeLen(w) < 4 {
		return false
	}
	if hexRe.MatchString(w) {
		return true
	}
	if measureRe.MatchString(w) {
		return false
	}
	letters, upper, digits, marks := 0, 0, 0, 0
	for _, r := range w {
		switch {
		case unicode.IsDigit(r):
			digits++
		case unicode.IsLetter(r):
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		case strings.ContainsRune("._/\\", r):
			marks++
		}
	}
	switch {
	case letters > 0 && (digits > 0 || marks > 0): // app.py, go1.25, src/main.go, max_tokens
		return true
	case letters == 0 && digits > 0 && strings.Count(w, ".") >= 2: // 192.168.1.10, 1.25.3
		return true
	}
	return letters >= 5 && upper == letters // ECONNREFUSED, ENOENT
}

// keywordRank classe les entrées qui contiennent au moins un des termes :
// couverture d'abord, puis rareté et fréquence, une note pesant plus qu'un
// échange brut, la fraîcheur en simple départage. Renvoie des indices dans all.
func keywordRank(terms []string, all []entry, now time.Time) []int {
	seenIn := map[string]int{}
	for _, e := range all {
		for _, t := range terms {
			if strings.Contains(e.folded, t) {
				seenIn[t]++
			}
		}
	}
	type hit struct {
		i, matched int
		score      float64
	}
	var hits []hit
	n := float64(len(all))
	for i, e := range all {
		matched, score := 0, 0.0
		for _, t := range terms {
			if c := strings.Count(e.folded, t); c > 0 {
				matched++
				score += (math.Log((n+1)/float64(seenIn[t]+1)) + 1) * (1 + math.Log(float64(c)))
			}
		}
		if matched == 0 {
			continue
		}
		if e.kind == kindNote {
			score *= 1.3 // une note voulue par l'assistant vaut plus qu'un échange brut
		}
		// Départage seulement : au plus +10 % pour une entrée toute fraîche.
		score *= 1 + 0.1*math.Exp(-now.Sub(e.at).Hours()/(24*90))
		hits = append(hits, hit{i, matched, score})
	}
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].matched != hits[b].matched {
			return hits[a].matched > hits[b].matched
		}
		return hits[a].score > hits[b].score
	})
	out := make([]int, len(hits))
	for k, h := range hits {
		out[k] = h.i
	}
	return out
}

// semanticRank : les entrées indexées les plus proches de la requête par le
// sens, la plus proche d'abord. Renvoie des indices dans all.
func semanticRank(q []float32, vecOf func(entry) vec, all []entry) []int {
	cos := map[int]float32{}
	var out []int
	for i, e := range all {
		if c := vecOf(e).cos(q); c >= semanticFloor {
			cos[i] = c
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return cos[out[a]] > cos[out[b]] })
	return out[:min(len(out), semanticKeep)]
}

// fuse fond plusieurs classements en un (reciprocal rank fusion), plus un bonus
// par jeton exact présent. Les meilleures d'abord ; à égalité, la plus récente.
func fuse(all []entry, rankings [][]int, exact []string) []int {
	score := map[int]float64{}
	for _, ranking := range rankings {
		for rank, i := range ranking {
			score[i] += 1 / float64(rrfK+rank+1)
		}
	}
	if len(exact) > 0 {
		for i, e := range all {
			if n := countTerms(exact, e.folded); n > 0 {
				score[i] += exactBonus * float64(n)
			}
		}
	}
	var order []int
	for i := range score {
		order = append(order, i)
	}
	sort.Slice(order, func(a, b int) bool {
		if sa, sb := score[order[a]], score[order[b]]; sa != sb {
			return sa > sb
		}
		return order[a] > order[b]
	})
	return order
}

// recallCount : combien de fois une entrée a été rendue par la recherche, et
// quels jours. Une note qui revient souvent a sa place dans le profil.
type recallCount struct {
	N    int      `json:"n"`
	Days []string `json:"days"`
}

// ficheHitsLocked : les fiches (archives comprises) qui contiennent au moins la
// moitié des termes : une requête bavarde ne doit pas tout rater.
func ficheHitsLocked(terms []string) []string {
	var out []string
	for _, archived := range []bool{false, true} {
		for _, f := range fichesLocked(archived) {
			matched := countTerms(terms, fold(f.Name+" "+f.When+" "+f.Content))
			switch {
			case matched == 0 || matched*2 < len(terms):
			case archived:
				out = append(out, "- [fiche archivée] "+f.Name+" : "+f.When+" (fiche_read la ramène dans ta liste)")
			default:
				out = append(out, "- [fiche] "+f.Name+" : "+f.When+" (fiche_read pour l'ouvrir)")
			}
		}
	}
	return out
}

// search (outil memory_search) : les fiches qui correspondent d'abord (une
// procédure vaut mieux qu'un vieil échange), puis des extraits datés du journal.
func search(query string, limit int) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return tool.Err("requête vide")
	}
	if limit <= 0 || limit > searchMax {
		limit = searchDefault
	}
	terms := searchTerms(query)
	if len(terms) == 0 {
		terms = uniqueTerms(fold(query)) // que des mots courts ou courants : on les garde
	}
	q, model, bySense := embedQuery(context.Background(), query, searchWait)

	mu.Lock()
	defer mu.Unlock()
	all := journalLocked()
	now := time.Now()
	rankings := [][]int{keywordRank(terms, all, now)}
	if bySense {
		rankings = append(rankings, semanticRank(q, vectorsOf(model, all), all))
	}
	order := fuse(all, rankings, exactTokens(query))
	fiches := ficheHitsLocked(terms)
	if len(order) == 0 && len(fiches) == 0 {
		return "[aucun résultat] (essaie d'autres mots, un synonyme, ou un seul mot-clé)"
	}
	order = order[:min(len(order), limit)]

	recalls := map[string]recallCount{}
	// Des compteurs illisibles ne sont pas réécrits : ils repartiraient de zéro.
	countable := readState(statePath("recalls.json"), &recalls) == nil
	loc := config.Location()
	today := now.In(loc).Format("2006-01-02")
	var b strings.Builder
	for _, line := range fiches {
		b.WriteString(line + "\n")
	}
	for _, i := range order {
		e := all[i]
		r := recalls[e.id]
		r.N++
		if len(r.Days) == 0 || r.Days[len(r.Days)-1] != today {
			r.Days = append(r.Days, today)
			r.Days = r.Days[max(0, len(r.Days)-recallDaysKept):]
		}
		recalls[e.id] = r
		fmt.Fprintf(&b, "- [%s · %s] %s\n", e.at.In(loc).Format(entryTimeLayout), e.kind, clip(e.text, searchExcerpt))
		if e.kind != kindProfile && r.N >= promoteRecalls && len(r.Days) >= promoteDays {
			fmt.Fprintf(&b, "  (ressorti %d fois sur %d jours : si c'est un fait durable, mets-le dans le profil avec memory_remember)\n", r.N, len(r.Days))
		}
	}
	if countable {
		_ = writeState(statePath("recalls.json"), recalls)
	}
	return strings.TrimRight(b.String(), "\n")
}
