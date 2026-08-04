package ajean

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// TestExtractArchiveSymlink : les archives macOS/Linux de llama.cpp livrent les
// bibliothèques sous leur nom versionné (libllama-common.0.0.10107.dylib) plus
// un LIEN SYMBOLIQUE portant le nom recherché par l'éditeur de liens
// (libllama-common.0.dylib). L'extracteur ignorait ces entrées : le backend
// s'installait mais llama-server mourait sur « Library not loaded ».
func TestExtractArchiveSymlink(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "backend.tar.gz")

	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("faux contenu de bibliothèque")
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "b/libllama-common.0.0.1.dylib",
		Mode: 0o644, Size: int64(len(body)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeSymlink, Name: "b/libllama-common.0.dylib",
		Linkname: "libllama-common.0.0.1.dylib", Mode: 0o777,
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []func() error{tw.Close, gz.Close, f.Close} {
		if err := c(); err != nil {
			t.Fatal(err)
		}
	}

	out := filepath.Join(dir, "out")
	if err := extractArchive(archive, out); err != nil {
		t.Fatalf("extractArchive: %v", err)
	}

	// Le lien doit être RÉSOLVABLE : symlink là où c'est permis, copie sinon
	// (Windows sans mode développeur). Dans les deux cas os.ReadFile réussit.
	got, err := os.ReadFile(filepath.Join(out, "b", "libllama-common.0.dylib"))
	if err != nil {
		t.Fatalf("lien non extrait : %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("contenu résolu = %q, attendu %q", got, body)
	}
}
