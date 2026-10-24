package ajean

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMoeSourceHashStable : l'empreinte des sources est déterministe et
// insensible à l'ordre du système de fichiers (tri sur le chemin POSIX), comme
// la source_hash() de setup.py dont elle doit être l'égale pour que le moteur
// compilé par ajean soit vu « local à jour » (src/vision_src) et non recompilé.
func TestMoeSourceHashStable(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("CMakeLists.txt", "project(strata)\n")
	write("src/a.cpp", "int a;\r\nint b;\r\n")
	write("src/b.cpp", "int b;\n")
	write("include/strata/x.hpp", "#pragma once\n")
	write("third_party/ggml/ggml.h", "struct ggml;\n")
	write("tools/vision/main.cpp", "int vision;\n")

	got, err := moeSourceHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["engine"] == "" || got["vision"] == "" {
		t.Fatalf("empreinte vide : %v", got)
	}
	// La normalisation CRLF est active : changer \r en \r ne doit pas changer l'empreinte.
	write("src/b.cpp", "int b;\r\n")
	got2, err := moeSourceHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got2["engine"] != got["engine"] {
		t.Errorf("CRLF a changé l'empreinte : %s → %s", got["engine"], got2["engine"])
	}
	// Un changement de contenu change l'empreinte.
	write("src/a.cpp", "int changed;\n")
	got3, err := moeSourceHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got3["engine"] == got["engine"] {
		t.Error("l'empreinte n'a pas changé après modification d'un fichier")
	}
}
