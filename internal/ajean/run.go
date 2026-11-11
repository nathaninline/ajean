// ajean : assistant AJEAN (interface web, mémoire, outils) ; les modèles sont des services système.
// Point d'entrée réel : Main(), appelé par cmd/ajean (les métadonnées Windows
// .syso/go:generate vivent là-bas, dans le dossier du package main).
package ajean

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Version = "0.19.2"

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
	case "start", "restart", "stop", "logs":
		// Raccourcis vers les modèles : sans id, le modèle du chat.
		mustExit(cmdService(append([]string{cmd}, defaultServiceArg(args)...)))
	case "status":
		mustExit(cmdService([]string{"list"}))
	case "set-web-key":
		mustExit(cmdSetWebKey(args))
	case "vram":
		mustExit(showVram())
	case "chat":
		mustExit(cmdChat(args))
	case "export":
		mustExit(cmdExport(args))
	case "cu-probe": // banc d'essai des outils du navigateur, sans modèle
		mustExit(cmdCUProbe(args))
	case "jean-sleep-test": // banc d'essai du sommeil, sur une copie de la mémoire
		mustExit(cmdJeanSleepTest(args))
	case "web":
		mustExit(cmdWeb(args))
	case "link":
		mustExit(cmdLink(args))
	case "ui":
		mustExit(cmdUI(args))
	case "oai":
		mustExit(cmdOAI(args))
	case "agent":
		mustExit(cmdAgent(args))
	case "internet":
		mustExit(cmdInternet(args))
	case "computer":
		mustExit(cmdComputer(args))
	case "memory":
		mustExit(cmdMemory(args))
	case "test":
		mustExit(cmdTest(args))
	case "bench":
		mustExit(cmdBench(args))
	case "svc-root": // interne : assistant root (via sudo)
		mustExit(cmdSvcRoot(args))
	case "install-helper":
		mustExit(cmdInstallHelper(args))
	case "guard": // interne : gardien RAM (root, ajean-guard.service)
		mustExit(cmdGuard(args))
	case "service", "services":
		mustExit(cmdService(args))
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
	fmt.Printf(`ajean %s : l'assistant AJEAN (interface web, mémoire, outils)

Usage: ajean <commande> [args]

AJEAN tourne comme un service (ajean-ui : interface web, tunnel d'accès
distant, endpoint OpenAI). Les modèles sont des services système qu'AJEAN
démarre et surveille (voir docs/moteurs.md).

Modèles :
  service list | show <id>      liste des modèles / définition d'un modèle
  service add <fichier.json>    ajouter ou remplacer un modèle
  service rm <id>               supprimer un modèle (il est arrêté)
  service use <id>              démarrer si besoin et brancher le chat dessus
  service autostart <id> on|off démarrage avec la machine
  service why <id>              raison du dernier arrêt
  service convert [--dry-run]   créer un modèle par ancien preset
  start | stop | restart [id]   piloter un modèle (sans id : celui du chat)
  status                        état des modèles
  logs [id] [n]                 journal d'un modèle
  test | bench [N]              vérifier que l'IA répond / mesurer prefill + decode tok/s
  vram                          utilisation GPU/VRAM (nvidia-smi)

Interface (ajean-ui) :
  ui [start|stop|restart|status]  pilote le service d'interface
  web [PORT]                    sert l'interface au premier plan (défaut :8090) ;
                                ouvre aussi le tunnel si un jeton est enregistré
  set-web-key [clé]             protéger l'API de pilotage; vide = générer, "" = retirer

Interaction :
  chat [system-prompt]          chat terminal streamé
  export [options] [fichier]    exporte la conversation de l'interface web
                                (Markdown par défaut, « - » = sortie standard)
                                --json  --last N  --no-reasoning
                                --no-tools  --no-results
  agent [on|off|status]         donne à l'IA ses outils (shell, fichiers, mémoire)
  computer [on|off|status]      contrôle du navigateur (l'IA pilote un Chrome local)
  memory [off|ondemand|always|search|status]  mode mémoire de l'IA
  internet [on|off|status]      accès web de l'IA (moteur intégré)

Accès distant (ajean.link) :
  link <token>                  enregistre le jeton et ouvre le tunnel
  link code                     code d'appairage (10 min, usage unique) pour le portail
  link newid                    change l'identifiant de la machine (si l'ancien a fuité)
  link status | logout          état du jeton / l'oublier

Installation :
  where                         affiche l'emplacement du binaire, de la base et des dossiers
  install | uninstall           installer / désinstaller
  install-helper [utilisateur]  Linux : assistant root des modèles et gardien RAM
  update [--check]              mettre à jour depuis les releases GitHub

Env :
  AJEAN_HOME          racine des données (défaut : /etc/ajean, %%ProgramData%%\ajean sous Windows)
  AJEAN_RAM_FLOOR_MB  RAM libre minimale avant l'arrêt d'un modèle par le garde-fou
`, Version)
}

// defaultServiceArg : sans id, le modèle du chat.
func defaultServiceArg(args []string) []string {
	if len(args) > 0 {
		return args
	}
	if id := ChatServiceID(); id != "" {
		return []string{id}
	}
	return args
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

// serviceName est le nom de l'unité de l'ancien moteur (0.18 et avant). AJEAN ne
// la lance plus : il ne sert qu'à l'arrêter et à la retirer (désinstallation).
func serviceName() string {
	if n := os.Getenv("AJEAN_SERVICE"); n != "" {
		return n
	}
	return "ajean-engine"
}

// Color helpers (ANSI). Disabled when stdout is not a TTY.
// NO_COLOR (convention no-color.org) coupe les couleurs même dans un terminal.
var colorOn = isTerminal() && os.Getenv("NO_COLOR") == ""

func col(code, s string) string {
	if !colorOn {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}
func bold(s string) string   { return col("1", s) }
func cyan(s string) string   { return col("1;36", s) }
func green(s string) string  { return col("32", s) }
func red(s string) string    { return col("31", s) }
func dim(s string) string    { return col("2", s) }
func yellow(s string) string { return col("33", s) }
