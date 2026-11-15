package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"ajean/internal/config"
)

// Le rappel automatique : on ne décide pas de se souvenir, le bon souvenir
// revient quand quelque chose y fait penser. Avant chaque réponse, le message
// de l'utilisateur est comparé aux fiches et au journal ; ce qui correspond
// est mis sous les yeux du modèle avec le message. Sans ça, une fiche n'aide
// que si le modèle pense à l'ouvrir, un échange passé que s'il pense à chercher.
//
// Le dosage est serré, car un contexte encombré rend l'assistant moins bon
// qu'un contexte court et juste : RIEN quand rien ne correspond nettement, une
// seule fiche, deux souvenirs courts au plus, sans doublons ni ce qui est déjà
// dans le fil.

const (
	recallFicheChars = 2500 // la fiche qui correspond, en entier jusqu'à ça
	recallEntries    = 2
	recallEntryChars = 250
	recallWait       = 3 * time.Second
	// Seuils de cosinus. Le but d'une fiche qui sert la demande ou un échange
	// sur le même sujet dépassent 0,3 ; des textes sans rapport restent sous 0,25.
	recallFicheFloor  = 0.30
	recallFollowFloor = 0.45 // au-dessus, la fiche est donnée en entier
	recallEntryFloor  = 0.32
	recallBand        = 0.10 // au-delà du seuil, seulement ce qui est proche du meilleur
	recallSame        = 0.92 // deux entrées aussi proches disent la même chose
	recallHead        = "\n\n[Recalled from memory for this message. Follow the fiche if it fits the request; ignore what does not apply.]\n"
)

// Recall : ce que la mémoire a de pertinent pour ce message ("" si rien de
// net). inContext : les messages de l'utilisateur encore dans le contexte du
// modèle ; un échange du journal qui correspond à l'un d'eux est déjà sous ses
// yeux et n'est pas rappelé.
func Recall(ctx context.Context, userText string, inContext []string) string {
	text := strings.TrimSpace(userText)
	if len(strings.Fields(text)) < 3 {
		return "" // « ok », « merci beaucoup » : rien à rappeler
	}
	terms := searchTerms(text)
	visible := map[string]bool{}
	for _, u := range inContext {
		visible[fold(squash(exchangeUserPart(u)))] = true
	}
	mu.Lock()
	fiches := fichesLocked(false)
	var past []entry
	for _, e := range journalLocked() {
		if e.kind == kindNote || e.kind == kindExchange && !visible[exchangeKey(e)] {
			past = append(past, e)
		}
	}
	mu.Unlock()

	var top *fiche
	full := false
	fichesJudged := false
	vecOf := func(entry) vec { return nil }
	var order []int
	ctx, cancel := context.WithTimeout(ctx, recallWait)
	defer cancel()
	q, model, bySense := embedQuery(ctx, text, recallWait)
	if bySense {
		vecOf = vectorsOf(model, past)
		order = recallBySense(q, vecOf, past, terms, exactTokens(text))
		if purposes, ok := purposeVectors(ctx, fiches); ok {
			var cos float32
			top, cos = ficheBySense(q, purposes, fiches)
			full, fichesJudged = cos >= recallFollowFloor, true
		}
	} else {
		order = recallByWords(past, terms, exactTokens(text))
	}
	if !fichesJudged {
		top, full = ficheByWords(terms, fiches)
	}

	var b strings.Builder
	switch {
	case top == nil:
	case full:
		content := top.Content
		if runeLen(content) > recallFicheChars {
			// Coupée : le dire, sinon le modèle croit tenir toute la procédure.
			content = string([]rune(content)[:recallFicheChars]) + "\n[… fiche cut here: fiche_read " + top.Name + " for the rest]"
		}
		fmt.Fprintf(&b, "Fiche %s (when: %s):\n%s\n", top.Name, top.When, content)
		// Une fiche donnée en entier a servi, comme si le modèle l'avait ouverte.
		mu.Lock()
		noteUseLocked(top.Name)
		mu.Unlock()
	default:
		fmt.Fprintf(&b, "Maybe relevant: fiche %s (when: %s), fiche_read to open it.\n", top.Name, top.When)
	}

	var picked []int
	var chosen []entry
	for _, i := range order {
		if len(picked) == recallEntries {
			break
		}
		if !saidBy(past[i], chosen, vecOf) {
			picked, chosen = append(picked, i), append(chosen, past[i])
		}
	}
	// Choisis par pertinence, montrés du plus récent au plus ancien : un fait
	// dit en mars et changé en juin ne doit pas passer pour l'actuel. L'ordre
	// est celui du journal : ses dates, à la minute, ne départagent pas tout.
	sort.Sort(sort.Reverse(sort.IntSlice(picked)))
	if len(picked) > 0 {
		b.WriteString("From your journal (newest first; an older line that disagrees with a newer one, your profile or your lessons is outdated):\n")
	}
	loc := config.Location()
	for _, i := range picked {
		e := past[i]
		fmt.Fprintf(&b, "- [%s · %s] %s\n", e.at.In(loc).Format("2006-01-02"), e.kind, clip(squash(e.text), recallEntryChars))
	}
	if b.Len() == 0 {
		return ""
	}
	return recallHead + strings.TrimRight(b.String(), "\n")
}

// recallNeed : le nombre de termes de la demande qu'un texte doit couvrir pour
// compter. Un seul mot en commun ne dit rien.
func recallNeed(terms []string) int { return max(2, (len(terms)+2)/3) }

func covers(terms []string, folded string) bool {
	return len(terms) > 0 && countTerms(terms, folded) >= recallNeed(terms)
}

// recallByWords : les entrées qui couvrent assez de termes ou portent un jeton
// exact de la demande, les meilleures d'abord (indices dans past).
func recallByWords(past []entry, terms, exact []string) []int {
	var covering []int
	for i, e := range past {
		if covers(terms, e.folded) {
			covering = append(covering, i)
		}
	}
	sort.SliceStable(covering, func(a, b int) bool {
		return countTerms(terms, past[covering[a]].folded) > countTerms(terms, past[covering[b]].folded)
	})
	return fuse(past, [][]int{covering}, exact)
}

// recallBySense : les entrées proches de la demande par le sens (au-dessus du
// seuil et près de la meilleure). Celles qui n'ont pas encore de vecteur sont
// jugées par les mots : une entrée toute fraîche ne doit pas être invisible.
func recallBySense(q []float32, vecOf func(entry) vec, past []entry, terms, exact []string) []int {
	cos := make([]float32, len(past))
	var best float32
	var unindexed []entry
	back := map[int]int{} // indice dans unindexed → indice dans past
	for i, e := range past {
		if v := vecOf(e); v != nil {
			cos[i] = v.cos(q)
			best = max(best, cos[i])
		} else {
			back[len(unindexed)] = i
			unindexed = append(unindexed, e)
		}
	}
	var near []int
	for i, c := range cos {
		if c >= recallEntryFloor && c >= best-recallBand {
			near = append(near, i)
		}
	}
	sort.SliceStable(near, func(a, b int) bool { return cos[near[a]] > cos[near[b]] })
	var byWords []int
	for _, k := range recallByWords(unindexed, terms, nil) {
		byWords = append(byWords, back[k])
	}
	return fuse(past, [][]int{near, byWords}, exact)
}

// ficheBySense : la fiche dont le but est le plus proche de la demande, si
// elle passe le seuil (nil sinon), et son cosinus.
func ficheBySense(q []float32, purposes map[string][]float32, fs []fiche) (*fiche, float32) {
	var top *fiche
	var best float32
	for i, f := range fs {
		if c := dot(purposes[f.Name], q); c >= recallFicheFloor && c > best {
			top, best = &fs[i], c
		}
	}
	return top, best
}

// ficheByWords : le repli par mots. La demande doit correspondre au BUT de la
// fiche (nom, « quand »), pas seulement à des mots de ses étapes. full : le
// but couvre à lui seul assez de termes, la fiche vaut d'être donnée en entier.
func ficheByWords(terms []string, fs []fiche) (top *fiche, full bool) {
	if len(terms) == 0 {
		return nil, false
	}
	need := recallNeed(terms)
	best := 0
	for i, f := range fs {
		purpose := countTerms(terms, fold(f.Name+" "+f.When))
		all := countTerms(terms, fold(f.Name+" "+f.When+" "+f.Content))
		if score := all + 2*purpose; purpose > 0 && all >= need && score > best {
			top, best, full = &fs[i], score, purpose >= need
		}
	}
	return top, full
}

// saidBy : e dit-elle la même chose qu'une des entrées déjà choisies ? Par
// leurs vecteurs quand les deux sont indexées, sinon par leur texte.
func saidBy(e entry, chosen []entry, vecOf func(entry) vec) bool {
	for _, c := range chosen {
		if a, b := vecOf(e), vecOf(c); a != nil && b != nil {
			if a.cos(b.float()) > recallSame {
				return true
			}
			continue
		}
		if squash(e.folded) == squash(c.folded) {
			return true
		}
	}
	return false
}
