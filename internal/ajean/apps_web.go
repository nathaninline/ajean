package ajean

// apps_web.go — API des apps pour l'écran Applications et le pont des apps.
//
// L'app tourne dans une iframe sandbox SANS origine (srcdoc construit par la
// page) : elle ne peut appeler aucune de ces routes elle-même. C'est la page
// AJEAN qui relaie ses demandes (ajean.scripts.run, storage, ai) en précisant
// l'id de l'app ouverte ; une app ne peut donc lancer que les scripts de son
// propre dossier, avec une entrée JSON passée sur stdin.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"strings"
	"sync"
	"time"
)

func registerAppRoutes(api func(string, http.HandlerFunc)) {
	api("/api/apps", handleAppsList)
	api("/api/apps/", handleAppSub)
}

func handleAppsList(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, http.StatusOK, map[string]any{"apps": listApps()})
}

func appErr(w http.ResponseWriter, code int, err error) {
	sendJSON(w, code, map[string]any{"error": err.Error()})
}

// handleAppSub : /api/apps/<id>[/<action>].
func handleAppSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	act := ""
	if len(parts) == 2 {
		act = parts[1]
	}
	if !appIDRe.MatchString(id) {
		sendJSON(w, http.StatusNotFound, map[string]any{"error": "app introuvable"})
		return
	}
	if act != "" && r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST attendu"})
		return
	}
	var body map[string]json.RawMessage
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 12<<20) // morceaux de fichiers (4 Mo en base64)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			sendJSON(w, http.StatusBadRequest, map[string]any{"error": "JSON invalide"})
			return
		}
	}
	bs := func(k string) string {
		var s string
		_ = json.Unmarshal(body[k], &s)
		return s
	}
	bsp := func(k string) *string {
		if _, ok := body[k]; !ok {
			return nil
		}
		s := bs(k)
		return &s
	}
	switch act {
	case "": // GET : fiche + versions + scripts
		m, err := readAppMeta(id)
		if err != nil {
			appErr(w, http.StatusNotFound, err)
			return
		}
		sendJSON(w, http.StatusOK, map[string]any{"app": m, "scripts": listAppScripts(id)})
	case "code": // code d'une version (0 = courante) ; open=true compte une ouverture
		m, err := readAppMeta(id)
		if err != nil {
			appErr(w, http.StatusNotFound, err)
			return
		}
		var n int
		_ = json.Unmarshal(body["version"], &n)
		if n <= 0 {
			n = m.Version
		}
		c, err := readAppCode(id, n)
		if err != nil {
			appErr(w, http.StatusNotFound, err)
			return
		}
		var open bool
		_ = json.Unmarshal(body["open"], &open)
		if open {
			countAppOpen(id)
		}
		sendJSON(w, http.StatusOK, map[string]any{"app": m, "version": n, "code": c, "kit": appKitCSS})
	case "meta":
		m, err := setAppMeta(id, bsp("name"), bsp("description"), bsp("icon"))
		if err != nil {
			appErr(w, http.StatusBadRequest, err)
			return
		}
		sendJSON(w, http.StatusOK, map[string]any{"app": m})
	case "delete":
		if err := deleteApp(id); err != nil {
			appErr(w, http.StatusNotFound, err)
			return
		}
		sendJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "restore":
		var n int
		_ = json.Unmarshal(body["version"], &n)
		m, err := restoreAppVersion(id, n)
		if err != nil {
			appErr(w, http.StatusBadRequest, err)
			return
		}
		sendJSON(w, http.StatusOK, map[string]any{"app": m})
	case "run":
		var input any
		if raw, ok := body["input"]; ok {
			_ = json.Unmarshal(raw, &input)
		}
		var to float64
		_ = json.Unmarshal(body["timeout"], &to)
		res := runAppScript(r.Context(), id, bs("script"), input, time.Duration(to*float64(time.Second)))
		sendJSON(w, http.StatusOK, res)
	case "scripts":
		sendJSON(w, http.StatusOK, map[string]any{"scripts": listAppScripts(id)})
	case "storage":
		v, err := appStorageOp(id, bs("op"), bs("key"), body["value"], bs("prefix"))
		if err != nil {
			appErr(w, http.StatusBadRequest, err)
			return
		}
		sendJSON(w, http.StatusOK, map[string]any{"value": v})
	case "files":
		sendJSON(w, http.StatusOK, map[string]any{"files": listAppFiles(id)})
	case "file_write":
		var off int64
		_ = json.Unmarshal(body["offset"], &off)
		raw, err := base64.StdEncoding.DecodeString(bs("b64"))
		if err != nil {
			appErr(w, http.StatusBadRequest, fmt.Errorf("morceau base64 invalide"))
			return
		}
		size, err := writeAppFileChunk(id, bs("name"), off, raw)
		if err != nil {
			appErr(w, http.StatusBadRequest, err)
			return
		}
		sendJSON(w, http.StatusOK, map[string]any{"size": size})
	case "file_read":
		var off, n int64
		_ = json.Unmarshal(body["offset"], &off)
		_ = json.Unmarshal(body["length"], &n)
		b, size, err := readAppFileChunk(id, bs("name"), off, n)
		if err != nil {
			appErr(w, http.StatusNotFound, err)
			return
		}
		sendJSON(w, http.StatusOK, map[string]any{"b64": base64.StdEncoding.EncodeToString(b), "size": size})
	case "file_delete":
		if err := deleteAppFile(id, bs("name")); err != nil {
			appErr(w, http.StatusNotFound, err)
			return
		}
		sendJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "ai":
		handleAppAI(w, r, id, body)
	default:
		sendJSON(w, http.StatusNotFound, map[string]any{"error": "action inconnue"})
	}
}

// appAIMu : un seul appel d'IA d'app à la fois (le moteur local n'a souvent
// qu'un emplacement ; deux apps en parallèle se gêneraient).
var appAIMu sync.Mutex

const appAIMaxIn = 100 << 10

// handleAppAI : ajean.ai.complete. Le modèle actif répond SANS aucun outil :
// il ne peut que rendre du texte, jamais agir.
func handleAppAI(w http.ResponseWriter, r *http.Request, id string, body map[string]json.RawMessage) {
	if _, err := readAppMeta(id); err != nil {
		appErr(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		System   string `json:"system"`
		Prompt   string `json:"prompt"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}
	b, _ := json.Marshal(body)
	_ = json.Unmarshal(b, &req)
	msgs := []Message{}
	if s := strings.TrimSpace(req.System); s != "" {
		msgs = append(msgs, Message{Role: "system", Content: s})
	}
	size := len(req.System)
	for _, m := range req.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			sendJSON(w, http.StatusBadRequest, map[string]any{"error": "role doit être user ou assistant", "code": "invalid"})
			return
		}
		size += len(m.Content)
		msgs = append(msgs, Message{Role: m.Role, Content: m.Content})
	}
	if strings.TrimSpace(req.Prompt) != "" {
		size += len(req.Prompt)
		msgs = append(msgs, Message{Role: "user", Content: req.Prompt})
	}
	if len(msgs) == 0 || msgs[len(msgs)-1].Role != "user" {
		sendJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt ou messages (finissant par user) requis", "code": "invalid"})
		return
	}
	if size > appAIMaxIn || len(req.Messages) > 50 {
		sendJSON(w, http.StatusBadRequest, map[string]any{"error": "demande trop grosse (100 Ko, 50 messages max)", "code": "invalid"})
		return
	}
	maxChars := 4000 * 4
	if req.MaxTokens > 0 && req.MaxTokens < 4000 {
		maxChars = req.MaxTokens * 4
	}
	if !appAIMu.TryLock() {
		sendJSON(w, http.StatusTooManyRequests, map[string]any{"error": "une autre demande d'IA d'app est en cours", "code": "busy"})
		return
	}
	defer appAIMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	var out strings.Builder
	truncated := false
	_, err := runChat(ctx, msgs, 0.7, Caps{}, func(ev StreamEvent) bool {
		if ev.Content != "" {
			out.WriteString(ev.Content)
			if out.Len() >= maxChars {
				truncated = true
				cancel()
				return false
			}
		}
		return true
	}, nil)
	if err != nil && !truncated && out.Len() == 0 {
		sendJSON(w, http.StatusBadGateway, map[string]any{"error": friendlyLLMError(err).Error(), "code": "provider"})
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"text": strings.TrimSpace(out.String()), "truncated": truncated})
}

// Interrupteur des Apps (Paramètres > Général). Activées par défaut : une
// installation existante ne perd rien. Coupées, l'IA n'a plus l'outil `app` et
// l'interface masque l'écran Applications ; les apps déjà faites restent sur le
// disque et reviennent quand on rallume.
const stAppsOff = "apps_off"

func appsEnabled() bool { return !getBool(bkState, stAppsOff) }

func handleAppsToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			On *bool `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.On == nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": "requête illisible"})
			return
		}
		if err := putBool(bkState, stAppsOff, !*req.On); err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "on": appsEnabled()})
}
