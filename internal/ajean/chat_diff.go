package ajean

// chat_diff.go — petit diff ligne à ligne pour l'interface : quand l'IA modifie
// un fichier (outil `edit`) ou une page de mémoire (`mem_edit`, `mem_add`), on
// envoie à l'UI le détail des lignes ajoutées et retirées, qu'elle affiche en
// vert (+) et rouge (-). Aucune dépendance : LCS classique sur les lignes, avec
// des garde-fous pour ne jamais transformer un gros remplacement en pavé.
//
// Les lignes envoyées sont bornées (diffMaxShown), mais les compteurs +N / -N
// sont calculés AVANT la coupe et renvoyés à part : l'UI les comptait dans le
// diff tronqué, d'où un « +500 » pendant l'écriture qui retombait à « +120 ».

import "strings"

const (
	diffMaxLines   = 400 // au-delà (partie modifiée), on compare sans détail (LCS trop lente)
	diffMaxShown   = 120 // lignes envoyées à l'UI (le reste est résumé)
	diffContext    = 3   // lignes identiques gardées autour d'un changement
	diffOmitPrefix = "…"
)

// DiffLine est une ligne de diff : Op vaut " " (contexte), "-" ou "+".
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// lineDiff compare deux blocs de texte ligne à ligne et renvoie les lignes à
// afficher (bornées) avec le nombre RÉEL de lignes ajoutées et retirées.
//
// Les lignes communes en tête et en queue sont écartées avant la LCS : un
// changement d'une ligne dans un gros bloc reste un +1 -1 lisible (au lieu d'un
// « tout retiré, tout ajouté ») et le changement n'est pas repoussé hors des
// lignes affichées par des centaines de lignes de contexte.
func lineDiff(oldText, newText string) (lines []DiffLine, added, removed int) {
	a := splitLines(oldText)
	b := splitLines(newText)
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]

	var mid []DiffLine
	if len(ma) > diffMaxLines || len(mb) > diffMaxLines {
		// Partie modifiée énorme : pas de LCS (coût quadratique), l'ancien en
		// retrait puis le nouveau en ajout.
		mid = make([]DiffLine, 0, len(ma)+len(mb))
		for _, l := range ma {
			mid = append(mid, DiffLine{Op: "-", Text: l})
		}
		for _, l := range mb {
			mid = append(mid, DiffLine{Op: "+", Text: l})
		}
	} else {
		mid = lcsDiff(ma, mb)
	}
	for _, d := range mid {
		switch d.Op {
		case "+":
			added++
		case "-":
			removed++
		}
	}

	// Contexte autour du changement : quelques lignes, le reste résumé.
	var out []DiffLine
	if pre > diffContext {
		out = append(out, DiffLine{Op: " ", Text: omitted(pre - diffContext)})
	}
	for _, l := range a[max(0, pre-diffContext):pre] {
		out = append(out, DiffLine{Op: " ", Text: l})
	}
	out = append(out, mid...)
	for _, l := range a[len(a)-suf : len(a)-suf+min(suf, diffContext)] {
		out = append(out, DiffLine{Op: " ", Text: l})
	}
	if suf > diffContext {
		out = append(out, DiffLine{Op: " ", Text: omitted(suf - diffContext)})
	}
	return capLines(out), added, removed
}

// lcsDiff : diff exact par plus longue sous-séquence commune.
func lcsDiff(a, b []string) []DiffLine {
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
	return out
}

// addedDiff présente un contenu entièrement nouveau (création d'une page ou
// d'un fichier) : toutes ses lignes en ajout.
func addedDiff(text string) (lines []DiffLine, added int) {
	ls := splitLines(text)
	out := make([]DiffLine, 0, len(ls))
	for _, l := range ls {
		out = append(out, DiffLine{Op: "+", Text: l})
	}
	return capLines(out), len(ls)
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	// Un saut de ligne final termine la dernière ligne, il n'en ouvre pas une vide.
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func omitted(n int) string {
	if n == 1 {
		return diffOmitPrefix + "(1 ligne identique)"
	}
	return diffOmitPrefix + "(" + itoa(n) + " lignes identiques)"
}

// capLines borne la taille envoyée à l'UI et signale ce qui a été coupé.
func capLines(d []DiffLine) []DiffLine {
	if len(d) <= diffMaxShown {
		return d
	}
	cut := len(d) - diffMaxShown
	out := append([]DiffLine{}, d[:diffMaxShown]...)
	return append(out, DiffLine{Op: " ", Text: diffOmitPrefix + "(" + itoa(cut) + " lignes de plus)"})
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
