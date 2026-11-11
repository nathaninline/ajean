package ajean

import (
	"strings"
	"testing"
)

// issue #95, dernière partie : un preset distant reste une config COMPLÈTE.
// externalPresetContent ne produit ni HOST ni PORT — contrairement à un preset
// normal (newPresetSeed). Comme applyPresetFile remplace TOUTE la config et que
// preservedKeys ne contient pas PORT, basculer sur un preset distant effaçait le
// port du moteur. (Depuis la 0.20 le moteur lui-même n'est plus un réglage : BIN
// n'existe plus, c'est le llama.cpp livré avec AJEAN.)

// saveExternalPreset enregistre un preset externe comme le fait
// handlePresetExternalSave à la création.
func saveExternalPreset(t *testing.T, name string) string {
	t.Helper()
	content := externalPresetContent("https://api.nanogpt.com/v1", "gpt-4o-mini", "sk-test", "32768", false)
	content = externalPresetWithMachine(content, externalMachineKeys(""))
	id, err := SavePreset("", name, content)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// 1) La bascule vers un preset distant ne doit plus emporter les réglages de
// MACHINE (où le moteur écoute).
func TestExternalPresetSwitchKeepsMachineKeys(t *testing.T) {
	testHome(t)
	setConfig(t, "MODEL=Qwen3.gguf\nHOST=0.0.0.0\nPORT=8080\n")

	p, err := safePresetPath(saveExternalPreset(t, "nanogpt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyPresetFile(p); err != nil {
		t.Fatal(err)
	}

	cfg := ReadConfig()
	if !isExternalConfig(cfg) {
		t.Fatal("le preset externe devrait être actif")
	}
	for k, want := range map[string]string{"HOST": "0.0.0.0", "PORT": "8080"} {
		if cfg[k] != want {
			t.Errorf("%s = %q après bascule, attendu %q (réglage machine perdu)", k, cfg[k], want)
		}
	}
	// MODEL, lui, reste un réglage de MODÈLE : il n'a pas à voyager dans un preset
	// distant (le modèle distant vit dans EXTERNAL_MODEL, et newPresetSeedKeys
	// l'exclut depuis l'issue #17). Il revient en rebasculant sur un preset local.
	if cfg["MODEL"] != "" {
		t.Errorf("MODEL = %q : un preset distant ne doit pas porter un .gguf local", cfg["MODEL"])
	}
}

// 2) Le corps produit porte les réglages fournis, ignore les valeurs vides, et
// n'écrase qu'avec une valeur réelle.
func TestExternalPresetWithMachineMergesCleanly(t *testing.T) {
	base := externalPresetContent("https://api.x/v1", "m", "sk", "32768", false)

	got := externalPresetWithMachine(base, map[string]string{"HOST": "   ", "PORT": "9090"})
	if strings.Contains(got, "HOST=") {
		t.Errorf("une valeur vide ne doit rien écrire :\n%s", got)
	}
	if !strings.Contains(got, "PORT=9090") {
		t.Errorf("PORT manquant :\n%s", got)
	}
	// Ré-application de la même valeur : pas de doublon de clé.
	if twice := externalPresetWithMachine(got, map[string]string{"PORT": "9090"}); strings.Count(twice, "PORT=") != 1 {
		t.Errorf("PORT dupliqué :\n%s", twice)
	}
	// Une nouvelle valeur écrase l'ancienne, sans doublon.
	over := externalPresetWithMachine(got, map[string]string{"PORT": "9191"})
	if strings.Count(over, "PORT=") != 1 || !strings.Contains(over, "PORT=9191") {
		t.Errorf("écrasement attendu :\n%s", over)
	}
	// Le reste du corps est intact.
	if !strings.Contains(over, "EXTERNAL=1") || !strings.Contains(over, "EXTERNAL_MODEL=m") {
		t.Errorf("le corps du preset a été abîmé :\n%s", over)
	}
}

// 3) Une ÉDITION reprend le PORT DU PRESET, pas celui d'un autre preset actif :
// changer l'URL d'un preset existant ne doit pas l'amputer.
func TestExternalPresetEditKeepsOwnPort(t *testing.T) {
	testHome(t)
	setConfig(t, "HOST=0.0.0.0\nPORT=8080\n")
	id := saveExternalPreset(t, "nanogpt")
	if old, err := ReadPreset(id); err != nil || !strings.Contains(old, "PORT=8080") {
		t.Fatalf("le preset créé devrait porter PORT (err=%v)", err)
	}

	// La config vive bascule sur un AUTRE port : l'édition ne doit pas l'importer.
	setConfig(t, "PORT=9999\nMODEL=Autre.gguf\n")
	edited := externalPresetWithMachine(
		externalPresetContent("https://api.nanogpt.com/v2", "gpt-4o", "sk-new", "", false),
		externalMachineKeys(id))

	if !strings.Contains(edited, "PORT=8080") {
		t.Errorf("l'édition doit conserver le PORT du preset :\n%s", edited)
	}
	if strings.Contains(edited, "9999") {
		t.Errorf("l'édition ne doit pas importer le PORT d'un autre preset :\n%s", edited)
	}
	// Un BIN laissé par une version d'avant la 0.20 ne se propage plus.
	if strings.Contains(edited, "BIN=") {
		t.Errorf("BIN ne doit plus être repris dans un preset :\n%s", edited)
	}
}
