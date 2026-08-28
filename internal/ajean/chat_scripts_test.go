package ajean

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuardToolOnly vérifie que memory est inaccessible en direct (shell/write/
// edit) et n'autorise que les outils dédiés, tandis que scripts reste libre.
func TestGuardToolOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AJEAN_HOME", home)

	// Shell : toute mention du dossier memory est refusée (lecture comprise). Le
	// dossier scripts, lui, est librement utilisable (l'IA y écrit et exécute).
	blockedCmds := []string{
		"cat " + memoryDir() + "/note.md",
		"echo x > " + memoryDir() + "/x",
	}
	for _, c := range blockedCmds {
		if msg := guardToolOnlyCommand(c); msg == "" {
			t.Errorf("commande aurait dû être refusée : %q", c)
		}
	}
	allowedCmds := []string{
		"ls " + workspaceDir(),
		"bash " + scriptsDir() + "/backup.sh", // exécuter un script : autorisé
		"ls " + scriptsDir(),
	}
	for _, c := range allowedCmds {
		if msg := guardToolOnlyCommand(c); msg != "" {
			t.Errorf("commande ne devait pas être bloquée : %q -> %s", c, msg)
		}
	}

	// write/edit : un chemin dans memory est refusé ; dans scripts ou workspace, non.
	if msg := guardToolOnlyPath(filepath.Join(memoryDir(), "a.md")); msg == "" {
		t.Errorf("écriture directe dans memory aurait dû être refusée")
	}
	for _, p := range []string{filepath.Join(scriptsDir(), "b.sh"), filepath.Join(workspaceDir(), "ok.txt")} {
		if msg := guardToolOnlyPath(p); msg != "" {
			t.Errorf("écriture ne devait pas être bloquée : %q -> %s", p, msg)
		}
	}
}

// TestScriptsPathEscape vérifie qu'on ne peut pas sortir du dossier scripts ni
// glisser un caractère hostile au shell dans le nom.
func TestScriptsPathEscape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AJEAN_HOME", home)

	bad := []string{"../evil.sh", "..", "/etc/passwd", `..\..\x`, `a";rm -rf ~;".sh`, "a$b.sh", "a`b`.sh"}
	for _, name := range bad {
		if _, err := scriptsPath(name); err == nil {
			t.Errorf("aurait dû refuser le nom : %q", name)
		}
	}
	good := []string{"backup.ps1", "sub/backup.sh", "a.sh", "my backup.sh"}
	for _, name := range good {
		p, err := scriptsPath(name)
		if err != nil {
			t.Errorf("aurait dû accepter %q : %v", name, err)
			continue
		}
		if !strings.HasPrefix(normPath(p), normPath(scriptsDir())) {
			t.Errorf("%q résolu hors de scriptsDir : %s", name, p)
		}
	}
}

// TestScriptExists couvre la validation d'existence utilisée par les tâches script.
func TestScriptExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AJEAN_HOME", home)

	if err := scriptExists("hello.sh"); err == nil {
		t.Fatalf("un script absent aurait dû échouer")
	}
	full := filepath.Join(scriptsDir(), "hello.sh")
	if err := os.MkdirAll(scriptsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := scriptExists("hello.sh"); err != nil {
		t.Fatalf("scriptExists : %v", err)
	}
	list, _ := listScripts()
	if len(list) != 1 || list[0].Name != "hello.sh" {
		t.Fatalf("list inattendu : %+v", list)
	}
	// Un dossier n'est pas un script.
	if err := os.MkdirAll(filepath.Join(scriptsDir(), "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := scriptExists("d"); err == nil {
		t.Fatalf("un dossier ne doit pas passer pour un script")
	}
}
