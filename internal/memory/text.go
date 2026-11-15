package memory

import (
	"regexp"
	"strings"
	"unicode"
)

// fold ramène un texte à sa forme de comparaison : minuscules, sans accents,
// sans espaces de bord. « Élève » et « eleve » doivent se trouver l'un l'autre.
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if f, ok := accentFold[r]; ok {
			r = f
		}
		b.WriteRune(r)
	}
	return b.String()
}

var accentFold = map[rune]rune{
	'à': 'a', 'â': 'a', 'ä': 'a', 'á': 'a', 'ã': 'a', 'ç': 'c',
	'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e', 'î': 'i', 'ï': 'i', 'í': 'i', 'ì': 'i',
	'ô': 'o', 'ö': 'o', 'ó': 'o', 'õ': 'o', 'ò': 'o', 'ù': 'u', 'û': 'u', 'ü': 'u', 'ú': 'u', 'ÿ': 'y', 'ñ': 'n',
}

// squash met un texte sur une seule ligne, espaces réduits.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// clip coupe à n caractères (pas octets) et le signale par « … ».
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func runeLen(s string) int { return len([]rune(s)) }

// uniqueTerms découpe en mots distincts, ordre gardé.
func uniqueTerms(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Fields(s) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// stopWords : mots trop courants pour départager deux souvenirs. Les recherches
// sont écrites en phrases : sans ce filtre, « pour » ou « the » se trouve dans
// chaque entrée et tout correspond.
var stopWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields("les des une un aux avec pour par sur dans chez qui que quoi dont est sont etait ete avait avons avez ont " +
		"mon ma mes ton ta tes son sa ses notre nos votre vos leur leurs ce cet cette ces cela ceci ca il elle ils elles nous vous on je tu " +
		"ne pas plus moins tres bien aussi mais donc car comme quand ou sans sous entre vers depuis avant apres fait faire dit dire " +
		"tout tous toute toutes quel quelle quels quelles parle parler propos sujet souvenir chose choses " +
		"the and for with about from that this what when where which who have has had was were are you your our") {
		m[w] = true
	}
	return m
}()

// searchTerms : les termes utiles d'une requête (repliés, sans ponctuation ni
// mots vides, 3 lettres au moins).
func searchTerms(query string) []string {
	var out []string
	// L'apostrophe sépare : « l'anniversaire » doit trouver « anniversaire ».
	for _, t := range strings.FieldsFunc(fold(query), func(r rune) bool { return unicode.IsSpace(r) || r == '\'' || r == '’' }) {
		t = strings.TrimFunc(t, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if runeLen(t) >= 3 && !stopWords[t] {
			out = append(out, t)
		}
	}
	return uniqueTerms(strings.Join(out, " "))
}

// countTerms : combien de termes figurent dans un texte déjà replié.
func countTerms(terms []string, folded string) int {
	n := 0
	for _, t := range terms {
		if strings.Contains(folded, t) {
			n++
		}
	}
	return n
}

var wordRe = regexp.MustCompile(`[a-z0-9]{4,}`)

// words : les mots importants d'un texte (4 lettres ou chiffres au moins,
// repliés). C'est l'unité de comparaison des doublons et du garde-fou.
func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRe.FindAllString(fold(s), -1) {
		out[w] = true
	}
	return out
}

// overlap : part des mots de a présents dans b (0..1).
func overlap(a, b map[string]bool) float64 {
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

// sameIdea : deux phrases disent-elles la même chose ? Il faut assez de mots
// pour juger, et que chacune se retrouve largement dans l'autre.
func sameIdea(a, b map[string]bool) bool {
	return len(a) >= 4 && len(b) >= 4 && overlap(a, b) >= 0.7 && overlap(b, a) >= 0.5
}
