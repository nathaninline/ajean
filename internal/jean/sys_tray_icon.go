//go:build windows || darwin

package jean

// sys_tray_icon.go — icône de Jean pour la zone de notification (Windows) et la
// barre de menus (macOS). RÉPLIQUE EXACTE du favicon de l'UI web : carré bleu
// arrondi #1f6feb + « j » blanc (rects (6,3) (6,5) (4,7) sur une grille 12x12).
// Générée à la volée — pas d'asset binaire à embarquer.

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

const trayIconSize = 32

// trayIconPNG rend l'icône en PNG 32x32 avec alpha (coins arrondis).
func trayIconPNG() []byte {
	const n = 32
	blue := color.RGBA{0x1f, 0x6f, 0xeb, 0xff}
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	clear := color.RGBA{0, 0, 0, 0}
	const r = 2.0 // rayon des coins, en unités de la grille 12
	// rects blancs du favicon (x, y, w, h en unités 12)
	rects := [][4]float64{{6, 3, 2, 2}, {6, 5, 2, 2}, {4, 7, 2, 2}}
	// hors du rectangle à coins arrondis (grille 12) → pixel transparent
	outside := func(gx, gy float64) bool {
		corner := func(cx, cy float64) bool {
			dx, dy := gx-cx, gy-cy
			return dx*dx+dy*dy > r*r
		}
		switch {
		case gx < r && gy < r:
			return corner(r, r)
		case gx > 12-r && gy < r:
			return corner(12-r, r)
		case gx < r && gy > 12-r:
			return corner(r, 12-r)
		case gx > 12-r && gy > 12-r:
			return corner(12-r, 12-r)
		}
		return false
	}
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	scale := float64(n) / 12
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			gx := (float64(x) + 0.5) / scale
			gy := (float64(y) + 0.5) / scale
			if outside(gx, gy) {
				img.Set(x, y, clear)
				continue
			}
			c := blue
			for _, rc := range rects {
				if gx >= rc[0] && gx < rc[0]+rc[2] && gy >= rc[1] && gy < rc[1]+rc[3] {
					c = white
				}
			}
			img.Set(x, y, c)
		}
	}

	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	return pngBuf.Bytes()
}
