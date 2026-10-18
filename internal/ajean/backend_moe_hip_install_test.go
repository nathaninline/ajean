package ajean

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func hipInstallFixture(t *testing.T) (moeHIPInstallEntry, moeHIPInstallManifest) {
	t.Helper()
	testHome(t)
	b, c := hipFixture(t)
	doc := moeHIPInstallManifest{Schema: 1, SourceDir: "source", Python: "runtime/python", Context: 131072}
	add := func(source, target, mode string) {
		sum, e := sha256File(source)
		if e != nil {
			t.Fatal(e)
		}
		doc.Files = append(doc.Files, moeHIPInstallFile{Source: source, Target: target, SHA: sum, Mode: mode})
	}
	add(filepath.Join(c["MOE_SOURCE_DIR"], "serve", "server.py"), "source/serve/server.py", "0600")
	add(c["MOE_PYTHON"], "runtime/python", "0700")
	add(b["exe"].(string), "engine/strata", "0700")
	for _, name := range []string{"vocab.json", "merges.txt", "token_type.json"} {
		add(filepath.Join(b["tokenizer"].(string), name), "tokenizer/"+name, "0600")
	}
	marker := filepath.Join(t.TempDir(), "marker")
	if e := os.WriteFile(marker, []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, target := range []string{"pack/marker", "mtp/marker", "libs/marker"} {
		add(marker, target, "0600")
	}
	native := b["args"].([]any)[3].(string)
	if e := os.Chmod(native, 0400); e != nil {
		t.Fatal(e)
	}
	sum, e := sha256File(native)
	if e != nil {
		t.Fatal(e)
	}
	doc.Refs = []moeHIPInstallFile{{Source: native, Target: "models/native.gguf", SHA: sum, ReadOnly: true}}
	doc.Config = map[string]any{"backend": "hip", "exe": "${INSTALL_ROOT}/engine/strata", "cwd": "${INSTALL_ROOT}", "tokenizer": "${INSTALL_ROOT}/tokenizer", "model_name": "fixture", "parallel": 1, "lib_dirs": []any{"${INSTALL_ROOT}/libs"}, "args": []any{"--pack", "${INSTALL_ROOT}/pack", "--native", "${INSTALL_ROOT}/models/native.gguf", "--max-context", "131072", "--spec", "3", "--mtp", "${INSTALL_ROOT}/mtp"}}
	entry := moeHIPInstallEntry{ID: "swift-cpu-fixture", Family: "swift", Label: "Swift fixture", Manifest: filepath.Join(t.TempDir(), "bundle.json")}
	hipInstallWriteManifest(t, &entry, doc)
	return entry, doc
}
func hipInstallWriteManifest(t *testing.T, entry *moeHIPInstallEntry, doc moeHIPInstallManifest) {
	t.Helper()
	if e := moeHIPInstallWriteJSON(entry.Manifest, doc); e != nil {
		t.Fatal(e)
	}
	sum, e := sha256File(entry.Manifest)
	if e != nil {
		t.Fatal(e)
	}
	entry.ManifestSHA = sum
}
func TestMoeHIPInstallTransaction(t *testing.T) {
	entry, _ := hipInstallFixture(t)
	if e := WriteConfig(map[string]string{"ENGINE": "moe", "MOE_BACKEND": "cuda", "MOE_MAIN_GPU": "0", "MOE_CONFIG": "existing-nvidia.json", "MODEL": "unchanged.gguf"}); e != nil {
		t.Fatal(e)
	}
	before := ReadConfig()
	recipe, preset, e := moeHIPInstallLocal(context.Background(), entry)
	if e != nil {
		t.Fatal(e)
	}
	if preset == "" || recipe.ID != entry.ID {
		t.Fatal("missing recipe/preset")
	}
	if !reflect.DeepEqual(before, ReadConfig()) {
		t.Fatal("active NVIDIA/llama configuration changed")
	}
	if _, _, e = moeHIPPreflight(moeHIPRecipePreset(recipe)); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{recipe.Config, recipe.SourceManifest, filepath.Join(presetsDir(), preset+".env")} {
		st, e := os.Stat(p)
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatalf("private file %s: %v", p, e)
		}
	}
	if _, e = os.Readlink(filepath.Join(filepath.Dir(recipe.Config), "models", "native.gguf")); e != nil {
		t.Fatal(e)
	}
	// Copied source is independent of the bundle's mutable source files.
	var manifest moeHIPInstallManifest
	if e = moeHIPInstallReadJSON(entry.Manifest, &manifest); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(manifest.Files[0].Source, []byte("source changed after installation"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = moeHIPPreflight(moeHIPRecipePreset(recipe)); e != nil {
		t.Fatal("installed source depended on bundle source", e)
	}
	if again, againPreset, err := moeHIPInstallLocal(context.Background(), entry); err != nil || !reflect.DeepEqual(again, recipe) || againPreset != preset {
		t.Fatal("exact retry failed", err)
	}
	got, e := moeHIPReadCatalog()
	if e != nil || len(got) != 1 {
		t.Fatal("catalogue", got, e)
	}
}
func TestMoeHIPInstallRejectsAndRollsBack(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*moeHIPInstallEntry, *moeHIPInstallManifest)
	}{
		{"file-digest", func(e *moeHIPInstallEntry, d *moeHIPInstallManifest) { d.Files[0].SHA = strings.Repeat("0", 64) }},
		{"target-escape", func(e *moeHIPInstallEntry, d *moeHIPInstallManifest) { d.Files[0].Target = "../escape" }},
		{"target-absolute", func(e *moeHIPInstallEntry, d *moeHIPInstallManifest) { d.Files[0].Target = "/tmp/escape" }},
		{"duplicate", func(e *moeHIPInstallEntry, d *moeHIPInstallManifest) { d.Files = append(d.Files, d.Files[0]) }},
		{"source-escape", func(e *moeHIPInstallEntry, d *moeHIPInstallManifest) { d.SourceDir = "../source" }},

		{"reference-digest", func(e *moeHIPInstallEntry, d *moeHIPInstallManifest) { d.Refs[0].SHA = strings.Repeat("0", 64) }},
		{"reference-unapproved", func(e *moeHIPInstallEntry, d *moeHIPInstallManifest) { d.Refs[0].ReadOnly = false }},
		{"reference-code", func(e *moeHIPInstallEntry, d *moeHIPInstallManifest) { d.Refs[0].Target = "extra.py" }},
		{"reference-source", func(e *moeHIPInstallEntry, d *moeHIPInstallManifest) { d.Refs[0].Target = "source/weights.gguf" }},
		{"bad-placeholder", func(e *moeHIPInstallEntry, d *moeHIPInstallManifest) { d.Config["cwd"] = "${SOURCE_ROOT}" }},
		{"bad-config-preflight", func(e *moeHIPInstallEntry, d *moeHIPInstallManifest) { d.Config["backend"] = "cuda" }},
		{"bad-mode", func(e *moeHIPInstallEntry, d *moeHIPInstallManifest) { d.Files[0].Mode = "0777" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry, doc := hipInstallFixture(t)
			tc.mutate(&entry, &doc)
			hipInstallWriteManifest(t, &entry, doc)
			if _, _, e := moeHIPInstallLocal(context.Background(), entry); e == nil {
				t.Fatal("unsafe bundle accepted")
			}
			contents, e := os.ReadDir(filepath.Join(AjeanHome(), "moe-hip", "installations"))
			if e != nil && !os.IsNotExist(e) {
				t.Fatal(e)
			}
			if len(contents) != 0 {
				t.Fatal("partial installation leaked")
			}
			recipes, e := moeHIPReadCatalog()
			if e != nil || len(recipes) != 0 {
				t.Fatal("failed transaction published catalogue")
			}
		})
	}
}
func TestMoeHIPInstallPinnedManifestAndCancel(t *testing.T) {
	entry, _ := hipInstallFixture(t)
	entry.ManifestSHA = strings.Repeat("0", 64)
	if _, _, e := moeHIPInstallLocal(context.Background(), entry); e == nil {
		t.Fatal("manifest drift accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e := moeHIPInstallLocal(ctx, entry); e != context.Canceled {
		t.Fatal(e)
	}
	// The stream used for copies and weight digest scans obeys cancellation.
	r := moeHIPInstallReader{ctx: ctx, r: strings.NewReader("fixture")}
	if _, e := r.Read(make([]byte, 1)); e != context.Canceled {
		t.Fatal(e)
	}
}
func TestMoeHIPInstallCatalogueIDsOnly(t *testing.T) {
	entry, _ := hipInstallFixture(t)
	if e := os.MkdirAll(filepath.Dir(moeHIPInstallCatalogPath()), 0700); e != nil {
		t.Fatal(e)
	}
	if e := moeHIPInstallWriteJSON(moeHIPInstallCatalogPath(), map[string]any{"entries": []moeHIPInstallEntry{entry}}); e != nil {
		t.Fatal(e)
	}
	views := moeHIPInstallViews()
	raw, e := json.Marshal(views)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), entry.Manifest) || len(views) != 1 {
		t.Fatal("private paths exposed")
	}
}

func hipInstallWaitJob(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		lcMu.Lock()
		running := lcCur != nil && lcCur.Running
		lcMu.Unlock()
		if !running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("CPU install job did not finish")
}
func TestMoeHIPInstallCancelAndHookLifetime(t *testing.T) {
	entry, _ := hipInstallFixture(t)
	if e := os.MkdirAll(filepath.Dir(moeHIPInstallCatalogPath()), 0700); e != nil {
		t.Fatal(e)
	}
	if e := moeHIPInstallWriteJSON(moeHIPInstallCatalogPath(), map[string]any{"entries": []moeHIPInstallEntry{entry}}); e != nil {
		t.Fatal(e)
	}
	// Hold the worker at the transaction boundary, cancel through the public
	// hook, then allow it to see cancellation before it copies anything.
	moeHIPInstallMu.Lock()
	rr := httptest.NewRecorder()
	handleMoeHIPInstall(rr, httptest.NewRequest("POST", "/api/moe/hip/install", strings.NewReader(`{"id":"`+entry.ID+`"}`)))
	if rr.Code != 202 {
		moeHIPInstallMu.Unlock()
		t.Fatal(rr.Code, rr.Body.String())
	}
	canceled := moeHIPCancelInstall()
	moeHIPInstallMu.Unlock()
	if !canceled {
		t.Fatal("cancel hook was not published")
	}
	hipInstallWaitJob(t)
	if moeHIPCancelInstall() {
		t.Fatal("completed job left a stale cancellation hook")
	}
	// Repeated fast completions previously exposed the worker/handler race.
	for i := 0; i < 10; i++ {
		rr = httptest.NewRecorder()
		handleMoeHIPInstall(rr, httptest.NewRequest("POST", "/api/moe/hip/install", strings.NewReader(`{"id":"`+entry.ID+`"}`)))
		if rr.Code != 202 {
			t.Fatal(rr.Code, rr.Body.String())
		}
		hipInstallWaitJob(t)
		if moeHIPCancelInstall() {
			t.Fatal("fast job left stale cancellation hook")
		}
	}
}
func TestMoeHIPInstallWritableReferenceIntegrity(t *testing.T) {
	entry, doc := hipInstallFixture(t)
	origin := doc.Refs[0].Source
	if e := os.Chmod(origin, 0600); e != nil {
		t.Fatal(e)
	}
	recipe, _, e := moeHIPInstallLocal(context.Background(), entry)
	if e != nil {
		t.Fatal(e)
	}
	st, e := os.Stat(origin)
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("operator data permissions changed", e)
	}
	cfg := moeHIPRecipePreset(recipe)
	if e = moeHIPInstallVerifyReferences(cfg); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(origin, []byte("operator changed weights"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = moeHIPInstallVerifyReferences(cfg); e == nil {
		t.Fatal("weight drift accepted")
	}
}

func TestMoeHIPInstallAPIRejectsBrowserPaths(t *testing.T) {
	entry, _ := hipInstallFixture(t)
	if e := os.MkdirAll(filepath.Dir(moeHIPInstallCatalogPath()), 0700); e != nil {
		t.Fatal(e)
	}
	if e := moeHIPInstallWriteJSON(moeHIPInstallCatalogPath(), map[string]any{"entries": []moeHIPInstallEntry{entry}}); e != nil {
		t.Fatal(e)
	}
	for _, body := range []string{`{"id":"` + entry.ID + `","manifest":"/tmp/browser-supplied.json"}`, `{"id":"` + entry.ID + `"} {"id":"second"}`} {
		rr := httptest.NewRecorder()
		handleMoeHIPInstall(rr, httptest.NewRequest("POST", "/api/moe/hip/install", strings.NewReader(body)))
		if rr.Code != 400 {
			t.Fatal(rr.Code, rr.Body.String())
		}
	}
	rr := httptest.NewRecorder()
	handleMoeHIPInstall(rr, httptest.NewRequest("POST", "/api/moe/hip/install", strings.NewReader(`{"id":"not-approved"}`)))
	if rr.Code != 404 {
		t.Fatal(rr.Code)
	}
}

// Pause at the real cancellation boundary immediately before catalogue commit,
// after the complete preset has been staged outside the discoverable namespace.
type hipPresetBoundaryContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *hipPresetBoundaryContext) Err() error {
	files, _ := filepath.Glob(filepath.Join(presetsDir(), ".hip-preset-*"))
	if len(files) > 0 {
		c.once.Do(func() { close(c.entered); <-c.release })
	}
	return c.Context.Err()
}
func TestMoeHIPInstallPresetPublicationBoundary(t *testing.T) {
	for _, mode := range []string{"success", "cancel-before-commit", "publish-conflict"} {
		t.Run(mode, func(t *testing.T) {
			entry, _ := hipInstallFixture(t)
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &hipPresetBoundaryContext{Context: base, entered: make(chan struct{}), release: make(chan struct{})}
			type outcome struct {
				recipe moeHIPRecipe
				preset string
				err    error
			}
			done := make(chan outcome, 1)
			go func() { r, p, e := moeHIPInstallLocal(ctx, entry); done <- outcome{r, p, e} }()
			select {
			case <-ctx.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("publication boundary not reached")
			}
			released := false
			defer func() {
				if !released {
					close(ctx.release)
					<-done
				}
			}()
			staged, err := filepath.Glob(filepath.Join(presetsDir(), ".hip-preset-*"))
			if err != nil || len(staged) != 1 {
				t.Fatal("missing staged preset", err)
			}
			body, err := os.ReadFile(staged[0])
			if err != nil {
				t.Fatal(err)
			}
			cfg := parseEnv(string(body))
			if cfg["MOE_BACKEND"] != "hip" || cfg["MOE_CONFIG"] == "" {
				t.Fatal("staged preset incomplete")
			}
			// Read the same public listing used by the UI, repeatedly while the worker
			// is held at this boundary. The staged file must never be selectable.
			for i := 0; i < 20; i++ {
				presets, e := ListPresets()
				if e != nil || len(presets) != 0 {
					t.Fatal("uncommitted preset visible", presets, e)
				}
			}
			if _, err = os.Stat(moeHIPCatalogPath()); !os.IsNotExist(err) {
				t.Fatal("catalogue committed too early", err)
			}
			destination := filepath.Dir(cfg["MOE_CONFIG"])
			sum := sha256.Sum256(body)
			presetID := "AMD HIP " + entry.ID + " " + hex.EncodeToString(sum[:4])
			target := filepath.Join(presetsDir(), presetID+".env")
			if mode == "cancel-before-commit" {
				cancel()
			}
			if mode == "publish-conflict" {
				if err = os.WriteFile(target, []byte("existing operator preset"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			observed := make(chan error, 1)
			if mode == "success" {
				go func() {
					deadline := time.After(5 * time.Second)
					tick := time.NewTicker(time.Millisecond)
					defer tick.Stop()
					for {
						presets, e := ListPresets()
						if e != nil {
							observed <- e
							return
						}
						if len(presets) > 0 {
							data, e := os.ReadFile(presets[0].Path)
							if e != nil || string(data) != string(body) {
								observed <- fmt.Errorf("concurrent reader saw partial preset: %v", e)
								return
							}
							recipes, e := moeHIPReadCatalog()
							if e != nil || len(recipes) != 1 {
								observed <- fmt.Errorf("preset visible before catalogue commit: %v", e)
								return
							}
							_, _, e = moeHIPPreflight(moeHIPRecipePreset(recipes[0]))
							observed <- e
							return
						}
						select {
						case <-tick.C:
						case <-deadline:
							observed <- fmt.Errorf("preset never became visible")
							return
						}
					}
				}()
			}
			close(ctx.release)
			released = true
			var result outcome
			select {
			case result = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("installer did not finish")
			}
			if mode == "cancel-before-commit" {
				if !errors.Is(result.err, context.Canceled) {
					t.Fatal("cancellation ignored", result.err)
				}
				if _, err = os.Stat(destination); !os.IsNotExist(err) {
					t.Fatal("cancelled installation retained", err)
				}
				if presets, e := ListPresets(); e != nil || len(presets) != 0 {
					t.Fatal("cancelled preset visible", presets, e)
				}
				if _, err = os.Stat(moeHIPCatalogPath()); !os.IsNotExist(err) {
					t.Fatal("cancelled catalogue visible", err)
				}
				return
			}
			if mode == "publish-conflict" {
				if result.err == nil {
					t.Fatal("publication conflict accepted")
				}
				existing, _ := os.ReadFile(target)
				if string(existing) != "existing operator preset" {
					t.Fatal("operator preset overwritten")
				}
				// Catalogue visibility is a commit: failure afterward must not roll back
				// the installation that prepare/switch could already have discovered.
				recipes, e := moeHIPReadCatalog()
				if e != nil || len(recipes) != 1 {
					t.Fatal("committed catalogue lost", e)
				}
				if _, _, e = moeHIPPreflight(moeHIPRecipePreset(recipes[0])); e != nil {
					t.Fatal("visible installation removed", e)
				}
				if err = os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if _, _, err = moeHIPInstallLocal(context.Background(), entry); err != nil {
					t.Fatal("publication retry failed", err)
				}
			} else {
				if err := <-observed; err != nil {
					t.Fatal(err)
				}
				if result.err != nil {
					t.Fatal(result.err)
				}
			}
			presets, e := ListPresets()
			if e != nil || len(presets) != 1 {
				t.Fatal("completed preset missing", presets, e)
			}
			got, e := os.ReadFile(presets[0].Path)
			if e != nil || string(got) != string(body) {
				t.Fatal("published preset is partial", e)
			}
			if files, _ := filepath.Glob(filepath.Join(presetsDir(), ".hip-preset-*")); len(files) != 0 {
				t.Fatal("staged preset leaked")
			}
		})
	}
}

func TestMoeHIPInstallCatalogueLimitPreservesExisting(t *testing.T) {
	entry, _ := hipInstallFixture(t)
	existing := hipCatalogFixture(t)
	existing.Label = ""
	base, e := json.MarshalIndent(map[string]any{"recipes": []moeHIPRecipe{existing}}, "", " ")
	if e != nil {
		t.Fatal(e)
	}
	existing.Label = strings.Repeat("x", 65500-len(base))
	before, e := json.MarshalIndent(map[string]any{"recipes": []moeHIPRecipe{existing}}, "", " ")
	if e != nil || len(before) != 65500 {
		t.Fatal("invalid capacity fixture", len(before), e)
	}
	if e = os.WriteFile(moeHIPCatalogPath(), before, 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = moeHIPInstallLocal(context.Background(), entry); e == nil || !strings.Contains(e.Error(), "catalogue size limit") {
		t.Fatal("oversized installed catalogue accepted", e)
	}
	after, e := os.ReadFile(moeHIPCatalogPath())
	if e != nil || string(after) != string(before) {
		t.Fatal("existing catalogue changed", e)
	}
	if _, e = moeHIPReadCatalog(); e != nil {
		t.Fatal("existing catalogue became unreadable", e)
	}
	installs, e := os.ReadDir(filepath.Join(AjeanHome(), "moe-hip", "installations"))
	if e != nil || len(installs) != 0 {
		t.Fatal("uncommitted installation left behind", e)
	}
}
