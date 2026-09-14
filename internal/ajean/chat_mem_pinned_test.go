package ajean

import (
	"strings"
	"testing"
)

// Une page mémoire lue (mem_read) doit être retrouvée par son nom (via l'argument
// `file` du tool_call), puis listée dans un rappel relisable.
func TestReminderRoundTrip(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "applique les règles de la page"},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "call_1", Function: ToolCallFunc{Name: "mem_read", Arguments: `{"file":"regles-tache.md"}`},
		}}},
		{Role: "tool", ToolCallID: "call_1", Content: "RÈGLE 1 : toujours vouvoyer."},
	}
	names := collectReadPageNames(msgs)
	if len(names) != 1 || names[0] != "regles-tache.md" {
		t.Fatalf("nom de page mal collecté : %v", names)
	}
	m, ok := buildReminderMessage(names)
	if !ok {
		t.Fatalf("un rappel aurait dû être construit")
	}
	txt, _ := m.Content.(string)
	if !strings.Contains(txt, "regles-tache.md") || !strings.Contains(txt, "mem_read") {
		t.Fatalf("le rappel ne cite pas la page ou n'invite pas à relire : %q", txt)
	}
	// Le rappel doit se relire (noms) pour survivre au compactage suivant.
	back := parseReminderNames(txt)
	if len(back) != 1 || back[0] != "regles-tache.md" {
		t.Fatalf("aller-retour du rappel cassé : %v", back)
	}
}

// Le rappel doit ACCUMULER : un nom listé à un compactage précédent (dont le mem_read
// d'origine a été résumé/effacé) est retrouvé via le rappel système, même sans le
// résultat d'outil correspondant.
func TestReminderAccumulatesAcrossCompactions(t *testing.T) {
	old, _ := buildReminderMessage([]string{"regles.md"})
	msgs := []Message{
		old, // rappel hérité (system)
		{Role: "user", Content: "[CONTEXT COMPACTED] résumé…"},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "c2", Function: ToolCallFunc{Name: "mem_read", Arguments: `{"file":"procedure.md"}`},
		}}},
		{Role: "tool", ToolCallID: "c2", Content: "procédure détaillée"},
	}
	names := collectReadPageNames(msgs)
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "regles.md") {
		t.Fatalf("la page héritée a été perdue : %v", names)
	}
	if !strings.Contains(joined, "procedure.md") {
		t.Fatalf("la nouvelle page lue n'a pas été captée : %v", names)
	}
}

// Une même page lue plusieurs fois n'est listée qu'une fois.
func TestReminderDedupes(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Function: ToolCallFunc{Name: "mem_read", Arguments: `{"file":"p.md"}`}}}},
		{Role: "tool", ToolCallID: "a", Content: "v1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "b", Function: ToolCallFunc{Name: "mem_read", Arguments: `{"file":"p.md"}`}}}},
		{Role: "tool", ToolCallID: "b", Content: "v2"},
	}
	if names := collectReadPageNames(msgs); len(names) != 1 || names[0] != "p.md" {
		t.Fatalf("attendu une seule occurrence de p.md, obtenu %v", names)
	}
}

// Une lecture ratée ([erreur] …) ne doit pas être rappelée.
func TestReminderSkipsErrors(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "e", Function: ToolCallFunc{Name: "mem_read", Arguments: `{"file":"absente.md"}`}}}},
		{Role: "tool", ToolCallID: "e", Content: "[erreur] page introuvable"},
	}
	if names := collectReadPageNames(msgs); len(names) != 0 {
		t.Fatalf("une lecture ratée ne doit pas être rappelée, obtenu %v", names)
	}
}

// La liste de noms est bornée : au-delà du plafond, on garde les plus récentes.
func TestReminderCapsNames(t *testing.T) {
	var names []string
	for i := 0; i < memReminderMaxNames+10; i++ {
		names = append(names, "page-"+string(rune('a'+i%26))+string(rune('0'+i/26))+".md")
	}
	m, ok := buildReminderMessage(names)
	if !ok {
		t.Fatalf("rappel attendu")
	}
	got := parseReminderNames(m.Content.(string))
	if len(got) != memReminderMaxNames {
		t.Fatalf("attendu %d noms max, obtenu %d", memReminderMaxNames, len(got))
	}
	// La toute dernière (plus récente) doit être conservée.
	if got[len(got)-1] != names[len(names)-1] {
		t.Fatalf("la page la plus récente doit être gardée : %q vs %q", got[len(got)-1], names[len(names)-1])
	}
}

// stripReminder retire le rappel sans toucher au reste.
func TestStripReminder(t *testing.T) {
	m, _ := buildReminderMessage([]string{"p.md"})
	msgs := []Message{
		m,
		{Role: "system", Content: "Project context — autre chose"},
		{Role: "user", Content: "salut"},
	}
	out := stripReminder(msgs)
	if len(out) != 2 {
		t.Fatalf("attendu 2 messages après strip, obtenu %d", len(out))
	}
	for _, mm := range out {
		if s, _ := mm.Content.(string); strings.HasPrefix(s, memReminderPrefix) {
			t.Fatalf("le rappel a survécu au strip")
		}
	}
}
