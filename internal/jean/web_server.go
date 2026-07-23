package jean

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:generate go run ../../tools/assemble-ui ui
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
	// api enregistre une route /api/* protégée par la clé de pilotage (web_auth.go).
	api := func(path string, h http.HandlerFunc) { mux.HandleFunc(path, requireWebAuth(h)) }
	api("/api/ping", handlePing)
	api("/api/status", handleStatus)
	api("/api/vram", handleVram)
	api("/api/ram", handleRam)
	api("/api/config", handleConfigEnv)
	api("/api/catalog", handleCatalog)
	api("/api/update", handleUpdateCheck)
	api("/api/update/apply", handleUpdateApply)
	api("/api/models", handleModels)
	api("/api/models/delete", handleModelDelete)
	api("/api/models/download", handleModelDownload)
	api("/api/models/download/status", handleModelDownloadStatus)
	api("/api/backends", handleBackends)
	api("/api/llamacpp", handleLlamacpp)                             // statut du backend llama.cpp
	api("/api/llamacpp/check", handleLlamacppCheck)                  // git fetch + retard sur origin
	api("/api/llamacpp/install", handleLlamacppInstall)              // job : clone + build + BIN
	api("/api/llamacpp/update", handleLlamacppUpdate)                // job : pull + rebuild + restart
	api("/api/llamacpp/job", handleLlamacppJob)                      // progression + logs du job
	api("/api/llamacpp/prebuilt", handleLlamacppPrebuilt)            // job : binaires officiels précompilés
	api("/api/llamacpp/prebuilt/check", handleLlamacppPrebuiltCheck) // dernière release officielle vs installée
	api("/api/llamacpp/use", handleLlamacppUse)                      // bascule BIN entre versions déjà installées
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
	api("/api/sysprompt", handleSysPrompt)
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
	api("/api/chat", handleChat)                // flux d'ABONNEMENT (SSE) : rejoue + suit le fil
	api("/api/chat/send", handleChatSend)       // envoie un message (lance la génération détachée)
	api("/api/chat/stop", handleChatStop)       // interrompt la génération en cours
	api("/api/chat/reset", handleChatReset)     // nouvelle conversation (pour tous les appareils)
	api("/api/chat/compact", handleChatCompact) // compaction manuelle du contexte
	api("/api/chat/state", handleChatState)     // instantané léger {seq, generating, ctx_used}
	api("/api/e2e/chat", handleE2EChat)         // même flux mais chiffré E2E (boîte noire via le relais)
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
	// Arrêt poli d'abord, puis forcé si le port ne se libère pas.
	killPid(pid, false)
	for i := 0; i < 15; i++ {
		time.Sleep(200 * time.Millisecond)
		if p, _ := pidOnPort(port); p == 0 {
			fmt.Printf("%s process %d terminé, redémarrage…\n", green("[ok]"), pid)
			return true
		}
	}
	killPid(pid, true)
	time.Sleep(500 * time.Millisecond)
	if p, _ := pidOnPort(port); p != 0 {
		fmt.Printf("%s impossible de libérer le port %d (PID %d toujours présent)\n", red("[err]"), port, p)
		return false
	}
	fmt.Printf("%s process %d terminé (forcé), redémarrage…\n", green("[ok]"), pid)
	return true
}

// killPid termine un process : kill TERM/KILL sous Unix, taskkill sous Windows
// (où il n'existe pas d'arrêt « poli » générique — taskkill sans /F échoue sur
// les process console, donc le second essai passe en forcé).
func killPid(pid int, force bool) {
	if runtime.GOOS == "windows" {
		args := []string{"/PID", strconv.Itoa(pid)}
		if force {
			args = append(args, "/F")
		}
		_ = exec.Command("taskkill", args...).Run()
		return
	}
	sig := "-TERM"
	if force {
		sig = "-KILL"
	}
	_ = exec.Command("kill", sig, strconv.Itoa(pid)).Run()
}

// pidOnPort returns the PID and command name of the process listening on the
// given TCP port, via `ss` (Linux) with an `lsof` fallback, or `netstat -ano`
// on Windows. Returns 0 if none is found or if the tools can't see it (e.g.
// owned by another user).
func pidOnPort(port int) (int, string) {
	if runtime.GOOS == "windows" {
		// netstat -ano : "  TCP    0.0.0.0:8090   0.0.0.0:0   LISTENING   1234"
		out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
		if err != nil {
			return 0, ""
		}
		suffix := ":" + strconv.Itoa(port)
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) >= 5 && f[0] == "TCP" && strings.HasSuffix(f[1], suffix) && f[3] == "LISTENING" {
				if pid, err := strconv.Atoi(f[4]); err == nil && pid > 0 {
					return pid, processName(pid)
				}
			}
		}
		return 0, ""
	}
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
	if runtime.GOOS == "windows" {
		// tasklist CSV : "jean.exe","1234","Console","1","12 345 K"
		out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").Output()
		if err == nil {
			if f := strings.SplitN(strings.TrimSpace(string(out)), "\",\"", 2); len(f) == 2 {
				return strings.TrimPrefix(f[0], "\"")
			}
		}
		return "?"
	}
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
