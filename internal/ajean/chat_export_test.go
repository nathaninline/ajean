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
	md := conv.ExportMarkdown(defaultExportOpts())
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
	b, err := conv.ExportJSON(defaultExportOpts())
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

// Les options taillent l'export, et un export tronqué le DIT (sans quoi, relu
// plus tard, il passerait pour le fil complet).
func TestExportOptionsTaillentLeMarkdown(t *testing.T) {
	convDeTest(t)
	o := defaultExportOpts()
	o.Reasoning = false
	md := conv.ExportMarkdown(o)
	if strings.Contains(md, "addition simple") {
		t.Error("raisonnement présent malgré --no-reasoning")
	}
	if !strings.Contains(md, "Export partiel") || !strings.Contains(md, "raisonnements retirés") {
		t.Errorf("export allégé non signalé :\n%s", md)
	}
	if !strings.Contains(md, "echo 4") {
		t.Error("les outils ont disparu alors que seule la réflexion était exclue")
	}

	o = defaultExportOpts()
	o.Results = false
	if md := conv.ExportMarkdown(o); strings.Contains(md, "```") {
		t.Error("sortie d'outil présente malgré --no-results")
	}

	// Couper les outils coupe forcément leurs sorties : la sortie n'a plus de
	// bulle où s'accrocher.
	o = exportOptsFromQuery(map[string][]string{"tools": {"0"}, "results": {"1"}})
	if o.Results {
		t.Error("results resté actif alors que tools est coupé")
	}
	md = conv.ExportMarkdown(o)
	if strings.Contains(md, "echo 4") {
		t.Error("outil présent malgré tools=0")
	}
}

// Sans paramètre, l'endpoint reste l'export COMPLET : les options ne doivent pas
// silencieusement appauvrir ceux qui ne les connaissent pas.
func TestExportOptsFromQueryDefautComplet(t *testing.T) {
	o := exportOptsFromQuery(map[string][]string{})
	if o != defaultExportOpts() {
		t.Fatalf("défauts modifiés : %+v", o)
	}
	o = exportOptsFromQuery(map[string][]string{"format": {"json"}, "turns": {"2"}, "full": {"0"}})
	if o.Format != "json" || o.Turns != 2 || o.JSONFull {
		t.Fatalf("options mal lues : %+v", o)
	}
	// Une valeur de tours absurde ne doit pas vider l'export.
	if o := exportOptsFromQuery(map[string][]string{"turns": {"-3"}}); o.Turns != 0 {
		t.Fatalf("turns=-3 accepté : %d", o.Turns)
	}
}

func TestExportDerniersEchanges(t *testing.T) {
	convDeTest(t)
	// Un deuxième échange s'ajoute au fil de test.
	conv.mu.Lock()
	conv.Messages = append(conv.Messages,
		Message{Role: "user", Content: "et 3+3 ?"}, Message{Role: "assistant", Content: "6"})
	conv.Log = append(conv.Log,
		LogEvent{Seq: 6, TS: 6, Delta: map[string]any{"user": "et 3+3 ?"}},
		LogEvent{Seq: 7, TS: 7, Delta: map[string]any{"content": "6"}})
	conv.mu.Unlock()

	o := defaultExportOpts()
	o.Turns = 1
	md := conv.ExportMarkdown(o)
	if strings.Contains(md, "2+2") {
		t.Errorf("le premier échange est encore là avec turns=1 :\n%s", md)
	}
	if !strings.Contains(md, "3+3") || !strings.Contains(md, "1 derniers échanges") {
		t.Errorf("dernier échange absent ou non signalé :\n%s", md)
	}
	// Demander plus d'échanges qu'il n'en existe rend tout le fil, sans erreur.
	o.Turns = 99
	if md := conv.ExportMarkdown(o); !strings.Contains(md, "2+2") {
		t.Error("turns supérieur au nombre d'échanges a tronqué le fil")
	}
	// Même découpe côté JSON.
	o = defaultExportOpts()
	o.Turns = 1
	b, err := conv.ExportJSON(o)
	if err != nil {
		t.Fatal(err)
	}
	var p exportPayload
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Messages) != 2 || p.Messages[0].Content != "et 3+3 ?" {
		t.Fatalf("messages mal découpés : %+v", p.Messages)
	}
	// --messages-only laisse tomber le journal. ⚠️ Structure NEUVE : Unmarshal ne
	// remet pas à zéro les champs absents du JSON, un `p` réutilisé garderait le
	// journal de l'appel précédent et le test passerait pour de mauvaises raisons.
	o.JSONFull = false
	b, _ = conv.ExportJSON(o)
	var sansLog exportPayload
	if err := json.Unmarshal(b, &sansLog); err != nil {
		t.Fatal(err)
	}
	if len(sansLog.Log) != 0 {
		t.Fatalf("journal présent malgré --messages-only : %d événements", len(sansLog.Log))
	}
	if len(sansLog.Messages) == 0 {
		t.Fatal("messages perdus avec --messages-only")
	}
}

// La sonde alimente le curseur de portée et la taille annoncée : elle doit
// rendre la taille EXACTE du fichier qui serait téléchargé avec les mêmes
// options, sinon l'interface annonce un poids qu'elle ne tient pas.
func TestHandleChatExportProbe(t *testing.T) {
	convDeTest(t)
	probe := func(q string) (size, turns int) {
		rr := httptest.NewRecorder()
		handleChatExport(rr, httptest.NewRequest("GET", "/api/chat/export?probe=1&"+q, nil))
		if rr.Code != 200 {
			t.Fatalf("%s : HTTP %d", q, rr.Code)
		}
		var out struct {
			Size  int `json:"size"`
			Turns int `json:"turns"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Size, out.Turns
	}
	download := func(q string) int {
		rr := httptest.NewRecorder()
		handleChatExport(rr, httptest.NewRequest("GET", "/api/chat/export?"+q, nil))
		return rr.Body.Len()
	}
	for _, q := range []string{"", "reasoning=0", "format=json", "turns=1"} {
		if s, _ := probe(q); s != download(q) {
			t.Errorf("q=%q : sonde %d o, téléchargement %d o", q, s, download(q))
		}
	}
	// Le compte d'échanges ne dépend PAS des options : c'est la borne du curseur,
	// elle ne doit pas rétrécir quand on réduit la portée.
	if _, n := probe("turns=1"); n != 1 {
		t.Errorf("échanges comptés = %d ; le fil de test en a 1", n)
	}
	if got := conv.countTurns(); got != 1 {
		t.Errorf("countTurns = %d", got)
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
