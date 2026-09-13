// web_upload_orient.go — redressement de l'orientation des images avant envoi
// au modèle.
//
// Les photos prises au téléphone sont presque toujours enregistrées dans
// l'orientation NATIVE du capteur (souvent paysage), avec un tag EXIF
// Orientation qui dit au visualiseur de les tourner à l'affichage. Les
// navigateurs et apps photo respectent ce tag, donc l'utilisateur voit l'image
// droite ; mais le projecteur multimodal (mmproj) de llama.cpp IGNORE l'EXIF et
// reçoit les pixels bruts. Le modèle voyait alors l'image tournée de 90° et
// annonçait « je dois la retourner mentalement ».
//
// On CUIT donc l'orientation dans les pixels ici : décoder, lire le tag EXIF,
// appliquer la rotation/le miroir, réencoder droit (l'EXIF disparaît au passage).
// Repli systématique sur les octets bruts en cas de pépin : redresser une image
// ne doit jamais faire échouer un envoi.
package ajean

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	_ "image/png" // au cas où, mais seuls les JPEG portent une orientation EXIF
)

// orientImageForModel prend les octets d'une image et son type MIME, et renvoie
// des octets redressés (JPEG) si — et seulement si — l'image portait un tag EXIF
// Orientation non trivial. Dans tous les autres cas (autre format, pas de tag,
// orientation normale, ou erreur de décodage) elle renvoie les octets d'origine
// inchangés, ainsi que le MIME à utiliser.
func orientImageForModel(raw []byte, mime string) ([]byte, string) {
	// Seuls les JPEG portent une orientation EXIF en pratique ; on ne touche pas
	// aux PNG/WEBP/GIF, qui arrivent déjà droits.
	if mime != "image/jpeg" {
		return raw, mime
	}
	orient := exifOrientation(raw)
	if orient <= 1 || orient > 8 {
		return raw, mime // pas de tag, normal, ou valeur aberrante : ne rien faire
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return raw, mime // illisible ici : on laisse le modèle se débrouiller
	}
	dst := applyOrientation(src, orient)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90}); err != nil {
		return raw, mime
	}
	return buf.Bytes(), "image/jpeg"
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
