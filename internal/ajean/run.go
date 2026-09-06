// ajean — single-binary LLM server manager + web UI for llama.cpp deployments.
// Point d'entrée réel : Main(), appelé par cmd/ajean (les métadonnées Windows
// .syso/go:generate vivent là-bas, dans le dossier du package main).
package ajean

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Version = "0.13.7"

// Main est le vrai main() du binaire (cmd/ajean ne fait que l'appeler).
func Main() {
	// Rattache la console du terminal parent si on est lancé depuis un shell
	// (Windows : binaire GUI). Retourne false au double-clic (aucune console) →
	// on bascule alors sur l'expérience « application ». Hors Windows : toujours
	// true. À faire AVANT toute écriture.
	haveConsole := setupConsole()

	// Nettoie un éventuel binaire .old laissé par une mise à jour Windows.
	cleanupOldBinary()

	args := os.Args[1:]
	noArgs := len(args) == 0
	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	// Double-clic sur le binaire (aucun argument, aucune console rattachée) → on
	// lance l'expérience « application » (UI web + navigateur + icône tray) plutôt
	// que d'afficher l'aide. Le binaire étant compilé en sous-système GUI, il n'y a
	// AUCUNE console à ce stade (donc plus de fenêtre noire). Lancé depuis un shell,
	// `ajean` sans argument garde son comportement d'aide.
	if noArgs && !haveConsole {
		mustExit(cmdApp(args))
		return
	}
	switch cmd {
	case "app":
		mustExit(cmdApp(args))
	case "start", "restart":
		// Un moteur sans BIN ni MODEL démarre, meurt, et systemd le relance en
		// boucle : l'utilisateur ne voyait qu'un « activating » rassurant et un
		// /health muet. On le dit AVANT de lancer le service.
		mustExit(preflightEngine())
		mustExit(serviceAction(cmd))
	case "stop", "status", "enable", "disable":
		mustExit(serviceAction(cmd))
	case "logs":
		mustExit(serviceLogs())
	case "edit":
		mustExit(editConfig())
	case "set-api-key":
		mustExit(cmdSetAPIKey(args))
	case "set-web-key":
		mustExit(cmdSetWebKey(args))
	case "vram":
		mustExit(showVram())
	case "gpu":
		mustExit(cmdGPU(args))
	case "network":
		mustExit(cmdNetwork(args))
	case "switch":
		mustExit(cmdSwitch(args))
	case "chat":
		mustExit(cmdChat(args))
	case "export":
		mustExit(cmdExport(args))
	case "web":
		mustExit(cmdWeb(args))
	case "link":
		mustExit(cmdLink(args))
	case "remote":
		mustExit(cmdRemote(args))
	case "postes":
		mustExit(cmdPostes(args))
	case "ui":
		mustExit(cmdUI(args))
	case "oai":
		mustExit(cmdOAI(args))
	case "agent":
		mustExit(cmdAgent(args))
	case "internet":
		mustExit(cmdInternet(args))
	case "memory":
		mustExit(cmdMemory(args))
	case "serve":
		mustExit(cmdServe(args))
	case "test":
		mustExit(cmdTest(args))
	case "bench":
		mustExit(cmdBench(args))
	case "llamacpp":
		mustExit(cmdLlamacpp(args))
	case "update":
		mustExit(cmdUpdate(args))
	case "where":
		mustExit(cmdWhere(args))
	case restartArg:
		// Sous-commande interne, absente de l'aide : l'accompagnateur détaché qui
		// attend la fermeture de l'app puis la relance.
		mustExit(cmdRestartAfterUpdate(args))
	case "install":
		mustExit(cmdInstall(args))
	case "uninstall":
		mustExit(cmdUninstall(args))
	case "version", "-v", "--version":
		fmt.Println("ajean", Version)
	case "help", "-h", "--help", "":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "commande inconnue: %s\n\n", cmd)
		printHelp()
		os.Exit(2)
	}
}

func printHelp() {
	fmt.Printf(`ajean %s — manager llama.cpp + UI web (single binary)

Usage: ajean <commande> [args]

AJEAN tourne en deux services : ajean-engine (le modèle) et ajean-ui
(l'interface web, le tunnel d'accès distant et l'endpoint OpenAI).

Moteur (ajean-engine) :
  start | stop | restart        gérer le service
  status | logs                 état / logs en direct
  enable | disable              auto-démarrage au boot
  edit                          éditer la configuration dans $EDITOR
  switch [N]                    activer un preset de presets/ (interactif ou par numéro)
  test | bench [N]              vérifier que l'IA répond / mesurer prefill + decode tok/s
  vram                          utilisation GPU/VRAM (nvidia-smi)
  gpu [index…]                  liste les GPU / choisit le(s)quel(s) utiliser (gpu all = tous)
  set-api-key [clé]             protéger llama-server (clé Bearer); vide = générer, "" = retirer
  network [on|off|status]       rendre l'endpoint OpenAI joignable depuis le réseau local
                                (HOST + règle de pare-feu sous Windows)

Interface (ajean-ui) :
  ui [start|stop|restart|status]  pilote le service d'interface
  web [PORT]                    sert l'interface au premier plan (défaut :8090) —
                                ouvre aussi le tunnel si un jeton est enregistré
  set-web-key [clé]             protéger l'API de pilotage; vide = générer, "" = retirer

Interaction:
  chat [system-prompt]          chat terminal streamé
  export [options] [fichier]    exporte la conversation de l'interface web
                                (Markdown par défaut, « - » = sortie standard)
                                --json  --last N  --no-reasoning
                                --no-tools  --no-results
  agent [on|off|status]         donne à l'IA ses outils (shell, fichiers, mémoire)
  memory [off|ondemand|always|status]  mode mémoire de l'IA
  internet [on|off|status|engine <go|crawl4ai>|url <url>|key <clé>]
                                accès web de l'IA (moteur intégré ou serveur Crawl4AI)

Accès distant (ajean.link) :
  link <token>                  enregistre le jeton et ouvre le tunnel
  link code                     code d'appairage (10 min, usage unique) pour le portail
  link status | logout          état du jeton / l'oublier

Poste distant (piloter CE PC depuis l'IA d'un serveur AJEAN) :
  remote install <url> --machine ID --key CLÉ --code CODE [--allow shell,read,write,list]
                                appaire ce PC + installe le service (démarrage auto)
  remote connect <url> …        idem en avant-plan (pour tester)
  remote status | uninstall | logout
                                (commande d'appairage prête à copier : UI web → Postes distants)

Backend llama.cpp :
  llamacpp install              clone + compile llama.cpp (CUDA/ROCm/Metal/CPU), pointe BIN dessus
  llamacpp update               git pull + recompile le backend existant
  llamacpp status               commit courant, backend détecté, retard sur origin

Installation:
  where                         affiche l'emplacement du binaire, de la base et des dossiers
  install | uninstall           installer / désinstaller
  update [--check]              mettre à jour depuis les releases GitHub
  serve                         entrypoint du service : exec llama-server (usage interne)

Env:
  AJEAN_HOME   racine des données (défaut : /etc/ajean, %%ProgramData%%\ajean sous Windows)
  EDITOR       éditeur pour 'ajean edit' (défaut : nano, notepad sous Windows)
`, Version)
}

func mustExit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "[err]", err)
		os.Exit(1)
	}
}

// AjeanHome resolves the AJEAN data directory.
// Précédence : $AJEAN_HOME → /etc/default/ajean (unix) → défaut plateforme.
func AjeanHome() string {
	if h := os.Getenv("AJEAN_HOME"); h != "" {
		return h
	}
	if h := readEtcDefault(); h != "" {
		return h
	}
	return defaultAjeanHome()
}

// readEtcDefault parses /etc/default/ajean for AJEAN_HOME=<path>. Quiet on errors.
func readEtcDefault() string {
	b, err := os.ReadFile("/etc/default/ajean")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		s = strings.TrimPrefix(s, "export ")
		if k, v, ok := strings.Cut(s, "="); ok && strings.TrimSpace(k) == "AJEAN_HOME" {
			return strings.Trim(strings.TrimSpace(v), "\"'")
		}
	}
	return ""
}

// Arborescence de $AJEAN_HOME. Elle tient en six dossiers, et rien d'autre :
// tout le reste (config, préférences, conversation, clés, drapeaux) vit dans la
// base ajean.db — voir store.go.
func backendsDir() string { return filepath.Join(AjeanHome(), "backends") }
func binDir() string      { return filepath.Join(AjeanHome(), "bin") }
func presetsDir() string  { return filepath.Join(AjeanHome(), "presets") }

// memoryDir est le dossier mémoire du projet ACTIF (memory/<slug>). Tout le code
// mémoire passe par ici : il hérite du cloisonnement par projet sans le savoir.
func memoryDir() string    { return projectMemoryDir(activeProjectSlug()) }
func modelsDir() string    { return filepath.Join(AjeanHome(), "models") }
func workspaceDir() string { return filepath.Join(AjeanHome(), "workspace") }

// scriptsDir est le dossier des scripts de l'agent : à côté de memory/presets,
// HORS du workspace. Le workspace est un bac à sable jetable (clones, tests) qu'un
// nettoyage peut raser ; les scripts qu'on veut CONSERVER vivent ici, protégés par
// guardDestructive (voir chat_scripts.go) contre toute suppression récursive.
func scriptsDir() string { return filepath.Join(AjeanHome(), "scripts") }

// serviceName est le nom de l'unité qui exécute llama-server. Son pendant est
// uiUnitName (« ajean-ui »), qui sert l'interface et le tunnel.
func serviceName() string {
	if n := os.Getenv("AJEAN_SERVICE"); n != "" {
		return n
	}
	return "ajean-engine"
}

// Color helpers (ANSI). Disabled when stdout is not a TTY.
var colorOn = isTerminal()

func col(code, s string) string {
	if !colorOn {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}
func bold(s string) string    { return col("1", s) }
func cyan(s string) string    { return col("1;36", s) }
func green(s string) string   { return col("32", s) }
func red(s string) string     { return col("31", s) }
func dim(s string) string     { return col("2", s) }
func yellow(s string) string  { return col("33", s) }
func magenta(s string) string { return col("35", s) }
