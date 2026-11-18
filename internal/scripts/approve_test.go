package scripts

import (
	"strings"
	"testing"

	"ajean/internal/tool"
)

func ready(t *testing.T) {
	t.Helper()
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := Init(); err != nil {
		t.Fatal(err)
	}
}

func put(t *testing.T, name, code string) {
	t.Helper()
	if _, err := save(Script{Name: name, Description: "d " + name, Code: code}); err != nil {
		t.Fatal(err)
	}
}

func TestExistingToolsStayValid(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	put(t, "ancien", `function run(){ return "ok" }`)
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if len(Changes()) != 0 {
		t.Fatalf("rien à valider au premier démarrage : %v", Changes())
	}
	if out, err := Run("ancien", nil); err != nil || out != "ok" {
		t.Fatalf("%q, %v", out, err)
	}
}

func TestChangesWaitForOneValidation(t *testing.T) {
	ready(t)
	put(t, "un", `function run(){ return "v1" }`)
	put(t, "un", `function run(){ return "v2" }`)
	put(t, "deux", `function run(){ return "d" }`)
	if got := Changes(); len(got) != 2 {
		t.Fatalf("plusieurs modifications = une seule liste : %v", got)
	}
	if out := execute(tool.Call{}, "un", nil); out != "v2" {
		t.Fatalf("essai pendant la réponse : %q", out)
	}
	if _, err := Run("un", nil); err == nil {
		t.Fatal("une tâche ou une app ne lance pas une version non validée")
	}
	if out := execute(tool.Call{Unattended: true}, "un", nil); !tool.IsErr(out) {
		t.Fatalf("tâche du modèle : %q", out)
	}
	if err := Accept(); err != nil {
		t.Fatal(err)
	}
	if out, err := Run("un", nil); err != nil || out != "v2" || len(Changes()) != 0 {
		t.Fatalf("après validation : %q, %v, %v", out, err, Changes())
	}
}

func TestRejectPutsEverythingBack(t *testing.T) {
	ready(t)
	put(t, "garde", `function run(){ return "bon" }`)
	put(t, "efface", `function run(){ return "là" }`)
	if err := Accept(); err != nil {
		t.Fatal(err)
	}
	put(t, "garde", `function run(){ return "mauvais" }`)
	put(t, "neuf", `function run(){ return "neuf" }`)
	if err := Delete("efface"); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, c := range Changes() {
		kinds = append(kinds, c.Name+":"+c.Kind)
	}
	if strings.Join(kinds, " ") != "efface:deleted garde:changed neuf:new" {
		t.Fatalf("changements : %v", kinds)
	}
	if err := Reject(); err != nil {
		t.Fatal(err)
	}
	if out, err := Run("garde", nil); err != nil || out != "bon" {
		t.Fatalf("garde : %q, %v", out, err)
	}
	if out, err := Run("efface", nil); err != nil || out != "là" {
		t.Fatalf("efface : %q, %v", out, err)
	}
	if _, err := load("neuf"); err == nil {
		t.Fatal("le nouvel outil refusé doit disparaître")
	}
}

func TestDiffShowsOnlyWhatChanges(t *testing.T) {
	got := lineDiff("a\nb\nc", "a\nB\nc")
	if got != "+ B\n- b" && got != "- b\n+ B" {
		t.Fatalf("%q", got)
	}
}

func TestRestoreBringsBackPreviousVersion(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	put(t, "outil", `function run(){ return "v1" }`)
	put(t, "outil", `function run(){ return nonDefini }`)
	if _, err := restore("outil", ""); err != nil {
		t.Fatal(err)
	}
	if out, err := Run("outil", nil); err != nil || out != "v1" {
		t.Fatalf("après restauration : %q, %v", out, err)
	}
}
