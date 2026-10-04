package ajean

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// #43 : block_count est trouvé derrière des valeurs de tous types, y compris un
// tableau de chaînes (le vocabulaire), quel que soit l'ordre des clés.
func TestGGUFBlockCount(t *testing.T) {
	var b bytes.Buffer
	le := binary.LittleEndian
	str := func(s string) { binary.Write(&b, le, uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	binary.Write(&b, le, uint32(3))
	binary.Write(&b, le, uint64(0)) // tenseurs
	binary.Write(&b, le, uint64(5)) // paires clé/valeur
	str("general.name")
	binary.Write(&b, le, uint32(ggufString))
	str("test")
	str("tokenizer.ggml.tokens")
	binary.Write(&b, le, uint32(ggufArray))
	binary.Write(&b, le, uint32(ggufString))
	binary.Write(&b, le, uint64(3))
	str("a")
	str("bb")
	str("ccc")
	str("qwen3.block_count")
	binary.Write(&b, le, uint32(ggufU32))
	binary.Write(&b, le, uint32(48))
	str("general.scores")
	binary.Write(&b, le, uint32(ggufArray))
	binary.Write(&b, le, uint32(ggufF32))
	binary.Write(&b, le, uint64(2))
	binary.Write(&b, le, [2]float32{1, 2})
	str("general.architecture")
	binary.Write(&b, le, uint32(ggufString))
	str("qwen3")

	p := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := ggufBlockCount(p)
	if err != nil || n != 48 {
		t.Fatalf("48 couches attendues, obtenu %d (%v)", n, err)
	}
	if _, err := ggufBlockCount(filepath.Join(t.TempDir(), "absent.gguf")); err == nil {
		t.Fatal("erreur attendue sur un fichier absent")
	}
}
