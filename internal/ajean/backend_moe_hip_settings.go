package ajean

// Settings preparation is a file transaction only. Strata 0.1.41's parser
// accepts fp16/int8 KV (generate.cpp 1687,2171), with explicit HIP int8 handling
// at4077. --spec0 disables MTP; the only offered active MTP recipe is3. This is
// source compatibility, never a claim that a saved variant passed GPU testing.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var moeHIPNumericListPattern = regexp.MustCompile(`^[0-9]+(,[0-9]+)*$`)
var moeHIPQuantPattern = regexp.MustCompile(`(?i)(UD-IQ4_XS|UD-Q4_K_XL|IQ3_XXS|IQ2_XS|IQ3_S|Q2_0)`)

func moeHIPQuant(label string) string { return strings.ToUpper(moeHIPQuantPattern.FindString(label)) }
func moeHIPArgValue(base map[string]any, flag string) string {
	args, _ := base["args"].([]any)
	for i, v := range args {
		if v == flag && i+1 < len(args) {
			return fmt.Sprint(args[i+1])
		}
	}
	return ""
}
func moeHIPSettingsMetadata(recipe moeHIPRecipe, base map[string]any) map[string]any {
	quant := moeHIPQuant(filepath.Base(moeHIPArgValue(base, "--native")))
	if quant == "" {
		quant = moeHIPQuant(recipe.Label)
	}
	spec, _ := strconv.Atoi(moeHIPArgValue(base, "--spec"))
	kv := moeHIPArgValue(base, "--kv")
	if kv == "" {
		kv = "fp16"
	}
	env, _ := base["env"].(map[string]any)
	details := map[string]any{"ctx": recipe.Context, "kv": kv, "spec": spec, "kv_resident": moeHIPArgValue(base, "--kv-resident"), "gpu": moeHIPArgValue(base, "--gpu"), "reserve_mib": moeHIPArgValue(base, "--vram-reserve-mib"), "layer_split": moeHIPArgValue(base, "--layer-split"), "concurrency": 1, "vision": false, "has_helper": false, "drop": false, "mmap": fmt.Sprint(env["STRATA_ARENA_MMAP"]) == "1"}
	if details["gpu"] == "" {
		if gpu, ok := base["gpu"]; ok {
			data, _ := json.Marshal(gpu)
			details["gpu"] = string(data)
		}
	}
	// Expose device ordinals and scalar settings only, never arbitrary text
	// from imported argv that could contain an operator's private paths.
	for _, key := range []string{"gpu", "reserve_mib", "kv_resident", "layer_split"} {
		value := fmt.Sprint(details[key])
		if key == "gpu" {
			value = strings.Trim(value, "[]")
		}
		if value != "auto" && !moeHIPNumericListPattern.MatchString(value) {
			value = ""
		}
		details[key] = value
	}
	if kv != "fp16" && kv != "int8" && kv != "q4_0" && kv != "k8v4" {
		details["kv"] = "unknown"
	}
	settings := map[string]any{"kv": []string{"fp16", "int8"}, "spec": []int{0}}
	if mtp := moeHIPArgValue(base, "--mtp"); mtp != "" && moeHIPPath(mtp, true, false) == nil {
		settings["spec"] = []int{0, 3}
	}
	max := recipe.MaxContext
	if max == 0 {
		max = recipe.Context
	}
	max = min(max, 131072)
	minctx := 32768
	if resident, e := strconv.Atoi(moeHIPArgValue(base, "--kv-resident")); e == nil && resident > minctx {
		minctx = ((resident + 4095) / 4096) * 4096
	}
	if max >= minctx {
		settings["ctx"] = map[string]int{"min": minctx, "max": max, "step": 4096}
	}
	return map[string]any{"derived_from": recipe.DerivedFrom, "quant": quant, "details": details, "settings_supported": settings, "settings_note": "Prepared settings only; source-compatible fp16/int8 and MTP0/3, pending native GPU qualification. Vision, helper, device placement and reserve remain read-only."}
}

type moeHIPSettingsRequest struct {
	ID   string  `json:"id"`
	Ctx  *int    `json:"ctx,omitempty"`
	KV   *string `json:"kv,omitempty"`
	Spec *int    `json:"spec,omitempty"`
}

func moeHIPSaveSettings(req moeHIPSettingsRequest) (moeHIPRecipe, string, error) {
	moeHIPInstallMu.Lock()
	defer moeHIPInstallMu.Unlock()
	var zero moeHIPRecipe
	recipes, err := moeHIPReadCatalog()
	if err != nil {
		return zero, "", err
	}
	var source moeHIPRecipe
	found := false
	for _, r := range recipes {
		if r.ID == req.ID {
			source = r
			found = true
			break
		}
	}
	if !found {
		return zero, "", fmt.Errorf("HIP operator recipe not found")
	}
	rootRecipe := source
	if source.DerivedFrom != "" {
		rootFound := false
		for _, r := range recipes {
			if r.ID == source.DerivedFrom {
				rootRecipe = r
				rootFound = true
				break
			}
		}
		if !rootFound {
			return zero, "", fmt.Errorf("HIP base recipe missing")
		}
		if _, _, e := moeHIPPreflight(moeHIPRecipePreset(rootRecipe)); e != nil {
			return zero, "", e
		}
		if rootRecipe.EngineSHA != source.EngineSHA || rootRecipe.SourceManifestSHA != source.SourceManifestSHA {
			return zero, "", fmt.Errorf("HIP base engine/source identity differs")
		}
	}
	source.MaxContext = rootRecipe.Context
	_, base, err := moeHIPPreflight(moeHIPRecipePreset(source))
	if err != nil {
		return zero, "", err
	}
	raw, err := json.Marshal(base)
	if err != nil {
		return zero, "", err
	}
	var derived map[string]any
	if err = json.Unmarshal(raw, &derived); err != nil {
		return zero, "", err
	}
	args := []string{}
	for _, v := range derived["args"].([]any) {
		args = append(args, v.(string))
	}
	metadata := moeHIPSettingsMetadata(source, base)
	supported := metadata["settings_supported"].(map[string]any)
	ctx := source.Context
	if req.Ctx != nil {
		bounds, ok := supported["ctx"].(map[string]int)
		if !ok || *req.Ctx < bounds["min"] || *req.Ctx > bounds["max"] || *req.Ctx%bounds["step"] != 0 {
			return zero, "", fmt.Errorf("HIP context outside imported recipe's supported bounds")
		}
		ctx = *req.Ctx
		args = setArg(args, "--max-context", strconv.Itoa(ctx))
	}
	if req.KV != nil {
		if *req.KV != "fp16" && *req.KV != "int8" {
			return zero, "", fmt.Errorf("HIP KV must be fp16 or int8")
		}
		args = setArg(args, "--kv", *req.KV)
	}
	if req.Spec != nil {
		allowed := false
		for _, v := range supported["spec"].([]int) {
			if *req.Spec == v {
				allowed = true
			}
		}
		if !allowed {
			return zero, "", fmt.Errorf("HIP MTP must be0, or3 with an existing MTP asset")
		}
		args = setArg(args, "--spec", strconv.Itoa(*req.Spec))
	}
	list := make([]any, len(args))
	for i, v := range args {
		list[i] = v
	}
	derived["args"] = list
	data, err := json.MarshalIndent(derived, "", " ")
	if err != nil {
		return zero, "", err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	id := rootRecipe.ID
	if len(id) > 60 {
		id = id[:60]
	}
	id += "-" + digest[:12]
	recipe := source
	recipe.DerivedFrom = rootRecipe.ID
	recipe.MaxContext = rootRecipe.Context
	recipe.ID = id
	recipe.Context = ctx
	recipe.ConfigSHA = digest
	recipe.Config = filepath.Join(filepath.Dir(source.Config), "settings-"+digest+".json")
	kv := moeHIPArgValue(derived, "--kv")
	spec := moeHIPArgValue(derived, "--spec")
	recipe.Label = rootRecipe.Label + " · " + strconv.Itoa(ctx) + " " + kv + " MTP" + spec
	// Keep variants next to their base: managed bundle receipts still apply to
	// unchanged runtime/assets, while the new config has its own exact digest.
	newlyCreated := false
	staged, err := os.CreateTemp(filepath.Dir(recipe.Config), ".hip-settings-")
	if err != nil {
		return zero, "", err
	}
	tmp := staged.Name()
	defer os.Remove(tmp)
	if _, err = staged.Write(data); err != nil {
		staged.Close()
		return zero, "", err
	}
	if err = staged.Close(); err != nil {
		return zero, "", err
	}
	if err = os.Link(tmp, recipe.Config); err != nil {
		if !os.IsExist(err) {
			return zero, "", err
		}
		existing, e := os.ReadFile(recipe.Config)
		if e != nil || string(existing) != string(data) {
			return zero, "", fmt.Errorf("existing HIP settings differ")
		}
	} else {
		newlyCreated = true
	}
	committed := false
	defer func() {
		if !committed && newlyCreated {
			os.Remove(recipe.Config)
		}
	}()
	if _, _, err = moeHIPPreflight(moeHIPRecipePreset(recipe)); err != nil {
		return zero, "", err
	}
	body := moeHIPPresetText(recipe)
	presetSum := sha256.Sum256([]byte(body))
	preset := "AMD HIP " + recipe.ID + " " + hex.EncodeToString(presetSum[:4])
	exists := false
	for _, r := range recipes {
		if r.ID == recipe.ID {
			if r.ConfigSHA != recipe.ConfigSHA || r.SourceManifestSHA != recipe.SourceManifestSHA || r.EngineSHA != recipe.EngineSHA {
				return zero, "", fmt.Errorf("existing HIP derived recipe differs")
			}
			exists = true
		}
	}
	if !exists {
		file, e := os.CreateTemp(filepath.Dir(moeHIPCatalogPath()), ".hip-catalogue-")
		if e != nil {
			return zero, "", e
		}
		catalogTmp := file.Name()
		file.Close()
		defer os.Remove(catalogTmp)
		catalogBytes, e := json.MarshalIndent(map[string]any{"recipes": append(recipes, recipe)}, "", " ")
		if e != nil {
			return zero, "", e
		}
		if len(catalogBytes) > 65536 {
			return zero, "", fmt.Errorf("HIP catalogue size limit reached; remove unused prepared variants before saving more")
		}
		if err = os.WriteFile(catalogTmp, catalogBytes, 0600); err != nil {
			return zero, "", err
		}
		if err = os.Rename(catalogTmp, moeHIPCatalogPath()); err != nil {
			return zero, "", err
		}
	}
	// Runtime config and catalogue commit first. If final preset publication
	// fails, keep the committed recipe so a retry can converge safely.
	committed = true
	presetPath := filepath.Join(presetsDir(), preset+".env")
	if err = moeHIPWritePreset(presetPath, body); err != nil {
		return zero, "", err
	}
	return recipe, preset, nil
}
func handleMoeHIPSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]any{"ok": false, "error": "POST attendu"})
		return
	}
	var req moeHIPSettingsRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "invalid HIP settings request"})
		return
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "invalid HIP settings request"})
		return
	}
	recipe, preset, err := moeHIPSaveSettings(req)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "id": recipe.ID, "preset": preset, "started": false, "activated": false, "proof_state": "prepared"})
}
