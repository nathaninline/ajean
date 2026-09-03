// web_api.go — handlers REST /api/* (statut, config, presets, modèles,
// mémoire, agent, clés, bench…) du serveur web local.
package ajean

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	// Si le modèle n'est pas prêt, on regarde POURQUOI : une tentative de
	// chargement qui a échoué (souvent un modèle incompatible avec le moteur)
	// doit être signalée clairement plutôt que de laisser un « chargement… »
	// perpétuel / un crash-loop muet.
	loadErr := ""
	if !health {
		loadErr = modelLoadError()
	}
	sendJSON(w, 200, map[string]any{
		"state":      state,
		"active":     active,
		"health":     health,
		"port":       LLMPort(),
		"ctx":        ctx,
		"version":    Version,
		"warn":       appWarning(),     // ex. App Translocation macOS — vide si tout va bien
		"load_error": loadErr,          // modèle qui ne charge pas (incompat moteur…) — vide sinon
		"boot":       procBoot,         // empreinte de démarrage du process (détecte un redémarrage côté UI)
		"preset":     activePresetID(), // id du preset actif : un autre appareil a pu basculer, l'UI se resynchronise sans reload (voir loadStatus)
	})
}

// modelLoadError inspecte la fin du journal du service et renvoie un message
// clair quand la DERNIÈRE tentative de chargement du modèle s'est soldée par un
// échec. Le cas le plus courant : un GGUF dont le format de quantification n'est
// pas reconnu par le moteur (BIN) sélectionné — typiquement un modèle ternaire
// qui exige un fork de llama.cpp. Renvoie "" quand le dernier chargement a
// réussi ou qu'aucune tentative n'est visible (évite les faux positifs sur un
// service simplement arrêté).
func modelLoadError() string {
	log := serviceLogTail(200)
	if log == "" {
		return ""
	}
	lines := strings.Split(log, "\n")
	// On ne considère que ce qui suit la DERNIÈRE tentative de chargement.
	start := 0
	for i, l := range lines {
		if strings.Contains(l, "loading model") || strings.Contains(l, "load_model") {
			start = i
		}
	}
	loaded, reason := false, ""
	for _, l := range lines[start:] {
		low := strings.ToLower(l)
		switch {
		case strings.Contains(low, "model loaded"), strings.Contains(low, "server is listening"):
			loaded = true
		case strings.Contains(l, "has offset") && strings.Contains(l, "expected"):
			reason = "format de quantification non reconnu par ce moteur"
		case strings.Contains(low, "unknown model architecture"),
			strings.Contains(low, "unknown ftype"),
			strings.Contains(low, "unknown type"),
			strings.Contains(low, "unsupported"):
			if reason == "" {
				reason = "type/architecture de modèle inconnu de ce moteur"
			}
		case strings.Contains(low, "error loading model"),
			strings.Contains(low, "failed to load model"),
			strings.Contains(low, "failed to read tensor data"):
			if reason == "" {
				reason = "échec du chargement du modèle"
			}
		}
	}
	if loaded || reason == "" {
		return ""
	}
	// Message PRÉCIS selon la cause : n'affirmer l'incompatibilité que quand le
	// journal la montre vraiment (quant/architecture). Un échec générique de
	// chargement peut avoir bien d'autres causes (mémoire, fichier, tenseur) — on
	// renvoie alors vers le journal plutôt que d'accuser à tort le moteur.
	switch reason {
	case "format de quantification non reconnu par ce moteur",
		"type/architecture de modèle inconnu de ce moteur":
		return "Modèle incompatible avec le moteur : " + reason +
			". Choisis un autre backend (édite le modèle → Moteur)."
	default:
		return "Le modèle n'a pas pu être chargé (" + reason +
			"). Ouvre le journal du moteur pour le détail."
	}
}

// handleServiceLog (GET /api/service/log) : dernières lignes du journal du
// service, pour que l'UI puisse montrer POURQUOI le modèle ne se charge pas
// (fichier .gguf illisible, VRAM insuffisante, bibliothèque manquante…) au lieu
// d'un « chargement… » perpétuel sans explication.
func handleServiceLog(w http.ResponseWriter, r *http.Request) {
	n := 80
	if v := r.URL.Query().Get("n"); v != "" {
		if k, err := strconv.Atoi(v); err == nil && k > 0 && k <= 500 {
			n = k
		}
	}
	sendJSON(w, 200, map[string]any{"log": serviceLogTail(n)})
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
	// Toujours rien ? Sous Windows, nvidia-smi/amd-smi/rocm-smi ne sont fournis
	// par AUCUN pilote AMD ou Intel : dernier repli via --list-devices du moteur
	// (Vulkan), qui voit ces cartes sans outil externe. Voir vulkanVramGPUs.
	if len(gpus) == 0 {
		if vk := vulkanVramGPUs(); len(vk) > 0 {
			gpus = vk
		}
	}
	sendJSON(w, 200, gpus)
}

// handleRam renvoie la RAM système {used, total} en Mo (mêmes unités que
// /api/vram → l'UI divise par 1024 pour des Gio). La mesure est spécifique à
// l'OS (ramUsageMB : /proc/meminfo sous Linux, sysctl+vm_stat sur macOS,
// GlobalMemoryStatusEx sous Windows). total=0 → l'UI masque le bloc.
func handleRam(w http.ResponseWriter, r *http.Request) {
	used, total := ramUsageMB()
	out := map[string]any{"used": used, "total": total}
	// Part du total qui revient à ajean lui-même et à son moteur (Windows
	// seulement pour l'instant, voir web_procmem_windows.go) : le total système
	// seul ne dit jamais si une machine qui sature est due au modèle chargé ou à
	// autre chose qui tourne à côté.
	pr := currentProcessRAM()
	if pr.HasAjean {
		out["ajean_used"] = pr.AjeanMiB
	}
	if pr.HasLlama {
		out["llama_used"] = pr.LlamaMiB
	}
	sendJSON(w, 200, out)
}

func handleConfigEnv(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, ReadConfig())
}

// handleReasoning (POST {effort}) change l'effort de réflexion de la config
// ACTIVE à chaud : REASONING_EFFORT est relu par applySampling à chaque tour, il
// prend donc effet dès le prochain message, sans redémarrage du moteur. C'est le
// raccourci du composeur (à côté du bouton « postes distants ») pour ajuster le
// niveau sans rouvrir l'éditeur de preset.
//
// La valeur n'a d'effet réel que si le moteur tourne avec --jinja ; on ne montre
// le raccourci côté UI que lorsque le preset a déjà défini un effort (donc --jinja
// posé), et on n'ajoute rien ici — écrire --jinja imposerait un redémarrage, tout
// l'intérêt du raccourci étant justement de l'éviter.
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
	sendJSON(w, 200, map[string]any{"ok": true, "effort": eff})
}

// handleBackends scans AJEAN_HOME/backends/<name>/ for a llama-server binary,
// trying common build subpaths (build/bin, build-sm120/bin, bin, .).
// Returns [{name, path}].
func handleBackends(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, listBackendBins())
}

// listBackendBins scanne AJEAN_HOME/backends/<name>/ pour un binaire llama-server
// (essaie les sous-chemins des différents générateurs CMake). Renvoie
// [{name, path}] — un par dossier de backend contenant un binaire.
func listBackendBins() []map[string]any {
	root := AjeanHome() + "/backends"
	entries, err := os.ReadDir(root)
	if err != nil {
		return []map[string]any{}
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
	return out
}

// handleBackendsCustom liste les seuls backends CUSTOM : les dossiers de
// backends/ hors moteurs gérés par les cartes ⚡ (prebuilt) et 🔧 (build
// canonique). Marque celui qui sert de moteur au modèle actif (in_use).
func handleBackendsCustom(w http.ResponseWriter, r *http.Request) {
	canonical := filepath.Base(defaultRepoDir()) // "llama.cpp"
	prebuilt := filepath.Base(prebuiltDir())     // "llama.cpp-prebuilt"
	cfgBin := ReadConfig()["BIN"]
	out := []map[string]any{}
	for _, b := range listBackendBins() {
		name, _ := b["name"].(string)
		if name == canonical || name == prebuilt {
			continue
		}
		path, _ := b["path"].(string)
		out = append(out, map[string]any{"name": name, "path": path, "in_use": samePath(path, cfgBin)})
	}
	sendJSON(w, 200, out)
}

// handleLlamacppUninstallCustom supprime le dossier d'un backend custom
// (backends/<name>). Refuse les moteurs gérés (canonique / prebuilt) et un
// backend actuellement utilisé par le modèle actif (il faut d'abord basculer de
// moteur pour ce modèle).
func handleLlamacppUninstallCustom(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	name := sanitizeBackendName(req.Name)
	if name == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "nom requis"})
		return
	}
	if name == filepath.Base(defaultRepoDir()) || name == filepath.Base(prebuiltDir()) {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "ce moteur se gère depuis les cartes ⚡ / 🔧"})
		return
	}
	dir, err := backendDir(name)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !isDir(dir) {
		sendJSON(w, 404, map[string]any{"ok": false, "error": "backend introuvable"})
		return
	}
	cfgBin := ReadConfig()["BIN"]
	for _, b := range listBackendBins() {
		if b["name"] == name {
			if p, _ := b["path"].(string); samePath(p, cfgBin) {
				sendJSON(w, 409, map[string]any{"ok": false, "error": "backend utilisé par le modèle actif — bascule d'abord ce modèle sur un autre moteur"})
				return
			}
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleModels lists *.gguf files (size in bytes) for the preset editor's model
// picker, dans AJEAN_HOME ET dans les dossiers supplémentaires déclarés (disque
// externe…). "value" est ce qu'il faut écrire dans MODEL= : un simple nom de
// fichier pour AJEAN_HOME (compatibilité avec l'existant), le chemin complet
// pour un dossier externe.
func handleModels(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	seen := map[string]bool{}
	home := AjeanHome()
	for _, dir := range modelDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		isHome := normDir(dir) == normDir(home)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
				continue
			}
			// Modèle découpé : seule la première tranche est un modèle lançable, les
			// suivantes sont ouvertes par llama-server tout seul. Les lister toutes
			// donnait trois entrées pour un modèle, dont deux qui échouent au démarrage.
			if isFollowerShard(e.Name()) {
				continue
			}
			full := filepath.Join(dir, e.Name())
			if seen[normDir(full)] {
				continue
			}
			seen[normDir(full)] = true
			info, _ := e.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			value := full
			if isHome {
				value = e.Name()
			}
			m := map[string]any{
				"name": e.Name(), "size": size, "value": value,
				"path": full, "dir": dir, "home": isHome,
			}
			// Taille annoncée = la famille entière, et on signale les tranches
			// manquantes : un modèle incomplet démarre puis meurt sur un tenseur
			// introuvable, autant le voir avant de le sélectionner.
			if _, total, ok := shardInfo(e.Name()); ok {
				m["shards"] = total
				m["size"] = shardFamilySize(dir, e.Name())
				if missing := shardFamilyMissing(dir, e.Name()); len(missing) > 0 {
					m["missing"] = missing
				}
			}
			out = append(out, m)
		}
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
			if c := presetCtx(content); c != "" {
				item["ctx"] = c
			}
			if r := presetReasoning(content); reasoningActive(r) {
				item["reasoning"] = strings.ToLower(r)
			}
			// Vision : le preset charge un projecteur multimodal (MMPROJ) → il sait
			// recevoir des images. L'UI affiche un œil pour le repérer sans ouvrir
			// les options (issue #35).
			if strings.TrimSpace(parseEnv(content)["MMPROJ"]) != "" {
				item["vision"] = true
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

// handlePresetsOrder enregistre l'ordre d'affichage des presets (drag & drop de
// l'UI). On stocke la liste d'IDs telle quelle ; ListPresets s'en sert ensuite.
func handlePresetsOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := savePresetOrder(req.IDs); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

func handlePreset(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		sendJSON(w, 200, map[string]any{"id": "", "name": "", "content": formatEnv(newPresetSeed())})
		return
	}
	content, err := ReadPreset(id)
	if err != nil {
		sendJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	sendJSON(w, 200, map[string]any{"id": id, "name": presetDisplayName(content, id), "content": content, "sysprompt": presetSysPrompt(id)})
}

// presetSaveReq is the preset editor payload. `id` identifies an existing
// preset to update ("" creates a new one); `name` is the display name.
type presetSaveReq struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	SysPrompt   string `json:"sysprompt"` // prompt système propre au preset (peut être vide)
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
	// Prompt système propre au preset (stocké en base, sous l'id retourné).
	_ = setPresetSysPrompt(newID, req.SysPrompt)
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
	deletePresetSysPrompt(req.ID) // nettoie le prompt système associé
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
	sendJSON(w, 200, map[string]any{"enabled": agentEnabled(), "compact": compactEnabled(), "machines": machinesEnabled(), "mem_mode": string(memMode()), "pages": out, "skills": out})
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

// handleMachinesToggle active/désactive la gestion autonome des machines
// (config.env MACHINES). Off par défaut : sans ça, ni outils machines_* ni bloc
// de prompt. Miroir de handleCompactToggle mais défaut inverse.
func handleMachinesToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	val := ""
	if req.On {
		val = "on"
	}
	if err := SetConfigKey("MACHINES", val); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "machines": machinesEnabled()})
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
//	POST {enabled, url, key} → enregistre CRAWL4AI_URL / CRAWL4AI_KEY + le
//	drapeau .internet_enabled. La clé n'est jamais relue par GET (juste un indice).
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
			Key     *string `json:"key"`
			Engine  *string `json:"engine"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		// Moteur web : "go" (intégré, rien à installer) ou "crawl4ai" (serveur
		// externe, rendu JavaScript).
		if req.Engine != nil {
			if err := setWebEngine(strings.ToLower(strings.TrimSpace(*req.Engine))); err != nil {
				sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		}
		// Clé d'accès au serveur Crawl4AI : chaîne vide = on l'enlève.
		if req.Key != nil {
			if err := writeCrawlKey(strings.TrimSpace(*req.Key)); err != nil {
				sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			reachMu.Lock()
			reachURL = ""
			reachMu.Unlock()
		}
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
	// La clé n'est JAMAIS renvoyée en clair : l'UI n'a besoin que de savoir
	// qu'elle existe (et de ses 4 derniers caractères pour la reconnaître).
	key, hint := crawl4aiKey(), ""
	if n := len([]rune(key)); n > 4 {
		hint = "…" + string([]rune(key)[n-4:])
	} else if key != "" {
		hint = "…"
	}
	sendJSON(w, 200, map[string]any{
		"ok":        true,
		"enabled":   internetEnabled(),
		"engine":    webEngine(),
		"url":       crawl4aiURL(),
		"reachable": crawlReachable(),
		"key_set":   key != "",
		"key_hint":  hint,
	})
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

// handleMCPTest force une reconnexion d'un serveur et renvoie son état (bouton
// « tester/rafraîchir » de l'UI).
func handleMCPTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	mcpInvalidate(req.Name)
	list, err := MCPStatus()
	if err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "servers": list})
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
	resumeMemMigration()    // si une migration attendait le déverrouillage
	reloadEncryptedStores() // recharge la conversation chiffrée en RAM
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

// handleBackupRestore restaure une sauvegarde. Corps : {id, secret}.
func handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := RestoreBackup(req.ID, req.Secret); err != nil {
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
	// On écrit config.env TOUT DE SUITE (c'est lui qui décide du preset actif),
	// puis on répond — le redémarrage du service part en arrière-plan. Passer par
	// SwitchToPreset bloquait la réponse pendant tout l'arrêt de llama-server plus
	// les 2 s de vérification de checkStarted : côté UI, le clic paraissait mou et
	// la sélection ne bougeait qu'au bout de plusieurs secondes, pour rien.
	if err := applyPresetFile(target.Path); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	fmt.Printf("%s config.env <- %s\n", green("[ok]"), filepath.Base(target.Path))
	fmt.Println(dim("[info] redémarrage du service..."))
	go func() {
		if err := serviceAction("restart"); err != nil {
			fmt.Printf("%s redémarrage après bascule: %v\n", red("[ERREUR]"), err)
		}
	}()
	sendJSON(w, 200, map[string]any{"ok": true, "preset": target.Name})
}

// svcHandler returns an HTTP handler that triggers a start/stop/restart through
// the cross-platform serviceAction (systemd sous Linux, supervision PID-file
// sous Windows — voir sys_service_*.go). C'est ce qui permet à un client distant
// de relancer AJEAN.
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
	// Message = texte du tour à lancer (/api/chat/send) ; From = dernier Seq déjà
	// vu par le client (le flux d'abonnement rejoue Log[From:] puis suit le direct).
	Message string `json:"message"`
	From    int    `json:"from"`
	// Files = chemins relatifs des fichiers déposés juste avant par
	// /api/chat/upload ("uploads/rapport.pdf"). Ils sont annoncés au modèle en
	// tête du message (voir attachNote) ; le contenu, lui, reste sur le disque et
	// n'entre dans le contexte que si le modèle décide de le lire.
	Files []string `json:"files"`
}
