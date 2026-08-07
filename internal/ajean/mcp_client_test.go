package ajean

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestMCPEnsureDeduplicatesConcurrentConnects vérifie que N appelants simultanés
// (pré-chauffage + tour de chat + panneau MCP de l'UI) partagent une seule
// tentative de connexion : sans ça, connecter en parallèle multiplierait les
// process stdio lancés pour un même serveur.
func TestMCPEnsureDeduplicatesConcurrentConnects(t *testing.T) {
	testHome(t)
	t.Cleanup(mcpCloseAll)

	// Commande inexistante : la connexion échoue, mais l'entrée du pool est bien
	// partagée — c'est ce qu'on teste.
	cfg := MCPServerConfig{Command: "ajean-binaire-inexistant-pour-test", Enabled: true}

	const callers = 8
	var wg sync.WaitGroup
	got := make([]*mcpSession, callers)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = mcpMgr.ensure("bidon", cfg)
		}(i)
	}
	wg.Wait()

	for i, s := range got {
		if s != got[0] {
			t.Fatalf("appelant %d a obtenu une session différente : %p vs %p", i, s, got[0])
		}
		select {
		case <-s.ready:
		default:
			t.Fatalf("appelant %d a reçu une session pas encore prête", i)
		}
	}
	if got[0].err == nil {
		t.Fatal("une commande inexistante devrait produire une erreur de connexion")
	}
}

// TestMCPEndToEnd connecte le serveur MCP de référence (@modelcontextprotocol/
// server-everything via npx), vérifie la découverte d'outils namespacés puis un
// appel réel (echo). Skippé si npx est absent (CI sans Node).
func TestMCPEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx indisponible — test MCP e2e ignoré")
	}
	testHome(t)

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

	out := mcpCall(echoName, map[string]any{"message": "bonjour-ajean"})
	if !strings.Contains(out, "bonjour-ajean") {
		t.Fatalf("echo n'a pas renvoyé le message ; got: %q", out)
	}
}
