package ajean

// jean_rules.go — les RÈGLES APPLIQUÉES par le harnais, pas par le modèle.
//
// Une leçon dépend de la mémoire du modèle : il y pense presque toujours, pas
// toujours. Certaines règles sont MÉCANIQUES (« jamais de tiret cadratin »,
// « pas de listes numérotées », « 5 éléments max ») : du code peut les tenir
// à 100 %. Jean les déclare avec jean_rule ; le harnais les applique ensuite à
// tout ce qu'il écrit, pendant la diffusion même (aucune latence ajoutée) :
//
//   - replace           : un texte interdit est remplacé à la volée ;
//   - no_numbered_lists : « 1. » en début de ligne devient « - » ;
//   - max_list_items    : ne se corrige pas à la volée (il faudrait réécrire),
//     mais l'infraction est DÉTECTÉE après la réponse et renvoyée à Jean comme
//     une correction urgente : il apprend sans que l'utilisateur ait rien vu à
//     signaler.
//
// Jamais dans les blocs de code (``` … ```) : un script doit rester exact.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	jeanRulesFile = "rules.json"
	jeanRulesMax  = 30
)

type jeanRule struct {
	N    int    `json:"n"`
	Kind string `json:"kind"` // replace | no_numbered_lists | max_list_items
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Max  int    `json:"max,omitempty"`
	Why  string `json:"why,omitempty"`
}

func (r jeanRule) String() string {
	switch r.Kind {
	case "replace":
		to := strings.TrimSpace(r.To)
		if to == "-" {
			to = ","
		}
		return fmt.Sprintf("rule-%d: never write %q, write %q instead", r.N, strings.TrimSpace(r.From), to)
	case "no_numbered_lists":
		return fmt.Sprintf("rule-%d: no numbered lists (numbers are turned into bullets)", r.N)
	case "max_list_items":
		return fmt.Sprintf("rule-%d: at most %d items per list (checked after each answer)", r.N, r.Max)
	}
	return fmt.Sprintf("rule-%d: %s", r.N, r.Kind)
}

func jeanRulesLocked() []jeanRule {
	var rs []jeanRule
	if s, err := jeanRead(jeanRulesFile); err == nil && s != "" {
		_ = json.Unmarshal([]byte(s), &rs)
	}
	return rs
}

func jeanRules() []jeanRule {
	jeanMu.Lock()
	defer jeanMu.Unlock()
	return jeanRulesLocked()
}

// JeanRule (outil jean_rule) : ajoute ou retire une règle appliquée.
func JeanRule(action, kind, from, to string, max int, why string) (string, error) {
	jeanMu.Lock()
	defer jeanMu.Unlock()
	rs := jeanRulesLocked()
	if strings.EqualFold(action, "remove") {
		n := 0
		fmt.Sscanf(strings.TrimPrefix(strings.ToLower(from), "rule-"), "%d", &n)
		for i, r := range rs {
			if r.N == n {
				rs = append(rs[:i], rs[i+1:]...)
				if err := jeanSaveRulesLocked(rs); err != nil {
					return "", err
				}
				_ = jeanAppendLocked("lecon", "Règle appliquée retirée : "+r.String())
				return fmt.Sprintf("[ok] rule-%d retirée", n), nil
			}
		}
		return "", fmt.Errorf("pour retirer : action=remove, from=rule-N (règles : %s)", jeanRulesSummary(rs))
	}
	r := jeanRule{Kind: strings.TrimSpace(kind), From: from, To: to, Max: max, Why: strings.TrimSpace(why)}
	if strings.TrimSpace(r.To) == "-" {
		r.To = "," // un tiret simple à la place d'un tiret long se lit mal ; voir jeanReplaceVariants
	}
	switch r.Kind {
	case "replace":
		if strings.TrimSpace(r.From) == "" {
			return "", fmt.Errorf("replace : 'from' (le texte interdit) est obligatoire")
		}
		if strings.Contains(r.To, r.From) {
			return "", fmt.Errorf("replace : 'to' ne doit pas contenir 'from'")
		}
	case "no_numbered_lists":
	case "max_list_items":
		if r.Max < 1 {
			return "", fmt.Errorf("max_list_items : 'max' doit être un entier ≥ 1")
		}
	default:
		return "", fmt.Errorf("kind inconnu : replace, no_numbered_lists ou max_list_items. Une règle qui ne se vérifie pas mécaniquement va dans jean_lesson")
	}
	next := 1
	for i, o := range rs {
		if o.Kind == r.Kind && o.From == r.From {
			rs[i].To, rs[i].Max, rs[i].Why = r.To, r.Max, r.Why // même règle : mise à jour
			if err := jeanSaveRulesLocked(rs); err != nil {
				return "", err
			}
			return fmt.Sprintf("[ok] règle mise à jour (rule-%d)", o.N), nil
		}
		if o.N >= next {
			next = o.N + 1
		}
	}
	if len(rs) >= jeanRulesMax {
		return "", fmt.Errorf("trop de règles appliquées (%d) : retire-en une", jeanRulesMax)
	}
	r.N = next
	rs = append(rs, r)
	if err := jeanSaveRulesLocked(rs); err != nil {
		return "", err
	}
	_ = jeanAppendLocked("lecon", "Règle appliquée ajoutée : "+r.String())
	return fmt.Sprintf("[ok] %s. Le harnais l'appliquera désormais à tout ce que tu écris.", r.String()), nil
}

func jeanSaveRulesLocked(rs []jeanRule) error {
	raw, err := json.MarshalIndent(rs, "", " ")
	if err != nil {
		return err
	}
	return jeanWrite(jeanRulesFile, string(raw))
}

func jeanRulesSummary(rs []jeanRule) string {
	var p []string
	for _, r := range rs {
		p = append(p, r.String())
	}
	return strings.Join(p, " ; ")
}

// jeanRulesBlock : la section du bloc Jean memory (le modèle sait qu'elles existent).
func jeanRulesBlock() string {
	rs := jeanRules()
	if len(rs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nRules your harness enforces on everything you write (follow them anyway; manage with jean_rule):\n")
	for _, r := range rs {
		b.WriteString(r.String() + "\n")
	}
	return b.String()
}

// ---- application pendant la diffusion -------------------------------------------

var (
	jeanNumItemRe    = regexp.MustCompile(`^(\s*)\d{1,3}[.)]\s`)
	jeanNumPendingRe = regexp.MustCompile(`^\d{1,3}[.)]?$`)
)

// jeanStreamFilter applique les règles à un texte qui arrive par morceaux. Il ne
// retient que le strict nécessaire : le début d'une ligne tant qu'on ne sait pas
// si c'est un élément numéroté, et les quelques derniers octets qui pourraient
// commencer un texte interdit.
type jeanStreamFilter struct {
	rules []jeanRule

	numbered   bool // règle no_numbered_lists active
	pend       string
	prefixDone bool // début de la ligne courante déjà traité
	inFence    bool
}

func newJeanStreamFilter(rs []jeanRule) *jeanStreamFilter {
	f := &jeanStreamFilter{}
	for _, r := range rs {
		switch r.Kind {
		case "replace":
			f.rules = append(f.rules, jeanReplaceVariants(r)...)

		case "no_numbered_lists":
			f.numbered = true
		}
	}
	// Le plus long d'abord : « rien — pas » doit prendre la variante avec espaces.
	sort.SliceStable(f.rules, func(i, j int) bool { return len(f.rules[i].From) > len(f.rules[j].From) })
	if len(f.rules) == 0 && !f.numbered {
		return nil
	}
	return f
}

// Push reçoit un morceau et renvoie ce qui peut déjà partir.
func (f *jeanStreamFilter) Push(s string) string {
	if f == nil {
		return s
	}
	f.pend += s
	var out strings.Builder
	for {
		i := strings.IndexByte(f.pend, '\n')
		if i < 0 {
			break
		}
		line := f.pend[:i]
		f.pend = f.pend[i+1:]
		out.WriteString(f.finishLine(line))
		out.WriteByte('\n')
	}
	out.WriteString(f.partial())
	return out.String()
}

// Flush rend tout ce qui reste (fin de réponse).
func (f *jeanStreamFilter) Flush() string {
	if f == nil || f.pend == "" {
		return ""
	}
	line := f.pend
	f.pend = ""
	return f.finishLine(line)
}

// linePrefix traite le début de ligne : bascule de bloc de code, numérotation.
// ok=false : pas encore assez de texte pour décider.
func (f *jeanStreamFilter) linePrefix(s string, final bool) (string, bool) {
	t := strings.TrimLeft(s, " \t")
	if strings.HasPrefix(t, "```") {
		return s, true
	}
	if !final && len(t) < 3 && strings.Trim(t, "`0123456789.)") == "" {
		return "", false // « 1 », « 12. », « `` » : on attend la suite
	}
	if f.numbered && !f.inFence {
		if m := jeanNumItemRe.FindString(s); m != "" {
			ws := m[:len(m)-len(strings.TrimLeft(m, " \t"))]
			return ws + "- " + s[len(m):], true
		}
		if !final && jeanNumPendingRe.MatchString(t) {
			return "", false // « 10 », « 10) » : l'espace qui suit décidera
		}
	}
	return s, true
}

func (f *jeanStreamFilter) finishLine(line string) string {
	if !f.prefixDone {
		line, _ = f.linePrefix(line, true)
	}
	f.prefixDone = false
	fence := strings.HasPrefix(strings.TrimLeft(line, " \t"), "```")
	if f.inFence || fence {
		if fence {
			f.inFence = !f.inFence
		}
		return line
	}
	return f.replaceAll(line)
}

// partial : morceau de ligne en cours, émis jusqu'au point sûr.
func (f *jeanStreamFilter) partial() string {
	if f.pend == "" {
		return ""
	}
	var out strings.Builder
	if !f.prefixDone {
		if strings.HasPrefix(strings.TrimLeft(f.pend, " \t"), "``") {
			return "" // bloc de code qui s'ouvre ou se ferme : la ligne entière décide
		}
		p, ok := f.linePrefix(f.pend, false)
		if !ok {
			return ""
		}
		f.prefixDone = true
		f.pend = p
	}
	if f.inFence {
		out.WriteString(f.pend)
		f.pend = ""
		return out.String()
	}
	// Remplacements jusqu'au point où un texte interdit pourrait encore commencer.
	i := 0
	for i < len(f.pend) {
		if r, ok := f.matchAt(f.pend[i:]); ok {
			out.WriteString(r.To)
			i += len(r.From)
			continue
		}
		if f.couldStart(f.pend[i:]) {
			break // peut-être le début d'un texte interdit : on attend la suite
		}
		_, sz := utf8.DecodeRuneInString(f.pend[i:])
		out.WriteString(f.pend[i : i+sz])
		i += sz
	}
	f.pend = f.pend[i:]
	return out.String()
}

func (f *jeanStreamFilter) matchAt(s string) (jeanRule, bool) {
	for _, r := range f.rules {
		if strings.HasPrefix(s, r.From) {
			return r, true
		}
	}
	return jeanRule{}, false
}

// couldStart : s (fin du tampon) est-il le début incomplet d'un texte interdit ?
func (f *jeanStreamFilter) couldStart(s string) bool {
	for _, r := range f.rules {
		if len(s) < len(r.From) && strings.HasPrefix(r.From, s) {
			return true
		}
	}
	return false
}

func (f *jeanStreamFilter) replaceAll(s string) string {
	for _, r := range f.rules {
		s = strings.ReplaceAll(s, r.From, r.To)
	}
	return s
}

// jeanApplyRules : version d'un bloc (messages de tâche, textes déjà complets).
func jeanApplyRules(s string) string {
	f := newJeanStreamFilter(jeanRules())
	if f == nil {
		return s
	}
	return f.Push(s) + f.Flush()
}

// ---- vérification après la réponse ----------------------------------------------

var jeanListItemRe = regexp.MustCompile(`^\s*([-*•]|\d{1,3}[.)])\s+\S`)

// jeanRuleViolations : règles enfreintes par une réponse (celles qu'on ne
// corrige pas à la volée). Chaque niveau d'indentation compte comme une liste.
func jeanRuleViolations(text string) []string {
	var out []string
	for _, r := range jeanRules() {
		if r.Kind != "max_list_items" {
			continue
		}
		worst, run, inFence := 0, 0, false
		for _, l := range strings.Split(text, "\n") {
			if strings.HasPrefix(strings.TrimLeft(l, " \t"), "```") {
				inFence = !inFence
				run = 0
				continue
			}
			if inFence {
				continue
			}
			switch {
			case jeanListItemRe.MatchString(l) && !strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "\t"):
				run++
				worst = max(worst, run)
			case strings.TrimSpace(l) == "" || strings.HasPrefix(l, "  ") || strings.HasPrefix(l, "\t"):
				// ligne vide ou sous-élément : la liste continue
			default:
				run = 0
			}
		}
		if worst > r.Max {
			out = append(out, fmt.Sprintf("You broke your enforced %s: your last answer has a list of %d items.", r.String(), worst))
		}
	}
	return out
}

// jeanReplaceVariants : une règle « — → , » doit donner « rien, pas même » et non
// « rien , pas même » ni « rien,pas ». Quand le remplacement est une ponctuation,
// on décline les espaces autour du texte interdit : la ponctuation se colle au
// mot d'avant et garde une espace après. Un tiret simple devient une virgule.
func jeanReplaceVariants(r jeanRule) []jeanRule {
	from := strings.TrimSpace(r.From)
	to := strings.TrimSpace(r.To)
	if to == "-" {
		to = ","
	}
	if from == "" || to == "" || !strings.ContainsRune(",.;:!?", rune(to[0])) {
		return []jeanRule{r}
	}
	mk := func(f, t string) jeanRule { v := r; v.From, v.To = f, t; return v }
	// Tiret simple : il vit aussi DANS les mots, noms de fichiers et liens
	// (capture-2026….png, Saint-Jean). Seul le tiret de ponctuation, entouré
	// d'espaces, est visé. Vu en test : l'image ne s'affichait plus, son nom
	// était devenu « capture, 20261003, 182751.png ».
	if from == "-" {
		return []jeanRule{mk(" - ", to+" ")}
	}
	return []jeanRule{
		mk(" "+from+" ", to+" "),
		mk(" "+from, to),
		mk(from+" ", to+" "),
		mk(from, to+" "),
	}
}
