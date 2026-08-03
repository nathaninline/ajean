package jean

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Le contenu doit arriver sur le disque à l'octet près : c'est tout l'intérêt de
// l'outil write (les guillemets imbriqués d'un script Python ne survivaient pas
// au passage par le shell).
func TestFileWriteVerbatim(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "kpi.py")
	content := "import sqlite3\nq = \"SELECT * FROM t WHERE n = 'a'\"\nprint(q)\n"

	res := fileWrite(path, content)
	if !strings.HasPrefix(res, "[ok]") {
		t.Fatalf("write a échoué: %s", res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("relecture: %v", err)
	}
	if string(got) != content {
		t.Fatalf("contenu altéré:\n%q\nattendu:\n%q", got, content)
	}
}

func TestFileWriteOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	if res := fileWrite(path, "ancien"); !strings.HasPrefix(res, "[ok]") {
		t.Fatalf("premier write: %s", res)
	}
	res := fileWrite(path, "nouveau")
	if !strings.Contains(res, "réécrit") {
		t.Fatalf("attendu « réécrit », obtenu: %s", res)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "nouveau" {
		t.Fatalf("contenu = %q", got)
	}
}

func TestFileWriteRejectsEmptyPath(t *testing.T) {
	if res := fileWrite("  ", "x"); !strings.HasPrefix(res, "[erreur]") {
		t.Fatalf("attendu une erreur, obtenu: %s", res)
	}
}
