// jean — single-binary LLM server manager + web UI for llama.cpp deployments.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Version = "0.2.6"

func main() {
	args := os.Args[1:]
	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
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
	case "skills":
		mustExit(cmdSkills(args))
	case "machine", "tools":
		mustExit(cmdTools(args))
	case "serve":
		mustExit(cmdServe(args))
	case "test":
		mustExit(cmdTest(args))
	case "bench":
		mustExit(cmdBench(args))
	case "llamacpp", "llama":
		mustExit(cmdLlamacpp(args))
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
  web [PORT]                    UI web (défaut :8090) — chat + presets + skills + tools

Accès distant (ajean.link) :
  link [token]                  démarre le lien au relais en arrière-plan (service) ; token = 1re fois / pour le changer
  link restart | stop           redémarre / arrête le service de lien
  link code                     génère un code d'appairage (valable 10 min, à usage unique) pour le portail
  link status | logout          état du lien / oublier le token
  link serve                    exécute le worker au premier plan (utilisé par jean-link.service ; pendant de 'jean serve')

LLM-side outils:
  skills [on|off|list]          active la lecture de SKILLS/<nom>/SKILL.md par l'IA
  machine [on|off|status]       active l'accès machine (l'IA dispose d'un shell complet sur le serveur)

Backend llama.cpp :
  llamacpp install              clone + compile llama.cpp (détecte CUDA/ROCm/Metal/CPU), pointe BIN dessus
  llamacpp update               git pull + recompile le backend existant (arrête/redémarre le service)
  llamacpp status               commit courant, backend détecté, retard sur origin

Entrypoint (utilisé par jean.service) :
  serve                         lit config.env et exec le binaire llama-server

Installation:
  install                       installer (Linux: unité systemd, sudoers, dossiers ; Windows: dossiers + config)
  uninstall                     désinstaller

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
// defaultJeanHome() is platform-specific (see platform_unix.go / platform_windows.go).
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

func confPath() string    { return filepath.Join(JeanHome(), "config.env") }
func presetsDir() string  { return filepath.Join(JeanHome(), "configs") }
func skillsDir() string   { return filepath.Join(JeanHome(), "SKILLS") }
func skillsFlag() string  { return filepath.Join(skillsDir(), ".enabled") }
func toolsFlag() string   { return filepath.Join(JeanHome(), ".tools_enabled") }
func apiKeyPath() string  { return filepath.Join(JeanHome(), ".api_key") }
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
func bold(s string) string  { return col("1", s) }
func cyan(s string) string  { return col("1;36", s) }
func green(s string) string { return col("32", s) }
func red(s string) string   { return col("31", s) }
func dim(s string) string   { return col("2", s) }
func yellow(s string) string { return col("33", s) }
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
