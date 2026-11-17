package llm

import (
	"encoding/json"
	"regexp"
	"strings"
)

// descRe : le début du champ « description » dans des arguments JSON en cours
// d'écriture. Le groupe s'arrête au guillemet fermant, ou à la fin du texte reçu.
var descRe = regexp.MustCompile(`"description"\s*:\s*"((?:[^"\\]|\\.)*)("?)`)

// partialDesc : la phrase « description » d'arguments encore incomplets ("" tant
// qu'elle n'a pas commencé). Tant qu'elle s'écrit, on la coupe au dernier mot
// entier, pour ne pas montrer un mot à moitié arrivé.
func partialDesc(args string) string {
	m := descRe.FindStringSubmatch(args)
	if m == nil {
		return ""
	}
	raw, closed := m[1], m[2] == `"`
	raw = strings.TrimSuffix(raw, `\`) // échappement coupé en deux par le flux
	var d string
	if err := json.Unmarshal([]byte(`"`+raw+`"`), &d); err != nil {
		d = raw
	}
	d = strings.TrimSpace(d)
	if !closed {
		if i := strings.LastIndex(d, " "); i > 0 {
			d = d[:i]
		} else {
			d = ""
		}
	}
	return d
}
