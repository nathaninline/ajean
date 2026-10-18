package ajean

// Named AMD connections are ordinary External API presets. The original chat,
// tools, history and compactor continue to use resolveChatEndpoint unchanged.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const externalEngineKey = "EXTERNAL_ENGINE"

var engineConnectionMu sync.Mutex

func namedExternalEngine(cfg map[string]string) bool {
	return isExternalConfig(cfg) && (cfg[externalEngineKey] == "radiance" || cfg[externalEngineKey] == "paiton")
}
func engineConnectionRequest(req externalSaveReq) (externalSaveReq, error) {
	req.Engine = strings.TrimSpace(req.Engine)
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	req.Model = strings.TrimSpace(req.Model)
	req.Ctx = strings.TrimSpace(req.Ctx)
	if req.Engine != "radiance" && req.Engine != "paiton" {
		return req, fmt.Errorf("moteur attendu : radiance ou paiton")
	}
	for _, s := range []string{req.ID, req.Name, req.URL, req.Model, req.Key, req.Ctx} {
		if len(s) > 4096 || strings.ContainsAny(s, "\r\n\x00") {
			return req, fmt.Errorf("champ de connexion invalide")
		}
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return req, fmt.Errorf("URL HTTP(S) sans identifiants, requête ou fragment requise")
	}
	if req.Ctx != "" {
		n, e := strconv.Atoi(req.Ctx)
		if e != nil || n < 4096 || n > 2097152 {
			return req, fmt.Errorf("contexte déclaré attendu entre 4096 et 2097152")
		}
	}
	if req.ID != "" && !req.KeyTouched {
		old, e := ReadPreset(req.ID)
		if e != nil {
			return req, fmt.Errorf("preset introuvable")
		}
		req.Key = parseEnv(old)[extKeyToken]
	}

	if req.Effort == nil && req.ID != "" {
		old, e := ReadPreset(req.ID)
		if e != nil {
			return req, fmt.Errorf("preset introuvable")
		}
		value := parseEnv(old)["REASONING_EFFORT"]
		req.Effort = &value
	}
	if req.Effort != nil {
		value := strings.TrimSpace(*req.Effort)
		switch value {
		case "", "none", "minimal", "low", "medium", "high", "xhigh":
		default:
			return req, fmt.Errorf("niveau de raisonnement invalide")
		}
		req.Effort = &value
	}
	return req, nil
}

type engineConnectionModel struct {
	ID           string `json:"id"`
	Capabilities struct {
		ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
	} `json:"capabilities,omitempty"`
}

// GET /models is the standard OpenAI-compatible discovery/identity check. No
// completion is sent and no optional engine-specific /health route is assumed.
func engineConnectionModels(ctx context.Context, req externalSaveReq) ([]engineConnectionModel, error) {
	endpoint := strings.TrimSuffix(completionsURL(req.URL), "/chat/completions") + "/models"
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("URL invalide")
	}
	if req.Key != "" {
		r.Header.Set("Authorization", "Bearer "+req.Key)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("découverte des modèles indisponible ou délai dépassé")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("découverte des modèles : HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1048577))
	if err != nil || len(raw) > 1048576 {
		return nil, fmt.Errorf("liste des modèles trop volumineuse ou illisible")
	}
	var doc struct {
		Data []engineConnectionModel `json:"data"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.Data == nil || len(doc.Data) > 1024 {
		return nil, fmt.Errorf("réponse /models invalide")
	}
	models := []engineConnectionModel{}
	seen := map[string]bool{}
	for _, m := range doc.Data {
		if m.ID == "" || len(m.ID) > 512 || strings.ContainsAny(m.ID, "\r\n\x00") {
			return nil, fmt.Errorf("identité de modèle invalide")
		}
		if !seen[m.ID] {
			models = append(models, m)
			seen[m.ID] = true
		}
	}
	if req.Engine == "radiance" && req.Effort != nil && *req.Effort != "" && req.Model != "" {
		supported := false
		for _, m := range models {
			if m.ID == req.Model {
				for _, v := range m.Capabilities.ReasoningEfforts {
					if v == *req.Effort {
						supported = true
					}
				}
			}
		}
		if !supported {
			return nil, fmt.Errorf("ce niveau de raisonnement n'est pas déclaré par le modèle Radiance sélectionné")
		}
	}
	if req.Model != "" && !seen[req.Model] {
		return nil, fmt.Errorf("le modèle sélectionné n'est pas annoncé par /models")
	}
	return models, nil
}

func saveEngineConnection(req externalSaveReq) (string, error) {
	engineConnectionMu.Lock()
	defer engineConnectionMu.Unlock()
	var err error
	req, err = engineConnectionRequest(req)
	if err != nil {
		return "", err
	}
	if err = validPresetName(req.Name); err != nil {
		return "", err
	}
	if req.Model == "" {
		return "", fmt.Errorf("modèle requis")
	}
	id := req.ID
	if id == "" {
		id, err = uniquePresetID(req.Name)
		if err != nil {
			return "", err
		}
	}
	path, err := safePresetPath(id)
	if err != nil {
		return "", err
	}
	body := externalPresetWithMachine(externalPresetContent(req.URL, req.Model, req.Key, req.Ctx, req.Vision), externalMachineKeys(req.ID))
	cfg := parseEnv(body)
	cfg[externalEngineKey] = req.Engine
	if req.Effort != nil && *req.Effort != "" {
		cfg["REASONING_EFFORT"] = *req.Effort
	}
	delete(cfg, extKeyService)
	body = withDisplayName(formatEnv(cfg), req.Name)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	old, _ := os.ReadFile(path)
	f, err := os.CreateTemp(filepath.Dir(path), ".engine-connection-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(body); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	if id == getStr(bkState, "active_preset") && presetFingerprint(old) != presetFingerprint([]byte(body)) {
		_ = putStr(bkState, "active_preset", "")
	}
	return id, nil
}

func selectEngineConnection(ctx context.Context, path string) error {
	engineConnectionMu.Lock()
	defer engineConnectionMu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cfg := parseEnv(string(raw))
	if !namedExternalEngine(cfg) {
		return fmt.Errorf("connexion nommée attendue")
	}
	req, err := engineConnectionRequest(externalSaveReq{Engine: cfg[externalEngineKey], URL: cfg[extKeyURL], Model: cfg[extKeyModel], Key: cfg[extKeyToken], KeyTouched: true, Ctx: cfg["CTX"], Effort: connectionEffort(cfg["REASONING_EFFORT"])})
	if err != nil {
		return err
	}
	if cfg[extKeyService] != "" || req.Model == "" {
		return fmt.Errorf("connexion gérée par service non prise en charge")
	}
	models, err := engineConnectionModels(ctx, req)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if id, _ := scriptRunningAny(); id != "" {
		return fmt.Errorf("tâche script en cours")
	}
	// Hold both gates through config publication: an already-running turn/job
	// cannot be silently redirected. No service is started, stopped or reloaded.
	lcMu.Lock()
	defer lcMu.Unlock()
	conv.mu.Lock()
	defer conv.mu.Unlock()
	if (lcCur != nil && lcCur.Running) || conv.Generating {
		return fmt.Errorf("génération ou installation en cours")
	}
	if err := applyPresetFile(path); err != nil {
		return err
	}
	return SetConfigKey("EXTERNAL_REASONING_EFFORTS", strings.Join(engineDeclaredEfforts(models, req.Model), ","))
}

func handleEngineConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, 405, map[string]any{"ok": false})
		return
	}
	presets, err := ListPresets()
	if err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": "presets indisponibles"})
		return
	}
	rows := []map[string]any{}
	for _, p := range presets {
		raw, e := ReadPreset(p.ID)
		if e != nil {
			continue
		}
		c := parseEnv(raw)
		if !namedExternalEngine(c) {
			continue
		}
		rows = append(rows, map[string]any{"id": p.ID, "engine": c[externalEngineKey], "name": p.Name, "url": c[extKeyURL], "model": c[extKeyModel], "ctx": c["CTX"], "vision": c[extKeyVision] == "1", "hasKey": c[extKeyToken] != "", "active": p.Active, "effort": c["REASONING_EFFORT"]})
	}
	sendJSON(w, 200, map[string]any{"ok": true, "connections": rows, "engines": []map[string]string{{"id": "radiance", "label": "Radiance"}, {"id": "paiton", "label": "Paiton"}}, "capabilities": map[string]bool{"select": true, "install": false, "reload": false, "server_settings": false}})
}
func handleEngineConnectionSelect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]any{"ok": false})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req) != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "requête invalide"})
		return
	}
	path, err := safePresetPath(req.ID)
	if err == nil {
		err = selectEngineConnection(r.Context(), path)
	}
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "id": req.ID})
}
func handleEngineConnectionSave(w http.ResponseWriter, r *http.Request, req externalSaveReq) {
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]any{"ok": false})
		return
	}
	id, err := saveEngineConnection(req)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "id": id, "name": req.Name})
}
func handleEngineConnectionTest(w http.ResponseWriter, r *http.Request, req externalSaveReq) {
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]any{"ok": false})
		return
	}
	req, err := engineConnectionRequest(req)
	var models []engineConnectionModel
	if err == nil {
		models, err = engineConnectionModels(r.Context(), req)
	}
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "models": models, "model_present": req.Model != ""})
}

func connectionEffort(value string) *string { return &value }

// Only recognized values explicitly announced by the selected server/model are
// exposed to the original composer. This is a capability snapshot at selection.
func engineDeclaredEfforts(models []engineConnectionModel, model string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, m := range models {
		if m.ID != model {
			continue
		}
		for _, v := range m.Capabilities.ReasoningEfforts {
			switch v {
			case "none", "minimal", "low", "medium", "high", "xhigh":
				if !seen[v] {
					result = append(result, v)
					seen[v] = true
				}
			}
		}
	}
	return result
}
