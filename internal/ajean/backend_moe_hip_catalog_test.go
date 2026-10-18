package ajean

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func hipCatalogFixture(t *testing.T) moeHIPRecipe {
	t.Helper()
	testHome(t)
	b, c := hipFixture(t)
	writeHIPFixture(t, b, c)
	e := moeHIPRecipe{ID: "swift-iq3", Family: "swift", Label: "Swift Flash Next IQ3_XXS", Config: c["MOE_CONFIG"], Source: c["MOE_SOURCE_DIR"], Python: c["MOE_PYTHON"], EngineSHA: c["MOE_ENGINE_SHA256"], Context: 131072, ConfigSHA: c["MOE_CONFIG_SHA256"], SourceManifest: c["MOE_SOURCE_MANIFEST"], SourceManifestSHA: c["MOE_SOURCE_MANIFEST_SHA256"]}
	if err := os.MkdirAll(filepath.Dir(moeHIPCatalogPath()), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"recipes": []moeHIPRecipe{e}})
	if err := os.WriteFile(moeHIPCatalogPath(), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return e
}
func TestMoeHIPCataloguePrepareDoesNotActivate(t *testing.T) {
	e := hipCatalogFixture(t)
	if err := WriteConfig(map[string]string{"MODEL": "previous.gguf", "ENGINE": "llama"}); err != nil {
		t.Fatal(err)
	}
	before := ReadConfig()
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/moe/hip/prepare", strings.NewReader(`{"id":"`+e.ID+`"}`))
		handleMoeHIPPrepare(rr, req)
		if rr.Code != 200 {
			t.Fatalf("prepare code %d: %s", rr.Code, rr.Body.String())
		}
		var response map[string]any
		json.Unmarshal(rr.Body.Bytes(), &response)
		if response["started"] != false || response["proof_state"] != "prepared" {
			t.Fatal(response)
		}
		path := filepath.Join(presetsDir(), response["preset"].(string)+".env")
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private preset missing")
		}
		raw, _ := os.ReadFile(path)
		cfg := parseEnv(string(raw))
		if cfg["MOE_BACKEND"] != "hip" || cfg["EXTERNAL"] != "" || cfg["ENGINE"] != "moe" {
			t.Fatal(cfg)
		}
		if _, _, err := moePreflight(cfg); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(before, ReadConfig()) {
		t.Fatal("active configuration was changed")
	}
}
func TestMoeHIPCatalogueRejectsHTTPPaths(t *testing.T) {
	hipCatalogFixture(t)
	for _, body := range []string{`{"id":"unknown"}`, `{"id":"swift-iq3","config":"/tmp/other"}`, `{"id":"../x"}`} {
		rr := httptest.NewRecorder()
		handleMoeHIPPrepare(rr, httptest.NewRequest("POST", "/api/moe/hip/prepare", strings.NewReader(body)))
		if rr.Code == 200 {
			t.Fatal("untrusted recipe accepted")
		}
	}
	rr := httptest.NewRecorder()
	handleMoeHIPPrepare(rr, httptest.NewRequest("GET", "/api/moe/hip/prepare", nil))
	if rr.Code != 405 {
		t.Fatal(rr.Code)
	}
}
func TestMoeHIPCatalogueRejectsInvalidRecipe(t *testing.T) {
	e := hipCatalogFixture(t)
	for _, mutate := range []func(*moeHIPRecipe){func(e *moeHIPRecipe) { e.Context = 262144 }, func(e *moeHIPRecipe) { e.Label = "label\nENGINE=cuda" }, func(e *moeHIPRecipe) { e.Source = "relative" }, func(e *moeHIPRecipe) { e.EngineSHA = "bad" }} {
		bad := e
		mutate(&bad)
		raw, _ := json.Marshal(map[string]any{"recipes": []moeHIPRecipe{bad}})
		os.WriteFile(moeHIPCatalogPath(), raw, 0600)
		if _, err := moeHIPReadCatalog(); err == nil {
			t.Fatal("bad catalogue accepted")
		}
	}
}
func TestMoeHIPExecLifecycleHelper(t *testing.T) {
	if os.Getenv("AJEAN_HIP_EXEC_HELPER") != "1" {
		return
	}
	var cfg map[string]string
	raw, err := os.ReadFile(os.Getenv("AJEAN_HIP_CFG_FIXTURE"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	// This must dispatch before the CUDA installer and exec our fake Python,
	// never an actual Strata binary, Python server or model.
	if err := serveMoe(cfg); err != nil {
		t.Fatal(err)
	}
	t.Fatal("exec unexpectedly returned")
}
func TestMoeHIPNativeProcessReplacement(t *testing.T) {
	root := t.TempDir()
	base, cfg := hipFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg["PORT"] = strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()
	record := filepath.Join(root, "exec-record")
	script := "#!/bin/sh\nprintf '%s\\n' \"$$\" \"$@\" > \"$AJEAN_HIP_EXEC_RECORD\"\n"
	if err = os.WriteFile(cfg["MOE_PYTHON"], []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	writeHIPFixture(t, base, cfg)
	config := filepath.Join(root, "preset.json")
	raw, _ := json.Marshal(cfg)
	os.WriteFile(config, raw, 0600)
	cmd := exec.Command(os.Args[0], "-test.run=^TestMoeHIPExecLifecycleHelper$")
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CUDA_VISIBLE_DEVICES=") && !strings.HasPrefix(kv, "ROCR_VISIBLE_DEVICES=") && !strings.HasPrefix(kv, "HIP_VISIBLE_DEVICES=") && !strings.HasPrefix(kv, "GPU_DEVICE_ORDINAL=") && !strings.HasPrefix(kv, "STRATA_BATCH_MTP=") && !strings.HasPrefix(kv, "STRATA_REMOTE_DROP=") && !strings.HasPrefix(kv, "AJEAN_HOME=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "AJEAN_HOME="+filepath.Join(root, "home"), "AJEAN_HIP_EXEC_HELPER=1", "AJEAN_HIP_CFG_FIXTURE="+config, "AJEAN_HIP_EXEC_RECORD="+record)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v %s", err, out)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if lines[0] != fmt.Sprint(cmd.ProcessState.Pid()) {
		t.Fatalf("process was not replaced: %v", lines)
	}
	if lines[1] != filepath.Join(cfg["MOE_SOURCE_DIR"], "serve/server.py") || lines[2] != "--engine" || lines[3] != "strata" {
		t.Fatalf("wrong native server argv: %v", lines)
	}
	configPath := ""
	for i, v := range lines {
		if v == "--config" && i+1 < len(lines) {
			configPath = lines[i+1]
		}
	}
	info, err := os.Stat(configPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private native config not found")
	}
	var final map[string]any
	b, _ := os.ReadFile(configPath)
	json.Unmarshal(b, &final)
	if final["backend"] != "hip" || final["host"] != "127.0.0.1" || final["parallel"] != float64(1) {
		t.Fatal("wrong native HIP config")
	}
	if !reflect.DeepEqual(final["args"], base["args"]) {
		t.Fatal("NVIDIA tuning changed HIP args")
	}
	if _, err = os.Stat(filepath.Join(root, "home/moe/ajean-moe-1.1")); !os.IsNotExist(err) {
		t.Fatal("CUDA engine directory created")
	}
}

func TestMoeHIPExplicitCatalogueSelectsBackendWithoutDeviceCommands(t *testing.T) {
	hipCatalogFixture(t)
	// Empty PATH ensures this CPU test cannot execute a GPU utility. The
	// request selects imports explicitly; a stored catalogue alone does not.
	t.Setenv("PATH", t.TempDir())
	env := moeDetect()
	if env.Backend == "hip" {
		t.Fatal("stored HIP catalogue overrode automatic hardware detection", env)
	}
	rr := httptest.NewRecorder()
	handleMoe(rr, httptest.NewRequest("GET", "/api/moe?backend=hip", nil))
	var data map[string]any
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &data) != nil || data["backend"] != "hip" || data["proof_state"] != "prepared" || data["version"] != Version {
		t.Fatal("incorrect HIP availability metadata", rr.Body.String())
	}
	rr = httptest.NewRecorder()
	handleMoe(rr, httptest.NewRequest("GET", "/api/moe", nil))
	data = nil
	if json.Unmarshal(rr.Body.Bytes(), &data) != nil || data["backend"] == "hip" || data["hip_available"] != true || len(data["families"].([]any)) == 0 {
		t.Fatal("local catalogue hid the standard installation choices", rr.Body.String())
	}
}
