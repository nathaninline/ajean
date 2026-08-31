package ajean

import "testing"

// testHome donne au test un $AJEAN_HOME à lui, et neutralise le pare-feu : un
// test ne doit jamais poser (ni retirer) une règle entrante sur la machine qui
// le fait tourner.
func testHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("AJEAN_HOME", home)
	firewallInert = true
	// Le projet actif est mis en cache au niveau du process : on le réinitialise pour
	// que chaque test parte du défaut (le AJEAN_HOME change à chaque test).
	activeProjMu.Lock()
	activeProjCache = ""
	activeProjMu.Unlock()
	setProjectOverride("")
	t.Cleanup(func() { firewallInert = false })
	return home
}

// La configuration écrite doit se relire à l'identique, et une clé vidée
// disparaître au lieu de rester à "".
func TestConfigAllerRetour(t *testing.T) {
	testHome(t)
	if err := WriteConfig(map[string]string{"MODEL": "m.gguf", "CTX": "4096"}); err != nil {
		t.Fatal(err)
	}
	if cfg := ReadConfig(); cfg["MODEL"] != "m.gguf" || cfg["CTX"] != "4096" {
		t.Fatalf("configuration relue incorrecte : %v", cfg)
	}
	if err := SetConfigKey("CTX", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadConfig()["CTX"]; ok {
		t.Error("CTX vidée mais toujours présente")
	}
}

// WriteConfig REMPLACE : aucune clé de l'ancienne configuration ne doit
// survivre, sans quoi une bascule de preset laisserait des réglages fantômes.
func TestWriteConfigRemplaceTout(t *testing.T) {
	testHome(t)
	if err := WriteConfig(map[string]string{"A": "1", "B": "2"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig(map[string]string{"B": "3"}); err != nil {
		t.Fatal(err)
	}
	cfg := ReadConfig()
	if _, ok := cfg["A"]; ok {
		t.Error("A a survécu au remplacement")
	}
	if cfg["B"] != "3" {
		t.Errorf("B = %q, attendu 3", cfg["B"])
	}
}

// Une base absente se comporte comme une base vide : les lecteurs ont tous un
// défaut, aucun ne doit paniquer.
func TestLecturesSurBaseVide(t *testing.T) {
	testHome(t)
	if len(ReadConfig()) != 0 {
		t.Error("configuration non vide sur une base neuve")
	}
	if agentEnabled() || internetEnabled() {
		t.Error("interrupteurs actifs sur une base neuve")
	}
	if readAPIKey() != "" || readWebKey() != "" || readLinkToken() != "" {
		t.Error("clés non vides sur une base neuve")
	}
}
