package ajean

import "testing"

// Le sampling du preset doit se retrouver dans le corps de la requête, avec les
// bons types (top_k entier, le reste flottant), et TEMP écrase la température
// déjà posée. Une clé vide n'ajoute rien : le serveur garde son défaut.
func TestApplySamplingFrom(t *testing.T) {
	payload := map[string]any{"temperature": 0.7}
	applySamplingFrom(payload, map[string]string{
		"TEMP":             "1.0",
		"TOP_P":            "0.95",
		"TOP_K":            "20",
		"MIN_P":            "0",
		"PRESENCE_PENALTY": "", // vide → ignoré
		"REASONING_EFFORT": "medium",
	})
	if payload["temperature"] != 1.0 {
		t.Errorf("TEMP doit écraser la température : %v", payload["temperature"])
	}
	if payload["top_p"] != 0.95 || payload["min_p"] != 0.0 {
		t.Errorf("top_p/min_p flottants attendus : %v %v", payload["top_p"], payload["min_p"])
	}
	if payload["top_k"] != 20 {
		t.Errorf("top_k doit être un entier 20, pas %#v", payload["top_k"])
	}
	if _, ok := payload["presence_penalty"]; ok {
		t.Error("une clé vide ne doit rien envoyer")
	}
	kw, ok := payload["chat_template_kwargs"].(map[string]any)
	if !ok || kw["reasoning_effort"] != "medium" {
		t.Errorf("reasoning_effort manquant : %v", payload["chat_template_kwargs"])
	}
}

// Sans aucune clé de sampling, le payload n'est pas modifié : on ne force pas de
// valeurs qui contrediraient le défaut du modèle.
func TestApplySamplingEmpty(t *testing.T) {
	payload := map[string]any{"temperature": 0.7}
	applySamplingFrom(payload, map[string]string{})
	if len(payload) != 1 || payload["temperature"] != 0.7 {
		t.Errorf("payload modifié à tort : %v", payload)
	}
}

// Issue #178 : vers une API externe, l'effort de réflexion part en champ natif,
// jamais dans chat_template_kwargs (propre à llama.cpp, rejeté par OpenAI & co).
func TestApplySamplingExternalEffort(t *testing.T) {
	ext := map[string]any{}
	applySamplingFrom(ext, map[string]string{"EXTERNAL": "1", "REASONING_EFFORT": "high"})
	if ext["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort absent : %v", ext)
	}
	if _, ok := ext["chat_template_kwargs"]; ok {
		t.Fatalf("chat_template_kwargs envoyé à une API externe : %v", ext)
	}
	local := map[string]any{}
	applySamplingFrom(local, map[string]string{"REASONING_EFFORT": "high"})
	if _, ok := local["reasoning_effort"]; ok || local["chat_template_kwargs"] == nil {
		t.Fatalf("llama.cpp local : %v", local)
	}
}
