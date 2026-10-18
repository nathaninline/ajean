package ajean

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func connectionFixture(t *testing.T, engine string) (externalSaveReq, *httptest.Server) {
	t.Helper()
	testHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			http.Error(w, "auth", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"swift-test"},{"id":"qwen-test"}]}`)
	}))
	t.Cleanup(srv.Close)
	return externalSaveReq{Engine: engine, Name: engine + " fixture", URL: srv.URL, Model: "swift-test", Key: "fixture-secret", KeyTouched: true, Ctx: "131072"}, srv
}
func TestEngineConnectionSaveSelectOriginalEndpoint(t *testing.T) {
	for _, engine := range []string{"radiance", "paiton"} {
		t.Run(engine, func(t *testing.T) {
			req, _ := connectionFixture(t, engine)
			setConfig(t, "MODEL=old.gguf\nENGINE=llama\nBIN=/fixture/llama-server\nMEM_MODE=always\n")
			before := ReadConfig()
			id, err := saveEngineConnection(req)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, ReadConfig()) {
				t.Fatal("save activated connection")
			}
			path := filepath.Join(presetsDir(), id+".env")
			st, err := os.Stat(path)
			if err != nil || st.Mode().Perm() != 0600 {
				t.Fatal("preset not private")
			}
			if err = SwitchToPreset(path); err != nil {
				t.Fatal(err)
			}
			ep := resolveChatEndpoint()
			if !ep.External || ep.Cloud || ep.Model != req.Model || ep.Key != req.Key || ep.URL != req.URL+"/v1/chat/completions" {
				t.Fatalf("original transport not selected: %#v", ep)
			}
			cfg := ReadConfig()
			if cfg[externalEngineKey] != engine || cfg[extKeyService] != "" || cfg["MEM_MODE"] != "always" || cfg["ENGINE"] == "moe" {
				t.Fatal("unexpected active config")
			}
			rr := httptest.NewRecorder()
			handleEngineConnections(rr, httptest.NewRequest("GET", "/api/engine/connections", nil))
			if rr.Code != 200 || strings.Contains(rr.Body.String(), req.Key) || !strings.Contains(rr.Body.String(), `"active":true`) {
				t.Fatal(rr.Body.String())
			}
			// An edit through the original external editor preserves both engine and key.
			req.ID = id
			req.Engine = ""
			req.Key = ""
			req.KeyTouched = false
			req.Name = "renamed"
			body, _ := json.Marshal(req)
			rr = httptest.NewRecorder()
			handlePresetExternalSave(rr, httptest.NewRequest("POST", "/api/preset/external/save", strings.NewReader(string(body))))
			if rr.Code != 200 {
				t.Fatal(rr.Body.String())
			}
			raw, _ := ReadPreset(id)
			if c := parseEnv(raw); c[externalEngineKey] != engine || c[extKeyToken] != "fixture-secret" {
				t.Fatal("edit lost engine/key")
			}
		})
	}
}
func TestEngineConnectionDiscoveryNoInference(t *testing.T) {
	req, _ := connectionFixture(t, "radiance")
	req.Model = ""
	body, _ := json.Marshal(req)
	rr := httptest.NewRecorder()
	handlePresetExternalTest(rr, httptest.NewRequest("POST", "/api/preset/external/test", strings.NewReader(string(body))))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "qwen-test") || strings.Contains(rr.Body.String(), req.Key) {
		t.Fatal(rr.Body.String())
	}
}
func TestEngineConnectionRejectMissingModelAndBusy(t *testing.T) {
	req, _ := connectionFixture(t, "paiton")
	id, err := saveEngineConnection(req)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(presetsDir(), id+".env")
	setConfig(t, "MODEL=keep.gguf\n")
	before := ReadConfig()
	conv.mu.Lock()
	conv.Generating = true
	conv.mu.Unlock()
	err = selectEngineConnection(context.Background(), path)
	conv.mu.Lock()
	conv.Generating = false
	conv.mu.Unlock()
	if err == nil || !reflect.DeepEqual(before, ReadConfig()) {
		t.Fatal("busy selection mutated config")
	}
	req.ID = id
	req.Model = "not-announced"
	if _, err = saveEngineConnection(req); err != nil {
		t.Fatal(err)
	}
	if err = selectEngineConnection(context.Background(), path); err == nil || !reflect.DeepEqual(before, ReadConfig()) {
		t.Fatal("missing model accepted")
	}
	lcMu.Lock()
	previous := lcCur
	lcCur = &lcJob{Running: true}
	lcMu.Unlock()
	req.Model = "swift-test"
	_, err = saveEngineConnection(req)
	if err != nil {
		t.Fatal(err)
	}
	err = selectEngineConnection(context.Background(), path)
	lcMu.Lock()
	lcCur = previous
	lcMu.Unlock()
	if err == nil || !reflect.DeepEqual(before, ReadConfig()) {
		t.Fatal("install gate bypassed")
	}
}
func TestEngineConnectionErrorsNoSecretsOrRedirects(t *testing.T) {
	for _, mode := range []string{"http-error", "invalid-json", "redirect", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			targetCalls := 0
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
			defer target.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "redirect":
					http.Redirect(w, r, target.URL, 302)
				case "invalid-json":
					io.WriteString(w, `{"data":null}`)
				default:
					http.Error(w, "fixture-secret", 503)
				}
			}))
			defer srv.Close()
			ctx := context.Background()
			if mode == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := engineConnectionModels(ctx, externalSaveReq{URL: srv.URL, Key: "fixture-secret", Model: "model"})
			if err == nil || strings.Contains(err.Error(), "fixture-secret") || targetCalls != 0 {
				t.Fatal("error/redirect boundary", err, targetCalls)
			}
		})
	}
}
func TestEngineConnectionInputAndMethods(t *testing.T) {
	req, _ := connectionFixture(t, "radiance")
	for _, change := range []func(*externalSaveReq){func(r *externalSaveReq) { r.Engine = "cuda" }, func(r *externalSaveReq) { r.URL = "http://user:secret@localhost" }, func(r *externalSaveReq) { r.Model = "model\nCLOUD=1" }, func(r *externalSaveReq) { r.Key = "key\r\nInjected: 1" }, func(r *externalSaveReq) { r.Ctx = "3" }} {
		bad := req
		change(&bad)
		if _, err := saveEngineConnection(bad); err == nil {
			t.Fatal("invalid connection saved")
		}
	}
	rr := httptest.NewRecorder()
	handleEngineConnectionSelect(rr, httptest.NewRequest("GET", "/api/engine/connections/select", nil))
	if rr.Code != 405 {
		t.Fatal(rr.Code)
	}
}

// Exercise the existing runChat SSE parser and tool loop, rather than a new
// engine-specific chat implementation. Both engines use exactly this route.
func TestEngineConnectionOriginalStreamAndTools(t *testing.T) {
	for _, engine := range []string{"radiance", "paiton"} {
		t.Run(engine, func(t *testing.T) {
			testHome(t)
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					io.WriteString(w, `{"data":[{"id":"fixture-model","capabilities":{"reasoning_efforts":["none","minimal","low","medium","xhigh"]}}]}`)
					return
				}
				if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected path %s", r.URL.Path)
					http.Error(w, "unexpected", 500)
					return
				}
				if r.Header.Get("Authorization") != "Bearer private-test-token" {
					t.Error("original transport lost bearer")
				}
				var body struct {
					Model           string         `json:"model"`
					Stream          bool           `json:"stream"`
					Tools           []Tool         `json:"tools"`
					Messages        []Message      `json:"messages"`
					ReasoningEffort string         `json:"reasoning_effort"`
					TemplateKwargs  map[string]any `json:"chat_template_kwargs"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if body.Model != "fixture-model" || !body.Stream || len(body.Tools) == 0 {
					t.Error("original streaming/tool request missing")
				}
				if engine == "radiance" {
					if body.ReasoningEffort != "none" || body.TemplateKwargs != nil {
						t.Error("Radiance effort must be top-level")
					}
				} else {
					if body.ReasoningEffort != "" || body.TemplateKwargs["reasoning_effort"] != "none" {
						t.Error("Paiton original kwargs changed")
					}
				}
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				if calls == 1 {
					io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"fixture-call","type":"function","function":{"name":"mem_search","arguments":"{\"query\":\"fixture-missing-memory\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
				} else {
					found := false
					for _, m := range body.Messages {
						if m.Role == "tool" {
							found = true
						}
					}
					if !found {
						t.Error("original tool result not returned to engine")
					}
					io.WriteString(w, sseChunk("streamed answer")+"data: [DONE]\n\n")
				}
			}))
			defer srv.Close()
			req := externalSaveReq{Engine: engine, Name: engine, URL: srv.URL, Model: "fixture-model", Key: "private-test-token", KeyTouched: true, Ctx: "131072", Effort: connectionEffort("none")}
			id, err := saveEngineConnection(req)
			if err != nil {
				t.Fatal(err)
			}
			if err = selectEngineConnection(context.Background(), filepath.Join(presetsDir(), id+".env")); err != nil {
				t.Fatal(err)
			}
			var content strings.Builder
			tools := 0
			_, err = runChat(context.Background(), []Message{{Role: "user", Content: "search fixture memory"}}, 0.7, Caps{Mem: MemOnDemand}, func(ev StreamEvent) bool {
				content.WriteString(ev.Content)
				if ev.ToolUsed != nil {
					tools++
				}
				return true
			}, nil)
			if err != nil || calls != 2 || content.String() != "streamed answer" || tools == 0 {
				t.Fatalf("original pipeline: error=%v calls=%d content=%q tool events=%d", err, calls, content.String(), tools)
			}
		})
	}
}

func TestEngineConnectionEffortWireFormat(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  map[string]string
		top  bool
	}{
		{"radiance", map[string]string{extKeyFlag: "1", externalEngineKey: "radiance", "REASONING_EFFORT": "medium"}, true},
		{"paiton", map[string]string{extKeyFlag: "1", externalEngineKey: "paiton", "REASONING_EFFORT": "medium"}, false},
		{"generic-external", map[string]string{extKeyFlag: "1", "REASONING_EFFORT": "medium"}, false},
		{"local", map[string]string{externalEngineKey: "radiance", "REASONING_EFFORT": "medium"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := map[string]any{}
			applySamplingFrom(p, tc.cfg)
			if tc.top {
				if p["reasoning_effort"] != "medium" || p["chat_template_kwargs"] != nil {
					t.Fatal(p)
				}
			} else {
				if p["reasoning_effort"] != nil || p["chat_template_kwargs"].(map[string]any)["reasoning_effort"] != "medium" {
					t.Fatal(p)
				}
			}
		})
	}
	p := map[string]any{}
	applySamplingFrom(p, map[string]string{extKeyFlag: "1", externalEngineKey: "radiance"})
	if len(p) != 0 {
		t.Fatal("server default was overridden", p)
	}
}
func TestEngineConnectionRadianceEffortCapabilityAndComposer(t *testing.T) {
	testHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"id":"model","capabilities":{"reasoning_efforts":["none","minimal","xhigh"]}}]}`)
	}))
	defer srv.Close()
	req := externalSaveReq{Engine: "radiance", Name: "reasoning", URL: srv.URL, Model: "model", Effort: connectionEffort("none")}
	id, err := saveEngineConnection(req)
	if err != nil {
		t.Fatal(err)
	}
	req.ID = id
	// Omitted optional effort preserves a saved value; explicit empty clears it.
	req.Effort = nil
	if _, err = saveEngineConnection(req); err != nil {
		t.Fatal(err)
	}
	raw, _ := ReadPreset(id)
	if parseEnv(raw)["REASONING_EFFORT"] != "none" {
		t.Fatal("omitted effort lost")
	}
	path := filepath.Join(presetsDir(), id+".env")
	if err = selectEngineConnection(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if ReadConfig()["EXTERNAL_REASONING_EFFORTS"] != "none,minimal,xhigh" {
		t.Fatal("capability snapshot missing")
	}
	for _, tc := range []struct {
		value string
		code  int
	}{{"minimal", 200}, {"none", 200}, {"high", 400}, {"low", 400}, {"", 200}} {
		rr := httptest.NewRecorder()
		handleReasoning(rr, httptest.NewRequest("POST", "/api/reasoning", strings.NewReader(`{"effort":"`+tc.value+`"}`)))
		if rr.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.value, rr.Code, rr.Body.String())
		}
	}
	before := ReadConfig()
	req.Effort = connectionEffort("high")
	if _, err = saveEngineConnection(req); err != nil {
		t.Fatal(err)
	}
	if err = selectEngineConnection(context.Background(), path); err == nil || !reflect.DeepEqual(before, ReadConfig()) {
		t.Fatal("undeclared high accepted")
	}
	req.Effort = connectionEffort("")
	if _, err = saveEngineConnection(req); err != nil {
		t.Fatal(err)
	}
	raw, _ = ReadPreset(id)
	if parseEnv(raw)["REASONING_EFFORT"] != "" {
		t.Fatal("explicit default not saved")
	}
}

func TestEngineConnectionOriginalCompactorEffort(t *testing.T) {
	for _, tc := range []struct {
		engine, caps string
		want         bool
	}{{"radiance", "none,minimal,xhigh", true}, {"radiance", "low,xhigh", false}, {"paiton", "none", false}, {"", "none", false}} {
		t.Run(tc.engine+tc.caps, func(t *testing.T) {
			testHome(t)
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/chat/completions" || r.Method != "POST" {
					t.Error("unexpected compactor route")
				}
				var p map[string]any
				if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
					t.Error(err)
				}
				if p["stream"] != false || p["model"] != "summary-model" || p["tools"] != nil || p["chat_template_kwargs"] != nil {
					t.Error("original external summary payload changed")
				}
				if tc.want {
					if p["reasoning_effort"] != "none" {
						t.Error("declared none not requested")
					}
				} else if p["reasoning_effort"] != nil {
					t.Error("unsupported compactor effort sent")
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"Preserved fixture facts."}}]}`)
			}))
			defer srv.Close()
			if err := WriteConfig(map[string]string{extKeyFlag: "1", extKeyURL: srv.URL, extKeyModel: "summary-model", externalEngineKey: tc.engine, "EXTERNAL_REASONING_EFFORTS": tc.caps, "REASONING_EFFORT": "xhigh"}); err != nil {
				t.Fatal(err)
			}
			summary, err := summarizeTranscript(context.Background(), "Fixture conversation findings.")
			if err != nil || summary != "Preserved fixture facts." || calls != 1 {
				t.Fatal(summary, err, calls)
			}
		})
	}
}
