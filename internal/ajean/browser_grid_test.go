package ajean

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// TestOverlayGrid : la grille préserve la taille, reste un PNG valide, et dessine
// bien quelque chose (au moins un pixel modifié sur une ligne de grille).
func TestOverlayGrid(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 300, 250))
	for y := 0; y < 250; y++ {
		for x := 0; x < 300; x++ {
			src.SetRGBA(x, y, color.RGBA{20, 20, 20, 255}) // fond uni sombre
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	out := overlayGrid(buf.Bytes())
	got, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("sortie non décodable: %v", err)
	}
	if got.Bounds().Dx() != 300 || got.Bounds().Dy() != 250 {
		t.Errorf("taille modifiée: %v", got.Bounds())
	}
	// La ligne verticale x=100 doit avoir teinté un pixel (fond uni au départ).
	r, g, b, _ := got.At(100, 180).RGBA()
	if r>>8 == 20 && g>>8 == 20 && b>>8 == 20 {
		t.Errorf("aucune ligne de grille dessinée en x=100 (pixel resté au fond)")
	}
}

// TestOverlayGridBadInput : une entrée non-image est renvoyée telle quelle.
func TestOverlayGridBadInput(t *testing.T) {
	in := []byte("pas une image")
	if !bytes.Equal(overlayGrid(in), in) {
		t.Error("une entrée illisible devrait être renvoyée inchangée")
	}
}
