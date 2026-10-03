package ajean

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestChatThumbReduitEtCache(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "capture.png")
	src := image.NewNRGBA(image.Rect(0, 0, 2400, 1500))
	x := uint32(1)
	for i := range src.Pix { // bruit : se comprime comme une photo, pas comme un motif
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		src.Pix[i] = byte(x)
	}
	src.Set(0, 0, color.NRGBA{0, 0, 0, 0})
	var buf bytes.Buffer
	_ = png.Encode(&buf, src)
	_ = os.WriteFile(p, buf.Bytes(), 0o644)
	st, _ := os.Stat(p)
	data, mime, err := chatThumb(p, st, 560)
	if err != nil || mime != "image/jpeg" {
		t.Fatalf("vignette : %v %s", err, mime)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 560 {
		t.Fatalf("taille inattendue : %v %v", err, img.Bounds())
	}
	if len(data) >= buf.Len() {
		t.Fatalf("vignette (%d o) pas plus légère que l'original (%d o)", len(data), buf.Len())
	}
	again, _, _ := chatThumb(p, st, 560)
	if &again[0] != &data[0] {
		t.Fatal("la 2e demande aurait dû venir du cache")
	}
	if _, _, err := chatThumb(filepath.Join(dir, "x.webp"), st, 560); err == nil {
		t.Fatal("un fichier illisible doit renvoyer une erreur (repli client sur l'original)")
	}
}
