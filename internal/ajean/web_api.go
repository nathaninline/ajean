// web_api.go — handlers REST /api/* (statut, config, presets, modèles,
// mémoire, agent, clés, bench…) du serveur web local.
package ajean

import (
	"encoding/json"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// procBoot marque l'instant de démarrage de CE process (celui qui sert l'UI et
// détient le coffre mémoire déchiffré en RAM). Exposé dans /api/status : quand le
// navigateur voit cette valeur changer, c'est que le serveur a redémarré et que
// le coffre s'est reverrouillé — il relance alors le déverrouillage automatique
// sans qu'on ait à rafraîchir la page. Voir loadStatus côté UI.
var procBoot = time.Now().UnixMilli()

func handlePing(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, map[string]any{"ok": true, "service": "ajean", "version": Version})
}

// handleStatus : état du modèle du chat (service système, services.go).
func handleStatus(w http.ResponseWriter, r *http.Request) {
	state, active, health, external := "inactive", false, false, true
	loadErr := ""
	if sv := explicitChatService(); sv != nil {
		external = !sv.Managed()
		state = osSvcStateOf(sv)
		active = state == "active" || state == "activating" || state == "remote"
		if state == "remote" {
			state = "active"
		}
		health = active && probeReady(sv, 1500*time.Millisecond) == nil
		if !health {
			loadErr = serviceLoadError(sv)
		}
	}
	if health {
		// Modèle prêt : on pré-chauffe le préfixe (système + outils) pour que le
		// premier message d'une conversation ne paie plus son préfill. Détaché :
		// la construction du préfixe peut sonder MCP/Crawl4AI.
		go LLMPrewarm(health)
	}
	sendJSON(w, 200, map[string]any{
		"state":      state,
		"active":     active,
		"health":     health,
		"port":       LLMPort(),
		"ctx":        ctxWindow(), // contexte annoncé par le modèle du chat
		"version":    Version,
		"warn":       joinWarn(appWarning(), legacyWarning()), // ex. App Translocation macOS ; vide si tout va bien
		"load_error": loadErr,                                 // modèle qui ne démarre pas ; vide sinon
		"boot":       procBoot,                                // empreinte de démarrage du process (détecte un redémarrage côté UI)
		"preset":     activePresetID(),                        // modèle du chat : un autre appareil a pu basculer, l'UI se resynchronise (loadStatus)
		"external":   external,                                // le chat tape une API (pas un moteur lancé par AJEAN)
		"jean_mem":   jeanActivityGet(),                       // mémoire de Jean rangée en arrière-plan (jean_activity.go)
	})
}

// vramGPUs échantillonne les GPU (VRAM utilisée/totale, util, température). Extrait
// de handleVram pour être réutilisé tel quel par /api/telemetry : nvidia-smi
// d'abord, repli amd-smi, puis rocm-smi, puis --list-devices Vulkan.
func vramGPUs() []map[string]any {
	vramMu.Lock()
	defer vramMu.Unlock()
	if vramCache != nil && time.Since(vramAt) < 2500*time.Millisecond {
		return vramCache
	}
	vramCache = sampleVramGPUs()
	vramAt = time.Now()
	return vramCache
}

// Cache court PARTAGÉ du relevé GPU : chaque onglet ouvert (PC, téléphone…) sonde
// toutes les 3 s, et un relevé peut coûter cher (sous Windows non-NVIDIA : un
// llama-server --list-devices + un PowerShell). Sans ça, N appareils = N relevés,
// et un relevé plus long que 3 s empilait les requêtes. Le mutex fait aussi
// attendre les appels concurrents au lieu de lancer des relevés en parallèle.
var (
	vramMu    sync.Mutex
	vramCache []map[string]any
	vramAt    time.Time
)

func sampleVramGPUs() []map[string]any {
	if gpuTelemetryOff() {
		return []map[string]any{}
	}
	// Mac Apple Silicon : mémoire unifiée lue via ioreg (voir sys_gpu_apple.go).
	// Avant les replis ci-dessous, qui sur Metal donnaient une barre à zéro à vie
	// et relançaient un llama-server toutes les 3 s.
	if apple := appleGPUs(); len(apple) > 0 {
		return apple
	}
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
	// Aucune carte NVIDIA (nvidia-smi absent ou muet) → on tente amd-smi, pour que
	// les GPU AMD apparaissent aussi dans l'UI (issue #32). Mélanger les deux
	// n'aurait pas de sens sur les rares machines mixtes : nvidia-smi prime, on ne
	// bascule sur AMD que s'il n'a rien donné.
	if len(gpus) == 0 {
		if amd := amdVramGPUs(); len(amd) > 0 {
			gpus = amd
		}
	}
	// Toujours rien ? Beaucoup d'installations ROCm n'ont que `rocm-smi` (pas
	// `amd-smi`) : dernier repli pour que la carte AMD apparaisse quand même dans
	// l'UI au lieu d'un « (pas de GPU) » trompeur (issue #49).
	if len(gpus) == 0 {
		if amd := rocmVramGPUs(); len(amd) > 0 {
			gpus = amd
		}
	}
	return gpus
}

func handleVram(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, vramGPUs())
}

// handleTelemetry regroupe les jauges matérielles (VRAM + RAM) en UN seul appel,
// pour que l'UI ne sonde plus deux endpoints séparés à intervalle rapproché.
// Forme et unités identiques à /api/vram et /api/ram (conservés pour les serveurs
// anciens et le repli côté UI) : {vram: [...], ram: {used, total}}.
func handleTelemetry(w http.ResponseWriter, r *http.Request) {
	used, total := ramUsageMB()
	var gpus any = vramGPUs()
	sendJSON(w, 200, map[string]any{
		"vram": gpus,
		"ram":  map[string]any{"used": used, "total": total},
		"cpu":  cpuTelemetry(),
		"disk": diskTelemetry(),
	})
}

// handleRam renvoie la RAM système {used, total} en Mo (mêmes unités que
// /api/vram → l'UI divise par 1024 pour des Gio). La mesure est spécifique à
// l'OS (ramUsageMB : /proc/meminfo sous Linux, sysctl+vm_stat sur macOS,
// GlobalMemoryStatusEx sous Windows). total=0 → l'UI masque le bloc.
func handleRam(w http.ResponseWriter, r *http.Request) {
	used, total := ramUsageMB()
	sendJSON(w, 200, map[string]any{"used": used, "total": total})
}

func handleConfigEnv(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, ReadConfig())
}

// handleReasoning (POST {effort}) change l'effort de réflexion du modèle du chat
// à chaud : REASONING_EFFORT est relu par applySampling à chaque tour, il prend
// donc effet dès le prochain message, sans redémarrer le modèle. C'est le
// raccourci du composeur pour ajuster le niveau sans rouvrir la fenêtre du
// modèle ; la valeur est aussi mémorisée dans ses réglages (setServiceChatParam).
//
// L'interface ne montre le raccourci que si le modèle a déjà un effort défini :
// sur un serveur qui l'ignore, la valeur n'aurait aucun effet.
//
// Effort accepté : "" (défaut du modèle), low, medium, high, xhigh. Une valeur
// inconnue est refusée pour ne pas glisser n'importe quoi dans chat_template_kwargs.
func handleReasoning(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Effort string `json:"effort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	eff := strings.TrimSpace(req.Effort)
	switch eff {
	case "", "low", "medium", "high", "xhigh":
	default:
		sendJSON(w, 400, map[string]any{"ok": false, "error": "effort inconnu"})
		return
	}
	if err := SetConfigKey("REASONING_EFFORT", eff); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	setServiceChatParam("REASONING_EFFORT", eff)
	sendJSON(w, 200, map[string]any{"ok": true, "effort": eff})
}

// saveReq is the skill editor payload (skills keep name-as-identity + rename).
type saveReq struct {
	Name    string `json:"name"`
	Old     string `json:"old"`
	Content string `json:"content"`
	// Project : projet ciblé (vide = actif). Permet d'éditer/supprimer une page
	// mémoire d'un autre projet depuis la vue « Voir la mémoire ».
	Project string `json:"project"`
}

// handleAgent renvoie l'état du mode agent ET la liste des pages mémoire (que
// l'IA gère via les outils mem_*) — un seul aller-retour pour l'UI. La clé
// "skills" est conservée en miroir de "pages" pour l'ancien portail ajean.link.
func handleAgent(w http.ResponseWriter, r *http.Request) {
	// ?project=<slug> : consulter la mémoire d'un AUTRE projet que l'actif, sans y
	// basculer (bouton « Voir la mémoire » du menu ⋯ d'un projet). Vide/inconnu =
	// projet actif. Seuls les champs mémoire (pages + mode) sont scopés ; le reste
	// (agent/compact) est machine-global.
	var out []map[string]any
	var mode string
	memScope(r.URL.Query().Get("project"), func() {
		for _, p := range MemList() {
			out = append(out, map[string]any{"name": p.Name, "desc": p.Title})
		}
		mode = string(memMode())
	})
	if out == nil {
		out = []map[string]any{}
	}
	sendJSON(w, 200, map[string]any{"compact": compactEnabled(), "compact_max": compactMaxTokens(), "prewarm": prewarmEnabled(), "mem_mode": mode, "computer": computerUseEnabled(), "pages": out, "skills": out})
}

// memScope exécute fn dans le contexte mémoire d'un projet donné (pour voir/éditer
// la mémoire d'un AUTRE projet que l'actif sans basculer dessus). slug vide ou
// inconnu = projet actif (aucun override).
func memScope(slug string, fn func()) {
	slug = strings.TrimSpace(slug)
	if slug == "" || !projectExists(slug) {
		fn()
		return
	}
	withProject(slug, fn)
}

// handleMemoryMode lit/écrit le mode mémoire (off / ondemand / always / search).
//
//	GET  → {mode} du projet actif
//	POST {mode} → persiste le mode mémoire DU PROJET ACTIF (off/ondemand/always/search)
func handleMemoryMode(w http.ResponseWriter, r *http.Request) {
	// project (query en GET, corps en POST) : cibler un AUTRE projet que l'actif
	// (vue « Voir la mémoire »). Vide/inconnu = projet actif.
	proj := strings.TrimSpace(r.URL.Query().Get("project"))
	if r.Method == http.MethodPost {
		var req struct {
			Mode    string `json:"mode"`
			Project string `json:"project"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Project != "" {
			proj = strings.TrimSpace(req.Project)
		}
		// normalizeMemMode valide et ramène toute entrée inconnue à "always".
		m := normalizeMemMode(req.Mode)
		var err error
		memScope(proj, func() { err = setMemMode(m) })
		if err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	mode := ""
	memScope(proj, func() { mode = string(memMode()) })
	sendJSON(w, 200, map[string]any{"ok": true, "mode": mode})
}

// handleCompactToggle active/désactive le compactage automatique du contexte
// (clé COMPACT des réglages). On=compacte (défaut), off=jamais. « max » (facultatif)
// règle le plafond du seuil en jetons (COMPACT_MAX, 0 = aucun) ; absent, il
// n'est pas touché.
func handleCompactToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On  bool `json:"on"`
		Max *int `json:"max"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Max != nil {
		if *req.Max < 0 || *req.Max > 1<<20 {
			sendJSON(w, 400, map[string]any{"ok": false, "error": "plafond hors limites"})
			return
		}
		v := ""
		if *req.Max > 0 {
			v = strconv.Itoa(*req.Max)
		}
		if err := SetConfigKey("COMPACT_MAX", v); err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	val := ""
	if !req.On {
		val = "off"
	}
	if err := SetConfigKey("COMPACT", val); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "compact": compactEnabled(), "compact_max": compactMaxTokens()})
}

// handlePrewarmToggle : interrupteur « pré-chauffage du prompt » (Paramètres).
func handlePrewarmToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := setPrewarmEnabled(req.On); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "prewarm": prewarmEnabled()})
}

// handleComputer pilote le computer use (pilotage d'un navigateur de l'hôte).
//
//	GET  → {enabled, browser, browser_ok, vision}
//	POST {on} → active/désactive (et ferme le navigateur quand on coupe).
func handleComputer(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			On bool `json:"on"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := setComputerUseEnabled(req.On); err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if !req.On {
			cdpShutdown()
		}
	}
	bin := chromePath()
	sendJSON(w, 200, map[string]any{
		"ok":         true,
		"enabled":    computerUseEnabled(),
		"browser":    bin,
		"browser_ok": bin != "",
		"vision":     visionEnabled(),
	})
}

// handleAPIKey : adresse, nom et clé de l'API du modèle du chat (panneau API).
// Lecture seule : la clé appartient au modèle et se règle dans sa fenêtre.
func handleAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "la clé se règle dans la fenêtre du modèle"})
		return
	}
	u, k, model := chatModelAPI()
	sendJSON(w, 200, map[string]any{
		"ok":     true,
		"set":    k != "",
		"key":    k,
		"masked": maskAPIKey(k),
		"url":    u,
		"model":  model,
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

// handleInternet pilote l'accès web de l'IA (moteur intégré).
//
//	GET  → {enabled}
//	POST {enabled} → l'interrupteur.
func handleInternet(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Enabled != nil {
			if err := setInternetEnabled(*req.Enabled); err != nil {
				sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "enabled": internetEnabled()})
}

// ─── Serveurs MCP ────────────────────────────────────────────────────────────
//
// Gestion des serveurs MCP depuis l'UI. Même modèle de protection que le reste
// de /api/* : la clé web est le seul garde. Un serveur MCP stdio exécute du code
// arbitraire sur l'hôte, au même titre que l'outil `bash` du mode agent — donc
// réservé de fait au propriétaire de la machine qui détient la clé.

// mcpSaveReq est le payload d'ajout/édition d'un serveur MCP.
type mcpSaveReq struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Enabled *bool             `json:"enabled"`
}

// handleMCP renvoie l'état de tous les serveurs MCP configurés (connexion tentée
// pour ceux activés → l'UI voit l'état réel + les outils découverts).
func handleMCP(w http.ResponseWriter, r *http.Request) {
	list, err := MCPStatus()
	if err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "servers": list})
}

// handleMCPSave ajoute ou remplace un serveur MCP.
func handleMCPSave(w http.ResponseWriter, r *http.Request) {
	var req mcpSaveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	cfg := MCPServerConfig{
		Command: strings.TrimSpace(req.Command),
		Args:    req.Args,
		Env:     req.Env,
		URL:     strings.TrimSpace(req.URL),
		Headers: req.Headers,
		Enabled: enabled,
	}
	if err := SetMCPServer(req.Name, cfg); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Renvoie l'état à jour (avec tentative de connexion) pour un retour immédiat.
	list, _ := MCPStatus()
	sendJSON(w, 200, map[string]any{"ok": true, "servers": list})
}

// handleMCPDelete retire un serveur MCP.
func handleMCPDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := DeleteMCPServer(req.Name); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleMCPToggle active/désactive un serveur MCP.
func handleMCPToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		On   bool   `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := SetMCPServerEnabled(req.Name, req.On); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	list, _ := MCPStatus()
	sendJSON(w, 200, map[string]any{"ok": true, "servers": list})
}

// handleMCPTool masque/démasque un outil précis d'un serveur.
func handleMCPTool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Tool string `json:"tool"`
		On   bool   `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := SetMCPToolEnabled(req.Name, req.Tool, req.On); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	list, _ := MCPStatus()
	sendJSON(w, 200, map[string]any{"ok": true, "servers": list})
}

// handleMCPTimeoutsGET renvoie les délais MCP configurés (secondes).
func handleMCPTimeouts(w http.ResponseWriter, r *http.Request) {
	t := LoadMCPTimeouts()
	sendJSON(w, 200, map[string]any{
		"ok":      true,
		"connect": t.Connect,
		"call":    t.Call,
	})
}

// handleMCPTimeoutsSave enregistre les délais MCP (secondes) et invalide les
// sessions pour que le nouveau délai s'applique au prochain appel.
func handleMCPTimeoutsSave(w http.ResponseWriter, r *http.Request) {
	var req MCPTimeouts
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Bornes raisonnables : 1 s à 3600 s (1 h).
	if req.Connect < 1 || req.Connect > 3600 {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "connect : 1 à 3600 s"})
		return
	}
	if req.Call < 1 || req.Call > 3600 {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "call : 1 à 3600 s"})
		return
	}
	if err := SaveMCPTimeouts(req); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Invalide toutes les sessions : le délai de connexion change.
	servers, _ := LoadMCPConfig()
	for name := range servers {
		mcpInvalidate(name)
	}
	sendJSON(w, 200, map[string]any{
		"ok":      true,
		"connect": req.Connect,
		"call":    req.Call,
	})
}

// handleMem / handleMemSave / handleMemDelete : éditeur web des pages mémoire
// (memory/<nom>.md). Payload partagé saveReq (name/old/content) ; "name" = nom
// de fichier de la page.
func handleMem(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		sendJSON(w, 200, map[string]any{"name": "", "content": "# nouvelle page\n\nNote ici ce que ajean doit retenir entre les sessions.\n"})
		return
	}
	var c string
	memScope(r.URL.Query().Get("project"), func() { c = MemContent(name) })
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
	var err error
	memScope(req.Project, func() { err = MemSave(req.Name, req.Old, req.Content) })
	if err != nil {
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
	var err error
	memScope(req.Project, func() { err = MemDelete(req.Name) })
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// --- Chiffrement de la mémoire -----------------------------------------------

// handleMemHealth renvoie l'état de santé (chiffré ? verrouillé ? pages lisibles ?
// copies du keyvault, snapshots…). Sans secret, sûr à appeler à tout moment.
func handleMemHealth(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, memHealth())
}

// handleMemEncrypt active le chiffrement. Le corps porte le mot de passe mémoire.
// La réponse contient la CLÉ DE RÉCUPÉRATION, à afficher UNE fois : elle n'est
// jamais reconsultable ensuite.
func handleMemEncrypt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	rec, err := EnableMemEncryption(req.Password)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "recovery": rec, "health": memHealth()})
}

// handleMemDecrypt remet la mémoire en clair (exige d'être déverrouillé).
func handleMemDecrypt(w http.ResponseWriter, r *http.Request) {
	if err := DisableMemEncryption(); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "health": memHealth()})
}

// handleMemUnlock déverrouille la mémoire : accepte le mot de passe OU la clé de
// récupération (on tente les deux formes). Charge la DEK en RAM.
func handleMemUnlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Secret string `json:"secret"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Secret) == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "secret vide"})
		return
	}
	v, err := loadVault()
	if err != nil || v == nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "aucun keyvault (mémoire non chiffrée ?)"})
		return
	}
	dek, kind, err := v.unlockWith(req.Secret)
	if err != nil {
		// La clé de récupération se saisit avec tirets/espaces : on retente normalisée.
		if d2, k2, e2 := v.unlockWith(normalizeRecovery(req.Secret)); e2 == nil {
			dek, kind, err = d2, k2, e2
		}
	}
	if err != nil {
		sendJSON(w, 401, map[string]any{"ok": false, "error": "secret incorrect"})
		return
	}
	setMemDEK(dek)
	resumeMemMigration()           // si une migration attendait le déverrouillage
	reloadEncryptedStores()        // recharge la conversation chiffrée en RAM
	startSlimArchives()            // allègement des anciennes conversations (attendait la clé)
	startHistSearchBackfill()      // index plein-texte des archives (attendait la clé), #98
	go jeanConsolidateArmAtStart() // une consolidation empêchée par le verrou (#159)
	// Migration douce : chiffre les conversations restées en clair (ex. mémoire
	// activée avant que le chiffrement des conversations existe). Idempotent :
	// n'encode que ce qui ne l'est pas encore.
	if memEncActive() && memUnlocked() {
		_ = reencryptChatStores()
	}
	sendJSON(w, 200, map[string]any{"ok": true, "kind": kind, "health": memHealth()})
}

// handleMemAddKey ajoute un nouveau wrap au coffre DÉJÀ OUVERT (DEK en RAM), sans
// re-chiffrer. Sert à la migration douce : quand on déverrouille avec l'ancien
// mot de passe, le client ajoute la clé d'API comme wrap pour que le
// déverrouillage automatique marche ensuite. Zéro-connaissance préservé : le
// serveur ne stocke pas ce secret, il ne fait que l'enfermer dans le coffre.
func handleMemAddKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Secret string `json:"secret"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Secret) == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "secret vide"})
		return
	}
	dek, err := currentDEK()
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "mémoire verrouillée"})
		return
	}
	v, _ := loadVault()
	if v == nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "aucun coffre"})
		return
	}
	if _, _, e := v.unlockWith(req.Secret); e == nil {
		sendJSON(w, 200, map[string]any{"ok": true}) // déjà un wrap pour ce secret
		return
	}
	if err := v.addSecretWrap(dek, wrapPassword, "clé d'api", req.Secret); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := saveVault(v); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleMemLock purge la DEK de la RAM (reverrouille sans redémarrer).
func handleMemLock(w http.ResponseWriter, r *http.Request) {
	clearMemDEK()
	sendJSON(w, 200, map[string]any{"ok": true, "health": memHealth()})
}

// --- Sauvegarde ajean.link ----------------------------------------------------

// handleBackupStatus renvoie l'état de la sauvegarde : liaison configurée ?
// auto activé ? dernière sauvegarde ? versions distantes (best-effort).
func handleBackupStatus(w http.ResponseWriter, r *http.Request) {
	res := map[string]any{
		"linked": backupLinked(),
		"auto":   backupAutoEnabled(),
		"last":   backupLast(),
	}
	if backupLinked() {
		if versions, err := relayBackupList(); err == nil {
			res["versions"] = versions
		} else {
			res["error"] = err.Error()
		}
	}
	sendJSON(w, 200, res)
}

// handleBackupNow lance une sauvegarde immédiate. Aucun mot de passe : le paquet
// est chiffré avec la clé de la mémoire (déjà ouverte).
func handleBackupNow(w http.ResponseWriter, r *http.Request) {
	id, err := RunBackupNow()
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "id": id})
}

// handleBackupRestore restaure une sauvegarde. Corps : {id, machine, secret}.
func handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      string `json:"id"`
		Machine string `json:"machine"`
		Secret  string `json:"secret"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := RestoreBackup(req.ID, req.Machine, req.Secret); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleBackupAuto active/désactive la sauvegarde automatique. Corps : {on}.
func handleBackupAuto(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	val := ""
	if req.On {
		val = "1"
	}
	if err := SetConfigKey("BACKUP_AUTO", val); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "auto": backupAutoEnabled()})
}

// handleMemSnapshots : GET liste les snapshots, POST {id} en restaure un.
func handleMemSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := restoreSnapshot(req.ID); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "snapshots": listSnapshots()})
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

// handleBenchProgress : avancement du test en cours (prefill, tokens générés).
func handleBenchProgress(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, benchProgressGet())
}

// chatReq est le corps d'une requête de chat (commun au chat clair et au chat E2E).
//
// `messages` et `ctx_used`, que les clients envoyaient du temps où l'historique
// vivait dans le navigateur, ont été RETIRÉS : depuis que la conversation est
// possédée par le serveur (chat_conversation.go), plus personne ne les lisait.
// Les garder laissait croire qu'un client pouvait encore imposer son historique
// ou son décompte de contexte. Un client qui les envoie encore n'est pas gêné :
// le décodeur JSON ignore les champs qu'il ne connaît pas.
type chatReq struct {
	Temperature float64 `json:"temperature"`
	// Surcharges par requête, portées par les agents ajean.link qui ont leurs
	// propres interrupteurs. nil = on prend la configuration de la machine.
	// ⚠️ Elles ne peuvent que RESTREINDRE, jamais rallumer (voir capsFromBody).
	// Tools/Skills : rétro-compat des anciens clients relais.
	Agent  *bool `json:"agent"`
	Tools  *bool `json:"tools"`
	Skills *bool `json:"skills"`
	// Surcharge par requête de l'accès internet (outils web).
	Internet *bool `json:"internet"`
	// Surcharge par requête du computer use (outils cu_*).
	Computer *bool `json:"computer"`
	// Fast = mode rapide : le tour tourne comme « ajean chat » (prompt court,
	// bash/write/edit seulement, ni mémoire, ni projet, ni web). Voir capsFromBody.
	Fast bool `json:"fast"`
	// Raw = mode « Modèle de base » : modèle nu, sans agent, outils, mémoire ni
	// prompt système (comme un llama-server direct). Voir capsFromBody.
	Raw bool `json:"raw"`
	// Mode = mode de chat demandé (fast / project / base). La conversation garde
	// celui de son premier message (voir Conversation.lockMode).
	Mode string `json:"mode"`
	// TZ = fuseau IANA du navigateur (« Europe/Paris »), retenu pour dater ce que
	// le modèle planifie ou note. Voir rememberUserTZ.
	TZ string `json:"tz"`
	// Message = texte du tour à lancer (/api/chat/send) ; From = dernier Seq déjà
	// vu par le client (le flux d'abonnement rejoue Log[From:] puis suit le direct).
	Message string `json:"message"`
	// Tail (flux d'abonnement) : nombre de derniers ÉCHANGES à rejouer au
	// chargement ; 0 = tout. nil = ancien client, qui ne sait pas afficher le
	// bouton « messages précédents » : il reçoit toujours tout (voir historyCut).
	Tail *int `json:"tail"`
	From int  `json:"from"`
	// ConvID = id de la conversation actuellement AFFICHÉE par le client (reçu au
	// dernier caught_up/reset du flux). Renvoyé à chaque (re)abonnement pour que le
	// serveur détecte qu'un AUTRE appareil a changé de conversation/projet entre-temps
	// et ordonne un reset avant de rejouer — sinon deux fils fusionnaient à l'écran.
	ConvID string `json:"conv_id"`
	// Files = chemins relatifs des fichiers déposés juste avant par
	// /api/chat/upload ("uploads/rapport.pdf"). Ils sont annoncés au modèle en
	// tête du message (voir attachNote) ; le contenu, lui, reste sur le disque et
	// n'entre dans le contexte que si le modèle décide de le lire.
	Files []string `json:"files"`
}

// joinWarn réunit les avertissements non vides (un par paragraphe).
func joinWarn(ws ...string) string {
	var out []string
	for _, w := range ws {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return strings.Join(out, "\n\n")
}
