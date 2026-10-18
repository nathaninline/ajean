package ajean

// Imported Strata HIP installations are explicit and separate from the CUDA
// release installer. This branch never downloads, migrates or probes a GPU.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func moeHIPDir() string { return filepath.Join(moeHome(), "hip") }

func moeHIPPath(p string, dir, executable bool) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("HIP path must be absolute: %q", p)
	}
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("HIP prerequisite %q: %w", p, err)
	}
	if st.IsDir() != dir || (!dir && !st.Mode().IsRegular()) {
		return fmt.Errorf("HIP prerequisite has wrong type: %q", p)
	}
	if executable && st.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("HIP prerequisite is not executable: %q", p)
	}
	return nil
}

// moeHIPBuildConfig keeps the imported engine arguments and HIP environment
// untouched. A changed context requires a separately prepared imported config;
// NVIDIA tuning is deliberately never applied to a HIP preset.
func moeHIPBuildConfig(base map[string]any, cfg map[string]string, key string) (map[string]any, error) {
	if cfg["MOE_BACKEND"] != "hip" || base["backend"] != "hip" {
		return nil, fmt.Errorf("HIP backend must be explicit in preset and imported JSON")
	}
	raw, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	for _, k := range []string{"MOE_HELPER_GPU", "MOE_HELPER", "MOE_DROP", "MOE_SPLIT", "MOE_MAIN_GPU", "MOE_VRAM_RESERVE", "MOE_KV", "MOE_SPEC"} {
		if v := strings.TrimSpace(cfg[k]); v != "" && v != "0" && v != "-1" {
			return nil, fmt.Errorf("HIP imported config does not accept NVIDIA/preset tuning %s", k)
		}
	}
	if v := cfg["MOE_VISION"]; v != "" && v != "0" {
		return nil, fmt.Errorf("HIP first port is text only")
	}
	for _, k := range []string{"vision", "before_load", "mcp_servers"} {
		if v, ok := out[k]; ok && v != nil {
			return nil, fmt.Errorf("HIP initial import forbids %s", k)
		}
	}
	if n, ok := out["parallel"]; ok && n != float64(1) {
		return nil, fmt.Errorf("HIP initial port supports parallel=1 only")
	}
	out["parallel"] = 1
	list, ok := out["args"].([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("HIP args must be a nonempty string array")
	}
	seen := map[string]bool{}
	context := 0
	for i, value := range list {
		a, ok := value.(string)
		if !ok || a == "" {
			return nil, fmt.Errorf("HIP args must contain nonempty strings")
		}
		flag := strings.SplitN(a, "=", 2)[0]
		if strings.HasPrefix(flag, "--") {
			if seen[flag] {
				return nil, fmt.Errorf("duplicate HIP flag %s", flag)
			}
			seen[flag] = true
		}
		if strings.Contains(strings.ToLower(flag), "cuda") || strings.Contains(flag, "remote") || strings.HasPrefix(flag, "--expert-cache-device") || strings.HasPrefix(flag, "--vision") || strings.HasPrefix(flag, "--batch") || flag == "--slots" {
			return nil, fmt.Errorf("unsupported HIP flag %s", a)
		}
		if flag == "--max-context" {
			if a != flag || i+1 >= len(list) {
				return nil, fmt.Errorf("HIP --max-context needs a separate value")
			}
			s, ok := list[i+1].(string)
			if !ok {
				return nil, fmt.Errorf("invalid HIP context")
			}
			context, err = strconv.Atoi(s)
			if err != nil || context < 1 || context > 131072 {
				return nil, fmt.Errorf("HIP context must be 1..131072")
			}
		}
	}
	if context == 0 {
		return nil, fmt.Errorf("HIP explicit --max-context is required")
	}
	if ctx := strings.TrimSpace(cfg["CTX"]); ctx != "" && ctx != strconv.Itoa(context) {
		return nil, fmt.Errorf("HIP CTX must match imported --max-context; prepare a new config")
	}
	if env, ok := out["env"].(map[string]any); ok {
		for k, v := range env {
			if strings.HasPrefix(k, "CUDA_") || k == "HIP_VISIBLE_DEVICES" || k == "ROCR_VISIBLE_DEVICES" || k == "GPU_DEVICE_ORDINAL" || strings.HasPrefix(k, "STRATA_REMOTE") || strings.HasPrefix(k, "STRATA_BATCH") {
				return nil, fmt.Errorf("HIP imported env forbids %s", k)
			}
			if _, ok := v.(string); !ok {
				return nil, fmt.Errorf("HIP env %s must be string", k)
			}
		}
	} else if out["env"] != nil {
		return nil, fmt.Errorf("HIP env must be an object")
	}
	host := firstNonEmpty(cfg["HOST"], "127.0.0.1")
	if host != "127.0.0.1" && host != "::1" && host != "localhost" {
		return nil, fmt.Errorf("HIP initial port binds loopback only")
	}
	port := 8080
	if cfg["PORT"] != "" {
		port, err = strconv.Atoi(cfg["PORT"])
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid HIP port")
		}
	}
	out["host"], out["port"] = host, port
	delete(out, "api_key")
	if key != "" {
		out["api_key"] = key
	}
	out["open_browser"] = false
	return out, nil
}

// The manifest pins imported application sources, not Python itself, its venv,
// system libraries, or model weights. It is not a full runtime qualification.
func moeHIPVerifyBytes(raw []byte, want, label string) error {
	want = strings.ToLower(strings.TrimSpace(want))
	if b, e := hex.DecodeString(want); e != nil || len(b) != 32 {
		return fmt.Errorf("HIP requires exact %s SHA256", label)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("HIP %s SHA256 mismatch", label)
	}
	return nil
}

func moeHIPVerifySources(cfg map[string]string) error {
	src := cfg["MOE_SOURCE_DIR"]
	if err := moeHIPPath(src, true, false); err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	path := cfg["MOE_SOURCE_MANIFEST"]
	if err = moeHIPPath(path, false, false); err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = moeHIPVerifyBytes(raw, cfg["MOE_SOURCE_MANIFEST_SHA256"], "source manifest"); err != nil {
		return err
	}
	var manifest struct {
		Files map[string]string `json:"files"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("HIP source manifest JSON: %w", err)
	}
	if len(manifest.Files) == 0 {
		return fmt.Errorf("HIP source manifest is empty")
	}
	for rel, want := range manifest.Files {
		if rel == "." || filepath.IsAbs(rel) || filepath.Clean(rel) != rel || strings.Contains(rel, "\\") || rel == ".." || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("HIP unsafe source manifest path %q", rel)
		}
		p := filepath.Join(root, rel)
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			return err
		}
		within, err := filepath.Rel(root, real)
		if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
			return fmt.Errorf("HIP manifest symlink escapes source: %s", rel)
		}
		if err = moeHIPPath(real, false, false); err != nil {
			return err
		}
		data, err := os.ReadFile(real)
		if err != nil {
			return err
		}
		if err = moeHIPVerifyBytes(data, want, "source "+rel); err != nil {
			return err
		}
	}
	if _, ok := manifest.Files[filepath.Join("serve", "server.py")]; !ok {
		return fmt.Errorf("HIP source manifest must include serve/server.py")
	}
	// Enumerate only application modules. Never walk large weight stores or venvs.
	check := func(p string, d fs.DirEntry) error {
		if d.Type()&os.ModeSymlink != 0 {
			st, err := os.Stat(p)
			if err != nil {
				return err
			}
			real, err := filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, real)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("HIP source symlink escapes root: %s", p)
			}
			if st.IsDir() {
				return fmt.Errorf("HIP module directory symlinks are unsupported: %s", p)
			}
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !d.IsDir() && (ext == ".py" || ext == ".so" || ext == ".pyd") {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			if _, ok := manifest.Files[rel]; !ok {
				return fmt.Errorf("HIP unlisted source module: %s", rel)
			}
		}
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, d := range entries {
		p := filepath.Join(root, d.Name())
		ext := strings.ToLower(filepath.Ext(p))
		if !d.IsDir() && (ext == ".py" || ext == ".so" || ext == ".pyd") {
			if err = check(p, d); err != nil {
				return err
			}
		}
		if d.Name() == "serve" || d.Name() == "tools" {
			if err = filepath.WalkDir(p, func(p string, d fs.DirEntry, e error) error {
				if e != nil {
					return e
				}
				return check(p, d)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func moeHIPPreflight(cfg map[string]string) (string, map[string]any, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return "", nil, fmt.Errorf("HIP imported port requires Linux amd64")
	}
	path := strings.TrimSpace(cfg["MOE_CONFIG"])
	if err := moeHIPPath(path, false, false); err != nil {
		return "", nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	if err = moeHIPVerifyBytes(raw, cfg["MOE_CONFIG_SHA256"], "config"); err != nil {
		return "", nil, err
	}
	var base map[string]any
	if err = json.Unmarshal(raw, &base); err != nil {
		return "", nil, fmt.Errorf("HIP JSON: %w", err)
	}
	if _, err = moeHIPBuildConfig(base, cfg, ""); err != nil {
		return "", nil, err
	}
	if err = moeHIPVerifySources(cfg); err != nil {
		return "", nil, err
	}
	src := cfg["MOE_SOURCE_DIR"]
	if err = moeHIPPath(src, true, false); err != nil {
		return "", nil, err
	}
	if err = moeHIPPath(filepath.Join(src, "serve", "server.py"), false, false); err != nil {
		return "", nil, err
	}
	if err = moeHIPPath(cfg["MOE_PYTHON"], false, true); err != nil {
		return "", nil, err
	}
	for _, k := range []string{"cwd", "tokenizer"} {
		p, ok := base[k].(string)
		if !ok {
			return "", nil, fmt.Errorf("HIP missing %s", k)
		}
		if err = moeHIPPath(p, true, false); err != nil {
			return "", nil, err
		}
	}
	for _, name := range []string{"vocab.json", "merges.txt", "token_type.json"} {
		if err = moeHIPPath(filepath.Join(base["tokenizer"].(string), name), false, false); err != nil {
			return "", nil, err
		}
	}
	if name, ok := base["model_name"].(string); !ok || strings.TrimSpace(name) == "" {
		return "", nil, fmt.Errorf("HIP model_name required")
	}
	exe, ok := base["exe"].(string)
	if !ok {
		return "", nil, fmt.Errorf("HIP exe missing")
	}
	if err = moeHIPPath(exe, false, true); err != nil {
		return "", nil, err
	}
	want := strings.ToLower(strings.TrimSpace(cfg["MOE_ENGINE_SHA256"]))
	if b, e := hex.DecodeString(want); e != nil || len(b) != 32 {
		return "", nil, fmt.Errorf("HIP requires exact MOE_ENGINE_SHA256")
	}
	got, err := sha256File(exe)
	if err != nil {
		return "", nil, err
	}
	if got != want {
		return "", nil, fmt.Errorf("HIP engine SHA256 mismatch")
	}
	args := base["args"].([]any)
	mtpRequired := false
	for i, a := range args {
		if strings.HasPrefix(fmt.Sprint(a), "--spec=") {
			return "", nil, fmt.Errorf("HIP --spec needs a separate value")
		}
		if a == "--spec" {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("HIP --spec value missing")
			}
			n, err := strconv.Atoi(fmt.Sprint(args[i+1]))
			if err != nil || (n != 0 && n != 3) {
				return "", nil, fmt.Errorf("HIP invalid --spec")
			}
			mtpRequired = n > 0
		}
	}
	required := map[string]bool{"--pack": true, "--native": false, "--ple-gguf": false, "--mtp": true}
	for flag, dir := range required {
		found := false
		for i, a := range args {
			if a == flag {
				found = true
				if i+1 >= len(args) {
					return "", nil, fmt.Errorf("HIP %s path missing", flag)
				}
				p, ok := args[i+1].(string)
				if !ok {
					return "", nil, fmt.Errorf("HIP %s path invalid", flag)
				}
				if err = moeHIPPath(p, dir, false); err != nil {
					return "", nil, err
				}
				if (flag == "--native" || flag == "--ple-gguf") && strings.ToLower(filepath.Ext(p)) != ".gguf" {
					return "", nil, fmt.Errorf("HIP %s requires Strata GGUF assets, not Radiance packs", flag)
				}
			}
		}
		if !found && (flag == "--pack" || flag == "--native" || (flag == "--mtp" && mtpRequired)) {
			return "", nil, fmt.Errorf("HIP required %s missing", flag)
		}
	}
	if libs, ok := base["lib_dirs"].([]any); ok {
		for _, v := range libs {
			p, ok := v.(string)
			if !ok {
				return "", nil, fmt.Errorf("HIP invalid lib_dirs")
			}
			if err = moeHIPPath(p, true, false); err != nil {
				return "", nil, err
			}
		}
	} else if base["lib_dirs"] != nil {
		return "", nil, fmt.Errorf("HIP lib_dirs must be array")
	}
	return path, base, nil
}

// serveMoeHIP uses AJEAN's normal process replacement: systemd supervises the
// Strata Python server directly, without a proxy or an SDK server in between.
func serveMoeHIP(cfg map[string]string) error {
	if err := moeHIPInstallVerifyReferences(cfg); err != nil {
		return err
	}
	path, base, err := moeHIPPreflight(cfg)
	if err != nil {
		return err
	}
	// Avoid a second device remapping inherited from the surrounding launcher.
	for _, k := range []string{"CUDA_VISIBLE_DEVICES", "HIP_VISIBLE_DEVICES", "ROCR_VISIBLE_DEVICES", "GPU_DEVICE_ORDINAL", "STRATA_BATCH_MTP", "STRATA_REMOTE_DROP"} {
		if os.Getenv(k) != "" {
			return fmt.Errorf("HIP launch requires clean inherited %s", k)
		}
	}
	final, err := moeHIPBuildConfig(base, cfg, readAPIKey())
	if err != nil {
		return err
	}
	dir := moeHIPDir()
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return err
	}
	sum, err := sha256File(path)
	if err != nil {
		return err
	}
	// The config content identifies independent presets without unsafe path names.
	stem := filepath.Join(dir, "import-"+sum[:16])
	log, err := os.OpenFile(stem+".log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = log.Chmod(0600); err != nil {
		log.Close()
		return err
	}
	log.Close()
	final["log"] = stem + ".log"
	data, err := json.MarshalIndent(final, "", " ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "config-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	out := stem + ".json"
	if err = os.Rename(tmpPath, out); err != nil {
		return err
	}
	host, port := fmt.Sprint(final["host"]), fmt.Sprint(final["port"])
	if err = waitPortFree(host, port, 5e9); err != nil {
		return err
	}
	py := cfg["MOE_PYTHON"]
	args := []string{py, filepath.Join(cfg["MOE_SOURCE_DIR"], "serve", "server.py"), "--engine", "strata", "--config", out, "--host", host, "--port", port}
	if err = os.Chdir(cfg["MOE_SOURCE_DIR"]); err != nil {
		return err
	}
	_ = putBytes(bkState, engineCmdlineKey, []byte(engineCmdline(args)))
	return execServer(py, args)
}
