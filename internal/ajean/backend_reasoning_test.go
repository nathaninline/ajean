package ajean

import "testing"

// Un endpoint réellement distant ne doit recevoir AUCUN champ llama.cpp : les
// fournisseurs tolérants les ignorent (le réglage ne marche alors pas) et les
// stricts les rejettent (400). Mesuré chez DeepSeek le 2026-10-28 : le corps
// contenait chat_template_kwargs et le raisonnement restait actif.
func TestApplySamplingExterneOmetLesChampsLlamaCpp(t *testing.T) {
	payload := map[string]any{}
	cfg := map[string]string{
		"TEMP":             "0.7",
		"TOP_P":            "0.95",
		"TOP_K":            "20",
		"MIN_P":            "0",
		"PRESENCE_PENALTY": "0.1",
		"REPEAT_PENALTY":   "1.1",
		"REASONING_EFFORT": "medium",
	}
	applySamplingFrom(payload, cfg, chatEndpoint{External: true})
	for _, k := range []string{"top_k", "min_p", "repeat_penalty", "chat_template_kwargs"} {
		if _, ok := payload[k]; ok {
			t.Errorf("%s ne doit pas partir vers une API distante : %v", k, payload)
		}
	}
	if payload["temperature"] != 0.7 || payload["top_p"] != 0.95 || payload["presence_penalty"] != 0.1 {
		t.Errorf("les champs OpenAI standard doivent rester : %v", payload)
	}
}

// Modal est External=true mais fait tourner llama.cpp : les kwargs de gabarit et
// les champs d'échantillonnage llama.cpp y restent valides.
func TestApplySamplingCloudGardeLesChampsLlamaCpp(t *testing.T) {
	payload := map[string]any{}
	applySamplingFrom(payload,
		map[string]string{"TOP_K": "20", "REASONING_EFFORT": "medium"},
		chatEndpoint{External: true, Cloud: true, Template: true})
	if payload["top_k"] != 20 {
		t.Errorf("top_k manquant : %v", payload)
	}
	if _, ok := payload["chat_template_kwargs"].(map[string]any); !ok {
		t.Errorf("chat_template_kwargs manquant : %v", payload)
	}
}

// DeepSeek : un effort défini active le raisonnement, même si REASONING=off est
// resté dans le preset. C'est le cas mesuré (REASONING=off + REASONING_EFFORT=high)
// où l'API raisonnait quand même à son défaut.
func TestApplyReasoningDeepSeekEffortGagneSurOff(t *testing.T) {
	payload := map[string]any{}
	cfg := map[string]string{
		"EXTERNAL":         "1",
		"EXTERNAL_URL":     "https://api.deepseek.com/v1",
		"EXTERNAL_MODEL":   "deepseek-flash",
		"REASONING":        "off",
		"REASONING_EFFORT": "medium",
	}
	applySamplingFrom(payload, cfg, chatEndpoint{External: true})
	th, _ := payload["thinking"].(map[string]any)
	if th["type"] != "enabled" || payload["reasoning_effort"] != "high" {
		t.Errorf("attendu thinking enabled + reasoning_effort high : %v", payload)
	}
	if _, ok := payload["chat_template_kwargs"]; ok {
		t.Error("chat_template_kwargs ne doit pas partir chez DeepSeek")
	}
}

// DeepSeek : off explicite et aucun effort → thinking désactivé, rien d'autre.
func TestApplyReasoningDeepSeekOff(t *testing.T) {
	payload := map[string]any{}
	cfg := map[string]string{
		"EXTERNAL":     "1",
		"EXTERNAL_URL": "https://api.deepseek.com/v1",
		"REASONING":    "off",
	}
	applySamplingFrom(payload, cfg, chatEndpoint{External: true})
	th, _ := payload["thinking"].(map[string]any)
	if th["type"] != "disabled" {
		t.Errorf("attendu thinking disabled : %v", payload)
	}
	if _, ok := payload["reasoning_effort"]; ok {
		t.Errorf("aucun reasoning_effort attendu : %v", payload)
	}
}

// OpenAI : champ natif de premier niveau, jamais de champ llama.cpp.
func TestApplyReasoningOpenAI(t *testing.T) {
	payload := map[string]any{}
	cfg := map[string]string{
		"EXTERNAL":           "1",
		"EXTERNAL_URL":       "https://api.openai.com/v1",
		"EXTERNAL_MODEL":     "gpt-5",
		"EXTERNAL_REASONING": extReasoningOpenAI,
		"REASONING_EFFORT":   "xhigh",
	}
	applySamplingFrom(payload, cfg, chatEndpoint{External: true})
	if payload["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort high attendu : %v", payload)
	}
	for _, k := range []string{"thinking", "chat_template_kwargs"} {
		if _, ok := payload[k]; ok {
			t.Errorf("%s ne doit pas partir chez OpenAI : %v", k, payload)
		}
	}
}

// Un fournisseur inconnu ne reçoit rien : mieux vaut un réglage inopérant qu'un
// 400 sur chaque requête.
func TestApplyReasoningFournisseurInconnuNEnvoieRien(t *testing.T) {
	payload := map[string]any{}
	cfg := map[string]string{
		"EXTERNAL":         "1",
		"EXTERNAL_URL":     "https://api.exemple.com/v1",
		"EXTERNAL_MODEL":   "mon-modele",
		"REASONING_EFFORT": "high",
		"REASONING":        "off",
	}
	applySamplingFrom(payload, cfg, chatEndpoint{External: true})
	if _, ok := payload["chat_template_kwargs"]; ok {
		t.Errorf("chat_template_kwargs envoyé à un fournisseur inconnu : %v", payload)
	}
	for _, k := range []string{"thinking", "reasoning_effort"} {
		if _, ok := payload[k]; ok {
			t.Errorf("%s envoyé à un fournisseur inconnu : %v", k, payload)
		}
	}
}

func TestDeepseekEffortMapping(t *testing.T) {
	cases := map[string]string{
		"low": "low", "medium": "high", "high": "high", "xhigh": "max",
		"inconnu": "",
	}
	for in, want := range cases {
		if got := deepseekEffort(in); got != want {
			t.Errorf("deepseekEffort(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestOpenAIEffortMapping(t *testing.T) {
	cases := map[string]string{
		"low": "low", "medium": "medium", "high": "high", "xhigh": "high",
		"inconnu": "",
	}
	for in, want := range cases {
		if got := openAIEffort(in); got != want {
			t.Errorf("openAIEffort(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestExternalReasoningMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]string
		want string
	}{
		{"clé explicite", map[string]string{extKeyReasoning: extReasoningOpenAI}, extReasoningOpenAI},
		{"none coupe l'auto-détection", map[string]string{
			extKeyReasoning: "none", extKeyURL: "https://api.deepseek.com/v1",
		}, ""},
		{"hôte deepseek", map[string]string{
			extKeyURL: "https://api.deepseek.com/v1",
		}, extReasoningDeepSeek},
		{"modèle deepseek derrière un proxy", map[string]string{
			extKeyURL: "https://proxy.exemple.com/v1", extKeyModel: "deepseek-chat",
		}, extReasoningDeepSeek},
		{"fournisseur inconnu", map[string]string{
			extKeyURL: "https://api.openai.com/v1", extKeyModel: "gpt-5",
		}, ""},
	}
	for _, c := range cases {
		if got := externalReasoningMode(c.cfg); got != c.want {
			t.Errorf("%s : externalReasoningMode = %q, attendu %q", c.name, got, c.want)
		}
	}
}
