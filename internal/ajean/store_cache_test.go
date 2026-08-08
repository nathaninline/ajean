package ajean

import "testing"

// Le cache de lecture ne doit jamais servir une valeur périmée après une
// écriture de ce process : un « ajean edit » suivi d'un « ajean restart » doit
// démarrer sur la NOUVELLE configuration.
func TestCacheConfigInvalideParEcriture(t *testing.T) {
	testHome(t)
	if err := WriteConfig(map[string]string{"CTX": "4096"}); err != nil {
		t.Fatal(err)
	}
	if got := ReadConfig()["CTX"]; got != "4096" { // remplit le cache
		t.Fatalf("CTX = %q, attendu 4096", got)
	}
	if err := SetConfigKey("CTX", "8192"); err != nil {
		t.Fatal(err)
	}
	if got := ReadConfig()["CTX"]; got != "8192" {
		t.Fatalf("valeur périmée servie par le cache : CTX = %q, attendu 8192", got)
	}
	if err := WriteConfig(map[string]string{"MODEL": "m.gguf"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadConfig()["CTX"]; ok {
		t.Error("WriteConfig remplace tout : CTX ne doit plus exister")
	}
}

// La carte renvoyée appartient à l'appelant : la modifier ne doit pas polluer
// le cache, sans quoi un appelant distrait corromprait la configuration vue par
// tous les autres.
func TestCacheConfigRenvoieUneCopie(t *testing.T) {
	testHome(t)
	if err := WriteConfig(map[string]string{"CTX": "4096"}); err != nil {
		t.Fatal(err)
	}
	cfg := ReadConfig()
	cfg["CTX"] = "999"
	delete(cfg, "CTX")
	if got := ReadConfig()["CTX"]; got != "4096" {
		t.Fatalf("le cache a été altéré par l'appelant : CTX = %q", got)
	}
}

// Une lecture ratée ne doit pas se confondre avec « aucune clé » : sans clé,
// l'API de pilotage est OUVERTE, donc l'erreur doit remonter pour que
// requireWebAuth ferme au lieu d'ouvrir.
func TestWebKeyDistingueVideEtErreur(t *testing.T) {
	testHome(t)
	k, err := readWebKeyErr()
	if err != nil || k != "" {
		t.Fatalf("base neuve : attendu (\"\", nil), reçu (%q, %v)", k, err)
	}
	if err := putStr(bkState, "web_key", "secret"); err != nil {
		t.Fatal(err)
	}
	if k, err := readWebKeyErr(); err != nil || k != "secret" {
		t.Fatalf("attendu (\"secret\", nil), reçu (%q, %v)", k, err)
	}
}
