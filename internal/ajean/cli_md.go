package ajean

// cli_md.go : rendu Markdown léger pour le terminal, ligne par ligne (la réponse
// arrive en streaming : chaque ligne est affichée brute pendant qu'elle s'écrit,
// puis réécrite mise en forme dès qu'elle est complète). Titres, gras, italique,
// code en ligne, blocs de code encadrés, listes, citations, liens, séparateurs.

import (
	"regexp"
	"strings"
)

func accent(s string) string   { return col("36", s) }
func italic(s string) string   { return col("3", s) }
func codeText(s string) string { return col("33", s) }

type mdRenderer struct {
	inCode bool
	fence  string
}

var (
	reMdBold   = regexp.MustCompile(`\*\*([^*\n]+?)\*\*|__([^_\n]+?)__`)
	reMdItalic = regexp.MustCompile(`(^|[^*\w])\*([^*\s][^*\n]*?)\*([^*\w]|$)`)
	reMdLink   = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)\)`)
	reMdList   = regexp.MustCompile(`^(\s*)([-*+])\s+(.*)$`)
	reMdOList  = regexp.MustCompile(`^(\s*)(\d+[.)])\s+(.*)$`)
	reMdTask   = regexp.MustCompile(`^\[([ xX])\]\s+(.*)$`)
	reMdHead   = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	reMdHR     = regexp.MustCompile(`^\s*(?:(?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$`)
)

// line met en forme une ligne complète (sans le retour ligne final).
func (m *mdRenderer) line(s string, width int) string {
	trim := strings.TrimSpace(s)
	if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
		f := trim[:3]
		if !m.inCode {
			m.inCode, m.fence = true, f
			lang := strings.TrimSpace(trim[3:])
			if lang == "" {
				return dim("╭─")
			}
			return dim("╭─ ") + accent(lang)
		}
		if f == m.fence && strings.Trim(trim, f[:1]) == "" {
			m.inCode = false
			return dim("╰─")
		}
	}
	if m.inCode {
		return dim("│ ") + codeText(strings.ReplaceAll(s, "\t", "    "))
	}
	if trim == "" {
		return ""
	}
	if reMdHR.MatchString(s) {
		return dim(strings.Repeat("─", min(width-1, 60)))
	}
	if h := reMdHead.FindStringSubmatch(s); h != nil {
		t := termMdInline(h[2])
		if len(h[1]) <= 2 {
			return col("1;36", stripANSI(t))
		}
		return bold(t)
	}
	if strings.HasPrefix(trim, ">") {
		q := strings.TrimSpace(strings.TrimLeft(trim, ">"))
		return dim("│ ") + italic(termMdInline(q))
	}
	if l := reMdList.FindStringSubmatch(s); l != nil {
		body := l[3]
		if t := reMdTask.FindStringSubmatch(body); t != nil {
			box := "☐"
			if t[1] != " " {
				box = accent("☑")
			}
			return l[1] + box + " " + termMdInline(t[2])
		}
		depth := len(strings.ReplaceAll(l[1], "\t", "  ")) / 2
		bullets := []string{"•", "◦", "▪"}
		return l[1] + accent(bullets[depth%len(bullets)]) + " " + termMdInline(body)
	}
	if l := reMdOList.FindStringSubmatch(s); l != nil {
		return l[1] + accent(l[2]) + " " + termMdInline(l[3])
	}
	if strings.HasPrefix(trim, "|") && strings.HasSuffix(trim, "|") {
		return mdTableRow(s)
	}
	return termMdInline(s)
}

// termMdInline : code en ligne, gras, italique, liens. Le code en ligne est traité
// d'abord et protégé : rien de ce qu'il contient n'est interprété.
func termMdInline(s string) string {
	parts := strings.Split(s, "`")
	if len(parts)%2 == 0 { // backtick non refermé : texte brut
		parts = []string{s}
	}
	var b strings.Builder
	for i, p := range parts {
		if i%2 == 1 {
			b.WriteString(codeText(p))
			continue
		}
		p = reMdLink.ReplaceAllStringFunc(p, func(x string) string {
			m := reMdLink.FindStringSubmatch(x)
			if m[1] == m[2] {
				return col("4", m[1])
			}
			return col("4", m[1]) + dim(" ("+m[2]+")")
		})
		p = reMdBold.ReplaceAllStringFunc(p, func(x string) string {
			m := reMdBold.FindStringSubmatch(x)
			return bold(m[1] + m[2])
		})
		p = reMdItalic.ReplaceAllString(p, "$1"+col("3", "$2")+"$3")
		b.WriteString(p)
	}
	return b.String()
}

func mdTableRow(s string) string {
	cells := strings.Split(strings.TrimSpace(s), "|")
	sep := true
	for _, c := range cells {
		if strings.Trim(strings.TrimSpace(c), ":-") != "" {
			sep = false
		}
	}
	if sep {
		return dim(strings.TrimSpace(s))
	}
	for i, c := range cells {
		cells[i] = termMdInline(c)
	}
	return strings.Join(cells, dim("│"))
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = skipANSI(s, i)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
