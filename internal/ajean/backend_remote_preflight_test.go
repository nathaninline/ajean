package ajean

import (
	"strings"
	"testing"
)

// issue #95, suite : un preset distant (API externe ou GPU cloud) n'a aucun
// moteur LOCAL. La v0.16.4 a corrigé le chemin du service (cmdServe sort
// proprement, plus de boucle systemd au boot) mais pas preflightEngine, qui
// garde `ajean start` et `ajean restart`.

// 1) `ajean start`/`restart` sur un preset distant ne doit pas réclamer BIN/MODEL.
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

// 2) Garde-fou inverse : le pré-vol doit continuer de refuser un preset LOCAL
// incomplet. C'est sa raison d'être (moteur qui démarre, meurt, et que systemd
// relance en boucle — cf. le commentaire de preflightEngine).
func TestPreflightStillGuardsLocalPresets(t *testing.T) {
	testHome(t)
	setConfig(t, "MODEL=Qwen3.gguf\n")
	err := preflightEngine()
	if err == nil || !strings.Contains(err.Error(), "BIN non défini") {
		t.Fatalf("un preset local sans BIN doit rester refusé, obtenu %v", err)
	}
}

// 3) Garde-fou pour la correction de la v0.16.4 : cmdServe doit sortir en code 0
// sur un preset externe (une erreur nourrirait la boucle Restart=on-failure).
func TestServeExitsCleanlyForExternalPreset(t *testing.T) {
	testHome(t)
	setConfig(t, "EXTERNAL=1\nEXTERNAL_URL=https://api.nanogpt.com/v1\nEXTERNAL_MODEL=gpt-4o-mini\n")
	if err := cmdServe(nil); err != nil {
		t.Errorf("cmdServe doit sortir proprement pour un preset externe, obtenu %v", err)
	}
}

// 4) Garde-fou inverse du précédent : le moteur local exige toujours BIN.
func TestServeStillNeedsBinForLocalPreset(t *testing.T) {
	testHome(t)
	setConfig(t, "MODEL=Qwen3.gguf\n")
	if err := cmdServe(nil); err == nil || !strings.Contains(err.Error(), "BIN non défini") {
		t.Fatalf("un preset local sans BIN doit toujours échouer, obtenu %v", err)
	}
}

// 5) Le « conflit EXTERNAL contre BIN/MODEL » évoqué dans l'issue n'existe pas :
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
