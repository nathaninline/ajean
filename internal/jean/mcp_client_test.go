package jean

import (
	"os/exec"
	"strings"
	"testing"
)

// TestMCPEndToEnd connecte le serveur MCP de référence (@modelcontextprotocol/
// server-everything via npx), vérifie la découverte d'outils namespacés puis un
// appel réel (echo). Skippé si npx est absent (CI sans Node).
func TestMCPEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx indisponible — test MCP e2e ignoré")
	}
	t.Setenv("JEAN_HOME", t.TempDir())

	cfg := MCPServerConfig{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-everything"},
		Enabled: true,
	}
	if err := SetMCPServer("everything", cfg); err != nil {
		t.Fatalf("SetMCPServer: %v", err)
	}
	t.Cleanup(mcpCloseAll)

	tools := mcpTools()
	if len(tools) == 0 {
		t.Fatal("aucun outil MCP découvert (le serveur s'est-il connecté ?)")
	}
	echoName := mcpExposedName("everything", "echo")
	found := false
	for _, tl := range tools {
		if tl.Function.Name == echoName {
			found = true
		}
		if !strings.HasPrefix(tl.Function.Name, "mcp__everything__") {
			t.Errorf("outil mal namespacé: %s", tl.Function.Name)
		}
	}
	if !found {
		t.Fatalf("outil %s introuvable dans %d outils", echoName, len(tools))
	}

	out := mcpCall(echoName, map[string]any{"message": "bonjour-jean"})
	if !strings.Contains(out, "bonjour-jean") {
		t.Fatalf("echo n'a pas renvoyé le message ; got: %q", out)
	}
}
