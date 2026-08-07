package ajean

import (
	"os"
	"path/filepath"
	"testing"
)

// Un modèle posé hors de models/ (disque externe) doit être utilisable dès
// que son dossier est déclaré — et refusé tant qu'il ne l'est pas.
func TestResolveModelPathExtraDir(t *testing.T) {
	testHome(t)
	ext := t.TempDir()
	t.Setenv("AJEAN_MODEL_DIRS", "")

	extModel := filepath.Join(ext, "gros.gguf")
	if err := os.WriteFile(extModel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveModelPath(extModel); err == nil {
		t.Fatal("un dossier non déclaré devrait être refusé")
	}

	if err := saveExtraModelDirs([]string{ext}); err != nil {
		t.Fatal(err)
	}
	got, err := resolveModelPath(extModel)
	if err != nil {
		t.Fatalf("chemin absolu déclaré refusé : %v", err)
	}
	if got != filepath.Clean(extModel) {
		t.Fatalf("chemin = %s, attendu %s", got, extModel)
	}
	// Le simple nom de fichier doit aussi être retrouvé dans le dossier ajouté.
	if got, err := resolveModelPath("gros.gguf"); err != nil || got != extModel {
		t.Fatalf("nom seul = %q (%v), attendu %s", got, err, extModel)
	}
}

// models/ garde la priorité et les non-.gguf restent refusés.
func TestResolveModelPathHomeFirst(t *testing.T) {
	testHome(t)
	ext := t.TempDir()
	t.Setenv("AJEAN_MODEL_DIRS", ext)
	if err := os.MkdirAll(modelsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{modelsDir(), ext} {
		if err := os.WriteFile(filepath.Join(d, "m.gguf"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := resolveModelPath("m.gguf")
	if err != nil || got != filepath.Join(modelsDir(), "m.gguf") {
		t.Fatalf("got %q (%v), attendu le fichier de models/", got, err)
	}
	if _, err := resolveModelPath("/etc/passwd"); err == nil {
		t.Fatal("un fichier non-.gguf devrait être refusé")
	}
}

// La ligne MODEL= garde son chemin complet, mais la quantization se déduit
// toujours du nom de fichier.
func TestPresetModelKeepsPath(t *testing.T) {
	content := "MODEL=\"/mnt/ext/Qwen3-30B-Q5_K_M.gguf\"\nCTX=8192\n"
	if got := modelFromPresetContent(content); got != "/mnt/ext/Qwen3-30B-Q5_K_M.gguf" {
		t.Fatalf("MODEL = %q, chemin tronqué", got)
	}
	if got := detectQuant(content); got != "Q5_K_M" {
		t.Fatalf("quant = %q, attendu Q5_K_M", got)
	}
}
