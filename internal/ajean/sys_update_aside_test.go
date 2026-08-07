package ajean

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Le remplacement du binaire doit fonctionner MÊME si un écartement précédent
// est toujours là et impossible à supprimer (processus encore vivant dessus).
// Avec un nom fixe « .old », le renommage échouait alors en « Accès refusé »,
// que ajean traduisait par « droits insuffisants, relance en administrateur » :
// un conseil faux, aucun privilège ne remplace une image en cours d'exécution.
func TestRenameAsideNeBloquePasSurUnAncienEcartement(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ajean.exe")
	if err := os.WriteFile(exe, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Un écartement d'une mise à jour précédente occupe déjà le nom fixe.
	if err := os.WriteFile(exe+".old", []byte("v0"), 0o755); err != nil {
		t.Fatal(err)
	}
	aside, err := renameAside(exe)
	if err != nil {
		t.Fatalf("renameAside a échoué alors qu'un .old existe déjà : %v", err)
	}
	if aside == exe+".old" {
		t.Error("nom d'écartement fixe : c'est précisément ce qui bloquait")
	}
	if !strings.HasPrefix(filepath.Base(aside), "ajean.exe.old-") {
		t.Errorf("nom inattendu : %s", aside)
	}
	if _, err := os.Stat(exe); !os.IsNotExist(err) {
		t.Error("le binaire n'a pas été écarté")
	}
	// Deux écartements successifs ne doivent jamais se marcher dessus.
	if err := os.WriteFile(exe, []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	aside2, err := renameAside(exe)
	if err != nil {
		t.Fatal(err)
	}
	if aside2 == aside {
		t.Error("deux écartements ont produit le même nom")
	}
}

// Le ménage doit ramasser l'ancien nom fixe ET les noms uniques.
func TestRemoveOldBinaries(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ajean.exe")
	for _, n := range []string{exe + ".old", exe + ".old-1", exe + ".old-2"} {
		if err := os.WriteFile(n, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(exe, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	removeOldBinaries(exe)
	restes, _ := filepath.Glob(exe + ".old*")
	if len(restes) != 0 {
		t.Errorf("écartements non nettoyés : %v", restes)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Errorf("le binaire courant a été supprimé : %v", err)
	}
}
