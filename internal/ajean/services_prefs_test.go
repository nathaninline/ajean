package ajean

import (
	"testing"
)

// Basculer le chat sur un modèle : réglages, prompt système et comportement
// « moteur local » doivent suivre, comme avec les presets.
func TestUseServiceForChatCarriesSettings(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	s := Service{ID: "qwen", Name: "Qwen", Exec: "/bin/true", URL: "http://127.0.0.1:1/v1", Model: "m",
		Context: 65536, Vision: true, Preset: "QWEN PRESET",
		ChatParams: map[string]string{"TEMP": "0.6", "TOP_K": "20", "REASONING_EFFORT": "high"}}
	if err := PutService(s); err != nil {
		t.Fatal(err)
	}
	if err := setPresetSysPrompt("QWEN PRESET", "prompt du preset"); err != nil {
		t.Fatal(err)
	}
	if err := UseServiceForChat("qwen"); err != nil {
		t.Fatal(err)
	}
	cfg := ReadConfig()
	if cfg["TEMP"] != "0.6" || cfg["TOP_K"] != "20" || cfg["CTX"] != "65536" || cfg[extKeyVision] != "1" {
		t.Fatalf("config : %v", cfg)
	}
	if ep := resolveChatEndpoint(); ep.External || ep.URL != "http://127.0.0.1:1/v1/chat/completions" {
		t.Fatalf("point d'accès : %+v", ep)
	}
	payload := map[string]any{}
	applySampling(payload)
	if payload["temperature"] != 0.6 || payload["top_k"] != 20 {
		t.Fatalf("échantillonnage : %v", payload)
	}
	if kw, ok := payload["chat_template_kwargs"].(map[string]any); !ok || kw["reasoning_effort"] != "high" {
		t.Fatalf("effort : %v", payload)
	}
	if got := readSysPrompt(); got != "prompt du preset" {
		t.Fatalf("prompt système : %q", got)
	}
	if activePresetName() != "Qwen" {
		t.Fatalf("nom : %q", activePresetName())
	}
	// Raccourci « niveau de réflexion » : reporté dans le service.
	setServiceChatParam("REASONING_EFFORT", "low")
	if g, _ := GetService("qwen"); g.ChatParams["REASONING_EFFORT"] != "low" {
		t.Fatalf("effort non reporté : %v", g.ChatParams)
	}
	// Bench rangé sous le preset d'origine, lu par l'état du service.
	saveServiceBench(&s, &benchResult{PromptPerSecond: 900, PredictedPerSec: 60})
	if b := serviceBench(&s); b == nil || b["decode"] != 60.0 {
		t.Fatalf("bench : %v", b)
	}
}

// Une API distante garde le comportement « externe ».
func TestUseRemoteServiceIsExternal(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := PutService(Service{ID: "api", URL: "https://api.example.invalid/v1", Model: "gpt",
		ChatParams: map[string]string{"REASONING_EFFORT": "high"}}); err != nil {
		t.Fatal(err)
	}
	if err := UseServiceForChat("api"); err != nil {
		t.Fatal(err)
	}
	if !resolveChatEndpoint().External {
		t.Fatal("devrait être externe")
	}
	payload := map[string]any{}
	applySampling(payload)
	if payload["reasoning_effort"] != "high" {
		t.Fatalf("effort externe : %v", payload)
	}
}

// Basculer le chat garde les réglages de la machine et oublie l'ancien modèle.
func TestUseServiceKeepsMachineKeys(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := WriteConfig(map[string]string{"MEM_MODE": "search", "COMPACT_MAX": "90000", "JEAN_IDLE_HOURS": "3",
		"HOST": "0.0.0.0", "MODEL": "vieux.gguf", "MMPROJ": "mm.gguf", "ENGINE": "exl3", "EXL3_MODEL": "x", "TEMP": "0.2"}); err != nil {
		t.Fatal(err)
	}
	if err := PutService(Service{ID: "m", URL: "http://127.0.0.1:9123/v1", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := UseServiceForChat("m"); err != nil {
		t.Fatal(err)
	}
	cfg := ReadConfig()
	for k, want := range map[string]string{"MEM_MODE": "search", "COMPACT_MAX": "90000", "JEAN_IDLE_HOURS": "3", "HOST": "0.0.0.0"} {
		if cfg[k] != want {
			t.Errorf("%s perdu : %q", k, cfg[k])
		}
	}
	for _, k := range []string{"MODEL", "MMPROJ", "ENGINE", "EXL3_MODEL", "TEMP"} {
		if cfg[k] != "" {
			t.Errorf("%s gardé à tort : %q", k, cfg[k])
		}
	}
	if LLMPort() != 9123 {
		t.Errorf("port : %d", LLMPort())
	}
}
