package ajean

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func hipInt(v int) *int          { return &v }
func hipString(v string) *string { return &v }
func TestMoeHIPSettingsSavePinsWithoutActivation(t *testing.T) {
	source := hipCatalogFixture(t)
	original, err := os.ReadFile(source.Config)
	if err != nil {
		t.Fatal(err)
	}
	before := ReadConfig()
	recipe, preset, err := moeHIPSaveSettings(moeHIPSettingsRequest{ID: source.ID, Ctx: hipInt(65536), KV: hipString("int8"), Spec: hipInt(0)})
	if err != nil {
		t.Fatal(err)
	}
	if recipe.DerivedFrom != source.ID || recipe.ID == source.ID || recipe.Context != 65536 || recipe.MaxContext != 131072 {
		t.Fatal(recipe)
	}
	if recipe.SourceManifestSHA != source.SourceManifestSHA || recipe.EngineSHA != source.EngineSHA || recipe.Source != source.Source || recipe.Python != source.Python {
		t.Fatal("source/engine identity changed")
	}
	if !reflect.DeepEqual(before, ReadConfig()) {
		t.Fatal("settings activated model")
	}
	got, err := os.ReadFile(source.Config)
	if err != nil || string(got) != string(original) {
		t.Fatal("base config changed")
	}
	sha, err := sha256File(recipe.Config)
	if err != nil || sha != recipe.ConfigSHA {
		t.Fatal("variant config not pinned")
	}
	cfg, base, err := moeHIPPreflight(moeHIPRecipePreset(recipe))
	if err != nil || cfg != recipe.Config {
		t.Fatal(err)
	}
	if moeHIPArgValue(base, "--kv") != "int8" || moeHIPArgValue(base, "--spec") != "0" || moeHIPArgValue(base, "--max-context") != "65536" {
		t.Fatal(base)
	}
	if st, e := os.Stat(recipe.Config); e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("variant not private", e)
	}
	if body, e := os.ReadFile(filepath.Join(presetsDir(), preset+".env")); e != nil || !strings.Contains(string(body), "MOE_CONFIG_SHA256="+sha) {
		t.Fatal("preset digest missing", e)
	}
	// Save from a variant can restore the original capacity, with a stable base
	// label/ID instead of accumulating suffixes at every edit.
	restored, _, err := moeHIPSaveSettings(moeHIPSettingsRequest{ID: recipe.ID, Ctx: hipInt(131072), KV: hipString("fp16"), Spec: hipInt(3)})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Context != 131072 || restored.DerivedFrom != source.ID || strings.Count(restored.Label, " · ") != 1 {
		t.Fatal(restored)
	}
	again, againPreset, err := moeHIPSaveSettings(moeHIPSettingsRequest{ID: source.ID, Ctx: hipInt(65536), KV: hipString("int8"), Spec: hipInt(0)})
	if err != nil || again.ID != recipe.ID || againPreset != preset {
		t.Fatal("save not repeatable", err)
	}
}
func TestMoeHIPSettingsRejectUnsupportedAndDrift(t *testing.T) {
	for _, req := range []moeHIPSettingsRequest{{Ctx: hipInt(262144)}, {Ctx: hipInt(65537)}, {Ctx: hipInt(0)}, {KV: hipString("q4_0")}, {KV: hipString("invalid")}, {Spec: hipInt(2)}, {Spec: hipInt(4)}} {
		t.Run("reject", func(t *testing.T) {
			source := hipCatalogFixture(t)
			req.ID = source.ID
			if _, _, e := moeHIPSaveSettings(req); e == nil {
				t.Fatal("unsupported settings accepted")
			}
			recipes, e := moeHIPReadCatalog()
			if e != nil || len(recipes) != 1 {
				t.Fatal("invalid settings published")
			}
		})
	}
	source := hipCatalogFixture(t)
	if e := os.WriteFile(source.Config, []byte(`{}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e := moeHIPSaveSettings(moeHIPSettingsRequest{ID: source.ID, Ctx: hipInt(65536)}); e == nil {
		t.Fatal("unpinned source accepted")
	}
}
func TestMoeHIPSettingsCommitBeforePresetAndRetry(t *testing.T) {
	source := hipCatalogFixture(t)
	os.RemoveAll(presetsDir())
	if e := os.WriteFile(presetsDir(), []byte("block publication"), 0600); e != nil {
		t.Fatal(e)
	}
	req := moeHIPSettingsRequest{ID: source.ID, Ctx: hipInt(65536)}
	if _, _, e := moeHIPSaveSettings(req); e == nil {
		t.Fatal("preset publication should have failed")
	}
	recipes, e := moeHIPReadCatalog()
	if e != nil || len(recipes) != 2 {
		t.Fatal("committed recipe missing", e)
	}
	var variant moeHIPRecipe
	for _, r := range recipes {
		if r.DerivedFrom == source.ID {
			variant = r
		}
	}
	if _, _, e = moeHIPPreflight(moeHIPRecipePreset(variant)); e != nil {
		t.Fatal("committed config was removed", e)
	}
	if e = os.Remove(presetsDir()); e != nil {
		t.Fatal(e)
	}
	retried, preset, e := moeHIPSaveSettings(req)
	if e != nil || retried.ID != variant.ID || preset == "" {
		t.Fatal("retry did not converge", e)
	}
	recipes, e = moeHIPReadCatalog()
	if e != nil || len(recipes) != 2 {
		t.Fatal("retry duplicated variant")
	}
}
func TestMoeHIPSettingsMetadataAndAPISchema(t *testing.T) {
	source := hipCatalogFixture(t)
	source.Label = "Swift IQ3_XXS"
	raw, _ := json.Marshal(map[string]any{"recipes": []moeHIPRecipe{source}})
	if e := os.WriteFile(moeHIPCatalogPath(), raw, 0600); e != nil {
		t.Fatal(e)
	}
	views := moeHIPViews()
	if len(views) != 1 || views[0]["quant"] != "IQ3_XXS" || views[0]["settings_supported"] == nil {
		t.Fatal(views)
	}
	raw, _ = json.Marshal(views)
	if strings.Contains(string(raw), source.Source) || strings.Contains(string(raw), source.Config) {
		t.Fatal("private paths leaked")
	}
	for _, body := range []string{`{"id":"` + source.ID + `","vision":true}`, `{"id":"` + source.ID + `","helper":true}`, `{"id":"` + source.ID + `","config":"/tmp/override"}`, `{"id":"` + source.ID + `"} {}`} {
		w := httptest.NewRecorder()
		handleMoeHIPSettings(w, httptest.NewRequest("POST", "/api/moe/hip/settings", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	handleMoeHIPSettings(w, httptest.NewRequest("POST", "/api/moe/hip/settings", strings.NewReader(`{"id":"`+source.ID+`","ctx":65536,"kv":"int8","spec":0}`)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"activated":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestMoeHIPSettingsCatalogueLimitPreservesExisting(t *testing.T) {
	source := hipCatalogFixture(t)
	source.Label = strings.Repeat("x", 60000)
	before, e := json.MarshalIndent(map[string]any{"recipes": []moeHIPRecipe{source}}, "", " ")
	if e != nil || len(before) > 65536 {
		t.Fatal("invalid capacity fixture", e)
	}
	if e = os.WriteFile(moeHIPCatalogPath(), before, 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = moeHIPSaveSettings(moeHIPSettingsRequest{ID: source.ID, Ctx: hipInt(65536)}); e == nil || !strings.Contains(e.Error(), "catalogue size limit") {
		t.Fatal("oversized catalogue accepted", e)
	}
	after, e := os.ReadFile(moeHIPCatalogPath())
	if e != nil || string(after) != string(before) {
		t.Fatal("original catalogue changed", e)
	}
	if _, e = moeHIPReadCatalog(); e != nil {
		t.Fatal("original catalogue became unreadable", e)
	}
	variants, e := filepath.Glob(filepath.Join(filepath.Dir(source.Config), "settings-*.json"))
	if e != nil || len(variants) != 0 {
		t.Fatal("uncommitted variant left behind", e)
	}
}

func TestMoeHIPSettingsMetadataHidesImportedPathValues(t *testing.T) {
	source := hipCatalogFixture(t)
	_, base, e := moeHIPPreflight(moeHIPRecipePreset(source))
	if e != nil {
		t.Fatal(e)
	}
	base["args"] = append(base["args"].([]any), "--gpu", "/private/operator-device", "--vram-reserve-mib", "/private/reserve")
	raw, e := json.Marshal(moeHIPSettingsMetadata(source, base))
	if e != nil || strings.Contains(string(raw), "/private/") {
		t.Fatal("imported path escaped metadata", e)
	}
}
