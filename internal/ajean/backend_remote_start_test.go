package ajean

import (
	"os"
	"strings"
	"testing"
)

// issue #95 — un preset distant (API externe, ou GPU cloud) n'a AUCUN moteur local.
// Trois défauts s'enchaînaient : la bascule effaçait BIN de la config vive, le
// pré-vol refusait ensuite de démarrer, et le moteur sortait en erreur au boot
// (donc systemd le relançait toutes les 3 s).

// seedExternalPreset reproduit ce que fait handlePresetExternalSave à la création :
// les réglages de machine viennent de la config vive (newPresetSeed).
func seedExternalPreset(t *testing.T, name string) string {
	t.Helper()
	content := externalPresetContent("https://api.nanogpt.com/v1", "gpt-4o-mini", "sk-test", "32768", newPresetSeed(), false)
	id, err := SavePreset("", name, content)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// 1) La bascule vers un preset distant ne doit plus emporter les réglages de
// MACHINE : sans BIN sur le disque, la seule issue était de réinstaller llama.cpp.
func TestExternalPresetSwitchKeepsMachineKeys(t *testing.T) {
	testHome(t)
	setConfig(t, "BIN=/opt/llama/llama-server\nMODEL=Qwen3.gguf\nHOST=0.0.0.0\nPORT=8080\n")

	p, err := safePresetPath(seedExternalPreset(t, "nanogpt"))
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
	for k, want := range map[string]string{"BIN": "/opt/llama/llama-server", "HOST": "0.0.0.0", "PORT": "8080"} {
		if cfg[k] != want {
			t.Errorf("%s = %q après bascule, attendu %q (réglage machine perdu)", k, cfg[k], want)
		}
	}
	// MODEL, lui, reste un réglage de MODÈLE : il n'a pas à voyager dans un preset
	// distant (le modèle distant vit dans EXTERNAL_MODEL, et c'est la règle de
	// newPresetSeedKeys depuis l'issue #17). Il se retrouve en rebasculant sur un
	// preset local, ou en le rechoisissant dans la liste.
	if cfg["MODEL"] != "" {
		t.Errorf("MODEL = %q : un preset distant ne doit pas porter un .gguf local", cfg["MODEL"])
	}
}

// 2) Le pré-vol exigeait BIN/MODEL même pour un preset sans moteur local : c'était
// le « BIN non défini » du rapport.
func TestPreflightAcceptsRemotePresets(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"externe (nanogpt)", "EXTERNAL=1\nEXTERNAL_URL=https://api.nanogpt.com/v1\nEXTERNAL_MODEL=gpt-4o-mini\n"},
		{"cloud (modal)", "CLOUD=modal\nCLOUD_MODEL=https://huggingface.co/a/b/resolve/main/m.gguf\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testHome(t)
			setConfig(t, tc.body)
			if err := preflightEngine(); err != nil {
				t.Errorf("un preset distant n'a pas de moteur local à vérifier, obtenu %v", err)
			}
		})
	}
}

// 3) Garde-fou inverse : le pré-vol doit continuer de refuser un preset LOCAL
// incomplet. C'est la raison d'être de preflightEngine (moteur qui démarre, meurt,
// et que systemd relance en boucle).
func TestPreflightStillGuardsLocalPresets(t *testing.T) {
	testHome(t)
	setConfig(t, "MODEL=Qwen3.gguf\n")
	err := preflightEngine()
	if err == nil || !strings.Contains(err.Error(), "BIN non défini") {
		t.Fatalf("un preset local sans BIN doit rester refusé, obtenu %v", err)
	}
}

// 4) Le moteur doit sortir PROPREMENT (code 0) sur un preset externe : l'unité est
// Restart=on-failure et démarre au boot, donc une erreur la relançait sans fin.
func TestServeExitsCleanlyForExternalPreset(t *testing.T) {
	testHome(t)
	setConfig(t, "EXTERNAL=1\nEXTERNAL_URL=https://api.nanogpt.com/v1\nEXTERNAL_MODEL=gpt-4o-mini\n")
	if err := cmdServe(nil); err != nil {
		t.Errorf("cmdServe doit sortir proprement pour un preset externe, obtenu %v", err)
	}
}

// 5) Garde-fou inverse : le moteur local doit toujours exiger BIN.
func TestServeStillNeedsBinForLocalPreset(t *testing.T) {
	testHome(t)
	setConfig(t, "MODEL=Qwen3.gguf\n")
	if err := cmdServe(nil); err == nil || !strings.Contains(err.Error(), "BIN non défini") {
		t.Fatalf("un preset local sans BIN doit toujours échouer, obtenu %v", err)
	}
}

// 6) Le corps du preset externe porte les réglages de machine fournis, et n'écrit
// pas de clé vide.
func TestExternalPresetContentCarriesMachineKeys(t *testing.T) {
	got := externalPresetContent("https://api.x/v1", "m", "", "32768",
		map[string]string{"BIN": "/opt/llama/llama-server", "HOST": "", "PORT": "9090"}, false)
	for _, want := range []string{"EXTERNAL=1", "BIN=/opt/llama/llama-server", "PORT=9090"} {
		if !strings.Contains(got, want) {
			t.Errorf("manque %q dans :\n%s", want, got)
		}
	}
	if strings.Contains(got, "HOST=") {
		t.Errorf("une valeur vide ne doit pas être écrite :\n%s", got)
	}
}

// 7) Le « conflit EXTERNAL contre BIN/MODEL » évoqué dans l'issue n'existe pas :
// resolveChatEndpoint teste cloud → externe → local, donc l'externe gagne même si
// BIN/MODEL sont présents. Le blocage venait du seul pré-vol.
func TestExternalWithBinPresentIsNotAConflict(t *testing.T) {
	testHome(t)
	setConfig(t, "EXTERNAL=1\nEXTERNAL_URL=https://api.nanogpt.com/v1\nEXTERNAL_MODEL=gpt-4o-mini\nBIN=/opt/llama/llama-server\nMODEL=Qwen3.gguf\n")
	ep := resolveChatEndpoint()
	if !ep.External || ep.Model != "gpt-4o-mini" {
		t.Fatalf("l'externe doit gagner sur BIN/MODEL, obtenu %+v", ep)
	}
	if err := preflightEngine(); err != nil {
		t.Errorf("pré-vol : %v", err)
	}
}

// 8) Une ÉDITION de preset externe ne doit pas perdre le BIN déjà enregistré
// (même logique que la conservation de la clé API).
func TestExternalPresetEditKeepsBin(t *testing.T) {
	testHome(t)
	setConfig(t, "BIN=/opt/llama/llama-server\nHOST=0.0.0.0\nPORT=8080\n")
	id := seedExternalPreset(t, "nanogpt")
	p, err := safePresetPath(id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, p), "BIN=/opt/llama/llama-server") {
		t.Fatal("le preset créé devrait porter BIN")
	}
	// La config vive est ailleurs (autre preset) : l'édition doit reprendre le BIN
	// DU PRESET, pas celui de la config courante.
	setConfig(t, "BIN=/ailleurs/llama-server\nMODEL=Autre.gguf\n")
	old, err := ReadPreset(id)
	if err != nil {
		t.Fatal(err)
	}
	machine := newPresetSeed()
	have := parseEnv(old)
	for _, k := range newPresetSeedKeys {
		if v := strings.TrimSpace(have[k]); v != "" {
			machine[k] = v
		}
	}
	edited := externalPresetContent("https://api.nanogpt.com/v2", "gpt-4o", "sk-new", "", machine, false)
	if !strings.Contains(edited, "BIN=/opt/llama/llama-server") {
		t.Errorf("l'édition doit conserver le BIN du preset :\n%s", edited)
	}
	if strings.Contains(edited, "/ailleurs/") {
		t.Errorf("l'édition ne doit pas importer le BIN d'un autre preset :\n%s", edited)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
