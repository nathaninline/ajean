package ajean

import (
	"strings"
	"testing"
)

// Le compactage de secours ne doit se déclencher que sur un vrai débordement de
// contexte, pas sur n'importe quel refus du moteur.
func TestContextOverflow(t *testing.T) {
	small := []Message{{Role: "user", Content: "salut"}}
	yes := []string{
		`{"error":{"code":400,"message":"request (70000 tokens) exceeds the available context size (65536 tokens), try increasing it","type":"exceed_context_size_error"}}`,
		`{"error":{"message":"This model's maximum context length is 8192 tokens","code":"context_length_exceeded"}}`,
	}
	no := []string{
		`{"error":{"code":500,"message":"Failed to parse tool call arguments as JSON","type":"server_error"}}`,
		`{"error":{"code":503,"message":"Loading model","type":"unavailable_error"}}`,
		`Unable to generate parser for this template`,
	}
	for _, m := range yes {
		if !contextOverflow(m, small) {
			t.Errorf("débordement non reconnu : %s", m)
		}
	}
	for _, m := range no {
		if contextOverflow(m, small) {
			t.Errorf("faux débordement (déclencherait un compactage) : %s", m)
		}
	}
}

// Le dernier recours ramène sous la fenêtre même quand tout est dans un seul tour.
func TestShrinkToFit(t *testing.T) {
	big := strings.Repeat("x", 200000)
	msgs := []Message{
		{Role: "system", Content: "ctx"},
		{Role: "user", Content: "vieux"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "lis tout"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "a"}}},
		{Role: "tool", ToolCallID: "a", Content: big},
		{Role: "tool", ToolCallID: "b", Content: big},
	}
	out, changed := shrinkToFit(msgs, 132291)
	if !changed || out[0].Role != "system" {
		t.Fatalf("pas réduit : %v", changed)
	}
	if est := estimateTokens(out); est > int(float64(ctxWindow())*0.6) {
		t.Fatalf("encore trop gros : %d", est)
	}
	if overflowTokens(`{"message":"request (132291 tokens) exceeds"}`) != 132291 {
		t.Fatal("taille réelle non lue")
	}
}
