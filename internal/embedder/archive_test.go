package embedder

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// zipOf écrit une archive .zip faite de ces entrées (nom → contenu).
func zipOf(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err == nil {
			_, err = w.Write([]byte(content))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// names : les fichiers d'un dossier, triés.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}

func TestExtractKeepsOnlyTheServer(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "moteur.zip")
	zipOf(t, archive, map[string]string{
		"build/bin/llama-server.exe": "serveur",
		"build/bin/llama-cli.exe":    "autre programme",
		"build/bin/ggml-base.dll":    "bibliothèque",
		"LICENSE":                    "licence",
	})
	dir := filepath.Join(root, "moteur")
	if err := extract(archive, dir); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); !slices.Equal(got, []string{"ggml-base.dll", "llama-server.exe"}) {
		t.Errorf("fichiers extraits = %v", got)
	}
}

func TestExtractRefusesPathsLeavingTheArchive(t *testing.T) {
	for _, name := range []string{"../evil.dll", "build/../../evil.dll", "/evil.dll"} {
		root := t.TempDir()
		archive := filepath.Join(root, "moteur.zip")
		zipOf(t, archive, map[string]string{"llama-server.exe": "serveur", name: "intrus"})
		dir := filepath.Join(root, "a", "moteur")
		if err := extract(archive, dir); err == nil || !strings.Contains(err.Error(), "suspect") {
			t.Errorf("%q : erreur = %v, un refus était attendu", name, err)
		}
		for _, outside := range []string{root, filepath.Dir(dir)} {
			if _, err := os.Stat(filepath.Join(outside, "evil.dll")); err == nil {
				t.Errorf("%q : un fichier a été écrit hors du dossier", name)
			}
		}
	}
}

func TestExtractTarGzFollowsLinksToNeighbours(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "moteur.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	add := func(h tar.Header, content string) {
		h.Size, h.Mode = int64(len(content)), 0o755
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	// Le lien précède sa cible, comme dans les archives officielles.
	add(tar.Header{Name: "llama-b1/libllama.so", Typeflag: tar.TypeSymlink, Linkname: "libllama.so.0.1"}, "")
	add(tar.Header{Name: "llama-b1/libllama.so.0.1", Typeflag: tar.TypeReg}, "bibliothèque")
	add(tar.Header{Name: "llama-b1/llama-server", Typeflag: tar.TypeReg}, "serveur")
	add(tar.Header{Name: "llama-b1/llama-bench", Typeflag: tar.TypeReg}, "autre programme")
	// Un lien vers l'extérieur ne mène nulle part : tout est remis à plat.
	add(tar.Header{Name: "llama-b1/libevil.so", Typeflag: tar.TypeSymlink, Linkname: "../../../etc/passwd"}, "")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "moteur")
	if err := extract(archive, dir); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); !slices.Equal(got, []string{"libllama.so", "libllama.so.0.1", "llama-server"}) {
		t.Errorf("fichiers extraits = %v", got)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "libllama.so")); err != nil || string(got) != "bibliothèque" {
		t.Errorf("le lien ne mène pas à la bibliothèque : %q, %v", got, err)
	}
}
