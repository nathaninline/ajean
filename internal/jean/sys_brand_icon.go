package jean

// sys_brand_icon.go — l'icône de la marque AJEAN, rendue à la volée.
//
// RÉPLIQUE EXACTE du favicon de l'UI web : carré à coins arrondis NOIR + « j »
// blanc (rects (6,3) (6,5) (4,7) sur une grille 12x12). Une seule source pour
// tous les usages — zone de notification Windows, barre de menus macOS, icône du
// .exe — pour qu'ils ne puissent plus diverger comme quand le favicon est passé
// au noir en laissant les icônes système en bleu.
//
// Aucun asset binaire à committer : tout est dessiné ici, et l'icône du .exe est
// produite par `go generate ./cmd/jean` (voir tools/gen-icon).

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

const trayIconSize = 32

var (
	brandBlack = color.RGBA{0x00, 0x00, 0x00, 0xff}
	brandWhite = color.RGBA{0xff, 0xff, 0xff, 0xff}
	brandClear = color.RGBA{0, 0, 0, 0}
)

// glyphRects : les trois rectangles blancs du « j », en unités de la grille 12.
var glyphRects = [][4]float64{{6, 3, 2, 2}, {6, 5, 2, 2}, {4, 7, 2, 2}}

// brandIconImage dessine l'icône à la taille n. bg peint le carré arrondi, fg le
// « j ». Les deux peuvent être transparents : c'est ce qui produit l'icône
// « template » de macOS (voir brandTemplatePNG).
func brandIconImage(n int, bg, fg color.RGBA) *image.RGBA {
	const r = 2.0 // rayon des coins, en unités de la grille 12
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
				img.Set(x, y, brandClear)
				continue
			}
			c := bg
			for _, rc := range glyphRects {
				if gx >= rc[0] && gx < rc[0]+rc[2] && gy >= rc[1] && gy < rc[1]+rc[3] {
					c = fg
				}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func encodePNG(img *image.RGBA) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// BrandIconPNG rend l'icône de marque (noir + « j » blanc) en PNG de n pixels.
// Exporté pour le générateur d'icône du .exe (tools/gen-icon).
func BrandIconPNG(n int) []byte { return encodePNG(brandIconImage(n, brandBlack, brandWhite)) }

// brandTemplatePNG rend la variante « template » attendue par macOS : seule la
// couche alpha compte, le système colore la forme selon le thème de la barre de
// menus. Le « j » est donc DÉCOUPÉ (transparent) dans un carré opaque — sans ça,
// une icône entièrement noire disparaît sur une barre de menus sombre.
func brandTemplatePNG(n int) []byte {
	return encodePNG(brandIconImage(n, brandBlack, brandClear))
}

// BrandICO emballe une ou plusieurs tailles PNG dans un conteneur .ico. Windows
// accepte le PNG comme image d'une entrée .ico (alpha conservé pour les coins
// arrondis). Plusieurs tailles = un rendu net partout, de la barre des tâches
// (16 px) à la grande tuile de l'explorateur (256 px).
func BrandICO(sizes ...int) []byte {
	if len(sizes) == 0 {
		sizes = []int{trayIconSize}
	}
	imgs := make([][]byte, 0, len(sizes))
	for _, n := range sizes {
		imgs = append(imgs, BrandIconPNG(n))
	}
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, uint16(0))         // réservé
	binary.Write(&ico, binary.LittleEndian, uint16(1))         // type = icône
	binary.Write(&ico, binary.LittleEndian, uint16(len(imgs))) // nombre d'images
	offset := uint32(6 + 16*len(imgs))                         // fin du répertoire
	for i, p := range imgs {
		n := sizes[i]
		// 256 px se code 0 dans un .ico (le champ ne fait qu'un octet).
		ico.WriteByte(byte(n % 256))
		ico.WriteByte(byte(n % 256))
		ico.WriteByte(0)                                        // couleurs de la palette (0 = truecolor)
		ico.WriteByte(0)                                        // réservé
		binary.Write(&ico, binary.LittleEndian, uint16(1))      // plans
		binary.Write(&ico, binary.LittleEndian, uint16(32))     // bits/pixel
		binary.Write(&ico, binary.LittleEndian, uint32(len(p))) // taille données
		binary.Write(&ico, binary.LittleEndian, offset)         // offset données
		offset += uint32(len(p))
	}
	for _, p := range imgs {
		ico.Write(p)
	}
	return ico.Bytes()
}
