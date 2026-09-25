package ajean

import (
	"strings"
	"testing"
)

func numbered(n int, suffix string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("ligne " + itoa(i) + suffix + "\n")
	}
	return b.String()
}

// Un fichier de 500 lignes : +500, même si l'UI n'en reçoit que 120.
func TestAddedDiffCountsBeyondCap(t *testing.T) {
	lines, added := addedDiff(numbered(500, ""))
	if added != 500 {
		t.Fatalf("added = %d, attendu 500", added)
	}
	if len(lines) > diffMaxShown+1 {
		t.Fatalf("%d lignes envoyées, plafond %d", len(lines), diffMaxShown)
	}
}

// Le saut de ligne final ne compte pas pour une ligne.
func TestAddedDiffTrailingNewline(t *testing.T) {
	if _, n := addedDiff("a\nb\n"); n != 2 {
		t.Fatalf("n = %d, attendu 2", n)
	}
	if _, n := addedDiff("a\nb"); n != 2 {
		t.Fatalf("n = %d, attendu 2", n)
	}
}

// Une ligne changée au milieu d'un gros bloc : +1 -1, et le changement est
// bien dans les lignes affichées (pas noyé sous le contexte).
func TestLineDiffSmallChangeInBigBlock(t *testing.T) {
	old := numbered(800, "")
	neu := strings.Replace(old, "ligne 600\n", "ligne 600 modifiée\n", 1)
	lines, add, del := lineDiff(old, neu)
	if add != 1 || del != 1 {
		t.Fatalf("+%d -%d, attendu +1 -1", add, del)
	}
	var sawAdd, sawDel bool
	for _, l := range lines {
		sawAdd = sawAdd || (l.Op == "+" && l.Text == "ligne 600 modifiée")
		sawDel = sawDel || (l.Op == "-" && l.Text == "ligne 600")
	}
	if !sawAdd || !sawDel {
		t.Fatalf("le changement n'est pas dans le diff affiché : %v", lines)
	}
	if len(lines) > 2*diffContext+4 {
		t.Fatalf("trop de contexte : %d lignes", len(lines))
	}
}

func TestLineDiffCountsAreExact(t *testing.T) {
	_, add, del := lineDiff("a\nb\nc\n", "a\nx\ny\nc\n")
	if add != 2 || del != 1 {
		t.Fatalf("+%d -%d, attendu +2 -1", add, del)
	}
	_, add, del = lineDiff(numbered(300, ""), numbered(300, " bis"))
	if add != 300 || del != 300 {
		t.Fatalf("+%d -%d, attendu +300 -300", add, del)
	}
}
