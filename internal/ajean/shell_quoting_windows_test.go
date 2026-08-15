//go:build windows

package ajean

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Le passage par un .bat temporaire doit rendre les accents correctement (chcp
// 65001) là où `cmd /C "echo café"` les massacrait selon la page de code.
func TestNewShellCmdWindowsAccents(t *testing.T) {
	cmd, cleanup := newShellCmd(context.Background(), "echo café résumé déjà")
	defer cleanup()
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("échec d'exécution : %v (sortie : %q)", err, out.String())
	}
	got := out.String()
	for _, want := range []string{"café", "résumé", "déjà"} {
		if !strings.Contains(got, want) {
			t.Errorf("sortie ne contient pas %q : %q", want, got)
		}
	}
}

// Le code de sortie de la commande doit remonter (exit /b %errorlevel%) : sans ça
// l'agent croirait qu'une commande échouée a réussi.
func TestNewShellCmdWindowsExitCode(t *testing.T) {
	cmd, cleanup := newShellCmd(context.Background(), "exit /b 7")
	defer cleanup()
	err := cmd.Run()
	if err == nil {
		t.Fatal("un code de sortie 7 doit produire une erreur")
	}
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("code de sortie = %d, attendu 7", code)
	}
}

// Une commande multi-ligne doit s'exécuter ligne par ligne (là où cmd /C ne sait
// pas faire) : les deux echo doivent sortir.
func TestNewShellCmdWindowsMultiline(t *testing.T) {
	cmd, cleanup := newShellCmd(context.Background(), "echo PREMIERE\necho SECONDE")
	defer cleanup()
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("échec : %v (sortie %q)", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "PREMIERE") || !strings.Contains(got, "SECONDE") {
		t.Errorf("les deux lignes doivent sortir : %q", got)
	}
}

// Le fichier .bat temporaire ne doit pas s'accumuler : le cleanup le supprime.
func TestNewShellCmdWindowsCleanup(t *testing.T) {
	before, _ := filepath.Glob(filepath.Join(os.TempDir(), "ajean-cmd-*.bat"))
	cmd, cleanup := newShellCmd(context.Background(), "echo test")
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	_ = cmd.Run()
	cleanup()
	after, _ := filepath.Glob(filepath.Join(os.TempDir(), "ajean-cmd-*.bat"))
	if len(after) > len(before) {
		t.Errorf("le .bat temporaire n'a pas été nettoyé : %d -> %d", len(before), len(after))
	}
}
