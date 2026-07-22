package jean

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// quantSegRe matches a single name segment that looks like a GGUF quantization
// token: Q8_0, Q6_K, Q5_K_M, Q4_K_XL, IQ4_XS, IQ3_XXS, Q4, 4bpw, BF16, F16…
var quantSegRe = regexp.MustCompile(`(?i)^(I?Q\d+(_[A-Za-z0-9]+)*|\d+BPW|BF16|FP16|F16|FP32|F32)$`)

// quantFromName extracts a quantization tag from a model filename by splitting
// on '-' and '.' and keeping the longest segment that looks like a quant token.
// Returns "" when nothing matches.
func quantFromName(name string) string {
	base := name
	if dot := strings.LastIndexByte(base, '.'); dot >= 0 && strings.EqualFold(base[dot:], ".gguf") {
		base = base[:dot]
	}
	segs := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '.' })
	best := ""
	for _, seg := range segs {
		if quantSegRe.MatchString(seg) && len(seg) > len(best) {
			best = seg
		}
	}
	return strings.ToUpper(best)
}

// presetReasoning returns the raw REASONING= value from a preset's config.env
// body, or "" if absent.
func presetReasoning(content string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		i := strings.IndexByte(s, '=')
		if i < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(s[:i]), "REASONING") {
			return strings.Trim(strings.TrimSpace(s[i+1:]), `"`)
		}
	}
	return ""
}

// reasoningActive reports whether a REASONING= value enables reasoning. backend_serve.go
// passes the flag whenever the value is non-empty, but an explicit off/none is
// treated here as disabled so the UI badge isn't misleading.
func reasoningActive(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "off", "none", "false", "0", "no", "disable", "disabled":
		return false
	}
	return true
}

// detectQuant returns the quantization tag for a preset: an explicit QUANT= line
// (manual override, with or without a leading '#') wins; otherwise it is
// auto-detected from the MODEL= filename. Returns "" when unknown.
func detectQuant(content string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		i := strings.IndexByte(s, '=')
		if i >= 0 && strings.EqualFold(strings.TrimSpace(s[:i]), "QUANT") {
			if v := strings.Trim(strings.TrimSpace(s[i+1:]), `"`); v != "" {
				return strings.ToUpper(v)
			}
		}
	}
	return quantFromName(modelFromPresetContent(content))
}

// modelFilePath resolves a model file name to a path inside JEAN_HOME, refusing
// anything that would escape it (path traversal). config.env conventionally
// stores only the basename, so we deliberately strip any directory component.
func modelFilePath(name string) (string, error) {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", fmt.Errorf("nom de modèle invalide")
	}
	if !strings.HasSuffix(strings.ToLower(base), ".gguf") {
		return "", fmt.Errorf("seuls les fichiers .gguf peuvent être supprimés")
	}
	return filepath.Join(JeanHome(), base), nil
}

// modelFromPresetContent extracts the MODEL= value (basename) from a preset's
// config.env body, or "" if absent.
func modelFromPresetContent(content string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		i := strings.IndexByte(s, '=')
		if i < 0 {
			continue
		}
		if strings.TrimSpace(s[:i]) == "MODEL" {
			v := strings.Trim(strings.TrimSpace(s[i+1:]), "\"")
			return filepath.Base(v)
		}
	}
	return ""
}

// deleteModelFile removes a .gguf file from JEAN_HOME after validating the name.
func deleteModelFile(name string) error {
	p, err := modelFilePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("modèle introuvable: %s", filepath.Base(p))
		}
		return err
	}
	return nil
}

// handleModelDelete deletes a single .gguf from JEAN_HOME.
func handleModelDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := deleteModelFile(req.Name); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// ---- Hugging Face downloads -------------------------------------------------

// dlState tracks a single in-flight (or finished) model download.
type dlState struct {
	Filename  string `json:"filename"`
	URL       string `json:"url"`
	Total     int64  `json:"total"`
	Done      int64  `json:"done"`
	Finished  bool   `json:"finished"`
	Err       string `json:"error"`
	StartedAt int64  `json:"started_at"`
}

var (
	dlMu        sync.Mutex
	dlDownloads = map[string]*dlState{} // keyed by filename
)

// normalizeHFURL turns a Hugging Face "blob" page URL into a direct "resolve"
// download URL, and leaves already-direct URLs untouched. Returns the URL to
// fetch and the target filename.
func normalizeHFURL(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("lien vide")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("lien invalide: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("lien invalide (http/https attendu)")
	}
	// huggingface.co/<repo>/blob/<rev>/<file> → /resolve/<rev>/<file>
	if strings.Contains(u.Host, "huggingface.co") {
		u.Path = strings.Replace(u.Path, "/blob/", "/resolve/", 1)
	}
	name := path.Base(u.Path)
	if name == "" || name == "/" || name == "." {
		return "", "", fmt.Errorf("impossible de déduire le nom du fichier depuis le lien")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".gguf") {
		return "", "", fmt.Errorf("le lien doit pointer vers un fichier .gguf")
	}
	return u.String(), name, nil
}

// handleModelDownload kicks off a background download of a .gguf from a URL
// (typically Hugging Face) into JEAN_HOME. Progress is polled via
// /api/models/download/status.
func handleModelDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	dlURL, name, err := normalizeHFURL(req.URL)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	dest, err := modelFilePath(name)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	dlMu.Lock()
	if st, ok := dlDownloads[name]; ok && !st.Finished {
		dlMu.Unlock()
		sendJSON(w, 409, map[string]any{"ok": false, "error": "téléchargement déjà en cours pour " + name})
		return
	}
	if _, err := os.Stat(dest); err == nil {
		dlMu.Unlock()
		sendJSON(w, 409, map[string]any{"ok": false, "error": "le modèle existe déjà: " + name})
		return
	}
	st := &dlState{Filename: name, URL: dlURL, StartedAt: time.Now().Unix()}
	dlDownloads[name] = st
	dlMu.Unlock()

	go runDownload(st, dlURL, dest)
	sendJSON(w, 200, map[string]any{"ok": true, "filename": name})
}

// runDownload streams the URL to a .part file then renames it on success.
func runDownload(st *dlState, dlURL, dest string) {
	finish := func(e error) {
		dlMu.Lock()
		if e != nil {
			st.Err = e.Error()
		}
		st.Finished = true
		dlMu.Unlock()
	}

	req, err := http.NewRequest("GET", dlURL, nil)
	if err != nil {
		finish(err)
		return
	}
	// HF gated/private repos may need a token; reuse the same key store if set.
	if k := os.Getenv("HF_TOKEN"); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	client := &http.Client{Timeout: 0} // large files: no overall timeout
	resp, err := client.Do(req)
	if err != nil {
		finish(err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		finish(fmt.Errorf("HTTP %d depuis la source", resp.StatusCode))
		return
	}
	dlMu.Lock()
	st.Total = resp.ContentLength
	dlMu.Unlock()

	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		finish(err)
		return
	}
	buf := make([]byte, 1<<20) // 1 MiB
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				_ = os.Remove(tmp)
				finish(werr)
				return
			}
			dlMu.Lock()
			st.Done += int64(n)
			dlMu.Unlock()
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			_ = os.Remove(tmp)
			finish(rerr)
			return
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		finish(err)
		return
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		finish(err)
		return
	}
	finish(nil)
}

// handleModelDownloadStatus returns the state of all known downloads this run.
func handleModelDownloadStatus(w http.ResponseWriter, r *http.Request) {
	dlMu.Lock()
	out := make([]dlState, 0, len(dlDownloads))
	for _, st := range dlDownloads {
		out = append(out, *st)
	}
	dlMu.Unlock()
	sendJSON(w, 200, out)
}
