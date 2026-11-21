package ajean

import (
	"os"
	"path/filepath"
	"testing"
)

// Deux dépôts publient le même nom de fichier : le second modèle reçoit un nom
// qui dit d'où il vient, au lieu d'être refusé comme « déjà téléchargé ».
func TestAltModelName(t *testing.T) {
	dir := t.TempDir()
	touch := func(n string) { _ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644) }
	name := "Qwen3.6-35B-A3B-UD-IQ3_XXS.gguf"
	touch(name)
	mtp := "https://huggingface.co/unsloth/Qwen3.6-35B-A3B-MTP-GGUF/resolve/main/" + name
	if got := altModelName(name, mtp, dir); got != "Qwen3.6-35B-A3B-UD-IQ3_XXS-MTP.gguf" {
		t.Errorf("variante MTP : %q", got)
	}
	// Même nom de dépôt chez un autre auteur : c'est l'auteur qui distingue.
	other := "https://huggingface.co/bartowski/Qwen3.6-35B-A3B-GGUF/resolve/main/" + name
	if got := altModelName(name, other, dir); got != "Qwen3.6-35B-A3B-UD-IQ3_XXS-bartowski.gguf" {
		t.Errorf("autre auteur : %q", got)
	}
	touch("Qwen3.6-35B-A3B-UD-IQ3_XXS-bartowski.gguf")
	if got := altModelName(name, other, dir); got != "Qwen3.6-35B-A3B-UD-IQ3_XXS-2.gguf" {
		t.Errorf("auteur déjà pris : %q", got)
	}
	// Lien hors Hugging Face : un simple numéro.
	if got := altModelName(name, "https://exemple.org/m/"+name, dir); got != "Qwen3.6-35B-A3B-UD-IQ3_XXS-2.gguf" {
		t.Errorf("lien quelconque : %q", got)
	}
}
