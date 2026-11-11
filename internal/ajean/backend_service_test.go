package ajean

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPortFromCommand(t *testing.T) {
	for cmd, want := range map[string]string{
		"llama-server -m x.gguf --port 9001":                 "9001",
		"python -m vllm.entrypoints.openai.api_server -p 8":  "8",
		`"C:\Program Files\x\srv.exe" --port=8123 --host ::`: "8123",
		"tabby --config c.yml":                               "",
		"srv --port":                                         "",
	} {
		if got := portFromCommand(cmd); got != want {
			t.Errorf("%q : port %q, attendu %q", cmd, got, want)
		}
	}
	if u := serviceURLFromCommand("srv --port 9001"); u != "http://127.0.0.1:9001/v1" {
		t.Errorf("adresse déduite : %q", u)
	}
}

// Un preset Service système est « externe » pour le chat (API OpenAI), mais il a
// un processus local : le service moteur doit le lancer, pas s'arrêter.
func TestServicePresetEstLocalPourLeServiceMoteur(t *testing.T) {
	testHome(t)
	api := map[string]string{"EXTERNAL": "1", "EXTERNAL_URL": "https://api.x/v1", "EXTERNAL_MODEL": "m"}
	svc := map[string]string{"EXTERNAL": "1", "EXTERNAL_URL": "http://127.0.0.1:9001/v1", svcKeyCmd: "srv --port 9001"}
	local := map[string]string{"MODEL": "x.gguf"}
	if !usesRemoteEndpoint(api) || usesRemoteEndpoint(svc) || usesRemoteEndpoint(local) {
		t.Fatal("usesRemoteEndpoint : seul le preset API n'a pas de processus local")
	}
	if serviceCmdOf(map[string]string{svcKeyCmd: "srv"}) != "" {
		t.Error("SERVICE_CMD sans EXTERNAL=1 ne fait pas un preset Service système")
	}
	for cfg, want := range map[*map[string]string]string{&api: "stop", &svc: "restart", &local: "restart"} {
		if err := WriteConfig(*cfg); err != nil {
			t.Fatal(err)
		}
		if got := presetServiceAction("restart"); got != want {
			t.Errorf("restart sur %v → %q, attendu %q", *cfg, got, want)
		}
		if got := presetServiceAction("stop"); got != "stop" {
			t.Errorf("stop doit rester stop, obtenu %q", got)
		}
	}
	// Programme introuvable : dit avant de lancer le service, et par serve.
	if err := WriteConfig(map[string]string{"EXTERNAL": "1", "EXTERNAL_URL": "http://127.0.0.1:9001/v1", svcKeyCmd: "ce-programme-n-existe-pas --port 9001"}); err != nil {
		t.Fatal(err)
	}
	if err := preflightEngine(); err == nil || !strings.Contains(err.Error(), "introuvable") {
		t.Errorf("pré-vol : %v", err)
	}
	if err := cmdServe(nil); err == nil || !strings.Contains(err.Error(), "introuvable") {
		t.Errorf("serve : %v", err)
	}
	if _, err := parseServiceEnv(`HF_HOME=/data FOO="a b"`); err != nil {
		t.Errorf("variables valides refusées : %v", err)
	}
	if _, err := parseServiceEnv("pas-une-variable"); err == nil {
		t.Error("variable sans = acceptée")
	}
}

// La modale : commande obligatoire, adresse déduite du port, modèle facultatif
// (le premier annoncé par l'API), et relecture fidèle.
func TestServicePresetSaveEtRelecture(t *testing.T) {
	testHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen-vllm"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	post := func(req externalSaveReq) (int, map[string]any) {
		b, _ := json.Marshal(req)
		w := httptest.NewRecorder()
		handlePresetExternalSave(w, httptest.NewRequest("POST", "/api/preset/external/save", bytes.NewReader(b)))
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, _ := post(externalSaveReq{Name: "vllm", Service: true}); code != 400 {
		t.Errorf("commande vide acceptée (%d)", code)
	}
	if code, _ := post(externalSaveReq{Name: "vllm", Service: true, Cmd: "srv --config c.yml"}); code != 400 {
		t.Errorf("sans port ni adresse : accepté (%d)", code)
	}
	code, out := post(externalSaveReq{Name: "vllm", Service: true, Cmd: "srv\n --port 9001", Env: "HF_HOME=/data", Workdir: "/srv"})
	if code != 200 || out["url"] != "http://127.0.0.1:9001/v1" {
		t.Fatalf("enregistrement : %d %v", code, out)
	}
	id, _ := out["id"].(string)
	content, err := ReadPreset(id)
	if err != nil {
		t.Fatal(err)
	}
	cfg := parseEnv(content)
	if serviceCmdOf(cfg) != "srv --port 9001" || cfg[svcKeyEnv] != "HF_HOME=/data" || cfg[svcKeyWorkdir] != "/srv" || usesRemoteEndpoint(cfg) {
		t.Fatalf("preset enregistré : %v", cfg)
	}
	w := httptest.NewRecorder()
	handlePresetExternal(w, httptest.NewRequest("GET", "/api/preset/external?id="+id, nil))
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["service"] != true || got["cmd"] != "srv --port 9001" || got["env"] != "HF_HOME=/data" {
		t.Errorf("relecture : %v", got)
	}
	// Modèle non nommé : le premier annoncé par l'API du service.
	cfg[extKeyURL] = srv.URL + "/v1"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if ep := resolveChatEndpoint(); !ep.External || ep.Model != "qwen-vllm" || !strings.HasSuffix(ep.URL, "/v1/chat/completions") {
		t.Errorf("endpoint : %+v", ep)
	}
	if !serviceCmdReady(cfg) {
		t.Error("une API qui répond sur /models doit compter comme prête")
	}
	// Repasser le preset en simple API retire la commande.
	if code, _ := post(externalSaveReq{ID: id, Name: "vllm", URL: "https://api.x/v1", Model: "m"}); code != 200 {
		t.Fatalf("retour en API : %d", code)
	}
	content, _ = ReadPreset(id)
	if c := parseEnv(content); serviceCmdOf(c) != "" || !usesRemoteEndpoint(c) {
		t.Errorf("la commande aurait dû disparaître : %v", c)
	}
}
