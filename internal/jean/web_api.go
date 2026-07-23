// web_api.go — handlers REST /api/* (statut, config, presets, modèles,
// mémoire, agent, clés, bench…) du serveur web local.
package jean

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func handlePing(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, map[string]any{"ok": true, "service": "jean", "version": Version})
}

// handleStatus reports service state cross-platform via serviceIsActive
// (systemd sous Linux, supervision par PID-file sous Windows — voir sys_service_*.go).
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

// handleRam renvoie la RAM système {used, total} en Mo (mêmes unités que
// /api/vram → l'UI divise par 1024 pour des Gio). Lit /proc/meminfo (Linux, la
// box de prod). Sur un OS sans /proc, renvoie total=0 → l'UI masque le bloc.
func handleRam(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"used": 0, "total": 0}
	b, err := os.ReadFile("/proc/meminfo")
	if err == nil {
		var totalKB, availKB int
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			v, _ := strconv.Atoi(f[1]) // valeur en kB
			switch f[0] {
			case "MemTotal:":
				totalKB = v
			case "MemAvailable:":
				availKB = v
			}
		}
		if totalKB > 0 {
			out["total"] = totalKB / 1024
			out["used"] = (totalKB - availKB) / 1024
		}
	}
	sendJSON(w, 200, out)
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
//
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
// sous Windows — voir sys_service_*.go). C'est ce qui permet à un client distant
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
