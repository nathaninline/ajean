package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AJEAN_HOME", root)
	for rel, s := range map[string]string{"MEMORY/profile.md": "contenu-privé", ".ajean/browser/cache": "à ignorer", "config.json": "{}"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := t.TempDir()
	path, err := Create(out, "phrase de test assez longue")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "contenu-privé") {
		t.Fatal("archive non chiffrée")
	}
	if err := Restore(path, filepath.Join(out, "faux"), "mauvaise phrase de test"); err == nil {
		t.Fatal("une mauvaise phrase doit échouer")
	}
	dest := filepath.Join(out, "bon")
	if err := Restore(path, dest, "phrase de test assez longue"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "MEMORY", "profile.md"))
	if err != nil || string(got) != "contenu-privé" {
		t.Fatalf("%q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".ajean", "browser", "cache")); err == nil {
		t.Fatal("le profil du navigateur ne doit pas être sauvegardé")
	}
}
