package ajean

import (
	"strings"
	"testing"
)

// L'allègement remplace un long résultat par son aperçu SANS PERTE : le texte
// complet se relit à l'identique, puis disparaît avec sa conversation.
func TestSlimArchiveLossless(t *testing.T) {
	testHome(t)
	full := strings.Repeat("ligne de sortie 0123456789\n", 400) // > toolPreviewChars
	a := &convArchive{ID: "sess1", Title: "t", Log: []LogEvent{
		{Seq: 1, Delta: map[string]any{"user": "q"}},
		{Seq: 2, Delta: map[string]any{"tool_used": map[string]any{"name": "bash", "done": true, "result": full}}},
		{Seq: 3, Delta: map[string]any{"tool_used": map[string]any{"name": "bash", "done": true, "result": "court"}}},
	}}
	if err := saveArchive(a); err != nil {
		t.Fatal(err)
	}
	if !slimArchiveLog(a) {
		t.Fatal("aucun allègement alors qu'un résultat dépasse l'aperçu")
	}
	tu := a.Log[1].Delta["tool_used"].(map[string]any)
	rid, _ := tu["result_id"].(string)
	if !strings.HasPrefix(rid, "sess1.") || len([]rune(tu["result"].(string))) != toolPreviewChars || tu["result_chars"] != len([]rune(full)) {
		t.Fatalf("événement allégé inattendu : id=%q aperçu=%d car.", rid, len([]rune(tu["result"].(string))))
	}
	if got, ok := loadToolResult(rid); !ok || got != full {
		t.Fatal("le texte complet ne se relit pas à l'identique")
	}
	if a.Log[2].Delta["tool_used"].(map[string]any)["result"] != "court" {
		t.Fatal("un résultat court ne doit pas être touché")
	}
	if slimArchiveLog(a) {
		t.Fatal("un second passage ne doit rien changer (idempotent)")
	}
	if err := deleteArchive("sess1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadToolResult(rid); ok {
		t.Fatal("les résultats doivent partir avec leur conversation")
	}
}
