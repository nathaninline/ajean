package ajean

import (
	"os"
	"path/filepath"
	"testing"
)

// Chiffrement désactivé : les .bak encore chiffrés (illisibles, la clé est
// partie) sont retirés ; les .bak en clair et les pages restent. Chiffrement
// actif : rien n'est touché.
func TestScrubEncryptedResidue(t *testing.T) {
	testHome(t)
	dir := filepath.Join(projectsRoot(), "_jean")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	enc := append(append([]byte{}, memPageMagic...), "opaque"...)
	files := map[string][]byte{
		"fiche.md":       []byte("page en clair"),
		"fiche.md.bak":   enc,
		"profile.md.bak": []byte("ancienne version en clair"),
	}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil }

	if err := SetConfigKey("MEM_ENCRYPTED", "1"); err != nil {
		t.Fatal(err)
	}
	if n := scrubEncryptedResidue(); n != 0 || !exists("fiche.md.bak") {
		t.Fatalf("chiffrement actif : rien ne doit être retiré (n=%d)", n)
	}
	if err := SetConfigKey("MEM_ENCRYPTED", ""); err != nil {
		t.Fatal(err)
	}
	if n := scrubEncryptedResidue(); n != 1 {
		t.Fatalf("1 .bak chiffré attendu retiré, obtenu %d", n)
	}
	if exists("fiche.md.bak") || !exists("fiche.md") || !exists("profile.md.bak") {
		t.Fatal("seul le .bak chiffré doit disparaître")
	}
}
