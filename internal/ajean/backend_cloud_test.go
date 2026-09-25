package ajean

import (
	"strings"
	"testing"
)

func TestCloudServerArgs(t *testing.T) {
	cfg := map[string]string{
		"CLOUD":       "modal",
		"CLOUD_MODEL": "https://huggingface.co/x/y/blob/main/Model-Q4_K_M.gguf?download=true",
		"CTX":         "65536",
		"KV_TYPE":     "q8_0",
		"REASONING":   "off",
		"EXTRA_ARGS":  `--device CUDA0 --flash-attn on --mlock --no-mmap -ts 0.9,0.1 --spec-type draft-mtp --chat-template-file "/etc/x y.jinja" --jinja`,
	}
	args, err := cloudServerArgs(cfg, "k")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	for _, want := range []string{"-m /models/Model-Q4_K_M.gguf", "-c 65536", "-ctk q8_0", "-ctv q8_0",
		"--reasoning off", "--api-key k", "--flash-attn on", "--spec-type draft-mtp", "--jinja", "--load-mode mlock"} {
		if !strings.Contains(got, want) {
			t.Errorf("manque %q dans %q", want, got)
		}
	}
	// Réglages propres à la machine locale : jamais envoyés au conteneur.
	for _, bad := range []string{"--device", "CUDA0", "-ts", "--chat-template-file", "--mlock", "--no-mmap"} {
		for _, a := range args {
			if a == bad {
				t.Errorf("%q ne doit pas partir vers Modal : %q", bad, got)
			}
		}
	}
}

func TestCloudServerArgsRejectsNonGGUF(t *testing.T) {
	if _, err := cloudServerArgs(map[string]string{"CLOUD_MODEL": "https://huggingface.co/x/y"}, ""); err == nil {
		t.Fatal("un lien sans .gguf doit être refusé")
	}
}

func TestCloudModelURL(t *testing.T) {
	if got := cloudModelURL("https://huggingface.co/a/b/blob/main/m.gguf"); got != "https://huggingface.co/a/b/resolve/main/m.gguf" {
		t.Fatal(got)
	}
}

func TestUsesRemoteEndpoint(t *testing.T) {
	if !usesRemoteEndpoint(map[string]string{"CLOUD": "modal"}) || !usesRemoteEndpoint(map[string]string{"EXTERNAL": "1"}) {
		t.Fatal("cloud et externe n'ont pas de moteur local")
	}
	if usesRemoteEndpoint(map[string]string{"MODEL": "x.gguf"}) {
		t.Fatal("un preset local a un moteur local")
	}
}

// Les erreurs encadrées de la CLI Modal deviennent une phrase courte et utile.
func TestModalErrorText(t *testing.T) {
	spend := "┌─ Error ──────┐\n│ Workspace ac-6uRQ has exceeded its spend limit │\n└──────────────┘"
	if got := modalErrorText(spend); !strings.Contains(got, "limite de dépense") {
		t.Errorf("limite : %q", got)
	}
	prof := "│ Modal profile 'test' was not found in /home/x/.modal.toml. │"
	if got := modalErrorText(prof); !strings.Contains(got, "introuvable") {
		t.Errorf("profil : %q", got)
	}
	if got := modalErrorText("│ something odd happened │"); got != "something odd happened" {
		t.Errorf("brut : %q", got)
	}
}
