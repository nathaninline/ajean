package ajean

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Un preset MoE n'a ni BIN ni MODEL : `ajean start` / `restart` le refusaient en
// réclamant llama.cpp. Le pré-vol vérifie à la place ce dont le moteur MoE a besoin.
func TestPreflightMoe(t *testing.T) {
	home := testHome(t)
	src := filepath.Join(home, "moe-src")
	conf := filepath.Join(src, "moe.json")
	setConfig(t, "ENGINE=moe\nMOE_CONFIG="+conf+"\n")

	err := preflightEngine()
	if err == nil || strings.Contains(err.Error(), "BIN") {
		t.Fatalf("config MoE absente : erreur MoE attendue, obtenu %v", err)
	}

	py := moeVenvPython(src)
	if err := os.MkdirAll(filepath.Dir(py), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte(`{"args":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := preflightEngine(); err == nil || !strings.Contains(err.Error(), "Python") {
		t.Fatalf("environnement Python absent : erreur attendue, obtenu %v", err)
	}

	if err := os.WriteFile(py, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := preflightEngine(); err != nil {
		t.Fatalf("preset MoE complet refusé : %v", err)
	}
}
