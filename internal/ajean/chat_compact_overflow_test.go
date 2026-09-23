package ajean

import "testing"

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
