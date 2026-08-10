package ajean

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Le nom vient du client : aucun d'eux ne doit permettre d'écrire hors du
// dossier de dépôt.
func TestSafeUploadNameNeverEscapes(t *testing.T) {
	for _, in := range []string{
		"../../evil.sh", `..\..\evil.sh`, "/etc/passwd", `C:\Windows\system32\x.dll`,
		"..", ".", "", "   ", "a\x00b", "nul\nname",
	} {
		got := safeUploadName(in)
		if got == "" || got == "." || got == ".." {
			t.Fatalf("%q → %q : nom inutilisable", in, got)
		}
		if strings.ContainsAny(got, `/\`) {
			t.Fatalf("%q → %q : contient un séparateur de chemin", in, got)
		}
		if filepath.Base(got) != got {
			t.Fatalf("%q → %q : n'est pas un nom simple", in, got)
		}
	}
}

func TestSafeUploadNameKeepsOrdinaryNames(t *testing.T) {
	for in, want := range map[string]string{
		"rapport.pdf":              "rapport.pdf",
		"Compte rendu (final).odt": "Compte rendu (final).odt",
		"données-été.csv":          "données-été.csv",
		`C:\Users\x\notes.md`:      "notes.md",
	} {
		if got := safeUploadName(in); got != want {
			t.Fatalf("safeUploadName(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestChatUploadWritesFileAndAttachNote(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"name": "../notes.txt",
		"data": "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("bonjour")),
	})
	rec := httptest.NewRecorder()
	handleChatUpload(rec, httptest.NewRequest("POST", "/api/chat/upload", strings.NewReader(string(body))))
	if rec.Code != 200 {
		t.Fatalf("code %d : %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
		Abs  string `json:"abs"`
		Size int    `json:"size"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(resp.Abs)
	if !resp.OK || resp.Size != 7 {
		t.Fatalf("réponse inattendue: %+v", resp)
	}
	// Le fichier atterrit DANS uploads/, malgré le "../" du nom.
	dir, err := uploadsDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(resp.Abs) != dir {
		t.Fatalf("écrit hors du dossier de dépôt : %s", resp.Abs)
	}
	if b, err := os.ReadFile(resp.Abs); err != nil || string(b) != "bonjour" {
		t.Fatalf("contenu = %q, err=%v", b, err)
	}
	// Le chemin renvoyé est celui que le client rejoue dans /api/chat/send.
	got := attachFiles([]string{resp.Path})
	if len(got) != 1 || got[0].Path != resp.Path || got[0].Size != 7 {
		t.Fatalf("attachFiles = %+v", got)
	}
	if note := attachNote(got); !strings.Contains(note, resp.Path) {
		t.Fatalf("note sans le fichier : %q", note)
	}
	// Un chemin inventé par le client n'atteint jamais le modèle.
	if n := attachFiles([]string{"uploads/jamais-deposé.bin", "../../etc/passwd"}); len(n) != 0 {
		t.Fatalf("fichiers absents retenus : %+v", n)
	}
	if attachNote(nil) != "" {
		t.Fatal("note non vide sans pièce jointe")
	}
}

// Le téléchargement est le point sensible : il lit des fichiers du serveur à
// partir d'un chemin fourni par le client.
func TestChatFileStaysInsideWorkspace(t *testing.T) {
	dir, err := uploadsDir()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "produit.txt")
	if err := os.WriteFile(p, []byte("resultat"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)

	rec := httptest.NewRecorder()
	handleChatFile(rec, httptest.NewRequest("GET", "/api/chat/file?path=uploads/produit.txt", nil))
	if rec.Code != 200 || rec.Body.String() != "resultat" {
		t.Fatalf("code %d, corps %q", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("pas en pièce jointe : %q", cd)
	}
	// Un .html produit par le modèle ne doit jamais s'exécuter dans l'origine de
	// l'UI : il y lirait la clé de pilotage en localStorage.
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	// Le chemin vient du texte du modèle : il peut pointer n'importe où.
	for _, bad := range []string{
		"../ajean.db", "../../../etc/passwd", `..\..\secret`,
		"/etc/passwd", "C:/Windows/win.ini", "", "uploads/absent.txt",
	} {
		rec := httptest.NewRecorder()
		handleChatFile(rec, httptest.NewRequest("GET", "/api/chat/file?path="+url.QueryEscape(bad), nil))
		if rec.Code == 200 {
			t.Fatalf("chemin %q servi (code 200)", bad)
		}
	}
}

func TestChatUploadRejectsEmptyAndBadBase64(t *testing.T) {
	for _, data := range []string{"", "pas du base64 !!"} {
		body, _ := json.Marshal(map[string]any{"name": "x.bin", "data": data})
		rec := httptest.NewRecorder()
		handleChatUpload(rec, httptest.NewRequest("POST", "/api/chat/upload", strings.NewReader(string(body))))
		if rec.Code != 400 {
			t.Fatalf("data=%q : code %d, attendu 400", data, rec.Code)
		}
	}
}

// L'export doit garder la trace des pièces jointes : un tour envoyé SANS texte
// donnait sinon une section « Vous » vide, sans dire qu'un fichier était passé.
func TestExportFileNamesBothForms(t *testing.T) {
	live := []attachInfo{{Name: "photo.jpg", Path: "uploads/photo.jpg", Size: 10}}
	if got := exportFileNames(live); len(got) != 1 || got[0] != "photo.jpg" {
		t.Fatalf("forme vivante : %v", got)
	}
	// Forme relue du journal persisté (JSON → []any de maps).
	replayed := []any{map[string]any{"name": "photo.jpg", "path": "uploads/photo.jpg"}}
	if got := exportFileNames(replayed); len(got) != 1 || got[0] != "photo.jpg" {
		t.Fatalf("forme rejouée : %v", got)
	}
	if got := exportFileNames(nil); len(got) != 0 {
		t.Fatalf("sans pièce jointe : %v", got)
	}
}
