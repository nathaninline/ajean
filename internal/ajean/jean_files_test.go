package ajean

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// jeanFilesTestRoot : un dossier de Jean à part pour le test, et un index vide.
func jeanFilesTestRoot(t *testing.T) string {
	t.Helper()
	testHome(t)
	root := t.TempDir()
	old := jeanFilesRoot
	jeanFilesRoot = func() string { return root }
	jeanFilesReset()
	t.Cleanup(func() { jeanFilesRoot = old; jeanFilesReset() })
	return root
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestJeanFileChunks(t *testing.T) {
	para := strings.Repeat("Une phrase sur le jardin. ", 30) // ~780 caractères
	text := para + "\n\n" + para + "\n\n" + para
	got := jeanFileChunks(text)
	if len(got) < 3 {
		t.Fatalf("%d passages, 3 attendus au moins", len(got))
	}
	for _, p := range got {
		if n := len([]rune(p)); n == 0 || n > jeanFileChunkChars {
			t.Fatalf("passage de %d caractères", n)
		}
	}
	if !strings.HasSuffix(got[0], "jardin.") {
		t.Fatalf("coupé au milieu d'une phrase : …%q", got[0][len(got[0])-20:])
	}
	if n := len(jeanFileChunks(strings.Repeat("x", 100*jeanFileChunkChars))); n != jeanFileMaxChunks {
		t.Fatalf("%d passages pour un très gros texte, %d attendus", n, jeanFileMaxChunks)
	}
	if len(jeanFileChunks("  \n ")) != 0 {
		t.Fatal("texte vide : aucun passage attendu")
	}
}

func TestJeanDecodeText(t *testing.T) {
	if got := jeanDecodeText([]byte{0xFF, 0xFE, 'h', 0, 0xE9, 0}); got != "hé" {
		t.Fatalf("UTF-16 : %q", got)
	}
	if got := jeanDecodeText([]byte("v\xe9t\xe9rinaire \x80")); got != "vétérinaire €" {
		t.Fatalf("cp1252 : %q", got)
	}
	if got := jeanDecodeText([]byte{0xEF, 0xBB, 0xBF, 'o', 'k'}); got != "ok" {
		t.Fatalf("BOM UTF-8 : %q", got)
	}
	if got := jeanDecodeText([]byte{0x01, 0x00, 0xFF}); got != "" {
		t.Fatalf("binaire : %q", got)
	}
}

func TestJeanInSpace(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"../x", "a/../../x", `..\x`} {
		if _, err := jeanInSpace(root, rel); err == nil {
			t.Errorf("%q accepté hors du dossier", rel)
		}
	}
	if p, err := jeanInSpace(root, "/photos/nouveau.jpg"); err != nil || p != filepath.Join(root, "photos", "nouveau.jpg") {
		t.Fatalf("chemin dans le dossier refusé : %v %v", p, err)
	}
	out := t.TempDir()
	if err := os.Symlink(out, filepath.Join(root, "lien")); err != nil {
		t.Skip("liens symboliques indisponibles ici")
	}
	if _, err := jeanInSpace(root, "lien/secret.txt"); err == nil {
		t.Fatal("un lien vers l'extérieur laisse sortir du dossier")
	}
}

func TestJeanCleanName(t *testing.T) {
	for _, bad := range []string{"", ".", "..", ".cache", "a:b", "nul", "con.txt", "photo (2).jpg", "fin."} {
		if _, err := jeanCleanName(bad); err == nil {
			t.Errorf("%q accepté", bad)
		}
	}
	if n, err := jeanCleanName("dossier/Factures 2026"); err != nil || n != "Factures 2026" {
		t.Fatalf("nom : %q %v", n, err)
	}
}

// Bout en bout : les fichiers du dossier sont indexés (pas l'environnement
// Python ni les fichiers cachés), puis jean_search et le rappel retrouvent un
// fichier par ce qu'il contient, avec d'autres mots.
func TestJeanFilesIndexSearchRecall(t *testing.T) {
	root := jeanFilesTestRoot(t)
	jeanEmbedTestPort = fakeEmbedServer(t)
	t.Cleanup(func() { jeanEmbedTestPort = 0 })
	writeTestFile(t, filepath.Join(root, "notes", "entretien.txt"), "Vidange de la Clio faite au garage, le mécanicien conseille les plaquettes.")
	writeTestFile(t, filepath.Join(root, "cuisine.md"), "# Tarte aux pommes\nLa recette de grand-mère.")
	writeTestFile(t, filepath.Join(root, "ytenv", "pyvenv.cfg"), "home = /usr/bin")
	writeTestFile(t, filepath.Join(root, "ytenv", "lib", "voiture.py"), "# garage clio vidange")
	writeTestFile(t, filepath.Join(root, ".cache", "garage.txt"), "garage")
	writeTestFile(t, filepath.Join(root, "node_modules", "x", "garage.js"), "garage")

	n, err := jeanFilesIndexOnce(context.Background(), 50)
	if err != nil || n != 2 {
		t.Fatalf("indexés : %d (%v), 2 attendus", n, err)
	}
	if indexed, total := jeanFilesCount(); indexed != 2 || total != 2 {
		t.Fatalf("décompte %d / %d, 2 / 2 attendu", indexed, total)
	}
	// Rien n'a changé : rien à refaire.
	if n, _ := jeanFilesIndexOnce(context.Background(), 50); n != 0 {
		t.Fatalf("%d fichiers réindexés sans changement", n)
	}
	// L'index survit à un redémarrage (relu depuis la mémoire).
	jeanFilesReset()
	if indexed, _ := jeanFilesCount(); indexed != 2 {
		t.Fatalf("index relu : %d fichiers", indexed)
	}

	got := JeanSearch("quand ai-je fait réparer la voiture", 6)
	if !strings.Contains(got, "notes/entretien.txt") || strings.Contains(got, "voiture.py") {
		t.Fatalf("jean_search ne trouve pas le bon fichier :\n%s", got)
	}
	rec := jeanRecallFor("est-ce que la voiture a besoin d'une révision bientôt", time.Now())
	if !strings.Contains(rec, "From your files") || !strings.Contains(rec, "notes/entretien.txt") || strings.Contains(rec, "cuisine.md") {
		t.Fatalf("rappel :\n%s", rec)
	}

	// Un fichier supprimé sort de l'index ; un fichier modifié est relu.
	if err := os.Remove(filepath.Join(root, "cuisine.md")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "notes", "entretien.txt"), "Facture d'électricité à payer.")
	if n, err := jeanFilesIndexOnce(context.Background(), 50); err != nil || n != 1 {
		t.Fatalf("après changement : %d (%v)", n, err)
	}
	if indexed, total := jeanFilesCount(); indexed != 1 || total != 1 {
		t.Fatalf("décompte après suppression %d / %d", indexed, total)
	}
	if got := JeanSearch("la facture à régler", 6); !strings.Contains(got, "notes/entretien.txt") {
		t.Fatalf("contenu modifié non relu :\n%s", got)
	}
}

// Sans vision, une image est connue par son nom ; elle sera relue quand le
// modèle saura voir.
func TestJeanFilesImageByName(t *testing.T) {
	root := jeanFilesTestRoot(t)
	jeanEmbedTestPort = fakeEmbedServer(t)
	t.Cleanup(func() { jeanEmbedTestPort = 0 })
	writeTestFile(t, filepath.Join(root, "photos", "garage-clio.jpg"), "\xff\xd8\xff")
	if n, err := jeanFilesIndexOnce(context.Background(), 50); err != nil || n != 1 {
		t.Fatalf("%d (%v)", n, err)
	}
	jeanFiles.Lock()
	doc := jeanFiles.index["photos/garage-clio.jpg"]
	jeanFiles.Unlock()
	if doc == nil || doc.Kind != "image" || doc.Read {
		t.Fatalf("image : %+v", doc)
	}
}

func jeanFilesCall(t *testing.T, h http.HandlerFunc, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b)))
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func TestJeanFilesHandlers(t *testing.T) {
	root := jeanFilesTestRoot(t)
	writeTestFile(t, filepath.Join(root, "IMG_1.jpeg"), "x")
	writeTestFile(t, filepath.Join(root, ".cache"), "x")

	if code, out := jeanFilesCall(t, handleJeanFilesMkdir, map[string]any{"dir": "", "name": "Photos"}); code != 200 {
		t.Fatalf("mkdir : %d %v", code, out)
	}
	if code, _ := jeanFilesCall(t, handleJeanFilesMkdir, map[string]any{"dir": "", "name": "Photos"}); code != 400 {
		t.Fatal("dossier créé deux fois")
	}
	code, out := jeanFilesCall(t, handleJeanFiles, map[string]any{"dir": ""})
	ents, _ := out["entries"].([]any)
	if code != 200 || len(ents) != 2 || ents[0].(map[string]any)["name"] != "Photos" {
		t.Fatalf("liste (dossiers d'abord, sans fichiers cachés) : %d %v", code, out)
	}
	if code, _ := jeanFilesCall(t, handleJeanFiles, map[string]any{"dir": "../"}); code != 400 {
		t.Fatal("liste hors du dossier acceptée")
	}
	// Renommé sans extension : il garde la sienne.
	if code, out := jeanFilesCall(t, handleJeanFilesRename, map[string]any{"path": "IMG_1.jpeg", "name": "plage"}); code != 200 || out["name"] != "plage.jpeg" {
		t.Fatalf("rename : %d %v", code, out)
	}
	writeTestFile(t, filepath.Join(root, "Photos", "plage.jpeg"), "autre")
	if code, out := jeanFilesCall(t, handleJeanFilesMove, map[string]any{"paths": []string{"plage.jpeg"}, "to": "Photos"}); code != 200 {
		t.Fatalf("move : %d %v", code, out)
	}
	if !isFile(filepath.Join(root, "Photos", "plage-2.jpeg")) || !isFile(filepath.Join(root, "Photos", "plage.jpeg")) {
		t.Fatal("déplacé par-dessus un fichier du même nom")
	}
	if code, _ := jeanFilesCall(t, handleJeanFilesMove, map[string]any{"paths": []string{"Photos"}, "to": "Photos"}); code != 400 {
		t.Fatal("dossier déplacé dans lui-même")
	}
	if code, _ := jeanFilesCall(t, handleJeanFilesDelete, map[string]any{"paths": []string{""}}); code != 400 || !isDir(root) {
		t.Fatal("le dossier de Jean lui-même a été supprimé")
	}
	if code, out := jeanFilesCall(t, handleJeanFilesDelete, map[string]any{"paths": []string{"Photos"}}); code != 200 || isDir(filepath.Join(root, "Photos")) {
		t.Fatalf("delete : %d %v", code, out)
	}
}

// Envoi depuis le gestionnaire : rangé dans le dossier choisi ; le même fichier
// renvoyé est gardé tel quel, un autre contenu prend un nom libre.
func TestJeanFilesUpload(t *testing.T) {
	root := jeanFilesTestRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	send := func(content string) map[string]any {
		dest := "docs"
		b, _ := json.Marshal(uploadReq{Name: "note.txt", Data: base64.StdEncoding.EncodeToString([]byte(content)), Dest: &dest})
		rr := httptest.NewRecorder()
		handleChatUpload(rr, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b)))
		var out map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		if rr.Code != 200 {
			t.Fatalf("envoi : %d %v", rr.Code, out)
		}
		return out
	}
	if out := send("bonjour"); out["path"] != "jean-workspace/docs/note.txt" {
		t.Fatalf("chemin : %v", out)
	}
	if out := send("bonjour"); out["path"] != "jean-workspace/docs/note.txt" {
		t.Fatalf("même fichier renvoyé copié : %v", out)
	}
	if out := send("autre chose"); out["path"] != "jean-workspace/docs/note-2.txt" {
		t.Fatalf("autre contenu : %v", out)
	}
	// Hors du dossier : refusé.
	dest := "../"
	b, _ := json.Marshal(uploadReq{Name: "x.txt", Data: base64.StdEncoding.EncodeToString([]byte("x")), Dest: &dest})
	rr := httptest.NewRecorder()
	handleChatUpload(rr, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b)))
	if rr.Code != 400 {
		t.Fatalf("envoi hors du dossier : %d", rr.Code)
	}

	// « Demander à Jean » : le fichier joint reste où il est, annoncé au modèle
	// par son chemin dans son dossier.
	files := attachFiles([]string{"jean-workspace/docs/note-2.txt", "jean-workspace/../x"})
	if len(files) != 1 || files[0].Name != "note-2.txt" || files[0].modelPath() != "docs/note-2.txt" {
		t.Fatalf("pièces jointes : %+v", files)
	}
	if note := attachNote(files); !strings.Contains(note, "- docs/note-2.txt") {
		t.Fatalf("note : %q", note)
	}
	if b, err := readAttach(files[0]); err != nil || string(b) != "autre chose" {
		t.Fatalf("lecture : %q %v", b, err)
	}
}

// Décrire une image coûte un appel au modèle : jamais en fond sur une API
// distante (facturée au jeton) ni sur un GPU cloud, seulement en local.
func TestJeanFilesLocalVision(t *testing.T) {
	testHome(t)
	set := func(url string) {
		t.Helper()
		if err := WriteConfig(map[string]string{extKeyFlag: "1", extKeyURL: url, extKeyModel: "m", extKeyVision: "1"}); err != nil {
			t.Fatal(err)
		}
	}
	set("https://api.example.com/v1")
	if !visionEnabled() {
		t.Skip("vision externe non reconnue par cette configuration")
	}
	if jeanFilesLocalVision() {
		t.Fatal("une API distante décrirait les images en fond")
	}
	for _, u := range []string{"http://127.0.0.1:8777/v1", "http://localhost:5000", "http://10.0.0.5:8777/v1"} {
		set(u)
		if !jeanFilesLocalVision() {
			t.Fatalf("%s : moteur local refusé", u)
		}
	}
}
