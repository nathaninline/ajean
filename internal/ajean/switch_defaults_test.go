package ajean

import (
	"os"
	"path/filepath"
	"testing"
)

// Installation neuve : internet et navigateur activés. Installation existante :
// rien ne change. Un « éteint » choisi tient au redémarrage suivant.
func TestSwitchDefaults(t *testing.T) {
	testHome(t)
	migrateSwitchDefaults()
	if !internetEnabled() || !computerUseEnabled() {
		t.Fatal("installation neuve : internet et navigateur doivent être activés")
	}
	if err := setInternetEnabled(false); err != nil {
		t.Fatal(err)
	}
	migrateSwitchDefaults()
	if internetEnabled() {
		t.Fatal("un accès internet coupé par l'utilisateur doit le rester")
	}

	testHome(t)
	if err := os.MkdirAll(presetsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(presetsDir(), "a.env"), []byte("MODEL=x.gguf\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	migrateSwitchDefaults()
	if internetEnabled() || computerUseEnabled() {
		t.Fatal("installation existante : la mise à jour ne doit rien allumer")
	}
}
