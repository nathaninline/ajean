package ajean

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// upload envoie un morceau et renvoie la réponse décodée.
func uploadChunk(t *testing.T, body map[string]any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	handleChatUpload(rec, httptest.NewRequest("POST", "/api/chat/upload", strings.NewReader(string(b))))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// Un fichier arrive en plusieurs morceaux et doit être reconstitué à l'octet
// près. C'est ce découpage qui permet le gigaoctet : sans lui, tout le fichier
// passait dans un seul corps JSON, gardé en mémoire des deux côtés.
func TestChatUploadChunked(t *testing.T) {
	part1 := strings.Repeat("A", 5000)
	part2 := strings.Repeat("B", 3000)
	part3 := "fin"

	code, resp := uploadChunk(t, map[string]any{
		"name": "gros.bin", "data": base64.StdEncoding.EncodeToString([]byte(part1)), "more": true})
	if code != 200 || resp["id"] == nil {
		t.Fatalf("1er morceau : code %d, resp %+v", code, resp)
	}
	id := resp["id"].(string)
	if got := resp["received"].(float64); got != 5000 {
		t.Fatalf("avancement après 1er morceau = %v", got)
	}
	if code, resp = uploadChunk(t, map[string]any{
		"id": id, "data": base64.StdEncoding.EncodeToString([]byte(part2)), "more": true}); code != 200 {
		t.Fatalf("2e morceau : code %d, resp %+v", code, resp)
	}
	code, resp = uploadChunk(t, map[string]any{
		"id": id, "data": base64.StdEncoding.EncodeToString([]byte(part3))})
	if code != 200 || resp["ok"] != true {
		t.Fatalf("dernier morceau : code %d, resp %+v", code, resp)
	}
	abs := resp["abs"].(string)
	defer os.Remove(abs)
	if resp["path"] != "uploads/gros.bin" {
		t.Fatalf("chemin = %v", resp["path"])
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != part1+part2+part3 {
		t.Fatalf("reconstitution : %d octets, attendu %d", len(got), len(part1+part2+part3))
	}
	// Aucun .part ne doit traîner une fois l'envoi terminé.
	dir, _ := uploadsDir()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Fatalf("fichier temporaire laissé : %s", e.Name())
		}
	}
}

// Un identifiant inconnu (envoi expiré, serveur redémarré) ne doit pas produire
// un fichier tronqué qui repart de son milieu.
func TestChatUploadUnknownSessionRefused(t *testing.T) {
	code, resp := uploadChunk(t, map[string]any{
		"id": "inconnu-1234.part", "data": base64.StdEncoding.EncodeToString([]byte("x"))})
	if code != 409 {
		t.Fatalf("code %d, attendu 409 — resp %+v", code, resp)
	}
}

// Le téléchargement via app.ajean.link : le proxy chiffré réemballe toute
// réponse en JSON, où du binaire ne survit pas. Le handler doit donc annoncer
// qu'on est derrière le tunnel, puis savoir servir des tranches base64.
func TestChatFileB64ForE2E(t *testing.T) {
	dir, _ := uploadsDir()
	// Des octets NON-UTF8 : c'est exactement ce que le réemballage JSON massacre.
	raw := make([]byte, 5000)
	for i := range raw {
		raw[i] = byte(i % 256)
	}
	p := filepath.Join(dir, "binaire.bin")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)

	// La fiche dit « tunnel » quand la requête vient du proxy, et pas autrement.
	meta := func(viaTunnel bool) map[string]any {
		req := httptest.NewRequest("GET", "/api/chat/file?meta=1&path=uploads/binaire.bin", nil)
		if viaTunnel {
			req.Header.Set(e2eInnerHeader, "1")
		}
		rec := httptest.NewRecorder()
		handleChatFile(rec, req)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m
	}
	if m := meta(true); m["e2e"] != true || m["size"].(float64) != 5000 {
		t.Fatalf("fiche via tunnel : %+v", m)
	}
	if m := meta(false); m["e2e"] != false {
		t.Fatalf("fiche en local annoncée comme tunnel : %+v", m)
	}

	// Reconstitution par tranches : l'octet doit revenir intact.
	var got []byte
	for off, guard := 0, 0; ; guard++ {
		if guard > 50 {
			t.Fatal("boucle de tranches sans fin")
		}
		rec := httptest.NewRecorder()
		handleChatFile(rec, httptest.NewRequest("GET",
			fmt.Sprintf("/api/chat/file?b64=1&len=1024&offset=%d&path=uploads/binaire.bin", off), nil))
		if rec.Code != 200 {
			t.Fatalf("tranche à %d : code %d — %s", off, rec.Code, rec.Body.String())
		}
		var j struct {
			Data string `json:"data"`
			EOF  bool   `json:"eof"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &j); err != nil {
			t.Fatal(err)
		}
		b, err := base64.StdEncoding.DecodeString(j.Data)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, b...)
		off += len(b)
		if j.EOF {
			break
		}
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("fichier reconstitué : %d octets, attendu %d", len(got), len(raw))
	}
}

// Plusieurs fichiers partent DE FRONT depuis le navigateur (un envoi par pièce
// jointe). Les sessions doivent donc être indépendantes : le verrou global ne
// protège que la table, l'écriture se fait sous le verrou de la session.
func TestChatUploadConcurrentSessions(t *testing.T) {
	const files, chunks = 4, 6
	var wg sync.WaitGroup
	paths := make([]string, files)
	errs := make([]string, files)
	for i := 0; i < files; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, want := "", ""
			for c := 0; c < chunks; c++ {
				piece := strings.Repeat(string(rune('A'+i)), 100+c)
				want += piece
				code, resp := uploadChunk(t, map[string]any{
					"name": fmt.Sprintf("concurrent-%d.txt", i),
					"data": base64.StdEncoding.EncodeToString([]byte(piece)),
					"id":   id, "more": c < chunks-1,
				})
				if code != 200 {
					errs[i] = fmt.Sprintf("morceau %d : code %d (%v)", c, code, resp["error"])
					return
				}
				if v, ok := resp["id"].(string); ok && v != "" {
					id = v
				}
				if c == chunks-1 {
					abs, _ := resp["abs"].(string)
					paths[i] = abs
					got, err := os.ReadFile(abs)
					if err != nil || string(got) != want {
						errs[i] = fmt.Sprintf("contenu melange : %d octets lus, %d attendus (err %v)", len(got), len(want), err)
					}
				}
			}
		}(i)
	}
	wg.Wait()
	for _, p := range paths {
		if p != "" {
			defer os.Remove(p)
		}
	}
	for i, e := range errs {
		if e != "" {
			t.Fatalf("fichier %d : %s", i, e)
		}
	}
}
