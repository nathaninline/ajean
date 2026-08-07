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

const Version = "0.7.13"

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
	case "start", "stop", "restart", "status", "enable", "disable":
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
	case "switch":
		mustExit(cmdSwitch(args))
	case "chat":
		mustExit(cmdChat(args))
	case "web":
		mustExit(cmdWeb(args))
	case "link":
		mustExit(cmdLink(args))
	case "oai":
		mustExit(cmdOAI(args))
	case "agent", "skills", "machine", "tools":
		// « mode agent » unifie l'ancien couple machine + skills : un seul
		// interrupteur active TOUS les outils de l'IA (shell + mémoire ; les
		// skills ont été fondus dans la mémoire). Les anciens noms restent
		// acceptés comme alias rétro-compatibles.
		mustExit(cmdAgent(args))
	case "internet", "web-access":
		mustExit(cmdInternet(args))
	case "memory", "mem":
		mustExit(cmdMemory(args))
	case "serve":
		mustExit(cmdServe(args))
	case "test":
		mustExit(cmdTest(args))
	case "bench":
		mustExit(cmdBench(args))
	case "llamacpp", "llama":
		mustExit(cmdLlamacpp(args))
	case "update", "upgrade", "self-update":
		mustExit(cmdUpdate(args))
	case "where", "paths":
		mustExit(cmdWhere(args))
	case restartArg:
		// Sous-commande interne, absente de l'aide : l'accompagnateur detache qui
		// attend la fermeture de l'app, migre, puis la relance.
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

Application:
  app                           lance l'UI web + ouvre le navigateur (auto au double-clic du binaire)

Service:
  start | stop | restart        gérer le service (systemd sous Linux, processus en arrière-plan sous Windows)
  status | logs                 état / logs en direct
  enable | disable              auto-démarrage au boot
  edit                          éditer $AJEAN_HOME/config.env
  set-api-key [clé]             protéger llama-server (clé Bearer); vide = générer, "" = retirer
  set-web-key [clé]             protéger l'API de pilotage 'ajean web'; vide = générer, "" = retirer
  vram                          utilisation GPU/VRAM (nvidia-smi)
  gpu [index…]                  liste les GPU / choisit le(s)quel(s) utiliser (gpu all = tous)
  test                          vérifie que l'IA répond (health + completion)
  bench [N]                     mesure prefill + decode tok/s (prompt 2000 tok, N=200 par défaut)

Presets:
  switch [N]                    choisir un preset dans configs/ (interactif ou par numéro)

Interaction:
  chat [system-prompt]          chat terminal streamé
  web [PORT]                    UI web (défaut :8090) — chat + presets + mode agent
  internet [on|off|status|engine <go|crawl4ai>|url <url>|key <clé>]
                                accès web de l'IA (web_search/open/read/grep) — moteur intégré ou serveur Crawl4AI
  memory [off|ondemand|always|status]  mode mémoire de l'IA (off / sur demande / auto)

Accès distant (ajean.link) :
  link <token>                  enregistre le token et démarre le lien au relais (token = 1re fois / pour le changer)
  link start | restart | stop   démarre / redémarre / arrête le service de lien
  link code                     génère un code d'appairage (valable 10 min, à usage unique) pour le portail
  link status | logout          état du lien / oublier le token
  link                          (sans argument) affiche l'aide des sous-commandes link
  link serve                    exécute le worker au premier plan (utilisé par ajean-link.service ; pendant de 'ajean serve')

Mode agent:
  agent [on|off|status]         active TOUS les outils de l'IA (shell complet + mémoire) — un seul interrupteur

Backend llama.cpp :
  llamacpp install              clone + compile llama.cpp (détecte CUDA/ROCm/Metal/CPU), pointe BIN dessus
  llamacpp update               git pull + recompile le backend existant (arrête/redémarre le service)
  llamacpp status               commit courant, backend détecté, retard sur origin

Entrypoint (utilisé par ajean.service) :
  serve                         lit config.env et exec le binaire llama-server

Installation:
  where                         affiche où sont le binaire, la config, la mémoire et le dossier de travail de l'agent
  install                       installer (Linux: unité systemd, sudoers, dossiers ; Windows: dossiers + config)
  uninstall                     désinstaller
  update [--check]              mettre à jour AJEAN depuis les releases GitHub (--check = signale sans installer)

Env:
  AJEAN_HOME   racine (défaut: /etc/ajean sur Linux/macOS, %%ProgramData%%\ajean sur Windows)
               AJEAN_HOME reste accepté (héritage)
  EDITOR       éditeur pour 'ajean edit' (défaut: nano sur Unix, notepad sur Windows)

Config: $AJEAN_HOME/config.env
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
func backendsDir() string  { return filepath.Join(AjeanHome(), "backends") }
func binDir() string       { return filepath.Join(AjeanHome(), "bin") }
func presetsDir() string   { return filepath.Join(AjeanHome(), "presets") }
func memoryDir() string    { return filepath.Join(AjeanHome(), "memory") }
func modelsDir() string    { return filepath.Join(AjeanHome(), "models") }
func workspaceDir() string { return filepath.Join(AjeanHome(), "workspace") }

// serviceName est le nom de l'unité qui exécute llama-server.
func serviceName() string {
	if n := os.Getenv("AJEAN_SERVICE"); n != "" {
		return n
	}
	return "ajean"
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

// trimSplit splits and drops empty tokens.
func trimSplit(s, sep string) []string {
	out := []string{}
	for _, p := range strings.Split(s, sep) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
