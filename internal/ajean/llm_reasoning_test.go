package ajean

import (
	"reflect"
	"testing"
)

func TestReasoningParams(t *testing.T) {
	kw := func(m map[string]any) map[string]any { return map[string]any{"chat_template_kwargs": m} }
	cases := []struct {
		base, level string
		want        map[string]any
	}{
		{"https://api.openai.com/v1", "", nil},
		{"https://api.openai.com/v1", "n'importe quoi", nil},
		{"https://api.openai.com/v1", "high", map[string]any{"reasoning_effort": "high"}},
		{"https://api.groq.com/openai/v1", "off", map[string]any{"reasoning_effort": "none"}},
		{"https://exemple.eu-west-1.maas.aliyuncs.com/compatible-mode/v1", "off", map[string]any{"enable_thinking": false}},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", "low", map[string]any{"enable_thinking": true, "thinking_budget": 1024}},
		{"https://dashscope-intl.aliyuncs.com/compatible-mode/v1", "high", map[string]any{"enable_thinking": true}},
		{"https://openrouter.ai/api/v1", "medium", map[string]any{"reasoning": map[string]any{"effort": "medium"}}},
		{"https://openrouter.ai/api/v1", "off", map[string]any{"reasoning": map[string]any{"effort": "none"}}},
		{"https://api.z.ai/api/paas/v4", "off", map[string]any{"thinking": map[string]any{"type": "disabled"}}},
		{"https://api.deepseek.com/v1", "high", map[string]any{"thinking": map[string]any{"type": "enabled"}}},
		{"http://127.0.0.1:8000/v1", "off", kw(map[string]any{"enable_thinking": false})},
		{"http://localhost:8000/v1", "low", kw(map[string]any{"enable_thinking": true, "reasoning_effort": "low"})},
		// Un nom qui contient seulement celui d'un fournisseur n'est pas ce fournisseur.
		{"https://notaliyuncs.com/v1", "off", map[string]any{"reasoning_effort": "none"}},
	}
	for _, c := range cases {
		if got := reasoningParams(c.base, c.level); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s / %q = %v, attendu %v", c.base, c.level, got, c.want)
		}
	}
}

func TestStripReasoning(t *testing.T) {
	p := map[string]any{"model": "m", "enable_thinking": true, "thinking_budget": 1024, "temperature": 0.7}
	if !stripReasoning(p) {
		t.Fatal("champs de réflexion non détectés")
	}
	if want := map[string]any{"model": "m", "temperature": 0.7}; !reflect.DeepEqual(p, want) {
		t.Errorf("après retrait = %v, attendu %v", p, want)
	}
	if stripReasoning(p) {
		t.Error("second retrait : plus rien à retirer")
	}
}

func TestReasoningRejected(t *testing.T) {
	ep := chatEndpoint{URL: "https://api.exemple.com/v1/chat/completions", Model: "m-test-rej", External: true}
	if reasoningRejected(ep, "high") {
		t.Fatal("refus déjà noté")
	}
	markReasoningRejected(ep, "high")
	if !reasoningRejected(ep, "high") {
		t.Error("refus non retenu")
	}
	// Autre niveau, autre modèle, ou pas de niveau : on réessaie.
	if reasoningRejected(ep, "off") || reasoningRejected(ep, "") || reasoningRejected(chatEndpoint{URL: ep.URL, Model: "autre"}, "high") {
		t.Error("refus appliqué trop largement")
	}
}
