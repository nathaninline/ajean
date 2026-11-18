package server

import (
	"strings"
	"testing"

	"ajean/internal/config"
	"ajean/internal/sandbox"
)

func TestSandboxPageAddresses(t *testing.T) {
	got := string(sandboxPage([]byte(`fetch('/api/state');url(/inter-var.woff2)`)))
	if !strings.Contains(got, `'/sandbox/api/state'`) || !strings.Contains(got, `url(/sandbox/inter-var.woff2)`) {
		t.Fatal(got)
	}
}

func TestSandboxEnvDropsRealSettings(t *testing.T) {
	t.Setenv("AJEAN_WEB_KEY", "secret")
	t.Setenv("AJEAN_BACKUP_KEY", "secret")
	t.Setenv("AJEAN_TOOLS_LAN", "1")
	env := strings.Join(sandboxEnv("d", "1", "http://x/v1", "", "m"), "\n")
	for _, k := range []string{"AJEAN_WEB_KEY", "AJEAN_BACKUP_KEY", "AJEAN_TOOLS_LAN"} {
		if strings.Contains(env, k) {
			t.Errorf("%s transmis à l'instance de test", k)
		}
	}
}

func TestSandboxSeedKeepsOnlyTheModel(t *testing.T) {
	conf := config.Config{Language: "fr", Services: []string{"secret.service"}}
	raw, err := sandboxSeed(conf, config.Preset{Name: "M", Model: "m", Effort: "high"}, config.Provider{Name: "P", URL: "http://a/v1", Key: "cle-secrete"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "cle-secrete") || strings.Contains(s, "secret.service") {
		t.Fatal(s)
	}
	if !strings.Contains(s, `"model":"m"`) || !strings.Contains(s, `"effort":"high"`) {
		t.Fatal(s)
	}
}

func TestSandboxRoutesAndTools(t *testing.T) {
	for _, p := range []string{"POST /api/services/act", "POST /api/config", "POST /api/auth/password", "POST /api/tasks/run", "POST /api/apps/bridge", "GET /api/machine"} {
		if sandbox.RouteAllowed(p) {
			t.Errorf("%s ne doit pas exister en mode test", p)
		}
	}
	if !sandbox.RouteAllowed("POST /api/send") {
		t.Error("la conversation doit rester possible")
	}
	for _, n := range []string{"tools", "browser", "app", "task_add"} {
		if !sandbox.Refused(n) {
			t.Errorf("%s doit être refusé", n)
		}
	}
	if sandbox.Refused("ws_write") || sandbox.Refused("memory_remember") {
		t.Error("le WORKSPACE et la mémoire de test restent permis")
	}
}
