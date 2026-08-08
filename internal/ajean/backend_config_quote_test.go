package ajean

import (
	"reflect"
	"testing"
)

// Un guillemet INTERNE (argument cité dans EXTRA_ARGS) ne doit pas être mangé :
// l'ancien Trim retirait le guillemet final et renvoyait une valeur déséquilibrée
// dans le preset (issue #17).
func TestParseEnvKeepsInnerQuotes(t *testing.T) {
	cases := map[string]string{
		`EXTRA_ARGS=--jinja --chat-template-file "/etc/ajean/tpl.jinja"`: `--jinja --chat-template-file "/etc/ajean/tpl.jinja"`,
		`EXTRA_ARGS="--jinja --flash-attn"`:                              `--jinja --flash-attn`,
		`MODEL='/mnt/d/x.gguf'`:                                          `/mnt/d/x.gguf`,
		`MODEL=/mnt/d/x.gguf`:                                            `/mnt/d/x.gguf`,
	}
	for line, want := range cases {
		m := parseEnv(line)
		var got string
		for _, v := range m {
			got = v
		}
		if got != want {
			t.Errorf("parseEnv(%q) = %q, veut %q", line, got, want)
		}
	}
}

// Écrire puis relire doit rendre exactement la même configuration.
func TestFormatEnvRoundTrip(t *testing.T) {
	in := map[string]string{
		"MODEL":      "/mnt/mes modèles/x.gguf",
		"EXTRA_ARGS": `--jinja --chat-template-file "/etc/ajean/tpl.jinja"`,
		"CTX":        "32768",
	}
	out := parseEnv(formatEnv(in))
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("aller-retour cassé :\n%v\n%v", in, out)
	}
}

func TestSplitArgs(t *testing.T) {
	got := splitArgs(`--jinja  --chat-template-file "/etc/mes modèles/tpl.jinja" --flash-attn`)
	want := []string{"--jinja", "--chat-template-file", "/etc/mes modèles/tpl.jinja", "--flash-attn"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitArgs = %q, veut %q", got, want)
	}
	if len(splitArgs("")) != 0 {
		t.Fatal("EXTRA_ARGS vide doit donner zéro argument")
	}
}

// L'éditeur doit proposer les clés utiles même quand la configuration est vide,
// et ne jamais perdre une clé déjà définie.
func TestConfigEditorText(t *testing.T) {
	txt := configEditorText(map[string]string{"MODEL": "x.gguf", "INCONNUE": "1"})
	round := parseEnv(txt)
	if round["MODEL"] != "x.gguf" || round["INCONNUE"] != "1" {
		t.Fatalf("clés perdues : %v", round)
	}
	if len(round) != 2 {
		t.Fatalf("les clés non renseignées doivent rester commentées : %v", round)
	}
	if !contains(txt, "#BIN=") {
		t.Fatal("le squelette doit documenter BIN")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
