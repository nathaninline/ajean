package secrets

import (
	"os"
	"strings"
	"testing"

	"ajean/internal/home"
)

func TestVaultRoundTrip(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := Set("mail.exemple", "valeur-de-test-42"); err != nil {
		t.Fatal(err)
	}
	if v, err := Get("mail.exemple"); err != nil || v != "valeur-de-test-42" {
		t.Fatalf("%q, %v", v, err)
	}
	raw, _ := os.ReadFile(home.State("vault.json"))
	if strings.Contains(string(raw), "valeur-de-test-42") {
		t.Fatal("le coffre contient le secret en clair")
	}
}

func TestRedact(t *testing.T) {
	for _, s := range []string{
		"mot de passe : Azerty123",
		"mdp sudo = valeurtest",
		"password=hunter2hunter",
		"echo 'valeurtest' | sudo -S -p '' systemctl restart x",
		"clé sk-abcdefghijklmnopqrstuvwxyz1234",
	} {
		if !Contains(s) {
			t.Errorf("non détecté : %s", s)
		}
	}
	for _, s := range []string{
		"m.login(cfg['email'], cfg['password'])",
		"password = os.environ.PASSWORD",
		"Le mot de passe a été changé hier",
		"token: <ton jeton>",
		`secrets.get("mail")`,
	} {
		if Contains(s) {
			t.Errorf("faux positif : %s -> %s", s, Redact(s))
		}
	}
}
