package ajean

// Local, pinned bundle installation. Nothing here executes the imported Python
// or engine, downloads weights, compiles, activates a preset, or touches CUDA.
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
	"strings"
	"sync"
)

type moeHIPInstallEntry struct {
	ID          string `json:"id"`
	Family      string `json:"family"`
	Label       string `json:"label"`
	Manifest    string `json:"manifest"`
	ManifestSHA string `json:"manifest_sha256"`
}
type moeHIPInstallFile struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	SHA      string `json:"sha256"`
	Mode     string `json:"mode,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
}
type moeHIPInstallManifest struct {
	Schema    int                 `json:"schema"`
	Files     []moeHIPInstallFile `json:"files"`
	Refs      []moeHIPInstallFile `json:"refs,omitempty"`
	Config    map[string]any      `json:"config_template"`
	SourceDir string              `json:"source_dir"`
	Python    string              `json:"python"`
	Context   int                 `json:"context"`
}

var moeHIPInstallMu sync.Mutex
var moeHIPInstallCancelMu sync.Mutex
var moeHIPInstallCancel context.CancelFunc

func moeHIPInstallCatalogPath() string {
	return filepath.Join(AjeanHome(), "moe-hip", "install-catalogue.json")
}
func moeHIPInstallID(id string) bool {
	if id == "" || len(id) > 80 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func moeHIPInstallRelative(p string) bool {
	return p != "" && p != "." && !filepath.IsAbs(p) && filepath.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../") && !strings.ContainsAny(p, "\\\x00\r\n")
}
func moeHIPInstallReadJSON(path string, out any) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("absolute local manifest path required")
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 16*1024*1024+1))
	if e != nil {
		return e
	}
	if len(raw) > 16*1024*1024 {
		return fmt.Errorf("HIP document too large")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if e = d.Decode(out); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("trailing HIP JSON")
	}
	return nil
}
func moeHIPReadInstallCatalog() ([]moeHIPInstallEntry, error) {
	var doc struct {
		Entries []moeHIPInstallEntry `json:"entries"`
	}
	if e := moeHIPInstallReadJSON(moeHIPInstallCatalogPath(), &doc); os.IsNotExist(e) {
		return nil, nil
	} else if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	for _, v := range doc.Entries {
		if !moeHIPInstallID(v.ID) || seen[v.ID] || (v.Family != "swift" && v.Family != "qwen") || v.Label == "" || strings.ContainsAny(v.Label, "\r\n\x00") || !filepath.IsAbs(v.Manifest) {
			return nil, fmt.Errorf("invalid HIP install entry")
		}
		if b, e := hex.DecodeString(v.ManifestSHA); e != nil || len(b) != 32 {
			return nil, fmt.Errorf("invalid HIP manifest digest")
		}
		seen[v.ID] = true
	}
	return doc.Entries, nil
}

// The browser sees operator-defined IDs and labels only, never local paths.
func moeHIPInstallViews() []map[string]any {
	entries, e := moeHIPReadInstallCatalog()
	if e != nil {
		return nil
	}
	out := []map[string]any{}
	for _, v := range entries {
		out = append(out, map[string]any{"id": v.ID, "family": v.Family, "label": v.Label, "proof_state": "prepared"})
	}
	return out
}

type moeHIPInstallReader struct {
	ctx context.Context
	r   io.Reader
}

func (r moeHIPInstallReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func moeHIPInstallDigest(ctx context.Context, path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, moeHIPInstallReader{ctx, f}); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func moeHIPInstallCopy(ctx context.Context, src, dst, want string, mode os.FileMode) error {
	if e := moeHIPPath(src, false, false); e != nil {
		return e
	}
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	if e = os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
		return e
	}
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if e != nil {
		return e
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, h), moeHIPInstallReader{ctx, in})
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(want) {
		return fmt.Errorf("HIP asset SHA256 mismatch: %s", filepath.Base(src))
	}
	return nil
}
func moeHIPInstallWriteJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", " ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, b, 0600)
}

func moeHIPInstallLocal(ctx context.Context, entry moeHIPInstallEntry) (moeHIPRecipe, string, error) {
	moeHIPInstallMu.Lock()
	defer moeHIPInstallMu.Unlock()
	var zero moeHIPRecipe
	if e := ctx.Err(); e != nil {
		return zero, "", e
	}
	if !moeHIPInstallID(entry.ID) || (entry.Family != "swift" && entry.Family != "qwen") || entry.Label == "" || strings.ContainsAny(entry.Label, "\r\n\x00") {
		return zero, "", fmt.Errorf("invalid HIP install identity")
	}
	if e := moeHIPPath(entry.Manifest, false, false); e != nil {
		return zero, "", e
	}
	st, e := os.Stat(entry.Manifest)
	if e != nil {
		return zero, "", e
	}
	if st.Size() > 16*1024*1024 {
		return zero, "", fmt.Errorf("HIP manifest too large")
	}
	raw, e := os.ReadFile(entry.Manifest)
	if e != nil {
		return zero, "", e
	}
	if len(raw) > 16*1024*1024 {
		return zero, "", fmt.Errorf("HIP manifest too large")
	}
	if e = moeHIPVerifyBytes(raw, entry.ManifestSHA, "install manifest"); e != nil {
		return zero, "", e
	}
	var doc moeHIPInstallManifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&doc); e != nil {
		return zero, "", e
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return zero, "", fmt.Errorf("trailing install manifest JSON")
	}
	if doc.Schema != 1 || !moeHIPInstallRelative(doc.SourceDir) || !moeHIPInstallRelative(doc.Python) || doc.Context < 4096 || doc.Context > 131072 || len(doc.Files) == 0 {
		return zero, "", fmt.Errorf("invalid HIP install manifest schema/runtime")
	}
	root := filepath.Join(AjeanHome(), "moe-hip", "installations")
	if e = os.MkdirAll(root, 0700); e != nil {
		return zero, "", e
	}
	// Exact completed installations are repeatable; partial/foreign directories
	// are never replaced, repaired in place, or reused.
	destination := filepath.Join(root, entry.ID+"-"+strings.ToLower(entry.ManifestSHA))
	if st, err := os.Lstat(destination); err == nil {
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return zero, "", fmt.Errorf("HIP installation path is not a private directory")
		}
		return moeHIPInstallExisting(ctx, entry, destination)
	} else if !os.IsNotExist(err) {
		return zero, "", err
	}
	staging, e := os.MkdirTemp(root, ".staging-")
	if e != nil {
		return zero, "", e
	}
	committed := false
	published := false
	defer func() {
		os.RemoveAll(staging)
		if !committed {
			if published {
				os.RemoveAll(destination)
			}
		}
	}()
	seen := map[string]bool{"config.json": true, "source-manifest.json": true, "bundle-manifest.json": true}
	for _, group := range [][]moeHIPInstallFile{doc.Files, doc.Refs} {
		for _, a := range group {
			if !moeHIPInstallRelative(a.Target) || seen[a.Target] {
				return zero, "", fmt.Errorf("unsafe or duplicate HIP asset target")
			}
			seen[a.Target] = true
			for p := filepath.Dir(a.Target); p != "."; p = filepath.Dir(p) {
				if seen[p] {
					return zero, "", fmt.Errorf("HIP asset target overlaps file")
				}
			}
			if !filepath.IsAbs(a.Source) || strings.ContainsAny(a.Source, "\r\n\x00") {
				return zero, "", fmt.Errorf("HIP local absolute asset required")
			}
			if b, e := hex.DecodeString(a.SHA); e != nil || len(b) != 32 {
				return zero, "", fmt.Errorf("HIP asset digest required")
			}
		}
	}
	for _, a := range doc.Files {
		if e = ctx.Err(); e != nil {
			return zero, "", e
		}
		mode := os.FileMode(0600)
		if a.Mode == "0700" {
			mode = 0700
		} else if a.Mode != "" && a.Mode != "0600" {
			return zero, "", fmt.Errorf("HIP asset mode must be0600/0700")
		}
		if e = moeHIPInstallCopy(ctx, a.Source, filepath.Join(staging, a.Target), a.SHA, mode); e != nil {
			return zero, "", e
		}
	}
	for _, a := range doc.Refs {
		if e = ctx.Err(); e != nil {
			return zero, "", e
		}
		if !a.ReadOnly {
			return zero, "", fmt.Errorf("HIP external asset reference must be declared read_only")
		}
		st, err := os.Stat(a.Source)
		if err != nil {
			return zero, "", err
		}
		if !st.Mode().IsRegular() {
			return zero, "", fmt.Errorf("HIP external reference must be a regular file")
		}
		// No external code/tokenizers: only explicitly approved model data references.
		for _, ext := range []string{".py", ".pyc", ".so", ".pyd"} {
			if strings.HasSuffix(strings.ToLower(a.Target), ext) {
				return zero, "", fmt.Errorf("HIP external code references forbidden")
			}
		}
		if a.Target == doc.Python || a.Target == doc.SourceDir || strings.HasPrefix(a.Target, doc.SourceDir+string(filepath.Separator)) {
			return zero, "", fmt.Errorf("HIP source/runtime must be installed locally")
		}
		got, err := moeHIPInstallDigest(ctx, a.Source)
		if err != nil {
			return zero, "", err
		}
		if got != strings.ToLower(a.SHA) {
			return zero, "", fmt.Errorf("HIP reference SHA256 mismatch")
		}
		dst := filepath.Join(staging, a.Target)
		if e = os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
			return zero, "", e
		}
		// read_only describes this installer's access: it never changes or
		// chmods an operator's data. The digest is rechecked before launch.
		if e = os.Symlink(a.Source, dst); e != nil {
			return zero, "", e
		}

	}
	// Template substitution is literal and recursive through JSON, so quoted
	// paths stay JSON strings. Unknown placeholders fail closed.
	configBytes, e := json.Marshal(doc.Config)
	if e != nil {
		return zero, "", e
	}
	configBytes = []byte(strings.ReplaceAll(string(configBytes), "${INSTALL_ROOT}", stringMustJSON(destination)[1:len(stringMustJSON(destination))-1]))
	if strings.Contains(string(configBytes), "${") {
		return zero, "", fmt.Errorf("unknown HIP configuration placeholder")
	}
	var config map[string]any
	if e = json.Unmarshal(configBytes, &config); e != nil {
		return zero, "", e
	}
	for _, key := range []string{"exe", "cwd", "tokenizer"} {
		p, ok := config[key].(string)
		if !ok || (p != destination && !strings.HasPrefix(p, destination+string(filepath.Separator))) || filepath.Clean(p) != p {
			return zero, "", fmt.Errorf("HIP %s must live inside installation", key)
		}
	}
	if libs, ok := config["lib_dirs"].([]any); ok {
		for _, v := range libs {
			p, ok := v.(string)
			if !ok || !strings.HasPrefix(p, destination+string(filepath.Separator)) || filepath.Clean(p) != p {
				return zero, "", fmt.Errorf("HIP runtime libraries must be installed locally")
			}
		}
	}
	if e = moeHIPInstallWriteJSON(filepath.Join(staging, "config.json"), config); e != nil {
		return zero, "", e
	}
	if e = os.WriteFile(filepath.Join(staging, "bundle-manifest.json"), raw, 0600); e != nil {
		return zero, "", e
	}
	sourceManifest := map[string]string{}
	sourcePrefix := doc.SourceDir + string(filepath.Separator)
	for _, a := range doc.Files {
		if strings.HasPrefix(a.Target, sourcePrefix) {
			sourceManifest[strings.TrimPrefix(a.Target, sourcePrefix)] = strings.ToLower(a.SHA)
		}
	}
	if e = moeHIPInstallWriteJSON(filepath.Join(staging, "source-manifest.json"), map[string]any{"files": sourceManifest}); e != nil {
		return zero, "", e
	}
	if e = ctx.Err(); e != nil {
		return zero, "", e
	}
	if e = os.Rename(staging, destination); e != nil {
		return zero, "", e
	}
	published = true
	recipe := moeHIPRecipe{ID: entry.ID, Family: entry.Family, Label: entry.Label, Config: filepath.Join(destination, "config.json"), Source: filepath.Join(destination, doc.SourceDir), Python: filepath.Join(destination, doc.Python), Context: doc.Context, SourceManifest: filepath.Join(destination, "source-manifest.json")}
	exe, ok := config["exe"].(string)
	if !ok {
		return zero, "", fmt.Errorf("HIP config exe required")
	}
	if !strings.HasPrefix(exe, destination+string(filepath.Separator)) {
		return zero, "", fmt.Errorf("HIP executable must be installed locally")
	}
	if recipe.EngineSHA, e = moeHIPInstallDigest(ctx, exe); e != nil {
		return zero, "", e
	}
	if recipe.ConfigSHA, e = sha256File(recipe.Config); e != nil {
		return zero, "", e
	}
	if recipe.SourceManifestSHA, e = sha256File(recipe.SourceManifest); e != nil {
		return zero, "", e
	}
	if _, _, e = moeHIPPreflight(moeHIPRecipePreset(recipe)); e != nil {
		return zero, "", e
	}
	existing, e := moeHIPReadCatalog()
	if e != nil {
		return zero, "", e
	}
	for _, r := range existing {
		if r.ID == entry.ID {
			return zero, "", fmt.Errorf("HIP recipe ID already exists")
		}
	}
	if e = ctx.Err(); e != nil {
		return zero, "", e
	}
	body := moeHIPPresetText(recipe)
	sum := sha256.Sum256([]byte(body))
	presetID := "AMD HIP " + recipe.ID + " " + hex.EncodeToString(sum[:4])
	if e = os.MkdirAll(presetsDir(), 0700); e != nil {
		return zero, "", e
	}
	p := filepath.Join(presetsDir(), presetID+".env")
	presetTemp, err := moeHIPStagePreset(body)
	if err != nil {
		return zero, "", err
	}
	defer os.Remove(presetTemp)
	catalog := moeHIPCatalogPath()
	if e = os.MkdirAll(filepath.Dir(catalog), 0700); e != nil {
		return zero, "", e
	}
	tmp, err := os.CreateTemp(filepath.Dir(catalog), ".catalogue-")
	if err != nil {
		return zero, "", err
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)
	catalogBytes, e := json.MarshalIndent(map[string]any{"recipes": append(existing, recipe)}, "", " ")
	if e != nil {
		return zero, "", e
	}
	if len(catalogBytes) > 65536 {
		return zero, "", fmt.Errorf("HIP catalogue size limit reached; remove unused prepared variants before installing more")
	}
	if e = os.WriteFile(tmpName, catalogBytes, 0600); e != nil {
		return zero, "", e
	}
	if e = ctx.Err(); e != nil {
		return zero, "", e
	}
	if e = os.Rename(tmpName, catalog); e != nil {
		return zero, "", e
	}
	// The catalogue now exposes a fully verified installation. From this point
	// onward it must survive cancellation or a failure to publish the preset.
	// Replay can finish preset publication; no cancellation check follows commit.
	committed = true
	if e = moeHIPPublishPreset(presetTemp, p, body); e != nil {
		return zero, "", e
	}
	return recipe, presetID, nil
}

// Idempotent replay only succeeds when the committed receipt, all pinned
// source/config/executable checks, and the prepared preset still agree.
func moeHIPInstallExisting(ctx context.Context, entry moeHIPInstallEntry, destination string) (moeHIPRecipe, string, error) {
	var zero moeHIPRecipe
	raw, e := os.ReadFile(filepath.Join(destination, "bundle-manifest.json"))
	if e != nil {
		return zero, "", fmt.Errorf("incomplete HIP installation receipt: %w", e)
	}
	if e = moeHIPVerifyBytes(raw, entry.ManifestSHA, "installed bundle manifest"); e != nil {
		return zero, "", e
	}
	recipes, e := moeHIPReadCatalog()
	if e != nil {
		return zero, "", e
	}
	for _, r := range recipes {
		if r.ID != entry.ID {
			continue
		}
		if r.Config != filepath.Join(destination, "config.json") || r.Family != entry.Family || r.Label != entry.Label {
			return zero, "", fmt.Errorf("existing HIP recipe differs")
		}
		if e = moeHIPInstallVerifyReferencesContext(ctx, moeHIPRecipePreset(r)); e != nil {
			return zero, "", e
		}
		if _, _, e = moeHIPPreflight(moeHIPRecipePreset(r)); e != nil {
			return zero, "", e
		}
		if e = ctx.Err(); e != nil {
			return zero, "", e
		}
		body := moeHIPPresetText(r)
		sum := sha256.Sum256([]byte(body))
		preset := "AMD HIP " + r.ID + " " + hex.EncodeToString(sum[:4])
		// A prior commit may have succeeded before preset publication failed.
		// Retry completes publication atomically without replacing any preset.
		if e = moeHIPWritePreset(filepath.Join(presetsDir(), preset+".env"), body); e != nil {
			return zero, "", e
		}
		return r, preset, nil
	}
	return zero, "", fmt.Errorf("incomplete HIP installation has no committed recipe")
}

// Native launch and explicit offline audit call this before any imported
// code runs. Mutable operator-owned weight files remain read-only to AJEAN;
// every file and reference is streamed through its pinned digest again.
func moeHIPInstallVerifyReferences(cfg map[string]string) error {
	return moeHIPInstallVerifyReferencesContext(context.Background(), cfg)
}
func moeHIPInstallVerifyReferencesContext(ctx context.Context, cfg map[string]string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	root := filepath.Join(AjeanHome(), "moe-hip", "installations")
	dir := filepath.Dir(cfg["MOE_CONFIG"])
	rel, e := filepath.Rel(root, dir)
	if e != nil {
		return e
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil
	} // An independently imported installation.
	if rel == "." || strings.Contains(rel, string(filepath.Separator)) || len(rel) < 65 {
		return fmt.Errorf("invalid managed HIP installation path")
	}
	digest := rel[len(rel)-64:]
	receipt := filepath.Join(dir, "bundle-manifest.json")
	st, e := os.Stat(receipt)
	if e != nil {
		return e
	}
	if st.Size() > 16*1024*1024 {
		return fmt.Errorf("HIP receipt too large")
	}
	raw, e := os.ReadFile(receipt)
	if e != nil {
		return e
	}
	if e = moeHIPVerifyBytes(raw, digest, "installed bundle manifest"); e != nil {
		return e
	}
	var doc moeHIPInstallManifest
	if e = json.Unmarshal(raw, &doc); e != nil {
		return e
	}
	if doc.Schema != 1 {
		return fmt.Errorf("invalid installed HIP receipt schema")
	}
	for _, group := range [][]moeHIPInstallFile{doc.Files, doc.Refs} {
		for _, asset := range group {
			if !moeHIPInstallRelative(asset.Target) {
				return fmt.Errorf("unsafe installed HIP target")
			}
			target := filepath.Join(dir, asset.Target)
			if e = moeHIPPath(target, false, false); e != nil {
				return e
			}
			got, err := moeHIPInstallDigest(ctx, target)
			if err != nil {
				return err
			}
			if got != strings.ToLower(asset.SHA) {
				return fmt.Errorf("installed HIP asset digest mismatch: %s", asset.Target)
			}
		}
	}
	return nil
}

func stringMustJSON(s string) string { b, _ := json.Marshal(s); return string(b) }

func moeHIPCancelInstall() bool {
	moeHIPInstallCancelMu.Lock()
	defer moeHIPInstallCancelMu.Unlock()
	if moeHIPInstallCancel == nil {
		return false
	}
	moeHIPInstallCancel()
	return true
}
func handleMoeHIPInstall(w http.ResponseWriter, r *http.Request) {
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
		sendJSON(w, 400, map[string]any{"ok": false, "error": "identifiant HIP requis"})
		return
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "requête HIP invalide"})
		return
	}
	entries, e := moeHIPReadInstallCatalog()
	if e != nil {
		sendJSON(w, 503, map[string]any{"ok": false, "error": "catalogue installation HIP indisponible"})
		return
	}
	for _, entry := range entries {
		if entry.ID != req.ID {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		// Prevent a fast worker from clearing the cancel hook before publication.
		moeHIPInstallCancelMu.Lock()
		e = startLcJob("moe-hip-install", func() {
			defer cancel()
			defer func() { moeHIPInstallCancelMu.Lock(); moeHIPInstallCancel = nil; moeHIPInstallCancelMu.Unlock() }()
			lcPhase("vérification et installation du bundle HIP local")
			_, preset, err := moeHIPInstallLocal(ctx, entry)
			if err != nil {
				lcFail(err)
				return
			}
			lcDone("HIP installé, preset préparé : " + preset + " (non activé)")
		})
		if e != nil {
			moeHIPInstallCancelMu.Unlock()
			cancel()
			sendJSON(w, 409, map[string]any{"ok": false, "error": e.Error()})
			return
		}
		moeHIPInstallCancel = cancel
		moeHIPInstallCancelMu.Unlock()
		sendJSON(w, 202, map[string]any{"ok": true, "started": false, "job": "moe-hip-install", "activated": false})
		return
	}
	sendJSON(w, 404, map[string]any{"ok": false, "error": "bundle HIP non autorisé"})
}
func handleMoeHIPInstallCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]any{"ok": false})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": moeHIPCancelInstall()})
}

// Stage outside the *.env namespace: ListPresets and switch cannot discover a
// partial preset. The temp file and final link share a filesystem.
func moeHIPStagePreset(body string) (string, error) {
	if err := os.MkdirAll(presetsDir(), 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(presetsDir(), ".hip-preset-")
	if err != nil {
		return "", err
	}
	name := f.Name()
	_, writeErr := io.WriteString(f, body)
	closeErr := f.Close()
	if writeErr != nil {
		os.Remove(name)
		return "", writeErr
	}
	if closeErr != nil {
		os.Remove(name)
		return "", closeErr
	}
	return name, nil
}

// Link atomically publishes the complete file, without replacing an existing
// preset. A concurrent preparation of this exact recipe is also successful.
func moeHIPPublishPreset(staged, target, body string) error {
	if err := os.Link(staged, target); err != nil {
		if os.IsExist(err) {
			existing, readErr := os.ReadFile(target)
			if readErr == nil && string(existing) == body {
				return nil
			}
		}
		return err
	}
	return nil
}
func moeHIPWritePreset(target, body string) error {
	staged, err := moeHIPStagePreset(body)
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	return moeHIPPublishPreset(staged, target, body)
}
