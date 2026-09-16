package ajean

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// buildExifJPEG fabrique un JPEG minimal portant un tag EXIF Orientation donné,
// pour tester le parseur sans dépendre d'un fichier binaire.
func buildExifJPEG(t *testing.T, orient uint16, img image.Image) []byte {
	t.Helper()
	// Bloc TIFF (little-endian) avec un unique IFD0 : 1 entrée = Orientation.
	var tiff bytes.Buffer
	tiff.WriteString("II")
	binary.Write(&tiff, binary.LittleEndian, uint16(0x002A))
	binary.Write(&tiff, binary.LittleEndian, uint32(8)) // IFD0 juste après l'en-tête
	binary.Write(&tiff, binary.LittleEndian, uint16(1)) // 1 entrée
	binary.Write(&tiff, binary.LittleEndian, uint16(0x0112))
	binary.Write(&tiff, binary.LittleEndian, uint16(3)) // type SHORT
	binary.Write(&tiff, binary.LittleEndian, uint32(1)) // count
	binary.Write(&tiff, binary.LittleEndian, orient)
	binary.Write(&tiff, binary.LittleEndian, uint16(0)) // padding du champ valeur
	binary.Write(&tiff, binary.LittleEndian, uint32(0)) // next IFD = 0

	exif := append([]byte("Exif\x00\x00"), tiff.Bytes()...)

	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8}) // SOI
	// APP1
	out.Write([]byte{0xFF, 0xE1})
	binary.Write(&out, binary.BigEndian, uint16(len(exif)+2))
	out.Write(exif)
	// Corps JPEG réel (sans son propre SOI, qu'on saute).
	var body bytes.Buffer
	if err := jpeg.Encode(&body, img, nil); err != nil {
		t.Fatal(err)
	}
	out.Write(body.Bytes()[2:]) // saute le SOI dupliqué
	return out.Bytes()
}

func TestExifOrientationParsing(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for _, want := range []int{1, 3, 6, 8} {
		b := buildExifJPEG(t, uint16(want), img)
		if got := exifOrientation(b); got != want {
			t.Errorf("orientation %d : lu %d", want, got)
		}
	}
	// Un JPEG sans EXIF renvoie 0.
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, img, nil); err != nil {
		t.Fatal(err)
	}
	if got := exifOrientation(plain.Bytes()); got != 0 {
		t.Errorf("sans EXIF : attendu 0, lu %d", got)
	}
}

func TestApplyOrientationDimensions(t *testing.T) {
	// 4 de large, 2 de haut. Une rotation d'un quart (6, 8) doit échanger les axes.
	src := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	if d := applyOrientation(src, 6).Bounds(); d.Dx() != 2 || d.Dy() != 4 {
		t.Errorf("orient 6 : %dx%d, attendu 2x4", d.Dx(), d.Dy())
	}
	if d := applyOrientation(src, 3).Bounds(); d.Dx() != 4 || d.Dy() != 2 {
		t.Errorf("orient 3 : %dx%d, attendu 4x2", d.Dx(), d.Dy())
	}
}

func TestScaleDownToMax(t *testing.T) {
	// Grande image paysage : le plus grand côté doit tomber à maxImageDim, ratio
	// conservé, et une image déjà petite ne bouge pas.
	big := image.NewNRGBA(image.Rect(0, 0, 4000, 2000))
	d := scaleDownToMax(big, maxImageDim).Bounds()
	if d.Dx() != maxImageDim {
		t.Errorf("grand côté : %d, attendu %d", d.Dx(), maxImageDim)
	}
	if d.Dy() != maxImageDim/2 {
		t.Errorf("petit côté : %d, attendu %d (ratio 2:1)", d.Dy(), maxImageDim/2)
	}
	small := image.NewNRGBA(image.Rect(0, 0, 100, 80))
	if sd := scaleDownToMax(small, maxImageDim).Bounds(); sd.Dx() != 100 || sd.Dy() != 80 {
		t.Errorf("image petite modifiée : %dx%d", sd.Dx(), sd.Dy())
	}
}

func TestPrepareImageForModelResizesJPEG(t *testing.T) {
	// Un JPEG plus grand que maxImageDim doit être réencodé plus petit ; ses octets
	// changent donc forcément.
	big := image.NewNRGBA(image.Rect(0, 0, 3000, 1500))
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, big, nil); err != nil {
		t.Fatal(err)
	}
	out, mime := prepareImageForModel(raw.Bytes(), "image/jpeg")
	if mime != "image/jpeg" {
		t.Fatalf("mime = %s", mime)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != maxImageDim {
		t.Errorf("largeur après préparation : %d, attendu %d", img.Bounds().Dx(), maxImageDim)
	}
}

func TestApplyOrientationRotate90(t *testing.T) {
	// Pixel repère en haut-gauche du capteur ; en orientation 6 (90° horaire) il
	// doit se retrouver en haut-droite de l'image redressée.
	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			src.Set(x, y, color.NRGBA{0, 0, 0, 255})
		}
	}
	src.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	dst := applyOrientation(src, 6)
	// dst est 2 de large, 3 de haut ; le repère va en (dw-1, 0).
	r := color.NRGBAModel.Convert(dst.At(dst.Bounds().Dx()-1, 0)).(color.NRGBA)
	if r.R != 255 {
		t.Errorf("orient 6 : repère mal placé, got %+v", r)
	}
}
