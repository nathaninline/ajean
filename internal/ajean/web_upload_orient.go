// web_upload_orient.go — préparation des images avant envoi au modèle :
// redressement de l'orientation EXIF ET redimensionnement des images trop
// grandes.
//
// Orientation : les photos prises au téléphone sont presque toujours
// enregistrées dans l'orientation NATIVE du capteur (souvent paysage), avec un
// tag EXIF Orientation qui dit au visualiseur de les tourner à l'affichage. Les
// navigateurs et apps photo respectent ce tag, donc l'utilisateur voit l'image
// droite ; mais le projecteur multimodal (mmproj) de llama.cpp IGNORE l'EXIF et
// reçoit les pixels bruts. Le modèle voyait alors l'image tournée de 90°.
//
// Redimensionnement : une image de plusieurs milliers de pixels de côté est
// inutilement lourde — le projecteur la redécoupe de toute façon à sa propre
// résolution (quelques centaines de pixels par tuile). L'envoyer en pleine
// définition ne fait que gonfler le base64 transféré et les tokens visuels, sans
// rien apprendre de plus au modèle. On plafonne donc le plus grand côté à
// maxImageDim : c'est ce que font les fournisseurs d'API vision (~1568 px), et
// c'est visuellement sans perte à l'échelle où le modèle regarde. Les PNG
// restent réencodés en PNG (sans perte, utile pour les captures d'écran à
// texte) ; le reste part en JPEG.
//
// On CUIT donc orientation et taille dans les pixels : décoder, redresser,
// redimensionner, réencoder. Repli systématique sur les octets bruts en cas de
// pépin : préparer une image ne doit jamais faire échouer un envoi.
package ajean

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
)

const (
	// maxImageDim plafonne le plus grand côté d'une image envoyée au modèle.
	maxImageDim = 1568
	// imageJPEGQuality : qualité de réencodage JPEG (photos). Assez haut pour
	// rester net à l'œil, assez bas pour couper franchement le poids du base64.
	imageJPEGQuality = 88
)

// prepareImageForModel prend les octets d'une image et son type MIME, et renvoie
// une version prête pour la vision : orientation EXIF cuite dans les pixels et
// grand côté ramené sous maxImageDim. Si rien n'est à faire (bon format, pas de
// tag, image déjà petite) ou si le décodage échoue (WEBP/BMP hors stdlib, octets
// abîmés), elle renvoie les octets d'origine inchangés — jamais d'échec.
func prepareImageForModel(raw []byte, mime string) ([]byte, string) {
	orient := 0
	if mime == "image/jpeg" {
		orient = exifOrientation(raw)
	}
	needOrient := orient >= 2 && orient <= 8

	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return raw, mime // format non décodable ici (webp/bmp…) : laisser tel quel
	}
	b := src.Bounds()
	needResize := b.Dx() > maxImageDim || b.Dy() > maxImageDim
	if !needOrient && !needResize {
		return raw, mime // rien à changer : préserver les octets d'origine
	}

	img := src
	if needOrient {
		img = applyOrientation(img, orient)
	}
	if img.Bounds().Dx() > maxImageDim || img.Bounds().Dy() > maxImageDim {
		img = scaleDownToMax(img, maxImageDim)
	}

	var buf bytes.Buffer
	if mime == "image/png" {
		if err := png.Encode(&buf, img); err != nil {
			return raw, mime
		}
		return buf.Bytes(), "image/png"
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: imageJPEGQuality}); err != nil {
		return raw, mime
	}
	return buf.Bytes(), "image/jpeg"
}

// toNRGBA renvoie une vue *image.NRGBA de src (sans copie s'il l'est déjà), pour
// un accès rapide aux octets de pixels dans scaleDownToMax.
func toNRGBA(src image.Image) *image.NRGBA {
	if n, ok := src.(*image.NRGBA); ok {
		return n
	}
	b := src.Bounds()
	n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(n, n.Bounds(), src, b.Min, draw.Src)
	return n
}

// scaleDownToMax réduit une image pour que son plus grand côté vaille au plus
// maxDim, en conservant le ratio. Moyennage par aire (box filter) : chaque pixel
// de destination est la moyenne des pixels source qu'il recouvre — le meilleur
// filtre simple pour une RÉDUCTION (pas de crénelage ni de halo), sans
// dépendance externe.
func scaleDownToMax(src image.Image, maxDim int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDim && h <= maxDim {
		return src
	}
	scale := float64(maxDim) / float64(w)
	if h > w {
		scale = float64(maxDim) / float64(h)
	}
	nw := int(math.Round(float64(w) * scale))
	nh := int(math.Round(float64(h) * scale))
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	s := toNRGBA(src)
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	for dy := 0; dy < nh; dy++ {
		sy0 := dy * h / nh
		sy1 := (dy + 1) * h / nh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for dx := 0; dx < nw; dx++ {
			sx0 := dx * w / nw
			sx1 := (dx + 1) * w / nw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var r, g, bl, a, cnt uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					i := s.PixOffset(s.Rect.Min.X+sx, s.Rect.Min.Y+sy)
					r += uint64(s.Pix[i])
					g += uint64(s.Pix[i+1])
					bl += uint64(s.Pix[i+2])
					a += uint64(s.Pix[i+3])
					cnt++
				}
			}
			di := dst.PixOffset(dx, dy)
			dst.Pix[di] = byte(r / cnt)
			dst.Pix[di+1] = byte(g / cnt)
			dst.Pix[di+2] = byte(bl / cnt)
			dst.Pix[di+3] = byte(a / cnt)
		}
	}
	return dst
}

// exifOrientation renvoie la valeur du tag EXIF Orientation (1..8) d'un JPEG, ou
// 0 si absent/illisible. On lit à la main le segment APP1 pour éviter une
// dépendance : la structure est simple et le tag vit dans l'IFD0.
func exifOrientation(b []byte) int {
	// En-tête JPEG : SOI = 0xFFD8.
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 0
	}
	pos := 2
	for pos+4 <= len(b) {
		if b[pos] != 0xFF {
			return 0 // désynchronisé
		}
		marker := b[pos+1]
		// SOS (début des données image) ou EOI : plus aucun segment de métadonnées.
		if marker == 0xDA || marker == 0xD9 {
			return 0
		}
		segLen := int(binary.BigEndian.Uint16(b[pos+2 : pos+4]))
		if segLen < 2 || pos+2+segLen > len(b) {
			return 0
		}
		seg := b[pos+4 : pos+2+segLen]
		// APP1 = 0xFFE1, préfixé "Exif\0\0" pour les données EXIF.
		if marker == 0xE1 && len(seg) >= 6 && string(seg[:6]) == "Exif\x00\x00" {
			return exifOrientationFromTIFF(seg[6:])
		}
		pos += 2 + segLen
	}
	return 0
}

// exifOrientationFromTIFF lit l'orientation dans le bloc TIFF d'un segment EXIF.
func exifOrientationFromTIFF(t []byte) int {
	if len(t) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(t[2:4]) != 0x002A {
		return 0
	}
	ifdOff := int(bo.Uint32(t[4:8]))
	if ifdOff < 8 || ifdOff+2 > len(t) {
		return 0
	}
	n := int(bo.Uint16(t[ifdOff : ifdOff+2]))
	entry := ifdOff + 2
	for i := 0; i < n; i++ {
		if entry+12 > len(t) {
			return 0
		}
		tag := bo.Uint16(t[entry : entry+2])
		if tag == 0x0112 { // Orientation
			// Type SHORT : la valeur tient dans les 2 premiers octets du champ valeur
			// (offset entry+8).
			return int(bo.Uint16(t[entry+8 : entry+10]))
		}
		entry += 12
	}
	return 0
}

// applyOrientation renvoie une nouvelle image où l'orientation EXIF a été cuite
// dans les pixels. Les 8 cas du tag combinent rotations (multiples de 90°) et
// miroirs.
func applyOrientation(src image.Image, orient int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	// Les orientations 5..8 échangent largeur et hauteur (rotation d'un quart).
	swap := orient >= 5
	dw, dh := w, h
	if swap {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			var nx, ny int
			switch orient {
			case 2: // miroir horizontal
				nx, ny = w-1-x, y
			case 3: // 180°
				nx, ny = w-1-x, h-1-y
			case 4: // miroir vertical
				nx, ny = x, h-1-y
			case 5: // transpose (diagonale principale)
				nx, ny = y, x
			case 6: // 90° horaire
				nx, ny = h-1-y, x
			case 7: // transverse (diagonale secondaire)
				nx, ny = h-1-y, w-1-x
			case 8: // 90° anti-horaire
				nx, ny = y, w-1-x
			default:
				nx, ny = x, y
			}
			dst.Set(nx, ny, c)
		}
	}
	return dst
}
