package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui/index.html ui/marked.min.js
var uiFS embed.FS

// cmdWeb starts the HTTP server on the given port (default 8090).
func cmdWeb(args []string) error {
	port := 8090
	if len(args) > 0 && args[0] != "" {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("port invalide: %s", args[0])
		}
		port = n
	}
	mux := newWebMux()
	addr := fmt.Sprintf("0.0.0.0:%d", port)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Port occupé : on identifie le process qui le tient et on propose de
		// le terminer pour relancer à sa place.
		if !resolvePortConflict(port) {
			return err
		}
		if ln, err = net.Listen("tcp", addr); err != nil {
			return err
		}
	}
	fmt.Printf("[jean web] http://%s  (Ctrl-C pour arrêter)\n", addr)
	if readWebKey() == "" {
		fmt.Printf("%s API de pilotage NON protégée (aucune clé). Avant de l'exposer sur internet :\n", yellow("[!]"))
		fmt.Printf("       %s\n", bold("jean set-web-key"))
	} else {
		fmt.Printf("%s API protégée par clé (Authorization: Bearer …)\n", green("[ok]"))
	}
	return http.Serve(ln, mux)
}

// newWebMux construit le routeur HTTP de l'UI web. Extrait de cmdWeb pour être
// réutilisé par `jean link`, qui sert ce même mux à travers le tunnel sans
// repasser par un écouteur TCP local.
var convLoadOnce sync.Once

func newWebMux() *http.ServeMux {
	// Charge l'état de conversation persisté (une fois par process : jean web ET
	// jean link serve appellent newWebMux).
	convLoadOnce.Do(LoadConversation)
	mux := http.NewServeMux()
	// Pages publiques : le HTML et le JS ne contiennent aucun secret. Toute la
	// donnée et toutes les actions passent par /api/* qui, lui, exige la clé.
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/marked.min.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := uiFS.ReadFile("ui/marked.min.js")
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(b)
	})
	// api enregistre une route /api/* protégée par la clé de pilotage (webauth.go).
	api := func(path string, h http.HandlerFunc) { mux.HandleFunc(path, requireWebAuth(h)) }
	api("/api/ping", handlePing)
	api("/api/status", handleStatus)
	api("/api/vram", handleVram)
	api("/api/config", handleConfigEnv)
	api("/api/catalog", handleCatalog)
	api("/api/update", handleUpdateCheck)
	api("/api/update/apply", handleUpdateApply)
	api("/api/models", handleModels)
	api("/api/models/delete", handleModelDelete)
	api("/api/models/download", handleModelDownload)
	api("/api/models/download/status", handleModelDownloadStatus)
	api("/api/backends", handleBackends)
	api("/api/presets", handlePresets)
	api("/api/preset", handlePreset)
	api("/api/preset/save", handlePresetSave)
	api("/api/preset/delete", handlePresetDelete)
	api("/api/agent", handleAgent)
	api("/api/agent/toggle", handleAgentToggle)
	api("/api/agent/tool-limit", handleToolLimitToggle)
	api("/api/agent/compact", handleCompactToggle)
	api("/api/apikey", handleAPIKey)
	api("/api/oai/public", handleOAIPublic)
	api("/api/internet", handleInternet)
	api("/api/memory", handleMemoryMode)
	api("/api/prefs", handleWebPrefs)
	// Alias rétro-compat : l'ancien portail ajean.link (dépôt jean-relay) pilote
	// encore l'agent via /api/tools* et /api/skills/toggle à travers le tunnel E2E.
	// On les mappe sur le mode agent unifié le temps que le portail soit mis à jour.
	api("/api/tools", handleAgent)
	api("/api/tools/toggle", handleAgentToggle)
	api("/api/skills", handleAgent)
	api("/api/skills/toggle", handleAgentToggle)
	api("/api/mem", handleMem)
	api("/api/mem/save", handleMemSave)
	api("/api/mem/delete", handleMemDelete)
	api("/api/switch", handleSwitch)
	api("/api/start", svcHandler("start"))
	api("/api/stop", svcHandler("stop"))
	api("/api/restart", svcHandler("restart"))
	api("/api/bench", handleBench)
	api("/api/bench/last", handleBenchLast)
	api("/api/chat", handleChat)                 // flux d'ABONNEMENT (SSE) : rejoue + suit le fil
	api("/api/chat/send", handleChatSend)        // envoie un message (lance la génération détachée)
	api("/api/chat/stop", handleChatStop)        // interrompt la génération en cours
	api("/api/chat/reset", handleChatReset)      // nouvelle conversation (pour tous les appareils)
	api("/api/chat/state", handleChatState)      // instantané léger {seq, generating, ctx_used}
	api("/api/e2e/chat", handleE2EChat)          // même flux mais chiffré E2E (boîte noire via le relais)
	return mux
}

// resolvePortConflict identifies the process listening on `port`, asks the user
// whether to terminate it, and (on yes) kills it and waits for the port to free.
// Returns true if the caller should retry binding.
func resolvePortConflict(port int) bool {
	pid, name := pidOnPort(port)
	if pid == 0 {
		fmt.Printf("%s port %d déjà utilisé, mais le process n'a pas pu être identifié (essaie en root ?)\n", red("[err]"), port)
		return false
	}
	fmt.Printf("%s le port %d est déjà utilisé par %s (PID %d).\n", yellow("[!]"), port, bold(name), pid)
	fmt.Print(dim("    terminer ce process et relancer ? [Y/n] "))
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() && strings.HasPrefix(strings.ToLower(strings.TrimSpace(sc.Text())), "n") {
		fmt.Println(dim("    annulé."))
		return false
	}
	// SIGTERM d'abord, puis SIGKILL si le port ne se libère pas.
	_ = exec.Command("kill", "-TERM", strconv.Itoa(pid)).Run()
	for i := 0; i < 15; i++ {
		time.Sleep(200 * time.Millisecond)
		if p, _ := pidOnPort(port); p == 0 {
			fmt.Printf("%s process %d terminé, redémarrage…\n", green("[ok]"), pid)
			return true
		}
	}
	_ = exec.Command("kill", "-KILL", strconv.Itoa(pid)).Run()
	time.Sleep(500 * time.Millisecond)
	if p, _ := pidOnPort(port); p != 0 {
		fmt.Printf("%s impossible de libérer le port %d (PID %d toujours présent)\n", red("[err]"), port, p)
		return false
	}
	fmt.Printf("%s process %d terminé (forcé), redémarrage…\n", green("[ok]"), pid)
	return true
}

// pidOnPort returns the PID and command name of the process listening on the
// given TCP port, via `ss` (Linux) with an `lsof` fallback. Returns 0 if none
// is found or if the tools can't see it (e.g. owned by another user).
func pidOnPort(port int) (int, string) {
	redir := regexp.MustCompile(`pid=(\d+)`)
	if out, err := exec.Command("ss", "-ltnHp", fmt.Sprintf("sport = :%d", port)).Output(); err == nil {
		if m := redir.FindStringSubmatch(string(out)); m != nil {
			pid, _ := strconv.Atoi(m[1])
			return pid, processName(pid)
		}
	}
	if out, err := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port), "-sTCP:LISTEN").Output(); err == nil {
		for _, line := range strings.Fields(string(out)) {
			if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
				return pid, processName(pid)
			}
		}
	}
	return 0, ""
}

// processName returns a short command name for a PID, or "?" if unknown.
func processName(pid int) string {
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		if n := strings.TrimSpace(string(b)); n != "" {
			return n
		}
	}
	if out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output(); err == nil {
		if n := strings.TrimSpace(string(out)); n != "" {
			return n
		}
	}
	return "?"
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	b, err := uiFS.ReadFile("ui/index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Write(b)
}

func sendJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// handlePing is a lightweight authenticated endpoint a client hits to verify
// connectivity AND that its key is valid (200 = bonne clé, 401 = mauvaise clé).
func handlePing(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, map[string]any{"ok": true, "service": "jean", "version": Version})
}

// handleStatus reports service state cross-platform via serviceIsActive
// (systemd sous Linux, supervision par PID-file sous Windows — voir service_*.go).
func handleStatus(w http.ResponseWriter, r *http.Request) {
	active := serviceIsActive()
	state := "inactive"
	if active {
		state = "active"
	}
	health := false
	if active {
		health = healthCheck()
	}
	ctx := 32768
	if v := ReadConfig()["CTX"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ctx = n
		}
	}
	sendJSON(w, 200, map[string]any{
		"state":   state,
		"active":  active,
		"health":  health,
		"port":    LLMPort(),
		"ctx":     ctx,
		"version": Version,
	})
}

func handleVram(w http.ResponseWriter, r *http.Request) {
	out, err := hideCmd(exec.Command("nvidia-smi",
		"--query-gpu=name,memory.used,memory.total,utilization.gpu,temperature.gpu",
		"--format=csv,noheader,nounits")).Output()
	gpus := []map[string]any{}
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.Split(line, ",")
			if len(parts) != 5 {
				continue
			}
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			used, _ := strconv.Atoi(parts[1])
			total, _ := strconv.Atoi(parts[2])
			util, _ := strconv.Atoi(parts[3])
			temp, _ := strconv.Atoi(parts[4])
			gpus = append(gpus, map[string]any{
				"name": parts[0], "used": used, "total": total, "util": util, "temp": temp,
			})
		}
	}
	sendJSON(w, 200, gpus)
}

func handleConfigEnv(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, ReadConfig())
}

// handleBackends scans JEAN_HOME/backends/<name>/ for a llama-server binary,
// trying common build subpaths (build/bin, build-sm120/bin, bin, .).
// Returns [{name, path}].
func handleBackends(w http.ResponseWriter, r *http.Request) {
	root := JeanHome() + "/backends"
	entries, err := os.ReadDir(root)
	if err != nil {
		sendJSON(w, 200, []map[string]any{})
		return
	}
	subpaths := []string{
		"build/bin/llama-server", "build-sm120/bin/llama-server",
		"build/llama-server", "bin/llama-server", "llama-server",
		// Layout du générateur Visual Studio (multi-config) + suffixe .exe Windows.
		"build/bin/Release/llama-server.exe", "build/bin/llama-server.exe",
		"build/bin/Release/llama-server", "llama-server.exe",
	}
	out := []map[string]any{}
	for _, e := range entries {
		// e can be a directory or a symlink to one; either is fine.
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		for _, sp := range subpaths {
			p := root + "/" + name + "/" + sp
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				out = append(out, map[string]any{"name": name, "path": p})
				break
			}
		}
	}
	sendJSON(w, 200, out)
}

// handleModels lists *.gguf files in JEAN_HOME (size in bytes) for the preset
// editor's model picker.
func handleModels(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(JeanHome())
	if err != nil {
		sendJSON(w, 200, []map[string]any{})
		return
	}
	out := []map[string]any{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
			continue
		}
		info, _ := e.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		out = append(out, map[string]any{"name": e.Name(), "size": size})
	}
	sendJSON(w, 200, out)
}

func handlePresets(w http.ResponseWriter, r *http.Request) {
	list, err := ListPresets()
	if err != nil {
		sendJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	store := loadBenchStore()
	out := []map[string]any{}
	for _, p := range list {
		item := map[string]any{"id": p.ID, "name": p.Name, "active": p.Active}
		if content, err := ReadPreset(p.ID); err == nil {
			if q := detectQuant(content); q != "" {
				item["quant"] = q
			}
			if r := presetReasoning(content); reasoningActive(r) {
				item["reasoning"] = strings.ToLower(r)
			}
		}
		if sb, ok := store[p.ID]; ok {
			item["bench"] = map[string]any{
				"prefill": sb.Result.PromptPerSecond,
				"decode":  sb.Result.PredictedPerSec,
				"at":      sb.At,
			}
		}
		out = append(out, item)
	}
	sendJSON(w, 200, out)
}

func handlePreset(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		// new preset → seed from current config.env so users can tweak rather than start blank
		b, _ := os.ReadFile(confPath())
		sendJSON(w, 200, map[string]any{"id": "", "name": "", "content": string(b)})
		return
	}
	content, err := ReadPreset(id)
	if err != nil {
		sendJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	sendJSON(w, 200, map[string]any{"id": id, "name": presetDisplayName(content, id), "content": content})
}

// presetSaveReq is the preset editor payload. `id` identifies an existing
// preset to update ("" creates a new one); `name` is the display name.
type presetSaveReq struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	DeleteModel bool   `json:"deleteModel"`
}

// saveReq is the skill editor payload (skills keep name-as-identity + rename).
type saveReq struct {
	Name    string `json:"name"`
	Old     string `json:"old"`
	Content string `json:"content"`
}

func handlePresetSave(w http.ResponseWriter, r *http.Request) {
	var req presetSaveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	newID, err := SavePreset(req.ID, req.Name, req.Content)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "id": newID, "name": req.Name})
}

func handlePresetDelete(w http.ResponseWriter, r *http.Request) {
	var req presetSaveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Capture the referenced model before the preset file disappears, so we can
	// optionally delete the .gguf alongside it.
	model := ""
	if req.DeleteModel {
		if content, err := ReadPreset(req.ID); err == nil {
			model = modelFromPresetContent(content)
		}
	}
	if err := DeletePreset(req.ID); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	modelDeleted, modelErr := "", ""
	if req.DeleteModel && model != "" {
		if err := deleteModelFile(model); err != nil {
			modelErr = err.Error()
		} else {
			modelDeleted = model
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "modelDeleted": modelDeleted, "modelError": modelErr})
}

// handleAgent renvoie l'état du mode agent ET la liste des pages mémoire (que
// l'IA gère via les outils mem_*) — un seul aller-retour pour l'UI. La clé
// "skills" est conservée en miroir de "pages" pour l'ancien portail ajean.link.
func handleAgent(w http.ResponseWriter, r *http.Request) {
	pages := MemList()
	out := []map[string]any{}
	for _, p := range pages {
		out = append(out, map[string]any{"name": p.Name, "desc": p.Title})
	}
	sendJSON(w, 200, map[string]any{"enabled": agentEnabled(), "tool_limit": toolLimitEnabled(), "compact": compactEnabled(), "mem_mode": string(memMode()), "pages": out, "skills": out})
}

// handleMemoryMode lit/écrit le mode mémoire (off / ondemand / always).
//
//	GET  → {mode}
//	POST {mode} → persiste MEM_MODE
func handleMemoryMode(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Mode string `json:"mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		// On normalise via memMode() en réinjectant la valeur : toute entrée
		// inconnue retombe sur "always", donc on valide en passant par le parseur.
		m := MemAlways
		switch MemMode(strings.ToLower(strings.TrimSpace(req.Mode))) {
		case MemOff:
			m = MemOff
		case MemOnDemand:
			m = MemOnDemand
		case MemAlways:
			m = MemAlways
		}
		if err := setMemMode(m); err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "mode": string(memMode())})
}

// handleToolLimitToggle active/désactive le plafond d'appels d'outils par tour
// (config.env TOOL_LIMIT). On=limité (défaut), off=quasi illimité.
func handleToolLimitToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	val := ""
	if !req.On {
		val = "off"
	}
	if err := SetConfigKey("TOOL_LIMIT", val); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "tool_limit": toolLimitEnabled()})
}

// handleCompactToggle active/désactive le compactage automatique du contexte
// (config.env COMPACT). On=compacte (défaut), off=jamais.
func handleCompactToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	val := ""
	if !req.On {
		val = "off"
	}
	if err := SetConfigKey("COMPACT", val); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "compact": compactEnabled()})
}

func handleAgentToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := setAgentEnabled(req.On); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "enabled": agentEnabled()})
}

// handleInternet pilote l'accès web de l'IA (serveur Crawl4AI).
//
//	GET  → {enabled, url, reachable}
//	POST {enabled, url} → enregistre CRAWL4AI_URL + le drapeau .internet_enabled
// handleAPIKey expose et pilote la clé d'accès à l'endpoint compatible OpenAI
// (llama-server /v1). GET renvoie l'état ; POST {action:"generate"|"set"|"clear",
// key?} l'écrit puis redémarre le service (llama-server lit --api-key au lancement).
func handleAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Action string `json:"action"`
			Key    string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		var key string
		switch req.Action {
		case "generate":
			key = genAPIKey()
		case "set":
			key = strings.TrimSpace(req.Key)
		case "clear":
			key = ""
		default:
			sendJSON(w, 400, map[string]any{"ok": false, "error": "action inconnue"})
			return
		}
		if err := writeAPIKey(key); err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		// La clé n'est appliquée qu'au (re)démarrage de llama-server.
		if serviceIsActive() {
			_ = serviceAction("restart")
		}
	}
	k := readAPIKey()
	sendJSON(w, 200, map[string]any{
		"ok":     true,
		"set":    k != "",
		"key":    k,
		"masked": maskAPIKey(k),
		"port":   LLMPort(),
		"host":   localIP(),
		// Accès OpenAI PUBLIC via ajean.link (passthrough SNI, VPS aveugle) : si
		// activé, l'URL publique est https://<machine>.oai.ajean.link/v1.
		"oai_public": oaiPublicEnabled(),
		"machine":    machineID(),
	})
}

// handleOAIPublic pilote le drapeau d'accès OpenAI public (exposition via
// ajean.link). GET renvoie l'état ; POST {enabled} l'active/coupe en direct
// (aucun redémarrage : le démux du tunnel relit le drapeau à chaque connexion).
func handleOAIPublic(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if req.Enabled != nil {
			if err := setOAIPublic(*req.Enabled); err != nil {
				sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		}
	}
	sendJSON(w, 200, map[string]any{
		"ok":      true,
		"enabled": oaiPublicEnabled(),
		"machine": machineID(),
	})
}

// localIP best-effort renvoie l'IPv4 LAN primaire de la machine (l'IP source du
// trafic sortant), ou "localhost" à défaut. Sert à annoncer l'endpoint OpenAI
// avec une adresse correcte sur le réseau local MÊME quand l'UI est atteinte via
// le tunnel ajean.link (où location.hostname serait le domaine du relais, faux).
func localIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "localhost"
	}
	defer conn.Close()
	if a, ok := conn.LocalAddr().(*net.UDPAddr); ok && a.IP != nil {
		return a.IP.String()
	}
	return "localhost"
}

func handleInternet(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Enabled *bool   `json:"enabled"`
			URL     *string `json:"url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.URL != nil {
			u := strings.TrimRight(strings.TrimSpace(*req.URL), "/")
			if err := SetConfigKey("CRAWL4AI_URL", u); err != nil {
				sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			reachMu.Lock()
			reachURL = "" // invalide le cache de reachability
			reachMu.Unlock()
		}
		if req.Enabled != nil {
			if err := setInternetEnabled(*req.Enabled); err != nil {
				sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		}
	}
	sendJSON(w, 200, map[string]any{
		"ok":        true,
		"enabled":   internetEnabled(),
		"url":       crawl4aiURL(),
		"reachable": crawlReachable(),
	})
}

// handleMem / handleMemSave / handleMemDelete : éditeur web des pages mémoire
// (MEMORY/<nom>.md). Payload partagé saveReq (name/old/content) ; "name" = nom
// de fichier de la page.
func handleMem(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		sendJSON(w, 200, map[string]any{"name": "", "content": "# nouvelle page\n\nNote ici ce que jean doit retenir entre les sessions.\n"})
		return
	}
	c := MemContent(name)
	if c == "" {
		sendJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	sendJSON(w, 200, map[string]any{"name": name, "content": c})
}

func handleMemSave(w http.ResponseWriter, r *http.Request) {
	var req saveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := MemSave(req.Name, req.Old, req.Content); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "name": req.Name})
}

func handleMemDelete(w http.ResponseWriter, r *http.Request) {
	var req saveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := MemDelete(req.Name); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

func handleSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		N int `json:"n"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	list, err := ListPresets()
	if err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.N < 1 || req.N > len(list) {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "index hors limites"})
		return
	}
	target := list[req.N-1]
	if err := SwitchToPreset(target.Path); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "preset": target.Name})
}

// svcHandler returns an HTTP handler that triggers a start/stop/restart through
// the cross-platform serviceAction (systemd sous Linux, supervision PID-file
// sous Windows — voir service_*.go). C'est ce qui permet à un client distant
// de relancer Jean.
func svcHandler(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := serviceAction(action)
		msg := "ok"
		if err != nil {
			msg = err.Error()
		}
		sendJSON(w, 200, map[string]any{"ok": err == nil, "out": msg})
	}
}

// handleChat is the SSE proxy with tool-calling. The HTTP handler writes raw
// data: lines matching what the embedded JS expects (delta.content,
// delta.reasoning_content, delta.tool_used).
// handleBench runs `runBench` synchronously. Long enough (~30-60s) that we
// rely on the client side to show a spinner / disable the button.
func handleBench(w http.ResponseWriter, r *http.Request) {
	nPrompt, nPredict := 2000, 300
	if v := r.URL.Query().Get("prompt"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			nPrompt = parsed
		}
	}
	if v := r.URL.Query().Get("n"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			nPredict = parsed
		}
	}
	res, err := runBench(nPrompt, nPredict)
	if err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "result": res})
}

// handleBenchLast returns the most recent persisted benchmark, or {ok:false}
// when none has been run yet.
func handleBenchLast(w http.ResponseWriter, r *http.Request) {
	sb := loadLastBench()
	if sb == nil {
		sendJSON(w, 200, map[string]any{"ok": false})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "result": sb.Result, "model": sb.Model, "at": sb.At})
}

// chatReq est le corps d'une requête de chat (commun au chat clair et au chat E2E).
type chatReq struct {
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	// Optional per-request override of the agent mode (used by ajean.link
	// agents, which carry their own toggle). nil = inherit the machine's
	// global config. Tools/Skills sont conservés pour la rétro-compat des
	// anciens clients relais : l'un OU l'autre à true active le mode agent.
	Agent  *bool `json:"agent"`
	Tools  *bool `json:"tools"`
	Skills *bool `json:"skills"`
	// Override par requête de l'accès internet (outils web). nil = config machine.
	Internet *bool `json:"internet"`
	// Taille réelle du contexte au tour précédent (usage.prompt_tokens + tokens
	// générés), rapportée par le client qui l'affiche déjà. Sert à décider du
	// compactage sur le VRAI décompte plutôt qu'une estimation. 0 = inconnu.
	CtxUsed int `json:"ctx_used"`
	// Nouveau modèle « conversation serveur » : Message = texte du tour à lancer
	// (via /api/chat/send) ; From = dernier Seq déjà vu par le client (le flux
	// d'abonnement rejoue Log[From:] puis suit le direct).
	Message string `json:"message"`
	From    int    `json:"from"`
}

// capsFromBody dérive les capacités du tour à partir des overrides éventuels du
// corps de requête (agents ajean.link portant leurs propres toggles), sinon la
// config machine.
func capsFromBody(body chatReq) Caps {
	caps := globalCaps()
	if body.Agent != nil {
		caps.Agent = *body.Agent
	} else if body.Tools != nil || body.Skills != nil {
		caps.Agent = (body.Tools != nil && *body.Tools) || (body.Skills != nil && *body.Skills)
	}
	if body.Internet != nil {
		caps.Internet = *body.Internet && crawlReachable()
	}
	return caps
}

// sseHeartbeat garde la réponse SSE active en écrivant un commentaire (`: ping`,
// ignoré par le parseur côté navigateur, aucun contenu donc rien à chiffrer)
// toutes les ~15 s. Sans ça, un long silence (exécution d'outil en mode agent,
// gros prefill) laisse la réponse inactive et un proxy intermédiaire (Cloudflare,
// ~100 s) la coupe → le fetch navigateur échoue (« Load failed »). Retourne un
// mutex à partager avec l'émetteur (writes concurrents sur le même w) et une
// fonction d'arrêt à différer.
func sseHeartbeat(w http.ResponseWriter, flusher http.Flusher) (*sync.Mutex, func()) {
	mu := &sync.Mutex{}
	done := make(chan struct{})
	go func() {
		// 4 s (et non 15) : borne le temps qu'un dernier bout de flux peut rester
		// coincé dans un buffer proxy (Cloudflare) faute d'octets pour le pousser.
		t := time.NewTicker(4 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				mu.Lock()
				_, err := w.Write([]byte(": ping\n\n"))
				if flusher != nil {
					flusher.Flush()
				}
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()
	return mu, func() { close(done) }
}

// runChatStream est désormais un pur ABONNÉ au journal de la conversation serveur :
// il rejoue Log[body.From:] puis suit le direct, jusqu'à ce que la connexion (ctx)
// se ferme. La GÉNÉRATION est lancée séparément par /api/chat/send dans une
// goroutine détachée — fermer le navigateur n'arrête donc plus rien. Partagé par
// handleChat (clair) et handleE2EChat (chiffré).
func runChatStream(ctx context.Context, body chatReq, emit func(map[string]any) bool) {
	conv.Subscribe(ctx, body.From, emit)
}

// handleChatSend ajoute un message et lance la génération en arrière-plan. Réponse
// req/resp (les événements arrivent par le flux d'abonnement). Passe par le proxy
// tunnel /api/e2e/req pour app.ajean.link — aucun code E2E spécifique requis.
func handleChatSend(w http.ResponseWriter, r *http.Request) {
	var body chatReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if strings.TrimSpace(body.Message) == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "message vide"})
		return
	}
	if err := conv.StartTurn(body.Message, capsFromBody(body), body.Temperature); err != nil {
		// 409 = occupé (génération en cours) ; 503 = modèle pas prêt.
		code := 503
		if err == ErrBusy {
			code = 409
		}
		sendJSON(w, code, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

func handleChatStop(w http.ResponseWriter, r *http.Request) {
	conv.Stop()
	sendJSON(w, 200, map[string]any{"ok": true})
}

func handleChatReset(w http.ResponseWriter, r *http.Request) {
	conv.Reset()
	sendJSON(w, 200, map[string]any{"ok": true})
}

func handleChatState(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, conv.state())
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	var body chatReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	mu, stop := sseHeartbeat(w, flusher)
	defer stop()
	emit := func(obj map[string]any) bool {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": obj}}})
		mu.Lock()
		defer mu.Unlock()
		if _, err := w.Write([]byte("data: " + string(b) + "\n\n")); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	runChatStream(r.Context(), body, emit)
}
