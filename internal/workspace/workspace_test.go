package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ajean/internal/home"
	"ajean/internal/tool"
)

// setup ouvre un workspace vide dans un dossier temporaire et rend ce dossier
// (le parent du workspace, donc l'« extérieur »).
func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AJEAN_HOME", dir)
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := Close(); err != nil {
			t.Error(err)
		}
	})
	return dir
}

func run(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	for _, d := range Tools() {
		if d.Name == name {
			return d.Run(tool.Call{Args: args})
		}
	}
	t.Fatalf("outil %s absent", name)
	return ""
}

func TestReadWrite(t *testing.T) {
	setup(t)
	if err := Write("notes/2026/todo.md", "acheter du pain"); err != nil {
		t.Fatal(err)
	}
	got, err := Read("notes/2026/todo.md")
	if err != nil || got != "acheter du pain" {
		t.Fatalf("Read = %q, %v", got, err)
	}
	if err := Write(`notes\2026\todo.md`, "remplacé"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Read("notes/2026/todo.md"); got != "remplacé" {
		t.Errorf("après remplacement : %q", got)
	}
	if Exists("notes/2026/todo.md.tmp") {
		t.Error("le fichier temporaire d'écriture est resté")
	}
	if _, err := Read("absent.txt"); err == nil || !strings.Contains(err.Error(), "introuvable") {
		t.Errorf("fichier absent : %v", err)
	}
	if _, err := Read("notes"); err == nil {
		t.Error("lire un dossier aurait dû échouer")
	}
	if err := Write("notes", "x"); err == nil {
		t.Error("écrire sur un dossier aurait dû échouer")
	}
	if err := Write("", "x"); err == nil {
		t.Error("écrire sans nom de fichier aurait dû échouer")
	}
}

func TestReadRefusesBinaryAndHuge(t *testing.T) {
	setup(t)
	if err := WriteBytes("image.png", []byte{0x89, 'P', 'N', 'G', 0xff, 0xfe}); err != nil {
		t.Fatal(err)
	}
	if _, err := Read("image.png"); err == nil || !strings.Contains(err.Error(), "binaire") {
		t.Errorf("fichier binaire : %v", err)
	}
	if err := WriteBytes("gros.txt", make([]byte, maxReadBytes+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := Read("gros.txt"); err == nil || !strings.Contains(err.Error(), "trop gros") {
		t.Errorf("fichier trop gros : %v", err)
	}
}

func TestListMoveDelete(t *testing.T) {
	setup(t)
	for _, p := range []string{"b.txt", "a.txt", "dossier/c.txt"} {
		if err := Write(p, "12345"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := List("")
	if err != nil {
		t.Fatal(err)
	}
	if want := "a.txt (5 octets)|b.txt (5 octets)|dossier/"; strings.Join(got, "|") != want {
		t.Errorf("List = %q, attendu %q", got, want)
	}

	if err := Move("a.txt", "archives/2026/a.txt"); err != nil {
		t.Fatal(err)
	}
	if Exists("a.txt") || !Exists("archives/2026/a.txt") {
		t.Error("le fichier n'a pas été déplacé")
	}
	if err := Move("b.txt", "archives/2026/a.txt"); err == nil {
		t.Error("déplacer sur un fichier existant aurait dû échouer")
	}
	if err := Move("absent.txt", "x.txt"); err == nil {
		t.Error("déplacer un fichier absent aurait dû échouer")
	}

	if err := Delete("archives"); err != nil {
		t.Fatal(err)
	}
	if Exists("archives") {
		t.Error("le dossier n'a pas été supprimé")
	}
	if err := Delete("absent.txt"); err == nil {
		t.Error("supprimer un fichier absent aurait dû échouer")
	}
}

func TestDeleteRootRefused(t *testing.T) {
	setup(t)
	if err := Write("garde.txt", "x"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", ".", "./", "a/.."} {
		if err := Delete(p); err == nil {
			t.Errorf("Delete(%q) aurait dû être refusé", p)
		}
	}
	if err := Move(".", "ailleurs"); err == nil {
		t.Error("déplacer la racine aurait dû être refusé")
	}
	if !Exists("garde.txt") {
		t.Error("le contenu du workspace a disparu")
	}
}

func TestEscapeRefused(t *testing.T) {
	dir := setup(t)
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		"../secret.txt",
		`..\secret.txt`,
		"a/../../secret.txt",
		"..",
		`C:\Windows\win.ini`,
		"C:/Windows/win.ini",
		"/etc/passwd",
		`\\serveur\partage\x`,
		secret,
		"fichier.txt:flux",
		"nul\x00.txt",
	}
	for _, p := range paths {
		if _, err := Read(p); err == nil {
			t.Errorf("Read(%q) aurait dû être refusé", p)
		}
		if err := Write(p, "x"); err == nil {
			t.Errorf("Write(%q) aurait dû être refusé", p)
		}
		if _, err := List(p); err == nil {
			t.Errorf("List(%q) aurait dû être refusé", p)
		}
		if err := Delete(p); err == nil {
			t.Errorf("Delete(%q) aurait dû être refusé", p)
		}
		if err := Move(p, "pris.txt"); err == nil {
			t.Errorf("Move(%q, …) aurait dû être refusé", p)
		}
		if err := Move("pris.txt", p); err == nil {
			t.Errorf("Move(…, %q) aurait dû être refusé", p)
		}
		if Exists(p) {
			t.Errorf("Exists(%q) aurait dû être faux", p)
		}
		if _, err := Abs(p); err == nil {
			t.Errorf("Abs(%q) aurait dû être refusé", p)
		}
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "secret" {
		t.Errorf("le fichier extérieur a été touché : %q, %v", b, err)
	}
}

// linkDir crée dans le workspace un lien vers un dossier extérieur. Sous Windows,
// créer un lien symbolique demande un privilège ; une jonction, non, et c'est
// la même porte de sortie.
func linkDir(t *testing.T, target, name string) {
	t.Helper()
	link := filepath.Join(home.Workspace(), name)
	err := os.Symlink(target, link)
	if err != nil && runtime.GOOS == "windows" {
		err = exec.Command("cmd", "/c", "mklink", "/J", link, target).Run()
	}
	if err != nil {
		t.Skipf("création de liens refusée : %v", err)
	}
}

func TestFileSymlinkOutsideRefused(t *testing.T) {
	dir := setup(t)
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(home.Workspace(), "lien.txt")); err != nil {
		t.Skipf("création de liens symboliques refusée : %v", err)
	}
	if got, err := Read("lien.txt"); err == nil {
		t.Errorf("Read a suivi le lien : %q", got)
	}
	if abs, err := Abs("lien.txt"); err == nil {
		t.Errorf("Abs = %q, refus attendu", abs)
	}
	// Écrire remplace au pire le lien par un vrai fichier, sans toucher sa cible.
	_ = Write("lien.txt", "écrasé")
	if b, err := os.ReadFile(secret); err != nil || string(b) != "secret" {
		t.Errorf("le fichier extérieur a été touché : %q, %v", b, err)
	}
}

func TestLinkOutsideRefused(t *testing.T) {
	dir := setup(t)
	outside := filepath.Join(dir, "dehors")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkDir(t, outside, "lien")

	if got, err := Read("lien/secret.txt"); err == nil {
		t.Errorf("Read a suivi le lien : %q", got)
	}
	if err := Write("lien/secret.txt", "écrasé"); err == nil {
		t.Error("Write a suivi le lien")
	}
	if err := Write("lien/nouveau.txt", "x"); err == nil {
		t.Error("Write a créé un fichier à travers le lien")
	}
	if got, err := List("lien"); err == nil {
		t.Errorf("List a suivi le lien : %q", got)
	}
	if err := Move("lien/secret.txt", "pris.txt"); err == nil {
		t.Error("Move a sorti un fichier à travers le lien")
	}
	if err := Delete("lien/secret.txt"); err == nil {
		t.Error("Delete a suivi le lien")
	}
	for _, p := range []string{"lien/nouveau.txt", "lien/sous/dossier/x.bin", "lien"} {
		if abs, err := Abs(p); err == nil {
			t.Errorf("Abs(%q) = %q, refus attendu", p, abs)
		}
	}

	// Supprimer le lien ôte le lien, pas ce qu'il désigne.
	if err := Delete("lien"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "secret" {
		t.Errorf("le fichier extérieur a été touché : %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "nouveau.txt")); err == nil {
		t.Error("un fichier a été créé hors du workspace")
	}
}

func TestAbs(t *testing.T) {
	setup(t)
	if err := Write("docs/a.txt", "x"); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"docs/a.txt":               filepath.Join(home.Workspace(), "docs", "a.txt"),
		"telechargements/new.pdf":  filepath.Join(home.Workspace(), "telechargements", "new.pdf"),
		`docs\sous\dossier\b.zip`:  filepath.Join(home.Workspace(), "docs", "sous", "dossier", "b.zip"),
		"":                         home.Workspace(),
		"docs/../docs/./a.txt":     filepath.Join(home.Workspace(), "docs", "a.txt"),
		"  telechargements/x.bin ": filepath.Join(home.Workspace(), "telechargements", "x.bin"),
	}
	for p, want := range cases {
		got, err := Abs(p)
		if err != nil || got != want {
			t.Errorf("Abs(%q) = %q, %v ; attendu %q", p, got, err, want)
		}
	}
}

func TestNotInitialized(t *testing.T) {
	if _, err := Read("a.txt"); err == nil {
		t.Error("Read sans Init aurait dû échouer")
	}
	if Exists("a.txt") {
		t.Error("Exists sans Init aurait dû être faux")
	}
}

func TestTools(t *testing.T) {
	setup(t)
	if got := run(t, "ws_list", nil); got != "(dossier vide)" {
		t.Errorf("ws_list vide : %q", got)
	}
	if got := run(t, "ws_write", map[string]any{"path": "a/b.txt", "content": "  bonjour\n"}); !tool.IsOK(got) {
		t.Errorf("ws_write : %q", got)
	}
	if got := run(t, "ws_read", map[string]any{"path": "a/b.txt"}); got != "  bonjour\n" {
		t.Errorf("ws_read : %q", got)
	}
	if got := run(t, "ws_move", map[string]any{"from": "a/b.txt", "to": "c.txt"}); !tool.IsOK(got) {
		t.Errorf("ws_move : %q", got)
	}
	if got := run(t, "ws_list", map[string]any{"path": ""}); got != "a/\nc.txt (10 octets)" {
		t.Errorf("ws_list : %q", got)
	}
	if got := run(t, "ws_delete", map[string]any{"path": "c.txt"}); !tool.IsOK(got) {
		t.Errorf("ws_delete : %q", got)
	}
	if got := run(t, "ws_write", map[string]any{"path": "vide.txt", "content": ""}); !tool.IsOK(got) {
		t.Errorf("ws_write vide : %q", got)
	}
	if got := run(t, "ws_read", map[string]any{"path": "vide.txt"}); got != "(fichier vide)" {
		t.Errorf("ws_read vide : %q", got)
	}
	for name, args := range map[string]map[string]any{
		"ws_read":   {"path": "../secret.txt"},
		"ws_write":  {"path": `C:\Windows\win.ini`, "content": "x"},
		"ws_list":   {"path": "/etc"},
		"ws_move":   {"from": "vide.txt", "to": "../dehors.txt"},
		"ws_delete": {"path": "."},
	} {
		if got := run(t, name, args); !tool.IsErr(got) {
			t.Errorf("%s %v : %q, erreur attendue", name, args, got)
		}
	}
}

func TestReadToolTruncates(t *testing.T) {
	setup(t)
	if err := Write("long.txt", strings.Repeat("é", maxReadChars+500)); err != nil {
		t.Fatal(err)
	}
	got := run(t, "ws_read", map[string]any{"path": "long.txt"})
	body, note, found := strings.Cut(got, "\n[… tronqué")
	if !found {
		t.Fatal("la lecture n'a pas été tronquée")
	}
	if n := len([]rune(body)); n != maxReadChars {
		t.Errorf("%d caractères gardés, attendu %d", n, maxReadChars)
	}
	if !strings.Contains(note, "60000 caractères sur 60500") {
		t.Errorf("mention de troncature : %q", note)
	}
}
