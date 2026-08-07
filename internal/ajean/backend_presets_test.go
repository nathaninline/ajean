package ajean

import (
	"os"
	"path/filepath"
	"testing"
)

// setConfig installe une configuration décrite au format des presets.
func setConfig(t *testing.T, body string) {
	t.Helper()
	if err := WriteConfig(parseEnv(body)); err != nil {
		t.Fatal(err)
	}
}

// TestPresetFingerprintIgnoresCosmetics vérifie que la détection du preset actif
// ne dépend QUE de la config effective (ensemble KEY=VALUE), pas de la mise en
// forme : ordre des lignes, commentaires (dont `# NAME=`), lignes vides, `export`,
// espaces, ni des clés « appareil » (preservedKeys) réappliquées par SwitchToPreset.
// C'est la régression « aucun preset en surbrillance après un reformatage de
// config.env » (toggle mémoire/internet, réordonnancement…).
func TestPresetFingerprintIgnoresCosmetics(t *testing.T) {
	base := []byte("# NAME=Mon preset\nMODEL=foo.gguf\nCTX=4096\nNGL=999\n")
	// Même config effective, présentée autrement.
	variant := []byte("export NGL=999\nMEM_MODE=off\n\nCTX=4096\n# un commentaire\nMODEL=foo.gguf\n")
	if presetFingerprint(base) != presetFingerprint(variant) {
		t.Fatal("empreintes différentes alors que la config effective est identique")
	}
	// Un vrai changement de valeur doit, lui, produire une empreinte différente.
	changed := []byte("MODEL=foo.gguf\nCTX=8192\nNGL=999\n")
	if presetFingerprint(base) == presetFingerprint(changed) {
		t.Fatal("empreintes identiques alors que CTX diffère")
	}
}

// TestSwitchToPresetGardeLesReglagesMachine : basculer de preset ne doit pas
// effacer les réglages qui décrivent la MACHINE et non le modèle. Le cas vécu :
// `ajean gpu 1` écrit CUDA_VISIBLE_DEVICES dans config.env, la bascule de preset
// suivante l'écrasait, llama.cpp revoyait les deux cartes et réétalait le modèle
// sur la petite — pendant que `ajean gpu` réaffichait « auto ».
func TestSwitchToPresetGardeLesReglagesMachine(t *testing.T) {
	home := testHome(t)
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Configuration courante : un preset + les réglages machine.
	setConfig(t, "MODEL=ancien.gguf\nCTX=4096\nCUDA_VISIBLE_DEVICES=1\nWEB_ENGINE=go\nMEM_MODE=always\n")
	target := filepath.Join(home, "cible.env")
	write(target, "MODEL=nouveau.gguf\nCTX=8192\n")

	if err := applyPresetFile(target); err != nil {
		t.Fatal(err)
	}

	cfg := ReadConfig()
	if cfg["MODEL"] != "nouveau.gguf" || cfg["CTX"] != "8192" {
		t.Fatalf("le preset n'a pas été appliqué : %v", cfg)
	}
	for k, want := range map[string]string{
		"CUDA_VISIBLE_DEVICES": "1", "WEB_ENGINE": "go", "MEM_MODE": "always",
	} {
		if cfg[k] != want {
			t.Errorf("%s = %q après bascule, attendu %q (réglage machine effacé)", k, cfg[k], want)
		}
	}
}

// Un preset qui définit LUI-MÊME la sélection de cartes doit gagner sur celle
// de la machine : « FABLE 2 GPU » impose CUDA_VISIBLE_DEVICES=1,0 pour que son
// --tensor-split ait deux cartes. Écraser ça par une sélection mono-GPU faisait
// mourir le chargement sur « cudaMalloc failed: out of memory ».
func TestSwitchToPresetLaisseLePresetImposerSesGPU(t *testing.T) {
	home := testHome(t)
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setConfig(t, "MODEL=ancien.gguf\nCUDA_VISIBLE_DEVICES=1\nWEB_ENGINE=go\n")
	deuxGPU := filepath.Join(home, "deux-gpu.env")
	write(deuxGPU, "MODEL=fable.gguf\nCUDA_VISIBLE_DEVICES=1,0\n")
	if err := applyPresetFile(deuxGPU); err != nil {
		t.Fatal(err)
	}
	if got := ReadConfig()["CUDA_VISIBLE_DEVICES"]; got != "1,0" {
		t.Errorf("CUDA_VISIBLE_DEVICES = %q, attendu 1,0 (le preset doit gagner)", got)
	}
	// Un preset MUET sur les GPU laisse, lui, la sélection machine en place.
	muet := filepath.Join(home, "muet.env")
	write(muet, "MODEL=autre.gguf\n")
	if err := applyPresetFile(muet); err != nil {
		t.Fatal(err)
	}
	cfg := ReadConfig()
	if cfg["CUDA_VISIBLE_DEVICES"] != "1,0" {
		t.Errorf("CUDA_VISIBLE_DEVICES = %q, attendu 1,0 conservé", cfg["CUDA_VISIBLE_DEVICES"])
	}
	if cfg["WEB_ENGINE"] != "go" {
		t.Errorf("WEB_ENGINE = %q, attendu go (réglage machine, jamais dicté par un preset)", cfg["WEB_ENGINE"])
	}
}
