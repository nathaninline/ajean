package ajean

// Destination des téléchargements : le dossier doit être choisi parmi les
// dossiers de modèles déclarés (sinon l'UI web écrirait n'importe où), et
// l'espace libre doit être vérifié AVANT de transférer 40 Go.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadDestPathDir(t *testing.T) {
	home := t.TempDir()
	extra := t.TempDir()
	t.Setenv("AJEAN_HOME", home)
	t.Setenv("AJEAN_MODEL_DIRS", extra)

	// Dossier vide → AJEAN_HOME.
	got, err := downloadDestPath("m.gguf", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "m.gguf"); got != want {
		t.Fatalf("défaut = %s, attendu %s", got, want)
	}

	// Dossier déclaré → accepté.
	got, err = downloadDestPath("m.gguf", extra)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(extra, "m.gguf"); got != want {
		t.Fatalf("dossier déclaré = %s, attendu %s", got, want)
	}

	// Dossier non déclaré → refusé.
	if _, err := downloadDestPath("m.gguf", t.TempDir()); err == nil {
		t.Fatal("un dossier non déclaré a été accepté")
	}

	// Le nom reste un simple fichier .gguf : pas de remontée de dossier.
	if _, err := downloadDestPath(filepath.Join("..", "evil.gguf"), extra); err != nil {
		t.Fatal(err)
	} else if p, _ := downloadDestPath(filepath.Join("..", "evil.gguf"), extra); filepath.Dir(p) != extra {
		t.Fatalf("path traversal : %s", p)
	}
}

func TestDiskFreeAndCheck(t *testing.T) {
	dir := t.TempDir()
	free := diskFree(dir)
	if free <= 0 {
		t.Skipf("espace libre non mesurable ici (%d)", free)
	}
	// Un dossier pas encore créé doit être mesuré via son parent.
	if diskFree(filepath.Join(dir, "pas", "encore")) <= 0 {
		t.Fatal("diskFree ne remonte pas aux parents existants")
	}
	if err := checkDiskSpace(dir, 1<<10); err != nil {
		t.Fatalf("1 Ko refusé alors que %d octets sont libres : %v", free, err)
	}
	err := checkDiskSpace(dir, free)
	if err == nil {
		t.Fatal("téléchargement accepté alors qu'il remplit le disque")
	}
	if !strings.Contains(err.Error(), "espace insuffisant") {
		t.Fatalf("message inattendu : %v", err)
	}
	// Taille inconnue (serveur sans Content-Length) : on laisse passer.
	if err := checkDiskSpace(dir, 0); err != nil {
		t.Fatalf("taille inconnue refusée : %v", err)
	}
}

// Les .part orphelins doivent aussi être nettoyés dans les dossiers ajoutés.
func TestCleanStalePartFilesExtraDirs(t *testing.T) {
	home, extra := t.TempDir(), t.TempDir()
	t.Setenv("AJEAN_HOME", home)
	t.Setenv("AJEAN_MODEL_DIRS", extra)
	stale := filepath.Join(extra, "coupe.gguf.part")
	if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanStalePartFiles()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal(".part orphelin d'un dossier ajouté non supprimé")
	}
}
