package scripts

import (
	"strings"
	"testing"
)

func TestShellRun(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	out, err := run(Script{Name: "t", Code: `function run(){ var r = sh.run("echo bonjour"); return r.code + ":" + r.out }`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "0:bonjour") {
		t.Fatalf("sortie inattendue : %q", out)
	}
	out, err = run(Script{Name: "t", Code: `function run(){ return sh.run("exit 3").code }`}, nil)
	if err != nil || out != "3" {
		t.Fatalf("code de sortie : %q %v", out, err)
	}
}

func TestShellPredefined(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	code := `function run(a){ return sh.run("echo " + a.v).out }`
	if out, err := run(Script{Name: "t", Code: code}, map[string]any{"v": "photos/2024"}); err != nil || !strings.Contains(out, "photos/2024") {
		t.Fatalf("valeur simple refusée : %q %v", out, err)
	}
	for _, bad := range []string{"x; reboot", "$(id)", "-rf", "a b"} {
		if _, err := run(Script{Name: "t", Code: code}, map[string]any{"v": bad}); err == nil {
			t.Fatalf("%q aurait dû être refusé", bad)
		}
	}
	if _, err := save(Script{Name: "libre", Description: "x", Params: map[string]string{"cmd": "commande"}, Code: "function run(a){return 1}"}); err == nil {
		t.Fatal("paramètre cmd accepté")
	}
}

func TestSaveRefusesDuplicate(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	code := "function run(a){ return 1 }"
	if _, err := save(Script{Name: "un", Description: "x", Code: code}); err != nil {
		t.Fatal(err)
	}
	if _, err := save(Script{Name: "deux", Description: "x", Code: code}); err == nil {
		t.Fatal("doublon accepté")
	}
	if _, err := save(Script{Name: "un", Description: "y", Code: code}); err != nil {
		t.Fatalf("réenregistrer sous le même nom refusé : %v", err)
	}
}
