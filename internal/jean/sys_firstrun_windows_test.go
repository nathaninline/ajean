//go:build windows

package jean

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// binaryVersion pilote la mise à jour automatique du binaire installé quand on
// lance le fichier téléchargé : si elle renvoie du vide, plus aucune mise à jour
// n'a lieu, et en silence. C'est exactement ce qui arrivait avec la première
// implémentation (exécuter `jean version`, dont la sortie est vide pour un
// binaire en sous-système GUI). D'où ces tests sur la plomberie syscall.
func TestBinaryVersionReadsResource(t *testing.T) {
	// kernel32.dll porte toujours une ressource de version.
	dll := filepath.Join(os.Getenv("SystemRoot"), "System32", "kernel32.dll")
	if _, err := os.Stat(dll); err != nil {
		t.Skip("kernel32.dll introuvable")
	}
	v := binaryVersion(dll)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
		t.Fatalf("binaryVersion(kernel32.dll) = %q, attendu une version x.y.z", v)
	}
}

// Un fichier sans ressource de version doit renvoyer "" : upgradeInstalled s'en
// sert pour s'abstenir plutôt que de remplacer un binaire au hasard.
func TestBinaryVersionWithoutResource(t *testing.T) {
	p := filepath.Join(t.TempDir(), "vide.exe")
	if err := os.WriteFile(p, []byte("pas un exe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v := binaryVersion(p); v != "" {
		t.Fatalf("binaryVersion sur un fichier sans ressource = %q, attendu \"\"", v)
	}
}
