package ajean

import (
	"os"
	"testing"
)

// Le preset actif se supprime : moteur arrêté, configuration du modèle vidée,
// réglages de l'appareil conservés.
func TestDeleteActivePreset(t *testing.T) {
	testHome(t)
	stopped := 0
	old := presetStopEngine
	presetStopEngine = func() { stopped++ }
	defer func() { presetStopEngine = old }()

	setConfig(t, "HOST=0.0.0.0\nMEM_MODE=always\n")
	id, err := SavePreset("", "Essai", "MODEL=essai.gguf\nCTX=4096\n")
	if err != nil {
		t.Fatal(err)
	}
	other, err := SavePreset("", "Autre", "MODEL=autre.gguf\n")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := safePresetPath(id)
	if err := applyPresetFile(p); err != nil {
		t.Fatal(err)
	}
	// Un preset inactif se supprime sans toucher au moteur ni à la configuration.
	if err := DeletePreset(other); err != nil || stopped != 0 || ReadConfig()["MODEL"] != "essai.gguf" {
		t.Fatalf("suppression d'un preset inactif : err=%v arrêts=%d cfg=%v", err, stopped, ReadConfig())
	}
	if err := DeletePreset(id); err != nil {
		t.Fatalf("le preset actif doit pouvoir être supprimé : %v", err)
	}
	if stopped != 1 {
		t.Errorf("le moteur aurait dû être arrêté une fois (%d)", stopped)
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("le fichier du preset est resté")
	}
	cfg := ReadConfig()
	if cfg["MODEL"] != "" || cfg["CTX"] != "" {
		t.Errorf("la configuration du modèle supprimé est restée : %v", cfg)
	}
	if cfg["HOST"] != "0.0.0.0" || cfg["MEM_MODE"] != "always" {
		t.Errorf("les réglages de l'appareil ont été perdus : %v", cfg)
	}
	if activePresetID() != "" {
		t.Errorf("preset actif encore noté : %q", activePresetID())
	}
}
