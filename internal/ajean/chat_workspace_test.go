package ajean

import (
	"path/filepath"
	"strings"
	"testing"
)

// Un chemin relatif du modèle doit atterrir dans le workspace, jamais dans le
// répertoire courant du processus (Bureau, C:\ProgramData\jean\bin…).
func TestResolveAgentPathRelative(t *testing.T) {
	got := resolveAgentPath("meteo.json")
	want := filepath.Join(agentWorkspace(), "meteo.json")
	if got != want {
		t.Fatalf("resolveAgentPath = %q, attendu %q", got, want)
	}
}

// Un chemin absolu demandé explicitement reste intouché.
func TestResolveAgentPathAbsolute(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "rapport.md")
	if got := resolveAgentPath(abs); got != abs {
		t.Fatalf("resolveAgentPath = %q, attendu %q", got, abs)
	}
}

func TestResolveAgentPathSubdir(t *testing.T) {
	got := resolveAgentPath("notes/2026/a.txt")
	if !strings.HasPrefix(got, agentWorkspace()) {
		t.Fatalf("%q hors du workspace %q", got, agentWorkspace())
	}
	if strings.Contains(got, "/") && filepath.Separator != '/' {
		t.Fatalf("séparateurs non normalisés : %q", got)
	}
}
