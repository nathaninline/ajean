package ajean

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
//go:embed ui/index.html ui/marked.min.js ui/sw.js ui/manifest.webmanifest
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
	fmt.Printf("[ajean web] http://%s  (Ctrl-C pour arrêter)\n", addr)
	if !webKeyConfigured() {
		fmt.Printf("%s API de pilotage NON protégée (aucune clé). Avant de l'exposer sur internet :\n", yellow("[!]"))
		fmt.Printf("       %s\n", bold("ajean set-web-key"))
	} else {
		fmt.Printf("%s API protégée par clé (Authorization: Bearer …)\n", green("[ok]"))
	}

	// Accès distant : le tunnel vers le relais est ouvert ICI, dans le process qui
	// sert déjà l'UI, et avec le MÊME mux. C'est la condition d'une conversation
	// unique — elle vit en mémoire (voir chat_conversation.go), donc deux process
	// qui la servent, c'est deux fils qui divergent et s'écrasent l'un l'autre.
	// Sans jeton enregistré, startAppLink ne fait rien : l'UI locale suffit.
	appOwnsLink = true
	appWebMux = mux
	if readLinkToken() != "" {
		killForeignUIWorker() // rescapé d'une version antérieure ou d'une autre copie
		fmt.Printf("%s connexion au relais %s …\n", cyan("[link]"), relayURL())
		if fp := e2eFingerprint(); fp != "" {
			fmt.Printf("%s empreinte E2E : %s   (code d'appairage : ajean link code)\n", green("[e2e]"), bold(fp))
		}
		fmt.Printf("%s front OpenAI public prêt (activation en direct via l'UI ; état: %v)\n", green("[oai]"), oaiPublicEnabled())
		startAppLink(mux)
	}
	// ReadHeaderTimeout : sans lui, une connexion qui n'envoie jamais sa requête
	// immobilise une goroutine pour toujours — et ce port écoute sur 0.0.0.0.
	// Surtout PAS de WriteTimeout ici : il couperait les flux SSE du chat, qui
	// restent ouverts aussi longtemps que l'utilisateur regarde la page.
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

// newWebMux construit le routeur HTTP de l'UI web. Extrait de cmdWeb pour être
// réutilisé par `ajean link`, qui sert ce même mux à travers le tunnel sans
// repasser par un écouteur TCP local.
var convLoadOnce sync.Once

func newWebMux() *http.ServeMux {
	// Charge l'état de conversation persisté (une fois par process : ajean web ET
	// ajean link serve appellent newWebMux).
	convLoadOnce.Do(func() {
		// Amorce le projet « Générale » et migre l'existant (mémoire plate + sessions
		// + keyvault) AVANT de charger la conversation, pour que memoryDir() et le
		// projet actif soient corrects dès le premier accès.
		ensureDefaultProject()
		LoadConversation()
	})
	// Pré-chauffe les serveurs MCP en tâche de fond : sinon le handshake (plusieurs
	// secondes pour un serveur lancé via npx) est payé par le premier message.
	MCPPrewarm()
	// Un téléchargement de modèle coupé net (crash, restart du service) laisse un
	// .part orphelin non reprenable : on nettoie au démarrage.
	cleanStalePartFiles()
	// Idem pour un envoi de fichier coupé en plein transfert : les sessions vivent
	// en mémoire, aucun .part ne survit utilement à l'arrêt du process.
	cleanStaleUploadParts()
	// Idem pour une installation de moteur : elle meurt avec le process. On
	// recharge son état pour l'annoncer « interrompue » au lieu de n'afficher
	// plus rien du tout.
	lcRestoreOnce.Do(lcRestore)
	// Reprend une migration de chiffrement mémoire interrompue (crash/coupure).
	// Sans DEK en RAM (démarrage à froid), laisse le journal en place : un
	// déverrouillage ultérieur la reprendra. Ne perd jamais de données.
	resumeMemMigration()
	// La clé de pilotage n'est plus stockée en clair (juste son empreinte) : on
	// convertit une ancienne valeur en clair une fois pour toutes.
	migrateWebKeyToHash()
	// Boucle de sauvegarde automatique vers ajean.link (no-op tant que non activée
	// / non liée / non armée depuis le démarrage).
	StartBackupScheduler()
	mux := http.NewServeMux()
	// Pages publiques : le HTML et le JS ne contiennent aucun secret. Toute la
	// donnée et toutes les actions passent par /api/* qui, lui, exige la clé.
	mux.HandleFunc("/", handleIndex)
	// Poste distant : deux routes PUBLIQUES (pas derrière la clé de pilotage).
	// L'enrôlement est authentifié par le code d'appairage à usage unique, et le
	// WebSocket par la clé d'appareil (Bearer) — le poste ne connaît pas la clé
	// de pilotage. Voir node_api.go / node_server.go.
	mux.HandleFunc("/api/node/enroll", handleNodeEnroll)
	mux.HandleFunc("/api/node/ws", handleNodeWS)
	mux.HandleFunc("/marked.min.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := uiFS.ReadFile("ui/marked.min.js")
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(b)
	})
	// Service worker + manifeste des notifications Web Push (voir push.go / sw.js).
	// PUBLICS (aucun secret) et servis en clair à la RACINE : un service worker doit
	// venir de l'origine même, et son scope est celui de son URL. no-store sur le SW
	// pour qu'une mise à jour du worker soit toujours reprise (pas de cache figé).
	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := uiFS.ReadFile("ui/sw.js")
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Write(b)
	})
	mux.HandleFunc("/manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		b, _ := uiFS.ReadFile("ui/manifest.webmanifest")
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(b)
	})
	// api enregistre une route /api/* protégée par la clé de pilotage (web_auth.go).
	api := func(path string, h http.HandlerFunc) { mux.HandleFunc(path, requireWebAuth(h)) }
	api("/api/ping", handlePing)
	api("/api/status", handleStatus)
	api("/api/service/log", handleServiceLog) // journal du service pour diagnostiquer un modèle qui ne charge pas
	api("/api/vram", handleVram)
	api("/api/ram", handleRam)
	api("/api/config", handleConfigEnv)
	api("/api/reasoning", handleReasoning) // change l'effort de réflexion à chaud (raccourci composeur)
	api("/api/catalog", handleCatalog)
	api("/api/paths", handlePaths)
	api("/api/update", handleUpdateCheck)
	api("/api/update/apply", handleUpdateApply)
	api("/api/models", handleModels)
	api("/api/models/delete", handleModelDelete)
	api("/api/models/dirs", handleModelDirs) // dossiers de modèles (disque externe…)
	api("/api/models/download", handleModelDownload)
	api("/api/models/download/probe", handleModelDownloadProbe) // taille + espace libre avant de lancer
	api("/api/models/download/status", handleModelDownloadStatus)
	api("/api/models/download/cancel", handleModelDownloadCancel)
	api("/api/backends", handleBackends)
	api("/api/backends/custom", handleBackendsCustom)                    // backends custom uniquement (hors ⚡/🔧)
	api("/api/backends/devices", handleBackendDevices)                   // GPU vus par CE moteur (noms/ordre propres au backend)
	api("/api/llamacpp", handleLlamacpp)                                 // statut du backend llama.cpp
	api("/api/llamacpp/check", handleLlamacppCheck)                      // git fetch + retard sur origin
	api("/api/llamacpp/install", handleLlamacppInstall)                  // job : clone + build + BIN
	api("/api/llamacpp/install-custom", handleLlamacppInstallCustom)     // job : clone d'un fork depuis une URL Git (par preset, sans BIN global)
	api("/api/llamacpp/uninstall-custom", handleLlamacppUninstallCustom) // supprime un backend custom (backends/<name>)
	api("/api/llamacpp/update", handleLlamacppUpdate)                    // job : pull + rebuild + restart
	api("/api/llamacpp/job", handleLlamacppJob)                          // progression + logs du job
	api("/api/llamacpp/job/dismiss", handleLlamacppJobDismiss)           // masque un job terminé (l'erreur ne revient plus au démarrage)
	api("/api/llamacpp/prebuilt", handleLlamacppPrebuilt)                // job : binaires officiels précompilés
	api("/api/llamacpp/prebuilt/check", handleLlamacppPrebuiltCheck)     // dernière release officielle vs installée
	api("/api/llamacpp/use", handleLlamacppUse)                          // bascule BIN entre versions déjà installées
	api("/api/presets", handlePresets)
	api("/api/presets/order", handlePresetsOrder)
	api("/api/preset", handlePreset)
	api("/api/preset/save", handlePresetSave)
	api("/api/preset/delete", handlePresetDelete)
	api("/api/agent", handleAgent)
	api("/api/agent/toggle", handleAgentToggle)
	api("/api/agent/compact", handleCompactToggle)
	api("/api/agent/machines", handleMachinesToggle)
	api("/api/apikey", handleAPIKey)
	api("/api/oai/public", handleOAIPublic)
	api("/api/link/status", handleLinkStatus)         // état de l'accès distant (ajean.link)
	api("/api/link/connect", handleLinkConnect)       // clé de liaison remise par connect.html → ajean link
	api("/api/link/start", handleLinkStart)           // (re)démarre le tunnel avec la clé déjà enregistrée
	api("/api/link/disconnect", handleLinkDisconnect) // arrête le lien + oublie la clé
	api("/api/link/paircode", handleLinkPairCode)     // code d'appairage + empreinte pour la 1re connexion
	// Postes distants : gestion réservée au propriétaire (clé de pilotage).
	api("/api/node", handleNodes)             // liste + état de connexion
	api("/api/node/pair", handleNodePair)     // génère un code d'appairage
	api("/api/node/caps", handleNodeCaps)     // règle capacités + dossier racine
	api("/api/node/target", handleNodeTarget) // choisit la machine cible de l'agent
	api("/api/node/revoke", handleNodeRevoke) // oublie la clé + déconnecte
	api("/api/internet", handleInternet)
	api("/api/mcp", handleMCP)
	api("/api/mcp/save", handleMCPSave)
	api("/api/mcp/delete", handleMCPDelete)
	api("/api/mcp/toggle", handleMCPToggle)
	api("/api/mcp/tool", handleMCPTool)
	api("/api/mcp/test", handleMCPTest)
	api("/api/memory", handleMemoryMode)
	api("/api/network", handleNetwork) // écoute LAN du moteur + pare-feu (Windows)
	api("/api/prefs", handleWebPrefs)
	api("/api/sysprompt", handleSysPrompt)
	// Alias rétro-compat : l'ancien portail ajean.link (dépôt ajean-relay) pilote
	// encore l'agent via /api/tools* et /api/skills/toggle à travers le tunnel E2E.
	// On les mappe sur le mode agent unifié le temps que le portail soit mis à jour.
	api("/api/tools", handleAgent)
	api("/api/tools/toggle", handleAgentToggle)
	api("/api/skills", handleAgent)
	api("/api/skills/toggle", handleAgentToggle)
	api("/api/projects", handleProjects)                        // liste des projets + projet actif
	api("/api/projects/create", handleProjectCreate)            // crée un projet
	api("/api/projects/rename", handleProjectRename)            // renomme (libellé)
	api("/api/projects/delete", handleProjectDelete)            // supprime (dossier + sessions)
	api("/api/projects/switch", handleProjectSwitch)            // bascule le projet actif (nouvelle session)
	api("/api/projects/describe", handleProjectDescribe)        // description du projet (fournie à l'IA)
	api("/api/projects/move-session", handleProjectMoveSession) // déplace une conversation vers un autre projet (#55)
	api("/api/projects/move-mem", handleProjectMoveMem)         // déplace une page mémoire vers un autre projet (#55)
	api("/api/tracker", handleTracker)                          // liste des trackers (données datées) du projet actif
	api("/api/tracker/events", handleTrackerEvents)             // événements d'un tracker (pour l'UI)
	api("/api/tracker/add", handleTrackerAdd)                   // ajoute un point
	api("/api/tracker/edit", handleTrackerEdit)                 // modifie un point
	api("/api/tracker/delete", handleTrackerDelete)             // supprime un point (ou le tracker entier)
	api("/api/tracker/rename", handleTrackerRename)             // renomme un tracker
	api("/api/tracker/move", handleTrackerMove)                 // déplace un tracker vers un autre projet
	api("/api/mem", handleMem)
	api("/api/mem/save", handleMemSave)
	api("/api/mem/delete", handleMemDelete)
	api("/api/mem/health", handleMemHealth)         // état chiffrement/verrou/pages/snapshots
	api("/api/mem/encrypt", handleMemEncrypt)       // active le chiffrement (renvoie la clé de récupération)
	api("/api/mem/decrypt", handleMemDecrypt)       // remet la mémoire en clair
	api("/api/mem/unlock", handleMemUnlock)         // déverrouille (mot de passe ou clé de récupération)
	api("/api/mem/addkey", handleMemAddKey)         // ajoute un wrap (ex. clé d'API) au coffre déjà ouvert
	api("/api/mem/lock", handleMemLock)             // reverrouille (purge la DEK de la RAM)
	api("/api/mem/snapshots", handleMemSnapshots)   // liste + restauration des snapshots locaux
	api("/api/backup/status", handleBackupStatus)   // état sauvegarde ajean.link
	api("/api/backup/now", handleBackupNow)         // sauvegarde immédiate (abonné)
	api("/api/backup/restore", handleBackupRestore) // restauration depuis le relais
	api("/api/backup/auto", handleBackupAuto)       // active/désactive l'auto
	api("/api/switch", handleSwitch)
	api("/api/start", svcHandler("start"))
	api("/api/stop", svcHandler("stop"))
	api("/api/restart", svcHandler("restart"))
	api("/api/bench", handleBench)
	api("/api/bench/last", handleBenchLast)
	api("/api/chat", handleChat)                               // flux d'ABONNEMENT (SSE) : rejoue + suit le fil
	api("/api/chat/send", handleChatSend)                      // envoie un message (lance la génération détachée)
	api("/api/chat/upload", handleChatUpload)                  // dépose un fichier dans le workspace agent (joint au message suivant)
	api("/api/chat/file", handleChatFile)                      // télécharge un fichier produit par l'agent (dossier de travail only)
	api("/api/chat/stop", handleChatStop)                      // interrompt la génération en cours
	api("/api/chat/reset", handleChatReset)                    // nouvelle conversation (archive la courante dans l'historique)
	api("/api/chat/history", handleChatHistory)                // liste des conversations archivées (?project= pour un autre projet, lecture seule)
	api("/api/chat/peek", handleChatPeek)                      // contenu d'une conversation archivée, LECTURE SEULE (ne restaure pas)
	api("/api/chat/history/restore", handleChatHistoryRestore) // recharge une conversation archivée
	api("/api/chat/history/delete", handleChatHistoryDelete)   // supprime définitivement une archive
	api("/api/chat/history/rename", handleChatHistoryRename)   // renomme une conversation archivée
	api("/api/chat/history/fav", handleChatHistoryFav)         // épingle/dépingle en favori
	api("/api/chat/history/clear", handleChatHistoryClear)     // supprime tout sauf les favoris
	api("/api/chat/compact", handleChatCompact)                // compaction manuelle du contexte
	api("/api/chat/state", handleChatState)                    // instantané léger {seq, generating, ctx_used}
	api("/api/chat/export", handleChatExport)                  // téléchargement du fil (?format=md|json)
	// /api/e2e/chat s'authentifie LUI-MÊME (e2eAuthOpenReq) : pas de requireWebAuth
	// (sinon 401, l'injection de clé ayant disparu). Le canal E2E EST l'auth.
	mux.HandleFunc("/api/e2e/chat", handleE2EChat)
	// Notifications Web Push : le serveur pousse une notif à la fin d'un tour
	// utilisateur (push.go), même app fermée / iPhone verrouillé.
	api("/api/push/key", handlePushKey)                 // clé publique VAPID (pour s'abonner)
	api("/api/push/subscribe", handlePushSubscribe)     // enregistre un abonnement
	api("/api/push/unsubscribe", handlePushUnsubscribe) // retire un abonnement
	// Tâches planifiées : l'IA exécute des consignes toute seule sur une fréquence
	// réglable (tasks.go). Le scheduler tourne dans CE process (celui qui possède la
	// conversation et le modèle).
	api("/api/tasks", handleTasks)
	api("/api/tasks/save", handleTaskSave)
	api("/api/tasks/delete", handleTaskDelete)
	api("/api/tasks/toggle", handleTaskToggle)
	api("/api/tasks/pause", handleTasksPause)
	api("/api/tasks/run", handleTaskRun)
	api("/api/tasks/stop", handleTaskStop)
	StartTaskScheduler()
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
		_ = hideCmd(exec.Command("taskkill", args...)).Run()
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
		out, err := hideCmd(exec.Command("netstat", "-ano", "-p", "tcp")).Output()
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
		// tasklist CSV : "ajean.exe","1234","Console","1","12 345 K"
		out, err := hideCmd(exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH")).Output()
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
