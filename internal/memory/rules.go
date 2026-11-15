package memory

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"ajean/internal/home"
	"ajean/internal/tool"
)

// Les règles d'écriture, APPLIQUÉES par le programme. Une leçon dépend de
// l'attention du modèle : il la suit presque toujours, pas toujours. Un
// remplacement mécanique (« jamais de tiret cadratin », « "courriel", pas
// "mail" ») peut être tenu à 100 % par du code : le texte interdit est remplacé
// dans tout ce que l'assistant écrit, pendant la diffusion même.

const (
	rulesMax     = 40
	ruleMaxChars = 80
	rulesHead    = "# Règles d'écriture\n\n"
	rulePrefix   = "regle-"
)

// Rule : un remplacement mécanique.
type Rule struct {
	N    int    `json:"n"`
	From string `json:"from"`
	To   string `json:"to"`
	Why  string `json:"why"`
}

// describe : la règle telle qu'on la montre au modèle.
func (r Rule) describe() string {
	s := fmt.Sprintf("%s%d: %s -> %s", rulePrefix, r.N, strconv.Quote(r.From), strconv.Quote(r.To))
	if r.Why != "" {
		s += " (" + r.Why + ")"
	}
	return s
}

func parseRules(s string) []Rule {
	out := []Rule{}
	for _, l := range strings.Split(s, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimRight(l, "\r"), "- ["+rulePrefix)
		if !ok {
			continue
		}
		num, rest, ok := strings.Cut(rest, "] ")
		n, err := strconv.Atoi(num)
		if !ok || err != nil {
			continue
		}
		fromQ, err := strconv.QuotedPrefix(rest)
		if err != nil {
			continue
		}
		rest, ok = strings.CutPrefix(rest[len(fromQ):], " -> ")
		if !ok {
			continue
		}
		toQ, err := strconv.QuotedPrefix(rest)
		if err != nil {
			continue
		}
		r := Rule{N: n}
		r.From, _ = strconv.Unquote(fromQ)
		r.To, _ = strconv.Unquote(toQ)
		if why, ok := strings.CutPrefix(rest[len(toQ):], " # "); ok {
			r.Why = strings.TrimSpace(why)
		}
		// Une règle au texte vide remplacerait entre chaque caractère.
		if r.From != "" {
			out = append(out, r)
		}
	}
	return out
}

func renderRules(rs []Rule) string {
	var b strings.Builder
	b.WriteString(rulesHead)
	for _, r := range rs {
		fmt.Fprintf(&b, "- [%s%d] %s -> %s", rulePrefix, r.N, strconv.Quote(r.From), strconv.Quote(r.To))
		if r.Why != "" {
			b.WriteString(" # " + r.Why)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// rulesLocked lit les règles. Une lecture ratée est une erreur, pas une liste
// vide : réécrire à partir d'elle effacerait toutes les règles.
func rulesLocked() ([]Rule, error) {
	s, err := readFile(memPath(rulesFile))
	if err != nil {
		return nil, fmt.Errorf("règles illisibles : %w", err)
	}
	return parseRules(s), nil
}

func writeRulesLocked(rs []Rule) error {
	return home.WriteFile(memPath(rulesFile), []byte(renderRules(rs)))
}

// Rules : les règles d'écriture, dans l'ordre où elles s'appliquent (aucune si
// le fichier est illisible : mieux vaut une réponse non corrigée que pas de
// réponse).
func Rules() []Rule {
	mu.Lock()
	defer mu.Unlock()
	rs, err := rulesLocked()
	if err != nil {
		return []Rule{}
	}
	return rs
}

// setRule (outil memory_rule) ajoute une règle, ou met à jour celle qui vise le
// même texte. Renvoie ce qui a été fait : sans « [ok] » si la règle disait
// déjà ça, car rien n'est écrit.
func setRule(from, to, why string) (string, error) {
	if from == "" {
		return "", errors.New("'from' vide : donne le texte exact à ne plus écrire")
	}
	if runeLen(from) > ruleMaxChars || runeLen(to) > ruleMaxChars {
		return "", fmt.Errorf("texte trop long (%d car. max) : une règle remplace un mot ou un caractère, pas une phrase", ruleMaxChars)
	}
	if strings.Contains(to, from) {
		return "", errors.New("le remplacement contient le texte interdit : il serait écrit quand même")
	}
	why = squash(why)
	mu.Lock()
	defer mu.Unlock()
	rs, err := rulesLocked()
	if err != nil {
		return "", err
	}
	next := 1
	for i, r := range rs {
		if r.From == from {
			if r.To == to && r.Why == why {
				return fmt.Sprintf("%s%d dit déjà ça : rien changé", rulePrefix, r.N), nil
			}
			rs[i].To, rs[i].Why = to, why
			if err := writeRulesLocked(rs); err != nil {
				return "", err
			}
			logLocked(kindRule, fmt.Sprintf("Règle modifiée : %s. Avant : %s", rs[i].describe(), r.describe()))
			return tool.OK("%s%d mise à jour", rulePrefix, r.N), nil
		}
		next = max(next, r.N+1)
	}
	if len(rs) >= rulesMax {
		return "", fmt.Errorf("trop de règles (%d max) : retires-en une (memory_forget %sN)", rulesMax, rulePrefix)
	}
	added := Rule{N: next, From: from, To: to, Why: why}
	if err := writeRulesLocked(append(rs, added)); err != nil {
		return "", err
	}
	logLocked(kindRule, "Règle ajoutée : "+added.describe())
	return tool.OK("%s%d : ce texte sera désormais remplacé dans tout ce que tu écris", rulePrefix, next), nil
}

// ForgetRule retire une règle (trace gardée au journal).
func ForgetRule(n int) error {
	mu.Lock()
	defer mu.Unlock()
	rs, err := rulesLocked()
	if err != nil {
		return err
	}
	for i, r := range rs {
		if r.N != n {
			continue
		}
		if err := writeRulesLocked(append(rs[:i], rs[i+1:]...)); err != nil {
			return err
		}
		noteRemovedLocked(ruleMark(n))
		logLocked(kindRule, "Règle retirée : "+r.describe())
		return nil
	}
	return fmt.Errorf("%s%d n'existe pas", rulePrefix, n)
}

// ApplyRules applique toutes les règles à un texte entier, dans l'ordre.
func ApplyRules(s string) string {
	for _, r := range Rules() {
		s = strings.ReplaceAll(s, r.From, r.To)
	}
	return s
}

// RuleFilter applique les règles à un texte qui arrive par morceaux, et donne
// au bout du compte exactement ApplyRules(texte entier), quel que soit le
// découpage. Chaque règle est un étage qui remplace au fil de l'eau et passe sa
// sortie au suivant ; un étage ne retient que la fin de ce qu'il a reçu quand
// elle peut être le début d'un texte interdit coupé entre deux morceaux.
type RuleFilter struct {
	stages []ruleStage
	tail   string // caractère UTF-8 encore incomplet : jamais affiché à moitié
}

type ruleStage struct {
	from, to string
	held     string // fin du flux qui commence peut-être un texte interdit
}

// NewRuleFilter : un filtre sur les règles du moment (une réponse en cours ne
// change pas de règles en route).
func NewRuleFilter() *RuleFilter {
	f := &RuleFilter{}
	for _, r := range Rules() {
		f.stages = append(f.stages, ruleStage{from: r.From, to: r.To})
	}
	return f
}

// push : ce qui peut sortir de l'étage après avoir reçu s.
func (st *ruleStage) push(s string) string {
	buf := st.held + s
	var out strings.Builder
	for {
		i := strings.Index(buf, st.from)
		if i < 0 {
			break
		}
		out.WriteString(buf[:i] + st.to)
		buf = buf[i+len(st.from):]
	}
	// Le plus long suffixe qui est un début du texte interdit : tout ce qui le
	// précède ne peut plus faire partie d'une occurrence.
	keep := min(len(buf), len(st.from)-1)
	for keep > 0 && !strings.HasSuffix(buf, st.from[:keep]) {
		keep--
	}
	out.WriteString(buf[:len(buf)-keep])
	st.held = buf[len(buf)-keep:]
	return out.String()
}

// Push reçoit un morceau et renvoie ce qui peut être affiché sans risque.
func (f *RuleFilter) Push(chunk string) string {
	for i := range f.stages {
		chunk = f.stages[i].push(chunk)
	}
	s := f.tail + chunk
	f.tail = ""
	// Un morceau peut finir au milieu d'un caractère : sa fin attend la suite.
	for i := len(s) - 1; i >= 0 && i >= len(s)-utf8.UTFMax; i-- {
		if utf8.RuneStart(s[i]) {
			if !utf8.FullRuneInString(s[i:]) {
				s, f.tail = s[:i], s[i:]
			}
			break
		}
	}
	return s
}

// Flush renvoie ce qui était retenu (fin du message).
func (f *RuleFilter) Flush() string {
	carry := ""
	for i := range f.stages {
		st := &f.stages[i]
		carry = st.push(carry) + st.held
		st.held = ""
	}
	out := f.tail + carry
	f.tail = ""
	return out
}
