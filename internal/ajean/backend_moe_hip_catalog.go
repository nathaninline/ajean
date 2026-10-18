package ajean

// The operator imports existing, pinned Strata HIP installations. Preparing a
// preset never starts a model, downloads assets or changes an active preset.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type moeHIPRecipe struct {
	DerivedFrom       string `json:"derived_from,omitempty"`
	MaxContext        int    `json:"settings_max_context,omitempty"`
	ID                string `json:"id"`
	Family            string `json:"family"`
	Label             string `json:"label"`
	Config            string `json:"config"`
	Source            string `json:"source"`
	Python            string `json:"python"`
	EngineSHA         string `json:"engine_sha256"`
	Context           int    `json:"context"`
	ConfigSHA         string `json:"config_sha256"`
	SourceManifest    string `json:"source_manifest"`
	SourceManifestSHA string `json:"source_manifest_sha256"`
}

func moeHIPCatalogPath() string { return filepath.Join(AjeanHome(), "moe-hip", "catalogue.json") }
func moeHIPReadCatalog() ([]moeHIPRecipe, error) {
	f, err := os.Open(moeHIPCatalogPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return nil, err
	}
	if len(raw) > 65536 {
		return nil, fmt.Errorf("catalogue HIP trop volumineux")
	}
	var doc struct {
		Recipes []moeHIPRecipe `json:"recipes"`
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("catalogue HIP illisible")
	}
	seen := map[string]bool{}
	for _, e := range doc.Recipes {
		if e.ID == "" || len(e.ID) > 80 || seen[e.ID] || (e.Family != "qwen" && e.Family != "swift") || e.Label == "" || e.Context < 4096 || e.Context > 131072 {
			return nil, fmt.Errorf("recette HIP invalide")
		}
		for _, c := range e.ID {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return nil, fmt.Errorf("identifiant HIP invalide")
			}
		}
		for _, v := range []string{e.Label, e.Config, e.Source, e.Python, e.EngineSHA, e.ConfigSHA, e.SourceManifest, e.SourceManifestSHA} {
			if strings.ContainsAny(v, "\r\n\x00") {
				return nil, fmt.Errorf("valeur HIP invalide")
			}
		}
		if !filepath.IsAbs(e.Config) || !filepath.IsAbs(e.Source) || !filepath.IsAbs(e.Python) || !filepath.IsAbs(e.SourceManifest) {
			return nil, fmt.Errorf("chemins HIP absolus requis")
		}
		if b, err := hex.DecodeString(e.EngineSHA); err != nil || len(b) != 32 {
			return nil, fmt.Errorf("empreinte du moteur HIP invalide")
		}
		for _, digest := range []string{e.ConfigSHA, e.SourceManifestSHA} {
			if b, err := hex.DecodeString(digest); err != nil || len(b) != 32 {
				return nil, fmt.Errorf("empreinte de recette HIP invalide")
			}
		}
		if e.DerivedFrom != "" && !moeHIPInstallID(e.DerivedFrom) {
			return nil, fmt.Errorf("invalid HIP base recipe ID")
		}
		if e.MaxContext != 0 && (e.MaxContext < e.Context || e.MaxContext > 131072) {
			return nil, fmt.Errorf("invalid HIP settings context bound")
		}
		seen[e.ID] = true
	}
	return doc.Recipes, nil
}
func moeHIPRecipePreset(e moeHIPRecipe) map[string]string {
	return map[string]string{"ENGINE": "moe", "MOE_BACKEND": "hip", "MOE_CONFIG": e.Config, "MOE_SOURCE_DIR": e.Source, "MOE_PYTHON": e.Python, "MOE_ENGINE_SHA256": e.EngineSHA, "MOE_CONFIG_SHA256": e.ConfigSHA, "MOE_SOURCE_MANIFEST": e.SourceManifest, "MOE_SOURCE_MANIFEST_SHA256": e.SourceManifestSHA, "MOE_VISION": "0", "MOE_HELPER": "0", "MOE_DROP": "0", "HOST": "127.0.0.1", "CTX": fmt.Sprint(e.Context), "NP": "1"}
}
func moeHIPPresetText(e moeHIPRecipe) string {
	cfg := moeHIPRecipePreset(e)
	keys := []string{"ENGINE", "MOE_BACKEND", "MOE_CONFIG", "MOE_SOURCE_DIR", "MOE_PYTHON", "MOE_ENGINE_SHA256", "MOE_CONFIG_SHA256", "MOE_SOURCE_MANIFEST", "MOE_SOURCE_MANIFEST_SHA256", "MOE_VISION", "MOE_HELPER", "MOE_DROP", "HOST", "CTX", "NP"}
	body := "# NAME=" + e.Label + " · AMD Strata HIP\n"
	for _, key := range keys {
		body += key + "=" + cfg[key] + "\n"
	}
	return body
}
func moeHIPLocalAvailable() bool {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return false
	}
	if recipes, err := moeHIPReadCatalog(); err == nil && len(recipes) > 0 {
		return true
	}
	if entries, err := moeHIPReadInstallCatalog(); err == nil && len(entries) > 0 {
		return true
	}

	return false
}
func moeHIPViews() []map[string]any {
	recipes, err := moeHIPReadCatalog()
	if err != nil {
		return nil
	}
	out := []map[string]any{}
	for _, e := range recipes {
		_, base, err := moeHIPPreflight(moeHIPRecipePreset(e))
		view := map[string]any{"id": e.ID, "family": e.Family, "label": e.Label, "context": e.Context, "concurrency": 1, "ready_to_prepare": err == nil, "proof_state": "prepared"}
		if err == nil {
			for key, value := range moeHIPSettingsMetadata(e, base) {
				view[key] = value
			}
		}
		out = append(out, view)
	}
	return out
}
func handleMoeHIPPrepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]any{"ok": false, "error": "POST attendu"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "recette HIP requise"})
		return
	}
	recipes, err := moeHIPReadCatalog()
	if err != nil {
		sendJSON(w, 503, map[string]any{"ok": false, "error": "catalogue HIP indisponible"})
		return
	}
	for _, e := range recipes {
		if e.ID != req.ID {
			continue
		}
		if _, _, err := moeHIPPreflight(moeHIPRecipePreset(e)); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		body := moeHIPPresetText(e)
		sum := sha256.Sum256([]byte(body))
		id := "AMD HIP " + e.ID + " " + hex.EncodeToString(sum[:4])
		if err := os.MkdirAll(presetsDir(), 0700); err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": "préparation impossible"})
			return
		}
		path := filepath.Join(presetsDir(), id+".env")
		// A changed operator recipe creates a different ID. Never overwrite an
		// existing preset or switch the currently active one during preparation.
		if err := moeHIPWritePreset(path, body); err != nil {
			sendJSON(w, 409, map[string]any{"ok": false, "error": "preset existant ou préparation impossible"})
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true, "preset": id, "started": false, "proof_state": "prepared"})
		return
	}
	sendJSON(w, 404, map[string]any{"ok": false, "error": "recette HIP non autorisée"})
}

// Offline operator audit: no device enumeration, listener, model load or
// service action. It can be run with a task-dedicated AJEAN_HOME.
func cmdMoeHIPCheck(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: ajean moe-hip-check (catalogue dans AJEAN_HOME/moe-hip)")
	}
	recipes, err := moeHIPReadCatalog()
	if err != nil {
		return err
	}
	if len(recipes) == 0 {
		return fmt.Errorf("aucune recette Strata HIP locale déclarée")
	}
	results := []map[string]any{}
	failed := false
	for _, recipe := range recipes {
		_, _, e := moeHIPPreflight(moeHIPRecipePreset(recipe))
		if e == nil {
			e = moeHIPInstallVerifyReferences(moeHIPRecipePreset(recipe))
		}
		entry := map[string]any{"id": recipe.ID, "ready_to_prepare": e == nil, "context": recipe.Context, "concurrency": 1}
		if e != nil {
			entry["error"] = e.Error()
			failed = true
		}
		results = append(results, entry)
	}
	raw, _ := json.Marshal(map[string]any{"backend": "strata-hip", "recipes": results, "service_started": false, "gpu_initialized": false, "proof_state": "prepared"})
	fmt.Println(string(raw))
	if failed {
		return fmt.Errorf("recette HIP non vérifiée")
	}
	return nil
}

// cmdMoeHIPInstall is an offline installer: it never activates a preset or runs
// Python, the engine, a web listener, or a service manager.
func cmdMoeHIPInstall(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ajean moe-hip-install ID")
	}
	entries, err := moeHIPReadInstallCatalog()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.ID != args[0] {
			continue
		}
		recipe, preset, err := moeHIPInstallLocal(context.Background(), entry)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(map[string]any{"id": recipe.ID, "preset": preset, "installed": true, "activated": false, "service_started": false, "gpu_initialized": false})
		if err != nil {
			return err
		}
		fmt.Println(string(raw))
		return nil
	}
	return fmt.Errorf("bundle HIP non autorisé")
}
