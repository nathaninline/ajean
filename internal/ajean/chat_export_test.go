package ajean

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// convDeTest pose un fil complet (question, raisonnement, outil, réponse) dans la
// conversation globale et la remet à zéro en fin de test.
func convDeTest(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { conv.Reset() })
	conv.mu.Lock()
	conv.Messages = []Message{
		{Role: "user", Content: "combien font 2+2 ?"},
		{Role: "assistant", Content: "4"},
	}
	conv.Log = []LogEvent{
		{Seq: 1, TS: 1, Delta: map[string]any{"user": "combien font 2+2 ?"}},
		{Seq: 2, TS: 2, Delta: map[string]any{"reasoning_content": "addition simple"}},
		{Seq: 3, TS: 3, Delta: map[string]any{"tool_used": map[string]any{
			"name": "bash", "label": "echo 4", "result": "4", "done": true}}},
		{Seq: 4, TS: 4, Delta: map[string]any{"content": "4"}},
		{Seq: 5, TS: 5, Delta: map[string]any{"turn_done": true}},
	}
	conv.Seq = 5
	conv.CtxUsed = 123
	conv.mu.Unlock()
}

func TestExportMarkdownPorteToutLeFil(t *testing.T) {
	convDeTest(t)
	md := conv.ExportMarkdown()
	for _, want := range []string{
		"## Vous", "combien font 2+2 ?",
		"## AJEAN",
		"<summary>Raisonnement</summary>", "addition simple",
		"bash", "echo 4",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown exporté sans %q :\n%s", want, md)
		}
	}
	// L'en-tête AJEAN ne doit apparaître qu'UNE fois pour un tour, même entrecoupé
	// d'un appel d'outil.
	if n := strings.Count(md, "\n## AJEAN\n"); n != 1 {
		t.Errorf("en-tête assistant répété %d fois", n)
	}
}

func TestExportJSONRelisible(t *testing.T) {
	convDeTest(t)
	b, err := conv.ExportJSON()
	if err != nil {
		t.Fatal(err)
	}
	var p exportPayload
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Messages) != 2 || len(p.Log) != 5 {
		t.Fatalf("export tronqué : %d messages, %d événements", len(p.Messages), len(p.Log))
	}
	if p.CtxUsed != 123 || p.Version != Version {
		t.Fatalf("métadonnées absentes : ctx=%d version=%q", p.CtxUsed, p.Version)
	}
}

// L'endpoint doit se présenter en TÉLÉCHARGEMENT (et pas s'afficher dans
// l'onglet), avec un nom de fichier horodaté.
func TestHandleChatExportEnPieceJointe(t *testing.T) {
	convDeTest(t)
	for _, tc := range []struct{ format, ctype, ext string }{
		{"", "text/markdown", ".md"},
		{"json", "application/json", ".json"},
	} {
		rr := httptest.NewRecorder()
		handleChatExport(rr, httptest.NewRequest("GET", "/api/chat/export?format="+tc.format, nil))
		if rr.Code != 200 {
			t.Fatalf("format=%q : HTTP %d", tc.format, rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.ctype) {
			t.Errorf("format=%q : Content-Type %q", tc.format, ct)
		}
		cd := rr.Header().Get("Content-Disposition")
		if !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, tc.ext) {
			t.Errorf("format=%q : Content-Disposition %q", tc.format, cd)
		}
		if rr.Body.Len() == 0 {
			t.Errorf("format=%q : corps vide", tc.format)
		}
	}
}

func TestCmdExportEcritLeFichier(t *testing.T) {
	testHome(t)
	convDeTest(t)
	// LoadConversation (appelé par cmdExport) relit la base : on y persiste d'abord
	// le fil, sinon la commande exporterait une conversation vide.
	conv.persist()

	dir := t.TempDir()
	for _, name := range []string{"fil.md", "fil.json"} {
		p := filepath.Join(dir, name)
		if err := cmdExport([]string{p}); err != nil {
			t.Fatalf("%s : %v", name, err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "2+2") {
			t.Fatalf("%s ne contient pas la conversation :\n%s", name, b)
		}
	}
	// L'extension impose le format, même sans drapeau.
	b, _ := os.ReadFile(filepath.Join(dir, "fil.json"))
	if !json.Valid(b) {
		t.Fatal("fil.json n'est pas du JSON valide")
	}
	if err := cmdExport([]string{"--zzz"}); err == nil {
		t.Fatal("option inconnue acceptée")
	}
}
