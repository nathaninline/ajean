package ajean

// Garde-fous mécaniques de la mémoire de Jean. Les consignes ne suffisent pas
// avec un petit modèle : vu en test, il a mis mots de passe et clé d'API dans le
// profil (injecté à CHAQUE tour), rangé un piège « films » dans la fiche « API »,
// recopié une leçon déjà présente dans une fiche, et le journal notait
// « Utilisateur : Image : » (le message d'image injecté par le harnais).

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Un secret : mot-clé d'identifiant suivi d'une valeur, ou une longue suite
// hexadécimale / base64 (clé d'API, jeton).
var (
	jeanSecretWordRe = regexp.MustCompile(`(?i)\b(mdp|mot de passe|password|passwd|pwd|identifiants?|credentials?|api[ _-]?key|cl[eé] (d')?api|token|jeton|secret)\b`)
	jeanSecretBlobRe = regexp.MustCompile(`\b[A-Za-z0-9_\-]{32,}\b`)
)

// jeanLooksSecret : la valeur contient-elle un identifiant ou une clé ?
func jeanLooksSecret(v string) bool {
	for _, b := range jeanSecretBlobRe.FindAllString(v, -1) {
		// Une clé mêle chiffres et lettres ; « xxxx… » ou un long mot n'en est pas une.
		if strings.ContainsAny(b, "0123456789") && strings.IndexFunc(b, func(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }) >= 0 {
			return true
		}
	}
	// « mdp X », « password: X » : un mot-clé ne suffit pas (« identifiants dans
	// la fiche media-server » est justement ce qu'on veut), il faut une valeur.
	for _, loc := range jeanSecretWordRe.FindAllStringIndex(v, -1) {
		rest := strings.TrimLeft(v[loc[1]:], " :=")
		w := strings.Fields(rest)
		if len(w) == 0 {
			continue
		}
		first := strings.Trim(foldSearch(w[0]), ".,;()")
		switch first {
		case "dans", "voir", "in", "see", "fiche", "a", "à", "en", "sur", "du", "de", "des", "le", "la", "les", "pas", "non", "inconnu", "unknown":
			continue
		}
		return true
	}
	return false
}

// jeanCleanUserText : le texte de l'utilisateur tel qu'il l'a écrit, sans les
// notes du harnais (pièces jointes) : pour le journal et la révision.
func jeanCleanUserText(s string) string {
	s = strings.TrimSpace(jeanStripNow(strings.TrimSpace(s)))
	if strings.HasPrefix(s, "Image jointe à ce message") || strings.HasPrefix(s, "Images jointes à ce message") {
		lines := strings.Split(s, "\n")
		var names []string
		i := 1
		for ; i < len(lines) && strings.HasPrefix(lines[i], "- "); i++ {
			p := strings.TrimPrefix(lines[i], "- ")
			if j := strings.LastIndex(p, " ("); j > 0 {
				p = p[:j]
			}
			if j := strings.LastIndexAny(p, `/\`); j >= 0 {
				p = p[j+1:]
			}
			names = append(names, p)
		}
		rest := strings.TrimSpace(jeanStripNow(strings.TrimSpace(strings.Join(lines[i:], "\n"))))
		s = "[image : " + strings.Join(names, ", ") + "]"
		if rest != "" {
			s += " " + rest
		}
	}
	return s
}

// jeanHarnessUserMsg : message « user » injecté par le harnais (image demandée
// par see_image ou capture du navigateur), pas écrit par l'utilisateur.
func jeanHarnessUserMsg(t string) bool {
	t = strings.TrimSpace(t)
	return t == "Image :" || (strings.HasPrefix(t, "Image demandée (") && strings.HasSuffix(t, ") :"))
}

// jeanTurnUserText : dernier message vraiment écrit par l'utilisateur.
func jeanTurnUserText(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		t := lastUserText(msgs[i : i+1])
		if jeanHarnessUserMsg(t) {
			continue
		}
		return jeanCleanUserText(t)
	}
	return ""
}

// --- pertinence et doublons des leçons ---

var jeanWordRe = regexp.MustCompile(`[a-z0-9]{4,}`)

func jeanWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range jeanWordRe.FindAllString(foldSearch(s), -1) {
		out[w] = true
	}
	return out
}

// jeanSimilar : part des mots de a présents dans b (0..1).
func jeanSimilar(a, b map[string]bool) float64 {
	if len(a) == 0 {
		return 0
	}
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return float64(n) / float64(len(a))
}

// jeanDupInFiches : fiche dont une ligne dit déjà la même chose que text.
func jeanDupInFiches(text string) string {
	tw := jeanWords(text)
	if len(tw) < 4 {
		return ""
	}
	for _, f := range jeanFiches(true) {
		for _, line := range strings.Split(f.Content, "\n") {
			lw := jeanWords(line)
			if len(lw) >= 4 && jeanSimilar(tw, lw) >= 0.7 && jeanSimilar(lw, tw) >= 0.5 {
				return f.Name
			}
		}
	}
	return ""
}

// jeanBetterFiche : une autre fiche colle-t-elle nettement mieux au sujet du
// piège que celle visée ? On compare le texte au nom + « when » de chaque fiche,
// sans les mots communs à la plupart des fiches (« nathan », « demande »).
func jeanBetterFiche(target, text string) string {
	fs := jeanFiches(false)
	if len(fs) < 2 {
		return ""
	}
	desc := map[string]map[string]bool{}
	df := map[string]int{}
	for _, f := range fs {
		w := jeanWords(strings.ReplaceAll(f.Name, "-", " ") + " " + f.When)
		desc[f.Name] = w
		for k := range w {
			df[k]++
		}
	}
	tw := jeanWords(text)
	score := func(name string) int {
		n := 0
		for k := range desc[name] {
			if tw[k] && df[k]*2 <= len(fs) {
				n++
			}
		}
		return n
	}
	type sc struct {
		name string
		n    int
	}
	var all []sc
	for _, f := range fs {
		all = append(all, sc{f.Name, score(f.Name)})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].n > all[j].n })
	if all[0].name != target && all[0].n > score(target) {
		return all[0].name
	}
	return ""
}

// Pièges refusés une fois pour mauvaise fiche : redemandés à l'identique, ils
// passent (le modèle a confirmé son choix).
var (
	jeanPitfallConfirm   = map[string]bool{}
	jeanPitfallConfirmMu sync.Mutex
)

// jeanCheckPitfall : "" si le piège peut aller dans la fiche, sinon le refus.
// Appelé jeanMu NON tenu (jeanFiches lit le dossier).
func jeanCheckPitfall(name, text string) string {
	if d := jeanDupInFiches(text); d != "" {
		return fmt.Sprintf("[ok] déjà dit dans la fiche « %s » : rien ajouté", d)
	}
	key := name + "\x00" + foldSearch(text)
	b := jeanBetterFiche(name, text)
	jeanPitfallConfirmMu.Lock()
	defer jeanPitfallConfirmMu.Unlock()
	if b != "" && !jeanPitfallConfirm[key] {
		jeanPitfallConfirm[key] = true
		return fmt.Sprintf("[refusé] ce piège semble concerner la fiche « %s » plutôt que « %s ». Rappelle jean_lesson avec fiche=%s ; si « %s » est vraiment la bonne, refais le même appel.", b, name, b, name)
	}
	return ""
}

// jeanVisibleAnswer : la réponse que l'utilisateur a lue. Le texte écrit avant
// un appel d'outil est affiché mais rangé dans le message porteur des
// tool_calls : ne journaliser que la fin perdait la vraie réponse quand Jean
// répondait PUIS rangeait sa mémoire (journal : « J'ai aussi gardé le script… »).
// On garde le plus long bloc du tour (la réponse), suivi de la fin s'il diffère ;
// les petites phrases de suivi (« je filtre… ») restent hors du journal.
func jeanVisibleAnswer(extra []Message, final string) string {
	final = strings.TrimSpace(final)
	best := ""
	for _, m := range extra {
		if m.Role == "assistant" {
			if t := strings.TrimSpace(msgText(m)); len(t) > len(best) && t != "null" {
				best = t
			}
		}
	}
	if len(best) <= len(final) {
		return final
	}
	if final == "" {
		return best
	}
	return best + "\n\n" + final
}

// jeanAnswerThenSave : le message porte une vraie réponse (texte assez long) et
// ses seuls outils sont des écritures de mémoire, toutes réussies. Une écriture
// refusée laisse le tour continuer : le modèle doit pouvoir la corriger.
func jeanAnswerThenSave(assistant Message, results []Message) bool {
	if len([]rune(strings.TrimSpace(msgText(assistant)))) < 150 || len(assistant.ToolCalls) == 0 {
		return false
	}
	for _, tc := range assistant.ToolCalls {
		switch tc.Function.Name {
		case "jean_save", "jean_patch", "jean_lesson", "jean_remember", "jean_note", "jean_rule", "jean_forget":
		default:
			return false
		}
	}
	for _, m := range results {
		if m.Role != "tool" {
			continue
		}
		t := strings.TrimSpace(msgText(m))
		if strings.HasPrefix(t, "[erreur]") || strings.HasPrefix(t, "[refusé]") {
			return false
		}
	}
	return true
}
