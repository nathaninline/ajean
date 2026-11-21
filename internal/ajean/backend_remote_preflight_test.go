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
// relance en boucle — cf. le commentaire de preflightEngine). Depuis la 0.21 un
// preset sans BIN tourne sur le moteur AJEAN (engine_resolve.go) : ce qui reste
// refusé, c'est un moteur de la 0.18 désigné par BIN et absent, ou un modèle
// introuvable.
func TestPreflightStillGuardsLocalPresets(t *testing.T) {
	testHome(t)
	setConfig(t, "BIN=/opt/llama/absent/llama-server\nMODEL=Qwen3.gguf\n")
	if err := preflightEngine(); err == nil || !strings.Contains(err.Error(), "moteur introuvable") {
		t.Fatalf("un preset local dont le moteur manque doit rester refusé, obtenu %v", err)
	}
	setConfig(t, "MODEL=Qwen3.gguf\n")
	if err := preflightEngine(); err == nil || strings.Contains(err.Error(), "BIN") || !strings.Contains(err.Error(), "Qwen3.gguf") {
		t.Fatalf("un preset local sans BIN passe au moteur AJEAN et bute sur son modèle absent, obtenu %v", err)
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

// 4) Garde-fou inverse du précédent : un preset local incomplet échoue toujours.
// Sans BIN il tourne sur le moteur AJEAN (0.21) ; son modèle absent l'arrête
// AVANT toute installation du moteur.
func TestServeStillFailsForIncompleteLocalPreset(t *testing.T) {
	testHome(t)
	failIfEngineDownloads(t)
	setConfig(t, "MODEL=Qwen3.gguf\n")
	if err := cmdServe(nil); err == nil || !strings.Contains(err.Error(), "Qwen3.gguf") {
		t.Fatalf("un preset local sans modèle présent doit toujours échouer, obtenu %v", err)
	}
	if engineInstalled() != nil {
		t.Error("le moteur AJEAN a été installé pour un preset qui ne peut pas démarrer")
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
