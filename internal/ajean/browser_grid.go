package ajean

// browser_grid.go — surimpose une GRILLE DE COORDONNÉES sur le screenshot envoyé
// au MODÈLE (pas sur celui sauvegardé pour l'utilisateur). Un petit modèle vise
// mal au pixel près ; avec des lignes graduées tous les 100 px et des étiquettes
// chiffrées, il lit x/y précisément et browser_click_xy tombe juste du premier
// coup (bandeaux cookies en iframe, canvas, jeux…). Sans dépendance externe : la
// police de chiffres est un bitmap 3×5 codé ici.

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"
)

const gridStep = 100 // espacement des lignes, en pixels

// digits5x3 : bitmap 5 lignes × 3 colonnes pour chaque chiffre (# = pixel plein).
var digits5x3 = map[rune][5]string{
	'0': {"###", "# #", "# #", "# #", "###"},
	'1': {" # ", "## ", " # ", " # ", "###"},
	'2': {"###", "  #", "###", "#  ", "###"},
	'3': {"###", "  #", "###", "  #", "###"},
	'4': {"# #", "# #", "###", "  #", "  #"},
	'5': {"###", "#  ", "###", "  #", "###"},
	'6': {"###", "#  ", "###", "# #", "###"},
	'7': {"###", "  #", "  #", "  #", "  #"},
	'8': {"###", "# #", "###", "# #", "###"},
	'9': {"###", "# #", "###", "  #", "###"},
}

// overlayGrid décode un PNG, y dessine la grille + les graduations, et renvoie le
// PNG résultant. En cas d'erreur (image illisible), renvoie l'entrée inchangée.
func overlayGrid(raw []byte) []byte {
	src, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return raw
	}
	b := src.Bounds()
	img := image.NewRGBA(b)
	draw.Draw(img, b, src, b.Min, draw.Src)

	line := color.RGBA{255, 0, 200, 130}    // magenta translucide pour les lignes
	labelBG := color.RGBA{0, 0, 0, 200}     // fond sombre derrière les chiffres
	labelFG := color.RGBA{255, 255, 0, 255} // chiffres jaunes (contraste sur toute page)
	w, h := b.Dx(), b.Dy()

	// Lignes verticales + étiquette x en haut.
	for x := gridStep; x < w; x += gridStep {
		for y := 0; y < h; y++ {
			blend(img, b.Min.X+x, b.Min.Y+y, line)
		}
		drawLabel(img, b.Min.X+x+2, b.Min.Y+2, strconv.Itoa(x), labelFG, labelBG)
	}
	// Lignes horizontales + étiquette y à gauche.
	for y := gridStep; y < h; y += gridStep {
		for x := 0; x < w; x++ {
			blend(img, b.Min.X+x, b.Min.Y+y, line)
		}
		drawLabel(img, b.Min.X+2, b.Min.Y+y+2, strconv.Itoa(y), labelFG, labelBG)
	}

	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return raw
	}
	return out.Bytes()
}

// blend applique une couleur semi-transparente (alpha) sur un pixel.
func blend(img *image.RGBA, x, y int, c color.RGBA) {
	if !(image.Pt(x, y).In(img.Bounds())) {
		return
	}
	a := float64(c.A) / 255
	o := img.RGBAAt(x, y)
	img.SetRGBA(x, y, color.RGBA{
		R: uint8(float64(c.R)*a + float64(o.R)*(1-a)),
		G: uint8(float64(c.G)*a + float64(o.G)*(1-a)),
		B: uint8(float64(c.B)*a + float64(o.B)*(1-a)),
		A: 255,
	})
}

// drawLabel écrit un nombre (police 3×5, échelle ×2) avec un fond sombre pour
// rester lisible sur n'importe quel fond.
func drawLabel(img *image.RGBA, x, y int, s string, fg, bg color.RGBA) {
	const sc = 2
	glyphW, glyphH, gap := 3*sc, 5*sc, sc
	boxW := len(s)*(glyphW+gap) + gap
	boxH := glyphH + 2*gap
	// Fond.
	for dy := -gap; dy < boxH-gap; dy++ {
		for dx := -gap; dx < boxW-gap; dx++ {
			setOpaque(img, x+dx, y+dy, bg)
		}
	}
	// Chiffres.
	cx := x
	for _, r := range s {
		g, ok := digits5x3[r]
		if !ok {
			cx += glyphW + gap
			continue
		}
		for row := 0; row < 5; row++ {
			for col := 0; col < 3; col++ {
				if g[row][col] != '#' {
					continue
				}
				for iy := 0; iy < sc; iy++ {
					for ix := 0; ix < sc; ix++ {
						setOpaque(img, cx+col*sc+ix, y+row*sc+iy, fg)
					}
				}
			}
		}
		cx += glyphW + gap
	}
}

func setOpaque(img *image.RGBA, x, y int, c color.RGBA) {
	if image.Pt(x, y).In(img.Bounds()) {
		img.SetRGBA(x, y, color.RGBA{c.R, c.G, c.B, 255})
	}
}
