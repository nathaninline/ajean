// jean — single-binary LLM server manager + web UI for llama.cpp deployments.
// Point d'entrée réel : Main(), appelé par cmd/jean (les métadonnées Windows
// .syso/go:generate vivent là-bas, dans le dossier du package main).
package jean

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Version = "0.4.15"

// Main est le vrai main() du binaire (cmd/jean ne fait que l'appeler).
func Main() {
	// Migration one-shot des anciens skills (SKILLS/<nom>/SKILL.md) vers la
	// nouvelle mémoire (MEMORY/<nom>.md). Idempotente, silencieuse si rien à faire.
	migrateSkillsToMemory()
	// Nettoie un éventuel binaire .old laissé par une mise à jour Windows.
	cleanupOldBinary()

	args := os.Args[1:]
	noArgs := len(args) == 0
	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	// Double-clic sur le binaire (aucun argument, console fraîche) → on lance
	// l'expérience « application » (UI web + navigateur) plutôt que d'afficher
	// l'aide dans une console qui se referme aussitôt. Lancé depuis un shell,
	// `jean` sans argument garde son comportement d'aide.
	if noArgs && launchedByDoubleClick() {
		relaunchDetachedApp() // relance sans console (ne revient pas), puis quitte l'original
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
	case "install":
		mustExit(cmdInstall(args))
	case "uninstall":
		mustExit(cmdUninstall(args))
	case "version", "-v", "--version":
		fmt.Println("jean", Version)
	case "help", "-h", "--help", "":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "commande inconnue: %s\n\n", cmd)
		printHelp()
		os.Exit(2)
	}
}

func printHelp() {
	fmt.Printf(`jean %s — manager llama.cpp + UI web (single binary)

Usage: jean <commande> [args]

Application:
  app                           lance l'UI web + ouvre le navigateur (auto au double-clic du binaire)

Service:
  start | stop | restart        gérer le service (systemd sous Linux, processus en arrière-plan sous Windows)
  status | logs                 état / logs en direct
  enable | disable              auto-démarrage au boot
  edit                          éditer $JEAN_HOME/config.env
  set-api-key [clé]             protéger llama-server (clé Bearer); vide = générer, "" = retirer
  set-web-key [clé]             protéger l'API de pilotage 'jean web'; vide = générer, "" = retirer
  vram                          utilisation GPU/VRAM (nvidia-smi)
  gpu [index…]                  liste les GPU / choisit le(s)quel(s) utiliser (gpu all = tous)
  test                          vérifie que l'IA répond (health + completion)
  bench [N]                     mesure prefill + decode tok/s (prompt 2000 tok, N=200 par défaut)

Presets:
  switch [N]                    choisir un preset dans configs/ (interactif ou par numéro)

Interaction:
  chat [system-prompt]          chat terminal streamé
  web [PORT]                    UI web (défaut :8090) — chat + presets + mode agent
  internet [on|off|status|url <url>]  accès web de l'IA via un serveur Crawl4AI (web_search/open/read/grep)
  memory [off|ondemand|always|status]  mode mémoire de l'IA (off / sur demande / auto)

Accès distant (ajean.link) :
  link <token>                  enregistre le token et démarre le lien au relais (token = 1re fois / pour le changer)
  link start | restart | stop   démarre / redémarre / arrête le service de lien
  link code                     génère un code d'appairage (valable 10 min, à usage unique) pour le portail
  link status | logout          état du lien / oublier le token
  link                          (sans argument) affiche l'aide des sous-commandes link
  link serve                    exécute le worker au premier plan (utilisé par jean-link.service ; pendant de 'jean serve')

Mode agent:
  agent [on|off|status]         active TOUS les outils de l'IA (shell complet + mémoire) — un seul interrupteur

Backend llama.cpp :
  llamacpp install              clone + compile llama.cpp (détecte CUDA/ROCm/Metal/CPU), pointe BIN dessus
  llamacpp update               git pull + recompile le backend existant (arrête/redémarre le service)
  llamacpp status               commit courant, backend détecté, retard sur origin

Entrypoint (utilisé par jean.service) :
  serve                         lit config.env et exec le binaire llama-server

Installation:
  install                       installer (Linux: unité systemd, sudoers, dossiers ; Windows: dossiers + config)
  uninstall                     désinstaller
  update [--check]              mettre à jour jean depuis les releases GitHub (--check = signale sans installer)

Env:
  JEAN_HOME    racine (défaut: /etc/jean sur Linux/macOS, %%ProgramData%%\jean sur Windows)
  EDITOR       éditeur pour 'jean edit' (défaut: nano sur Unix, notepad sur Windows)

Config: $JEAN_HOME/config.env
`, Version)
}

func mustExit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "[err]", err)
		os.Exit(1)
	}
}

// JeanHome resolves the JEAN data directory.
// Precedence: $JEAN_HOME → /etc/default/jean (unix only) → defaultJeanHome().
// defaultJeanHome() is platform-specific (see sys_platform_unix.go / sys_platform_windows.go).
func JeanHome() string {
	if h := os.Getenv("JEAN_HOME"); h != "" {
		return h
	}
	if h := readEtcDefault(); h != "" {
		return h
	}
	return defaultJeanHome()
}

// readEtcDefault parses /etc/default/jean for JEAN_HOME=<path>. Quiet on errors.
func readEtcDefault() string {
	b, err := os.ReadFile("/etc/default/jean")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		s = strings.TrimPrefix(s, "export ")
		if eq := strings.IndexByte(s, '='); eq > 0 {
			k := strings.TrimSpace(s[:eq])
			v := strings.Trim(strings.TrimSpace(s[eq+1:]), "\"'")
			if k == "JEAN_HOME" {
				return v
			}
		}
	}
	return ""
}

func confPath() string     { return filepath.Join(JeanHome(), "config.env") }
func presetsDir() string   { return filepath.Join(JeanHome(), "configs") }
func skillsDir() string    { return filepath.Join(JeanHome(), "SKILLS") }
func memoryDir() string    { return filepath.Join(JeanHome(), "MEMORY") }
func agentFlag() string    { return filepath.Join(JeanHome(), ".agent_enabled") }
func internetFlag() string { return filepath.Join(JeanHome(), ".internet_enabled") }

// Anciens drapeaux séparés, conservés pour la migration vers le mode agent unifié.
func legacySkillsFlag() string { return filepath.Join(skillsDir(), ".enabled") }
func legacyToolsFlag() string  { return filepath.Join(JeanHome(), ".tools_enabled") }
func apiKeyPath() string       { return filepath.Join(JeanHome(), ".api_key") }
func serviceName() string {
	if n := os.Getenv("JEAN_SERVICE"); n != "" {
		return n
	}
	return "jean"
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
