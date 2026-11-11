package ajean

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateFrom019(t *testing.T) {
	home := testHome(t)
	var retired []string
	old := retire019Unit
	retire019Unit = func(id string) error { retired = append(retired, id); return nil }
	defer func() { retire019Unit = old }()

	// Jamais passé par la 0.19 : rien ne bouge.
	migrateFrom019()
	if getStr(bkState, migrated019Key) != "" {
		t.Fatal("migration marquée sans services.json")
	}

	// Un preset 0.18 (resté sur le disque), et trois services 0.19 : celui issu
	// de ce preset, un modèle local créé en 0.19 (celui du chat), une API.
	oldID, err := SavePreset("", "Qwen local", "MODEL=qwen.gguf\nCTX=8192\n")
	if err != nil {
		t.Fatal(err)
	}
	services := `[
	 {"id":"qwen-local","name":"Qwen local","command":"llama-server -m qwen.gguf --port 8080","url":"http://127.0.0.1:8080/v1","preset":"` + oldID + `"},
	 {"id":"vllm","name":"vLLM","command":"vllm serve mon/modele\n --port 8000","workdir":"/opt/vllm","env":{"HF_HOME":"/data/hf cache"},"url":"http://127.0.0.1:8000/v1","model":"mon/modele","context":65536,"vision":true,"chat_params":{"TEMP":"0.6","REASONING_EFFORT":"high","bad key":"x"}},
	 {"id":"groq","name":"Groq","url":"https://api.groq.com/openai/v1","api_key":"gsk_test","model":"llama-3"}
	]`
	if err := os.WriteFile(filepath.Join(home, "services.json"), []byte(services), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig(map[string]string{"AJEAN_SERVICE": "vllm", "PORT": "8080"}); err != nil {
		t.Fatal(err)
	}

	migrateFrom019()

	ps, err := ListPresets()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Preset{}
	for _, p := range ps {
		byName[p.Name] = p
	}
	if len(ps) != 3 {
		t.Fatalf("%d presets après reprise, attendu 3 : %v", len(ps), byName)
	}
	read := func(name string) map[string]string {
		p, ok := byName[name]
		if !ok {
			t.Fatalf("preset %q absent", name)
		}
		c, err := ReadPreset(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return parseEnv(c)
	}
	v := read("vLLM")
	for k, want := range map[string]string{
		"EXTERNAL": "1", "SERVICE_CMD": "vllm serve mon/modele --port 8000", "SERVICE_WORKDIR": "/opt/vllm",
		"SERVICE_ENV": `HF_HOME="/data/hf cache"`, "EXTERNAL_URL": "http://127.0.0.1:8000/v1", "EXTERNAL_MODEL": "mon/modele",
		"CTX": "65536", "EXTERNAL_VISION": "1", "TEMP": "0.6", "REASONING_EFFORT": "high",
	} {
		if v[k] != want {
			t.Errorf("vLLM %s = %q, attendu %q", k, v[k], want)
		}
	}
	if env, err := parseServiceEnv(v["SERVICE_ENV"]); err != nil || len(env) != 1 || env[0] != "HF_HOME=/data/hf cache" {
		t.Errorf("SERVICE_ENV relu = %v (%v)", env, err)
	}
	g := read("Groq")
	if g["EXTERNAL"] != "1" || g["SERVICE_CMD"] != "" || g["EXTERNAL_KEY"] != "gsk_test" || g["EXTERNAL_MODEL"] != "llama-3" {
		t.Errorf("Groq = %v", g)
	}
	// Le preset 0.18 n'est ni dupliqué ni modifié.
	if c, _ := ReadPreset(oldID); !strings.Contains(c, "MODEL=qwen.gguf") {
		t.Errorf("preset 0.18 modifié : %q", c)
	}
	// Unités des deux services locaux arrêtées ; l'API n'en a pas.
	if strings.Join(retired, ",") != "qwen-local,vllm" {
		t.Errorf("unités arrêtées = %v", retired)
	}
	// Le chat utilisait vLLM : c'est le preset actif, et la config le reflète.
	if got := getStr(bkState, "active_preset"); got != byName["vLLM"].ID {
		t.Errorf("preset actif = %q, attendu %q", got, byName["vLLM"].ID)
	}
	if cfg := ReadConfig(); cfg["SERVICE_CMD"] == "" || cfg["AJEAN_SERVICE"] != "" {
		t.Errorf("config active = %v", cfg)
	}

	// Seconde fois : rien de plus.
	migrateFrom019()
	if ps2, _ := ListPresets(); len(ps2) != 3 {
		t.Errorf("reprise rejouée : %d presets", len(ps2))
	}
}
