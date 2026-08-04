package ajean

// chat_diff.go — petit diff ligne à ligne pour l'interface : quand l'IA modifie
// un fichier (outil `edit`) ou une page de mémoire (`mem_edit`, `mem_add`), on
// envoie à l'UI le détail des lignes ajoutées et retirées, qu'elle affiche en
// vert (+) et rouge (-). Aucune dépendance : LCS classique sur les lignes, avec
// des garde-fous pour ne jamais transformer un gros remplacement en pavé.

import "strings"

const (
	diffMaxLines = 400 // au-delà, on compare sans détail (trop gros / trop lent)
	diffMaxShown = 120 // lignes envoyées à l'UI (le reste est résumé)
)

// DiffLine est une ligne de diff : Op vaut " " (contexte), "-" ou "+".
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// lineDiff compare deux blocs de texte ligne à ligne. Le résultat garde les
// lignes communes comme contexte : c'est ce qui rend le changement lisible
// quand l'IA ne touche qu'un mot au milieu d'un paragraphe.
func lineDiff(oldText, newText string) []DiffLine {
	a := splitLines(oldText)
	b := splitLines(newText)
	// Blocs énormes : on ne calcule pas la LCS (coût quadratique), on montre
	// simplement l'ancien en retrait et le nouveau en ajout.
	if len(a) > diffMaxLines || len(b) > diffMaxLines {
		out := make([]DiffLine, 0, len(a)+len(b))
		for _, l := range a {
			out = append(out, DiffLine{Op: "-", Text: l})
		}
		for _, l := range b {
			out = append(out, DiffLine{Op: "+", Text: l})
		}
		return capLines(out)
	}

	// LCS : table des longueurs, puis remontée.
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []DiffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{Op: " ", Text: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{Op: "-", Text: a[i]})
			i++
		default:
			out = append(out, DiffLine{Op: "+", Text: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{Op: "-", Text: a[i]})
	}
	for ; j < m; j++ {
		out = append(out, DiffLine{Op: "+", Text: b[j]})
	}
	return capLines(out)
}

// addedDiff présente un contenu entièrement nouveau (création d'une page).
func addedDiff(text string) []DiffLine {
	lines := splitLines(text)
	out := make([]DiffLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, DiffLine{Op: "+", Text: l})
	}
	return capLines(out)
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// capLines borne la taille envoyée à l'UI et signale ce qui a été coupé.
func capLines(d []DiffLine) []DiffLine {
	if len(d) <= diffMaxShown {
		return d
	}
	cut := len(d) - diffMaxShown
	out := append([]DiffLine{}, d[:diffMaxShown]...)
	return append(out, DiffLine{Op: " ", Text: "…(" + itoa(cut) + " lignes de plus)"})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
