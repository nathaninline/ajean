package jean

import "testing"

// helpers pour construire des historiques de test lisibles.
func um(s string) Message { return Message{Role: "user", Content: s} }
func am(s string) Message { return Message{Role: "assistant", Content: s} }
func atc(name string) Message {
	return Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Function: ToolCallFunc{Name: name, Arguments: "{}"}}}}
}
func tm(s string) Message { return Message{Role: "tool", ToolCallID: "c1", Content: s} }

func TestCompactBoundsProtectsHead(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		um("premier"), am("r1"),
		um("q2"), am("r2"),
		um("q3"), am("r3"),
	}
	// budget minuscule → queue = juste le dernier tour, tête = system + 1er user.
	head, tail := compactBounds(msgs, 1)
	if head != 2 { // system + premier user
		t.Fatalf("head = %d, attendu 2", head)
	}
	if msgs[tail].Role != "user" {
		t.Fatalf("la queue doit démarrer sur un message user, obtenu %q", msgs[tail].Role)
	}
	if tail <= head {
		t.Fatalf("torse vide (tail=%d head=%d) alors qu'il y a du milieu à compacter", tail, head)
	}
}

// La queue ne doit jamais démarrer entre un assistant+tool_calls et ses
// résultats `tool` : elle recule jusqu'au message user du tour.
func TestCompactBoundsKeepsToolPairs(t *testing.T) {
	msgs := []Message{
		um("q1"), am("r1"),
		um("q2"), atc("bash"), tm("sortie longue"), am("r2"),
		um("q3"), am("r3"),
	}
	// budget moyen qui, sans le recul, couperait au milieu du tour outillé.
	_, tail := compactBounds(msgs, msgTokens(msgs[6])+msgTokens(msgs[7])+msgTokens(msgs[5])+1)
	if msgs[tail].Role != "user" {
		t.Fatalf("la queue démarre sur %q, doit être un user (pas d'orphelin tool/assistant)", msgs[tail].Role)
	}
	// Vérifie qu'aucun message `tool` de la queue n'a perdu son assistant parent.
	for i := tail; i < len(msgs); i++ {
		if msgs[i].Role == "tool" {
			if i == tail || (msgs[i-1].Role != "assistant" && msgs[i-1].Role != "tool") {
				t.Fatalf("message tool orphelin à l'index %d de la queue", i)
			}
		}
	}
}

func TestEstimateTokensGrows(t *testing.T) {
	small := estimateTokens([]Message{um("court")})
	big := estimateTokens([]Message{um("un message nettement plus long que le précédent pour dépasser")})
	if big <= small {
		t.Fatalf("estimateTokens ne croît pas avec la taille: small=%d big=%d", small, big)
	}
}
