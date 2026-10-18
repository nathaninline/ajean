package ajean

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func hipFixture(t *testing.T) (map[string]any, map[string]string) {
	t.Helper()
	d := t.TempDir()
	mkdir := func(p string) string {
		p = filepath.Join(d, p)
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	file := func(p string, mode os.FileMode) string {
		p = filepath.Join(d, p)
		if err := os.WriteFile(p, []byte("CPU fixture; never execute"), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	src := mkdir("source")
	mkdir("source/serve")
	file("source/serve/server.py", 0600)
	tok := mkdir("tokenizer")
	file("tokenizer/vocab.json", 0600)
	file("tokenizer/merges.txt", 0600)
	file("tokenizer/token_type.json", 0600)
	exe := file("engine", 0700)
	python := file("python", 0700)
	sum, err := sha256File(exe)
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"backend": "hip", "exe": exe, "cwd": d, "tokenizer": tok, "model_name": "fixture", "parallel": float64(1), "args": []any{"--pack", mkdir("pack"), "--native", file("native.gguf", 0600), "--max-context", "131072", "--kv", "fp16", "--spec", "3", "--mtp", mkdir("mtp")}, "env": map[string]any{"STRATA_ARENA_MMAP": "1"}, "lib_dirs": []any{mkdir("lib")}}
	cfg := map[string]string{"ENGINE": "moe", "MOE_BACKEND": "hip", "MOE_CONFIG": filepath.Join(d, "config.json"), "MOE_SOURCE_DIR": src, "MOE_PYTHON": python, "MOE_ENGINE_SHA256": sum, "CTX": "131072", "MOE_VISION": "0"}
	manifestPath := filepath.Join(d, "source-manifest.json")
	serverSum, e := sha256File(filepath.Join(src, "serve", "server.py"))
	if e != nil {
		t.Fatal(e)
	}
	body, e := json.Marshal(map[string]any{"files": map[string]string{"serve/server.py": serverSum}})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(manifestPath, body, 0600); e != nil {
		t.Fatal(e)
	}
	manifestSum, e := sha256File(manifestPath)
	if e != nil {
		t.Fatal(e)
	}
	cfg["MOE_SOURCE_MANIFEST"], cfg["MOE_SOURCE_MANIFEST_SHA256"] = manifestPath, manifestSum
	return base, cfg
}

func writeHIPFixture(t *testing.T, base map[string]any, cfg map[string]string) {
	t.Helper()
	raw, e := json.Marshal(base)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(cfg["MOE_CONFIG"], raw, 0600); e != nil {
		t.Fatal(e)
	}
	sum, e := sha256File(cfg["MOE_CONFIG"])
	if e != nil {
		t.Fatal(e)
	}
	cfg["MOE_CONFIG_SHA256"] = sum
}

func TestMoeHIPImportedConfigKeepsRecipe(t *testing.T) {
	base, cfg := hipFixture(t)
	writeHIPFixture(t, base, cfg)
	path, got, err := moeHIPPreflight(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if path != cfg["MOE_CONFIG"] {
		t.Fatal(path)
	}
	out, err := moeHIPBuildConfig(got, cfg, "private-key")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"args", "env", "lib_dirs", "exe", "cwd", "tokenizer"} {
		if !reflect.DeepEqual(out[k], got[k]) {
			t.Fatalf("recipe changed: %s", k)
		}
	}
	if out["host"] != "127.0.0.1" || out["api_key"] != "private-key" {
		t.Fatal("host/key")
	}
	out["args"].([]any)[0] = "changed"
	if got["args"].([]any)[0] != "--pack" {
		t.Fatal("input mutated")
	}
}

func TestMoeHIPRejectsUnsafeImports(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any, map[string]string)
	}{
		{"backend", func(b map[string]any, c map[string]string) { b["backend"] = "cuda" }},
		{"preset-backend", func(b map[string]any, c map[string]string) { c["MOE_BACKEND"] = "" }},
		{"hash", func(b map[string]any, c map[string]string) { c["MOE_ENGINE_SHA256"] = strings.Repeat("0", 64) }},
		{"missing-hash", func(b map[string]any, c map[string]string) { delete(c, "MOE_ENGINE_SHA256") }},
		{"context", func(b map[string]any, c map[string]string) { b["args"].([]any)[5] = "262144" }},
		{"context-override", func(b map[string]any, c map[string]string) { c["CTX"] = "32768" }},
		{"parallel", func(b map[string]any, c map[string]string) { b["parallel"] = 2 }},
		{"cuda-flag", func(b map[string]any, c map[string]string) { b["args"] = append(b["args"].([]any), "--cuda", "12") }},
		{"equals-cuda-flag", func(b map[string]any, c map[string]string) { b["args"] = append(b["args"].([]any), "--cuda=12") }},
		{"batch", func(b map[string]any, c map[string]string) { b["args"] = append(b["args"].([]any), "--batch", "2") }},
		{"vision", func(b map[string]any, c map[string]string) { b["vision"] = map[string]any{"gpu": true} }},
		{"helper", func(b map[string]any, c map[string]string) { c["MOE_HELPER_GPU"] = "1" }},
		{"public-bind", func(b map[string]any, c map[string]string) { c["HOST"] = "0.0.0.0" }},
		{"port", func(b map[string]any, c map[string]string) { c["PORT"] = "99999" }},
		{"python", func(b map[string]any, c map[string]string) { c["MOE_PYTHON"] = "python3" }},
		{"missing-server", func(b map[string]any, c map[string]string) { c["MOE_SOURCE_DIR"] = t.TempDir() }},
		{"relative-exe", func(b map[string]any, c map[string]string) { b["exe"] = "engine" }},
		{"missing-tokenizer", func(b map[string]any, c map[string]string) { b["tokenizer"] = t.TempDir() }},
		{"cuda-env", func(b map[string]any, c map[string]string) { b["env"] = map[string]any{"CUDA_VISIBLE_DEVICES": "0"} }},
		{"remap-env", func(b map[string]any, c map[string]string) { b["env"] = map[string]any{"HIP_VISIBLE_DEVICES": "0"} }},
		{"hook", func(b map[string]any, c map[string]string) { b["before_load"] = "run something" }},
		{"bad-lib", func(b map[string]any, c map[string]string) { b["lib_dirs"] = []any{"relative"} }},
		{"missing-pack", func(b map[string]any, c map[string]string) {
			b["args"].([]any)[1] = filepath.Join(t.TempDir(), "absent")
		}},
		{"duplicate-context", func(b map[string]any, c map[string]string) {
			b["args"] = append(b["args"].([]any), "--max-context", "131072")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, c := hipFixture(t)
			tc.mutate(b, c)
			writeHIPFixture(t, b, c)
			if _, _, err := moeHIPPreflight(c); err == nil {
				t.Fatal("unsafe import accepted")
			}
		})
	}
}

func TestMoeHIPIntegrityAndTokenizer(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, map[string]any, map[string]string)
	}{
		{"config-drift", func(t *testing.T, b map[string]any, c map[string]string) {
			f, e := os.OpenFile(c["MOE_CONFIG"], os.O_APPEND|os.O_WRONLY, 0600)
			if e != nil {
				t.Fatal(e)
			}
			_, e = f.WriteString("\n")
			f.Close()
			if e != nil {
				t.Fatal(e)
			}
		}},
		{"config-digest-required", func(t *testing.T, b map[string]any, c map[string]string) { delete(c, "MOE_CONFIG_SHA256") }},
		{"source-drift", func(t *testing.T, b map[string]any, c map[string]string) {
			if e := os.WriteFile(filepath.Join(c["MOE_SOURCE_DIR"], "serve", "server.py"), []byte("drift"), 0600); e != nil {
				t.Fatal(e)
			}
		}},
		{"manifest-drift", func(t *testing.T, b map[string]any, c map[string]string) {
			if e := os.WriteFile(c["MOE_SOURCE_MANIFEST"], []byte("{}"), 0600); e != nil {
				t.Fatal(e)
			}
		}},
		{"manifest-digest-required", func(t *testing.T, b map[string]any, c map[string]string) { delete(c, "MOE_SOURCE_MANIFEST_SHA256") }},
		{"manifest-absolute", func(t *testing.T, b map[string]any, c map[string]string) { c["MOE_SOURCE_MANIFEST"] = "manifest.json" }},
		{"unlisted-root-py", func(t *testing.T, b map[string]any, c map[string]string) {
			if e := os.WriteFile(filepath.Join(c["MOE_SOURCE_DIR"], "extra.py"), []byte(""), 0600); e != nil {
				t.Fatal(e)
			}
		}},
		{"unlisted-nested-so", func(t *testing.T, b map[string]any, c map[string]string) {
			d := filepath.Join(c["MOE_SOURCE_DIR"], "tools", "nested")
			if e := os.MkdirAll(d, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(d, "extra.so"), []byte(""), 0600); e != nil {
				t.Fatal(e)
			}
		}},
		{"unlisted-nested-pyd", func(t *testing.T, b map[string]any, c map[string]string) {
			if e := os.WriteFile(filepath.Join(c["MOE_SOURCE_DIR"], "serve", "extra.pyd"), []byte(""), 0600); e != nil {
				t.Fatal(e)
			}
		}},
		{"missing-merges", func(t *testing.T, b map[string]any, c map[string]string) {
			if e := os.Remove(filepath.Join(b["tokenizer"].(string), "merges.txt")); e != nil {
				t.Fatal(e)
			}
		}},
		{"missing-token-type", func(t *testing.T, b map[string]any, c map[string]string) {
			if e := os.Remove(filepath.Join(b["tokenizer"].(string), "token_type.json")); e != nil {
				t.Fatal(e)
			}
		}},

		{"unsupported-spec2", func(t *testing.T, b map[string]any, c map[string]string) {
			b["args"].([]any)[9] = "2"
			writeHIPFixture(t, b, c)
		}},
		{"equals-spec-bypass", func(t *testing.T, b map[string]any, c map[string]string) {
			b["args"] = append(b["args"].([]any), "--spec=3")
			writeHIPFixture(t, b, c)
		}},
		{"missing-mtp-spec3", func(t *testing.T, b map[string]any, c map[string]string) {
			b["args"] = b["args"].([]any)[:10]
			writeHIPFixture(t, b, c)
		}},
		{"source-link-escape", func(t *testing.T, b map[string]any, c map[string]string) {
			if e := os.Symlink(c["MOE_PYTHON"], filepath.Join(c["MOE_SOURCE_DIR"], "serve", "extra.py")); e != nil {
				t.Fatal(e)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, c := hipFixture(t)
			writeHIPFixture(t, b, c)
			tc.mutate(t, b, c)
			if _, _, e := moeHIPPreflight(c); e == nil {
				t.Fatal("integrity violation accepted")
			}
		})
	}
}

func TestMoeHIPSourceManifestPaths(t *testing.T) {
	for _, rel := range []string{"../python", "/absolute", "serve/../serve/server.py", "serve\\server.py", "."} {
		t.Run(rel, func(t *testing.T) {
			b, c := hipFixture(t)
			writeHIPFixture(t, b, c)
			body, e := json.Marshal(map[string]any{"files": map[string]string{rel: strings.Repeat("0", 64)}})
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(c["MOE_SOURCE_MANIFEST"], body, 0600); e != nil {
				t.Fatal(e)
			}
			sum, e := sha256File(c["MOE_SOURCE_MANIFEST"])
			if e != nil {
				t.Fatal(e)
			}
			c["MOE_SOURCE_MANIFEST_SHA256"] = sum
			if _, _, e = moeHIPPreflight(c); e == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
}
