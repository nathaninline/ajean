package ajean

import "strings"

// cli_logo.go : le logo AJEAN (le mot en pixels) dessiné dans le terminal avec
// des demi-blocs (▀ ▄ █) : un caractère = deux pixels l'un au-dessus de l'autre,
// donc des pixels carrés, dans la couleur du texte du terminal.

// lettres du logo, 4x5 (le J fait 2 de large), une colonne d'écart
var logoLetters = [][]string{
	{".##.", "#..#", "####", "#..#", "#..#"},
	{".#", ".#", ".#", ".#", "#."},
	{"####", "#...", "###.", "#...", "####"},
	{".##.", "#..#", "####", "#..#", "#..#"},
	{"#..#", "##.#", "#.##", "#..#", "#..#"},
}

// termLogo renvoie les lignes du logo (vide si le terminal n'a pas de couleurs).
func termLogo() []string {
	if !colorOn {
		return nil
	}
	word := map[[2]int]bool{}
	x := 0
	for _, l := range logoLetters {
		for j, r := range l {
			for i, c := range r {
				if c == '#' {
					word[[2]int{x + i, j}] = true
				}
			}
		}
		x += len(l[0]) + 1
	}
	width := x - 1
	var out []string
	for ry := 0; ry < 3; ry++ { // 5 lignes de pixels = 3 lignes de texte
		var b strings.Builder
		for cx := 0; cx < width; cx++ {
			t, u := word[[2]int{cx, ry * 2}], word[[2]int{cx, ry*2 + 1}]
			switch {
			case t && u:
				b.WriteString("█")
			case t:
				b.WriteString("▀")
			case u:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}
