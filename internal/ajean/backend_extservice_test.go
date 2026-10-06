package ajean

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSyncExternalService(t *testing.T) {
	testHome(t)
	var calls []string
	extUnitAction = func(unit, action string) error {
		calls = append(calls, action+" "+unit)
		return nil
	}
	defer func() { extUnitAction = unitAction }()
	var waited [][]int
	extWaitGPUs = func(p []int, _ time.Duration) { waited = append(waited, p) }
	defer func() { extWaitGPUs = waitGPUsReleased }()
	extUnitPIDs = func(string) []int { return []int{4242} }
	defer func() { extUnitPIDs = unitPIDs }()

	moe := map[string]string{"EXTERNAL": "1", "EXTERNAL_URL": "http://127.0.0.1:8080/v1", extKeyService: "ajean-moe"}
	local := map[string]string{"MODEL": "x.gguf"}
	plainExt := map[string]string{"EXTERNAL": "1", "EXTERNAL_URL": "https://api.example.com/v1"}

	steps := []struct {
		cfg  map[string]string
		want []string
	}{
		{moe, []string{"start ajean-moe"}},
		{moe, []string{"start ajean-moe"}}, // re-bascule : start est idempotent
		{local, []string{"stop ajean-moe"}},
		{local, nil}, // plus rien à arrêter
		{moe, []string{"start ajean-moe"}},
		{plainExt, []string{"stop ajean-moe"}}, // externe sans service
	}
	for i, s := range steps {
		calls = nil
		if err := syncExternalService(s.cfg); err != nil {
			t.Fatalf("étape %d: %v", i, err)
		}
		if !reflect.DeepEqual(calls, s.want) {
			t.Fatalf("étape %d: appels %v, attendu %v", i, calls, s.want)
		}
	}
	// après chaque arrêt (2 dans le scénario), on attend les process DU service
	// (relevés avant l'arrêt), pas tous ceux qui occupent les GPU
	if len(waited) != 2 || !reflect.DeepEqual(waited[0], []int{4242}) {
		t.Fatalf("attente GPU : %v, attendu 2 fois [4242]", waited)
	}
}

func TestSyncExternalServiceRejectsForeignUnits(t *testing.T) {
	testHome(t)
	called := false
	extUnitAction = func(unit, action string) error { called = true; return nil }
	defer func() { extUnitAction = unitAction }()
	for _, bad := range []string{"sshd", "ajean-moe; rm -rf /", "../ajean-x", "Ajean-moe", "ajean-"} {
		cfg := map[string]string{"EXTERNAL": "1", extKeyService: bad}
		if err := syncExternalService(cfg); err == nil {
			t.Fatalf("%q accepté", bad)
		}
	}
	if called {
		t.Fatal("systemctl appelé pour une unité refusée")
	}
	// un moteur local ne déclenche jamais EXTERNAL_SERVICE, même renseigné
	if s := externalServiceOf(map[string]string{extKeyService: "ajean-moe"}); s != "" {
		t.Fatalf("service %q pour un preset non externe", s)
	}
}

func TestExternalServiceReady(t *testing.T) {
	up := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" && up {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	state := "activating"
	extUnitState = func(string) string { return state }
	defer func() { extUnitState = unitActiveState }()
	cfg := map[string]string{"EXTERNAL": "1", extKeyURL: srv.URL + "/v1", extKeyService: "ajean-moe"}

	if ok, msg := externalServiceReady(cfg); ok || msg != "" {
		t.Fatalf("unité en démarrage : prêt=%v msg=%q", ok, msg)
	}
	state = "active" // le process tourne, le modèle charge encore
	if ok, _ := externalServiceReady(cfg); ok {
		t.Fatal("prêt alors que /health répond 503")
	}
	up = true
	if ok, _ := externalServiceReady(cfg); !ok {
		t.Fatal("pas prêt alors que /health répond 200")
	}
	state = "failed"
	if ok, msg := externalServiceReady(cfg); ok || msg == "" {
		t.Fatalf("unité en échec : prêt=%v msg=%q", ok, msg)
	}
	// preset externe sans service : toujours prêt (comportement d'avant)
	if ok, _ := externalServiceReady(map[string]string{"EXTERNAL": "1", extKeyURL: "http://x/v1"}); !ok {
		t.Fatal("preset externe simple annoncé non prêt")
	}
}

// Le bench d'un moteur tiers se mesure en streaming et se range sous
// « ext:<modèle> », jamais sous un GGUF ni pour une simple API distante.
func TestBenchExternalService(t *testing.T) {
	testHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		time.Sleep(40 * time.Millisecond) // « lecture du prompt »
		for i := 0; i < 5; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"mot \"}}]}\n\n")
			fl.Flush()
			time.Sleep(10 * time.Millisecond)
		}
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":5}}\n\n")
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	extUnitState = func(string) string { return "active" }
	defer func() { extUnitState = unitActiveState }()
	cfg := map[string]string{"EXTERNAL": "1", extKeyURL: srv.URL + "/v1", extKeyModel: "swift", extKeyService: "ajean-moe"}
	// un vrai preset actif : le bench doit aussi être rangé sous lui (liste de gauche)
	if err := os.MkdirAll(presetsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	pf := filepath.Join(presetsDir(), "MOE.env")
	if err := os.WriteFile(pf, []byte(externalPresetContent(cfg[extKeyURL], "swift", "", "", false)+extKeyService+"=ajean-moe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyPresetFile(pf); err != nil {
		t.Fatal(err)
	}
	res, err := runBench(100, 5)
	if err != nil {
		t.Fatal(err)
	}
	if res.PromptN != 1000 || res.PredictedN != 5 || res.PromptPerSecond <= 0 || res.PredictedPerSec <= 0 {
		t.Fatalf("mesure incohérente : %+v", res)
	}
	if res.PromptMs < 30 {
		t.Fatalf("lecture %.0f ms : le délai avant le 1er token n'est pas compté", res.PromptMs)
	}
	sb := loadLastBench()
	if sb == nil || sb.Model != "ext:swift" || !benchMatchesPreset(*sb, cfg) {
		t.Fatalf("bench mal rangé : %+v", sb)
	}
	store := loadBenchStore()
	if sp, ok := store["MOE"]; !ok || !benchMatchesPreset(sp, cfg) {
		t.Fatalf("bench absent de la liste des presets : %+v", store)
	}
	if a, b := benchNonce(), benchNonce(); a == b {
		t.Fatal("deux benchs consécutifs partagent le début du prompt : le cache du serveur fausserait la lecture")
	}
	if benchModelKey(map[string]string{"EXTERNAL": "1", extKeyModel: "gpt"}) != "" {
		t.Fatal("une API distante sans service ne doit pas avoir de bench")
	}
}

// Réédition d'un preset externe par la modale : les réglages de machine (#97)
// et EXTERNAL_SERVICE (qui n'a pas de champ) coexistent, sans doublon ni perte.
func TestExternalPresetEditKeepsServiceAndMachine(t *testing.T) {
	testHome(t)
	body := externalPresetContent("http://127.0.0.1:8080/v1", "swift", "", "131072", true) +
		"BIN=/opt/llama/llama-server\n" + extKeyService + "=ajean-moe\n"
	id, err := SavePreset("", "MOE", body)
	if err != nil {
		t.Fatal(err)
	}
	save := func(ctx string) map[string]string {
		req := externalSaveReq{ID: id, Name: "MOE", URL: "http://127.0.0.1:8080/v1", Model: "swift", Ctx: ctx, Vision: true}
		b, _ := json.Marshal(req)
		w := httptest.NewRecorder()
		handlePresetExternalSave(w, httptest.NewRequest("POST", "/api/preset/external", bytes.NewReader(b)))
		if w.Code != 200 {
			t.Fatalf("enregistrement : %d %s", w.Code, w.Body.String())
		}
		c, err := ReadPreset(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{extKeyService + "=", "BIN="} {
			if n := strings.Count(c, "\n"+k) + map[bool]int{true: 1}[strings.HasPrefix(c, k)]; n != 1 {
				t.Fatalf("%s présent %d fois après édition :\n%s", k, n, c)
			}
		}
		return parseEnv(c)
	}
	for _, ctx := range []string{"200000", "131072"} { // deux éditions successives
		cfg := save(ctx)
		if cfg[extKeyService] != "ajean-moe" || cfg["BIN"] != "/opt/llama/llama-server" || cfg["CTX"] != ctx {
			t.Fatalf("édition (CTX=%s) : %v", ctx, cfg)
		}
	}
}
