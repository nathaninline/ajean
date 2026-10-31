package ajean

// jean_recall.go — le RAPPEL : on ne décide pas de se souvenir, le bon souvenir
// revient tout seul quand quelque chose y fait penser. Avant chaque tour Jean,
// le message de l'utilisateur est comparé aux fiches et au journal ; ce qui
// correspond est mis sous les yeux du modèle avec le message. Sans ça, une fiche
// n'aidait que si le modèle pensait à l'ouvrir, et un échange passé que s'il
// pensait à chercher : un petit modèle y pense rarement.
//
// LE CONTEXTE EST ROI : un contexte encombré rend l'agent moins bon qu'un
// contexte court et juste. Le rappel est donc dosé serré : RIEN quand rien ne
// correspond nettement (seuils mesurés avec EmbeddingGemma), une seule fiche en
// entier et seulement si elle correspond fortement, deux souvenirs courts au plus,
// sans doublons ni ce qui est déjà dans le fil. Le bloc reste attaché à son
// message (le cache de llama.cpp le garde tel quel aux tours suivants).

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	jeanRecallFicheChars = 2500 // la fiche qui correspond, en entier jusqu'à ça
	jeanRecallPast       = 2    // souvenirs du journal (échanges, notes, correctifs)
	jeanRecallPastChars  = 250
	// Mesuré avec EmbeddingGemma sur des demandes en français : le but d'une
	// fiche obtenait 0,29 à 0,66 pour les demandes qu'elle sert et au plus 0,21
	// pour les autres ; un échange passé 0,31 à 0,60 contre au plus 0,28.
	jeanRecallFicheFloor  = 0.30
	jeanRecallFollowFloor = 0.45 // au-dessus : la fiche visée, donnée en entier et jugée par le message suivant
	jeanRecallPastFloor   = 0.32
	jeanRecallBand        = 0.10 // au-delà du seuil, seulement ce qui est proche du meilleur
	jeanRecallSame        = 0.92 // deux entrées aussi proches disent la même chose
	// Fichiers du dossier de Jean (jean_files.go) : le seuil des souvenirs, deux
	// fichiers au plus, leur meilleur passage en court.
	jeanRecallFileFloor = jeanRecallPastFloor
	jeanRecallFiles     = 2
	jeanRecallWait      = 3 * time.Second
	jeanRecallMark      = "\n\n[Recalled from memory for this message"
)

// jeanRecalled : les fiches données en entier au dernier tour, jugées comme si
// Jean les avait ouvertes (le message suivant les crédite ou les corrige).
var jeanRecalled struct {
	mu    sync.Mutex
	names []string
}

func jeanTakeRecalled() []string {
	jeanRecalled.mu.Lock()
	defer jeanRecalled.mu.Unlock()
	n := jeanRecalled.names
	jeanRecalled.names = nil
	return n
}

// jeanFicheVecs : les vecteurs du BUT des fiches (nom et ligne « quand »), en mémoire.
var jeanFicheVecs = struct {
	sync.Mutex
	m map[string][]float32
}{m: map[string][]float32{}}

func jeanFichePurpose(f jeanFiche) string { return "title: " + f.Name + " | text: " + f.When }

// jeanCarryMark ouvre la reprise du dernier échange après une pause.
const jeanCarryMark = "[Before the pause, the last exchange was. "

// jeanCarryNote : le dernier échange du contexte qu'on vient de vider, en court
// ("" s'il n'y en a pas).
func jeanCarryNote(old []Message) string {
	user := clipRunes(squashSpaces(jeanTurnUserText(old)), 300)
	if user == "" {
		return ""
	}
	answer := ""
	for i := len(old) - 1; i >= 0; i-- {
		if old[i].Role == "assistant" {
			if t := strings.TrimSpace(msgText(old[i])); t != "" && t != "null" {
				answer = clipRunes(squashSpaces(t), 300)
				break
			}
		}
		if old[i].Role == "user" && !jeanHarnessUserMsg(lastUserText(old[i:i+1])) {
			break // pas de réponse après le dernier message : seulement la question
		}
	}
	note := jeanCarryMark + "User: " + user
	if answer != "" {
		note += " | Jean: " + answer
	}
	return note + "]\n"
}

// jeanStripCarry retire la reprise du dernier échange en tête de s.
func jeanStripCarry(s string) string {
	if strings.HasPrefix(s, jeanCarryMark) {
		if i := strings.Index(s, "]\n"); i > 0 {
			return s[i+2:]
		}
	}
	return s
}

// jeanContextSince : depuis quand le contexte actuel du modèle court (dernier
// vidage, sinon début du fil). fresh : le prochain message part d'un contexte
// vide, tout le journal est alors du passé.
func jeanContextSince(log []LogEvent, fresh bool) time.Time {
	if fresh || len(log) == 0 {
		return time.Now()
	}
	for i := len(log) - 1; i >= 0; i-- {
		if _, ok := log[i].Delta["ctx_cleared"]; ok {
			return time.UnixMilli(log[i].TS)
		}
	}
	return time.UnixMilli(log[0].TS)
}

// jeanRecallFor : le bloc de mémoire à joindre au message text ("" si rien ne
// correspond nettement). Les entrées du journal postérieures à since sont déjà
// dans le fil : laissées de côté.
func jeanRecallFor(text string, since time.Time) string {
	jeanRecalled.mu.Lock()
	jeanRecalled.names = nil // ce tour ne juge que ce qu'il a lui-même rappelé
	jeanRecalled.mu.Unlock()
	clean := jeanCleanUserText(text)
	if len(strings.Fields(clean)) < 3 {
		return "" // « ok », « merci beaucoup » : rien à rappeler
	}
	terms := jeanSearchTerms(clean)
	exact := jeanExactTokens(clean)
	from := since.Truncate(time.Minute) // le journal est daté à la minute
	fiches := jeanFiches(true)
	jeanMu.Lock()
	var past []jeanEntry
	for _, e := range jeanJournalAll() {
		switch e.Kind {
		case "echange", "note", jeanFixKind:
			if e.When.Before(from) {
				past = append(past, e)
			}
		}
	}
	jeanMu.Unlock()

	var top []jeanFiche
	var topCos float32
	var order []int
	var vecs map[string][]float32
	var files []jeanFileHit
	meaning := false
	if jeanEmbedEnabled() {
		ctx, cancel := context.WithTimeout(context.Background(), jeanRecallWait)
		vs, err := jeanEmbed(ctx, []string{jeanEmbedQueryText(clean)})
		cancel()
		if err == nil {
			q := vs[0]
			files = jeanFileHits(q, jeanRecallFileFloor, jeanRecallBand, jeanRecallFiles)
			top, topCos, meaning = jeanFichesByMeaning(q, fiches)
			vecs = jeanJournalVecs()
			cos := make([]float32, len(past))
			var near []int
			best := float32(0)
			for i, e := range past {
				if v, ok := vecs[e.ID]; ok {
					cos[i] = dotVec(q, v)
					best = max(best, cos[i])
				} else {
					cos[i] = -1 // pas encore indexée : les mots jugent
				}
			}
			for i, c := range cos {
				if c >= jeanRecallPastFloor && c >= best-jeanRecallBand {
					near = append(near, i)
				}
			}
			sort.SliceStable(near, func(a, b int) bool { return cos[near[a]] > cos[near[b]] })
			in := map[int]bool{}
			for _, i := range near {
				in[i] = true
			}
			var unindexed []int
			for i, c := range cos {
				if c < 0 {
					unindexed = append(unindexed, i)
				}
			}
			order = jeanFuse(past, [][]int{near, jeanRecallByWords(terms, past, unindexed)}, exact, func(i, n int) bool {
				return in[i] || n > 0 || cos[i] < 0 && jeanRecallCovers(terms, past[i].Text)
			})
		}
	}
	if !meaning {
		every := make([]int, len(past))
		for i := range every {
			every[i] = i
		}
		order = jeanFuse(past, [][]int{jeanRecallByWords(terms, past, every)}, exact, func(i, n int) bool {
			return n > 0 || jeanRecallCovers(terms, past[i].Text)
		})
		top = jeanFichesByWords(terms, fiches)
	}

	var b strings.Builder
	var followed []string
	if len(top) > 0 {
		f := top[0]
		if !meaning || topCos >= jeanRecallFollowFloor {
			content := f.Content
			if len([]rune(content)) > jeanRecallFicheChars {
				// Coupée : le dire, sinon le modèle croit tenir toute la procédure.
				content = string([]rune(content)[:jeanRecallFicheChars]) + "\n[… fiche coupée ici : jean_read " + f.Name + " pour la suite]"
			}
			fmt.Fprintf(&b, "Fiche %s (when: %s):\n%s\n", f.Name, f.When, content)
			followed = []string{f.Name}
		} else {
			fmt.Fprintf(&b, "Maybe relevant: fiche %s (when: %s), jean_read to open.\n", f.Name, f.When)
		}
	}
	var shown []int
	for _, i := range order {
		if len(shown) >= jeanRecallPast {
			break
		}
		if !jeanRecallSameAs(past, vecs, i, shown) {
			shown = append(shown, i)
		}
	}
	// Choisis par pertinence, montrés du plus récent au plus ancien : un fait dit
	// en mars et changé en juin ne doit pas passer pour l'actuel.
	sort.SliceStable(shown, func(a, b int) bool { return past[shown[a]].When.After(past[shown[b]].When) })
	userLoc := loc(defaultTaskTZ())
	for k, i := range shown {
		if k == 0 {
			b.WriteString("From your journal (newest first; an older line that disagrees with a newer one, your profile or your lessons is outdated):\n")
		}
		e := past[i]
		fmt.Fprintf(&b, "- [%s · %s] %s\n", e.When.In(userLoc).Format("2006-01-02"), e.Kind, clipRunes(squashSpaces(e.Text), jeanRecallPastChars))
	}
	if len(files) > 0 {
		b.WriteString("From your files (paths in your workspace):\n" + jeanFormatFileHits(files, jeanRecallPastChars))
	}
	if b.Len() == 0 {
		return ""
	}
	jeanRecalled.mu.Lock()
	jeanRecalled.names = followed
	jeanRecalled.mu.Unlock()
	return jeanRecallMark + ". Follow the fiche if it fits the request; ignore what does not apply.]\n" + strings.TrimRight(b.String(), "\n")
}

// jeanFichesByMeaning : les fiches dont le BUT est proche du message, la
// meilleure d'abord. ok=false quand une fiche n'a pu être vectorisée à temps :
// les mots jugent alors toutes les fiches, plutôt que d'en laisser une de côté.
func jeanFichesByMeaning(q []float32, fiches []jeanFiche) (_ []jeanFiche, top float32, ok bool) {
	jeanFicheVecs.Lock()
	defer jeanFicheVecs.Unlock()
	var missing []string
	for _, f := range fiches {
		if p := jeanFichePurpose(f); jeanFicheVecs.m[p] == nil {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), jeanRecallWait)
		vs, err := jeanEmbed(ctx, missing)
		cancel()
		if err != nil {
			return nil, 0, false
		}
		k := 0
		for _, f := range fiches {
			if p := jeanFichePurpose(f); jeanFicheVecs.m[p] == nil {
				jeanFicheVecs.m[p] = vs[k]
				k++
			}
		}
	}
	cos := map[string]float32{}
	var out []jeanFiche
	for _, f := range fiches {
		if v := jeanFicheVecs.m[jeanFichePurpose(f)]; v != nil {
			if c := dotVec(q, v); c >= jeanRecallFicheFloor {
				cos[f.Name] = c
				out = append(out, f)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return cos[out[i].Name] > cos[out[j].Name] })
	if len(out) > 0 {
		top = cos[out[0].Name]
	}
	for k, f := range out {
		if cos[f.Name] < top-jeanRecallBand {
			return out[:k], top, true
		}
	}
	return out, top, true
}

// jeanRecallNeed : le nombre de mots de la requête qu'une entrée doit couvrir.
func jeanRecallNeed(terms []string) int { return max(2, (len(terms)+2)/3) }

func jeanRecallCount(terms []string, text string) int {
	f, n := foldSearch(text), 0
	for _, t := range terms {
		if strings.Contains(f, t) {
			n++
		}
	}
	return n
}

// jeanRecallCovers : l'entrée couvre-t-elle assez de mots de la requête ?
func jeanRecallCovers(terms []string, text string) bool {
	return len(terms) > 0 && jeanRecallCount(terms, text) >= jeanRecallNeed(terms)
}

// jeanRecallByWords : les entrées past[idx] qui couvrent assez de mots, les
// mieux couvertes d'abord (index dans past).
func jeanRecallByWords(terms []string, past []jeanEntry, idx []int) []int {
	var out []int
	for _, i := range idx {
		if jeanRecallCovers(terms, past[i].Text) {
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		return jeanRecallCount(terms, past[out[a]].Text) > jeanRecallCount(terms, past[out[b]].Text)
	})
	return out
}

// jeanFichesByWords : le repli par mots. La demande doit correspondre au BUT de
// la fiche (nom, « quand »), pas seulement à des mots de ses étapes.
func jeanFichesByWords(terms []string, fiches []jeanFiche) []jeanFiche {
	if len(terms) == 0 {
		return nil
	}
	need := jeanRecallNeed(terms)
	score := map[string]int{}
	var out []jeanFiche
	for _, f := range fiches {
		purpose := jeanRecallCount(terms, f.Name+" "+f.When)
		if all := jeanRecallCount(terms, f.Name+" "+f.When+" "+f.Content); purpose > 0 && all >= need {
			score[f.Name] = all + 2*purpose
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return score[out[i].Name] > score[out[j].Name] })
	return out
}

// jeanRecallSameAs : past[i] dit-il la même chose qu'une des entrées choisies ?
// Par leurs vecteurs quand les deux sont indexées, sinon par leur texte.
func jeanRecallSameAs(past []jeanEntry, vecs map[string][]float32, i int, chosen []int) bool {
	for _, j := range chosen {
		a, b := vecs[past[i].ID], vecs[past[j].ID]
		if a != nil && b != nil {
			if dotVec(a, b) > jeanRecallSame {
				return true
			}
			continue
		}
		if foldSearch(squashSpaces(past[i].Text)) == foldSearch(squashSpaces(past[j].Text)) {
			return true
		}
	}
	return false
}
