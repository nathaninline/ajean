package ajean

import (
	"os"
	"testing"
)

// Round-trip complet : la mémoire chiffrée fournit la DEK, le blob s'ouvre avec
// le mot de passe de chiffrement (= la clé), et tout se restaure. AUCUN mot de
// passe de sauvegarde séparé.
func TestBackupBlobRoundTrip(t *testing.T) {
	testHome(t)
	clearMemDEK()
	pages := seedPages(t)
	os.MkdirAll(presetsDir(), 0o755)
	os.WriteFile(presetsDir()+"/p1.env", []byte("# NAME=Test\nMODEL=x.gguf\n"), 0o600)
	SetConfigKey("CTX", "4096")

	// Le chiffrement fournit la DEK + le keyvault (ouvrable par "cle").
	if _, err := EnableMemEncryption("cle"); err != nil {
		t.Fatal(err)
	}
	dek, err := currentDEK()
	if err != nil {
		t.Fatal(err)
	}
	v, _ := loadVault()

	tarData, err := buildBundleTar()
	if err != nil {
		t.Fatalf("buildBundleTar: %v", err)
	}
	blob, err := buildBackupBlob(v, dek, tarData)
	if err != nil {
		t.Fatalf("buildBackupBlob: %v", err)
	}
	// Une mauvaise clé échoue.
	if _, err := openBackupBlob(blob, "mauvaise"); err == nil {
		t.Fatal("une mauvaise clé n'aurait pas dû ouvrir la sauvegarde")
	}
	// La bonne clé ouvre.
	back, err := openBackupBlob(blob, "cle")
	if err != nil {
		t.Fatalf("openBackupBlob: %v", err)
	}

	// Casse tout puis restaure.
	for name := range pages {
		MemDelete(name)
	}
	os.Remove(presetsDir() + "/p1.env")
	SetConfigKey("CTX", "")

	if err := restoreBundleTar(back); err != nil {
		t.Fatalf("restoreBundleTar: %v", err)
	}
	// La mémoire est chiffrée : elle reste lisible (DEK toujours en RAM) et intacte.
	for name, want := range pages {
		if got := MemContent(name); got != want {
			t.Fatalf("page %s après restauration: %q != %q", name, got, want)
		}
	}
	if _, err := os.Stat(presetsDir() + "/p1.env"); err != nil {
		t.Fatal("preset non restauré")
	}
	if ReadConfig()["CTX"] != "4096" {
		t.Fatal("réglage non restauré")
	}
}

// La clé de récupération ouvre aussi la sauvegarde.
func TestBackupBlobRecovery(t *testing.T) {
	testHome(t)
	clearMemDEK()
	seedPages(t)
	rec, err := EnableMemEncryption("cle")
	if err != nil {
		t.Fatal(err)
	}
	dek, _ := currentDEK()
	v, _ := loadVault()
	tarData, _ := buildBundleTar()
	blob, err := buildBackupBlob(v, dek, tarData)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openBackupBlob(blob, normalizeRecovery(rec)); err != nil {
		t.Fatalf("la clé de récupération devrait ouvrir la sauvegarde: %v", err)
	}
}
