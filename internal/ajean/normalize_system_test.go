package ajean

import (
	"reflect"
	"testing"
)

// LE test qui rassure : sur une conversation NORMALE (un seul system en tête,
// suivi de user/assistant/tool, appels d'outils et contenu multimodal compris),
// la normalisation ne doit RIEN changer — même contenu, même ordre. C'est la
// garantie que le fix est invisible pour les 99 % de cas qui marchaient déjà.
func TestNormalizeSystemMessagesNoOpOnNormalConversation(t *testing.T) {
	in := []Message{
		{Role: "system", Content: "Tu es un assistant."},
		{Role: "user", Content: "cherche la météo"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Type: "function"}}},
		{Role: "tool", Content: "résultat brut", ToolCallID: "call_1"},
		{Role: "assistant", Content: "Il fait beau."},
		// contenu multimodal (image) : Content n'est pas une string
		{Role: "user", Content: []any{map[string]any{"type": "text", "text": "et demain ?"}}},
	}
	// copie profonde de référence pour détecter toute mutation de l'entrée
	ref := append([]Message(nil), in...)
	out := normalizeSystemMessages(in)
	if !reflect.DeepEqual(out, in) {
		t.Errorf("séquence normale modifiée !\navant: %#v\naprès: %#v", in, out)
	}
	if !reflect.DeepEqual(in, ref) {
		t.Errorf("l'entrée a été mutée (interdit) : %#v", in)
	}
}

// L'ordre relatif de tous les messages NON-system (et le lien assistant→tool via
// ToolCallID) doit être préservé quand on ramène un system égaré en tête.
func TestNormalizeSystemMessagesPreservesOrderAndToolLink(t *testing.T) {
	in := []Message{
		{Role: "system", Content: "S1"},
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_9"}}},
		{Role: "tool", Content: "r", ToolCallID: "call_9"},
		{Role: "system", Content: "n'appelle plus d'outil"}, // égaré, en fin
	}
	out := normalizeSystemMessages(in)
	want := []Message{
		{Role: "system", Content: "S1\n\nn'appelle plus d'outil"},
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_9"}}},
		{Role: "tool", Content: "r", ToolCallID: "call_9"},
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("ordre/lien outil cassé :\n%#v", out)
	}
}

// Un system inséré en fin de séquence (relance anti-boucle) ou en double doit
// être ramené en un seul bloc, en tête : sinon les gabarits Qwen3.x renvoient
// « System message must be at the beginning » (issue #26).
func TestNormalizeSystemMessages(t *testing.T) {
	sys := func(c string) Message { return Message{Role: "system", Content: c} }
	usr := func(c string) Message { return Message{Role: "user", Content: c} }

	firstRoleSystemOnce := func(t *testing.T, out []Message) {
		count := 0
		for i, m := range out {
			if m.Role == "system" {
				count++
				if i != 0 {
					t.Errorf("system trouvé en position %d, doit être en 0", i)
				}
			}
		}
		if count > 1 {
			t.Errorf("%d messages system, doit être fusionné en un seul", count)
		}
	}

	t.Run("system en fin", func(t *testing.T) {
		out := normalizeSystemMessages([]Message{sys("A"), usr("q"), sys("stop")})
		firstRoleSystemOnce(t, out)
		if out[0].Content != "A\n\nstop" {
			t.Errorf("fusion attendue 'A\\n\\nstop', obtenu %q", out[0].Content)
		}
		if len(out) != 2 {
			t.Fatalf("attendu 2 messages, obtenu %d", len(out))
		}
	})

	t.Run("sans system, inchangé", func(t *testing.T) {
		in := []Message{usr("a"), usr("b")}
		out := normalizeSystemMessages(in)
		if len(out) != 2 || out[0].Role != "user" {
			t.Fatalf("séquence sans system ne doit pas changer : %#v", out)
		}
	})

	t.Run("system vide ignoré", func(t *testing.T) {
		out := normalizeSystemMessages([]Message{sys("  "), usr("q")})
		if len(out) != 1 || out[0].Role != "user" {
			t.Fatalf("un system vide ne doit rien ajouter : %#v", out)
		}
	})
}
