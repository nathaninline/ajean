//go:build darwin

package ajean

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// sys_service_darwin.go — gestion du service via launchd (LaunchDaemon), équivalent
// macOS de sys_service_linux.go (systemd). ⚠️ NON TESTÉ sur un vrai Mac : jusqu'ici
// le support macOS était absent (le code systemd/Linux était utilisé par erreur,
// cf. issue #4). Implémentation prudente basée sur launchctl load/unload/list.

// launchdLabel dérive le label launchd du service (ex. "com.ajean.ajean"), en
// conservant le préfixe hérité « com.ajean. » tant que c'est LUI qui est
// réellement installé : le plist d'un daemon déjà chargé ne se renomme pas tout
// seul, et viser le mauvais label reviendrait à ne plus voir le service.
func launchdLabel(svc string) string {
	modern := "com.ajean." + svc
	if _, err := os.Stat("/Library/LaunchDaemons/" + modern + ".plist"); err == nil {
		return modern
	}
	if legacy := "com.ajean." + svc; fileExists("/Library/LaunchDaemons/" + legacy + ".plist") {
		return legacy
	}
	return modern
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// launchdPlistPath : chemin du LaunchDaemon (domaine système, exécuté par root
// puis abaissé à l'utilisateur cible via la clé UserName du plist).
func launchdPlistPath(svc string) string {
	return "/Library/LaunchDaemons/" + launchdLabel(svc) + ".plist"
}

// launchdLogPath : sortie standard/erreur du service, sous AJEAN_HOME (accessible
// en écriture par l'utilisateur du service après le chown de l'installation).
func launchdLogPath() string { return filepath.Join(AjeanHome(), serviceName()+".log") }

// serviceAction mappe start/stop/restart/enable/disable sur launchctl. `load -w`
// (re)active le service ET le rend persistant au boot ; `unload -w` le désactive.
func serviceAction(action string) error {
	svc := serviceName()
	plist := launchdPlistPath(svc)
	// Pas de LaunchDaemon installé — cas normal d'un Mac de bureau, où AJEAN tourne
	// via AJEAN.app sans jamais passer par `sudo ajean install` : on gère alors le
	// service comme sous Windows, avec un `ajean serve` détaché suivi par un fichier
	// PID. Aucun droit root requis. Sans ça, `start` échouait sur un plist absent
	// et l'UI affichait éternellement « service arrêté ».
	if _, err := os.Stat(plist); err != nil {
		return userSvcAction(action)
	}
	run := func(args ...string) error {
		bin, a := "launchctl", args
		if os.Geteuid() != 0 {
			bin, a = "sudo", append([]string{"-n", "launchctl"}, args...)
		}
		cmd := exec.Command(bin, a...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	switch action {
	case "start", "enable":
		if err := run("load", "-w", plist); err != nil {
			return err
		}
		return checkStarted(svc)
	case "stop", "disable":
		if err := run("unload", "-w", plist); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " arrêté")
		return nil
	case "restart":
		_ = run("unload", plist) // best-effort : peut ne pas être chargé
		if err := run("load", "-w", plist); err != nil {
			return err
		}
		return checkStarted(svc)
	case "status":
		if serviceIsActive() {
			fmt.Printf("%s %s: actif (launchd)\n", green("[ok]"), svc)
		} else {
			fmt.Printf("%s %s: arrêté\n", yellow("[info]"), svc)
		}
		fmt.Printf("  logs   : %s\n", launchdLogPath())
		fmt.Printf("  config : %s\n", confPath())
		return nil
	}
	return fmt.Errorf("action inconnue: %s", action)
}

func checkStarted(svc string) error {
	time.Sleep(2 * time.Second)
	if serviceIsActive() {
		fmt.Printf("%s %s: actif\n", green("[ok]"), svc)
		return nil
	}
	fmt.Printf("%s %s: non démarré — derniers logs :\n", red("[ERREUR]"), svc)
	fmt.Println("------------------------------------------------")
	if b, err := os.ReadFile(launchdLogPath()); err == nil {
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		if len(lines) > 20 {
			lines = lines[len(lines)-20:]
		}
		fmt.Println(strings.Join(lines, "\n"))
	}
	fmt.Println("------------------------------------------------")
	fmt.Printf("→ ajean logs   pour plus de détails\n→ ajean edit   pour corriger config.env\n")
	return fmt.Errorf("service %s non démarré", svc)
}

func serviceLogs() error {
	// tail -f du fichier de log défini dans le plist (StandardOut/ErrorPath).
	cmd := exec.Command("tail", "-n", "80", "-f", launchdLogPath())
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// serviceIsActive : le service est chargé ET possède un PID courant. `launchctl
// list <label>` renvoie un dict incluant la clé "PID" uniquement quand il tourne.
// Sans LaunchDaemon installé, on lit le fichier PID du mode utilisateur.
func serviceIsActive() bool {
	if _, err := os.Stat(launchdPlistPath(serviceName())); err != nil {
		pid := readServicePID()
		return pid > 0 && processAlive(pid)
	}
	out, err := exec.Command("launchctl", "list", launchdLabel(serviceName())).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "\"PID\"")
}

// ---------------------------------------------------------------------------
// Mode utilisateur (sans launchd) — même approche que Windows : `ajean serve` est
// lancé détaché, son PID va dans AJEAN_HOME/<svc>.pid et sa sortie dans
// AJEAN_HOME/<svc>.log. Setsid le détache de notre session pour qu'il survive à
// la fermeture de AJEAN.app, et permet de tuer tout le groupe (llama-server
// enfant compris) d'un seul signal.

func pidFilePath() string { return filepath.Join(AjeanHome(), serviceName()+".pid") }
func logFilePath() string { return filepath.Join(AjeanHome(), serviceName()+".log") }

func userSvcAction(action string) error {
	switch action {
	case "start":
		return userSvcStart()
	case "stop":
		return userSvcStop(true)
	case "restart":
		_ = userSvcStop(false)
		time.Sleep(500 * time.Millisecond)
		return userSvcStart()
	case "status":
		pid := readServicePID()
		if pid > 0 && processAlive(pid) {
			fmt.Printf("%s %s: actif (PID %d)\n", green("[ok]"), serviceName(), pid)
		} else {
			fmt.Printf("%s %s: arrêté\n", yellow("[info]"), serviceName())
		}
		fmt.Printf("  logs   : %s\n", logFilePath())
		fmt.Printf("  config : %s\n", confPath())
		return nil
	case "enable", "disable":
		fmt.Printf("%s '%s' réclame un service système : lance %s pour installer le LaunchDaemon.\n",
			yellow("[info]"), action, bold("sudo ajean install"))
		return nil
	}
	return fmt.Errorf("action inconnue: %s", action)
}

func userSvcStart() error {
	if pid := readServicePID(); pid > 0 && processAlive(pid) {
		fmt.Printf("%s déjà démarré (PID %d)\n", yellow("[info]"), pid)
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(AjeanHome(), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(logFilePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("ouverture du log %s: %w", logFilePath(), err)
	}
	defer logf.Close()

	cmd := exec.Command(self, "serve")
	cmd.Stdout, cmd.Stderr = logf, logf
	// Même répertoire de travail que sous systemd/launchd : les chemins relatifs
	// de config.env (MODEL=…gguf) se résolvent depuis AJEAN_HOME, pas depuis « / »
	// que nous hérite le Finder.
	cmd.Dir = AjeanHome()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("démarrage de 'ajean serve': %w", err)
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(pidFilePath(), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("écriture du PID: %w", err)
	}
	_ = cmd.Process.Release()
	return userCheckStarted(pid)
}

func userCheckStarted(pid int) error {
	time.Sleep(2 * time.Second)
	if processAlive(pid) {
		fmt.Printf("%s %s: démarré (PID %d)\n", green("[ok]"), serviceName(), pid)
		fmt.Printf("       logs: %s  (ajean logs pour suivre)\n", dim(logFilePath()))
		return nil
	}
	fmt.Printf("%s %s: le processus s'est arrêté — derniers logs :\n", red("[ERREUR]"), serviceName())
	fmt.Println("------------------------------------------------")
	fmt.Print(tailFile(logFilePath(), 20))
	fmt.Println("------------------------------------------------")
	fmt.Printf("→ ajean logs   pour plus de détails\n→ ajean edit   pour corriger config.env\n")
	_ = os.Remove(pidFilePath())
	return fmt.Errorf("service %s non démarré", serviceName())
}

func userSvcStop(verbose bool) error {
	pid := readServicePID()
	if pid <= 0 || !processAlive(pid) {
		_ = os.Remove(pidFilePath())
		if verbose {
			fmt.Println(yellow("[info]") + " aucun service en cours d'exécution")
		}
		return nil
	}
	// Setsid a fait de l'enfant un chef de groupe : le PID négatif vise le groupe
	// entier, donc llama-server s'arrête avec lui.
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	for i := 0; i < 40 && processAlive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(pid) {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	_ = os.Remove(pidFilePath())
	if verbose {
		fmt.Println(green("[ok]") + " arrêté")
	}
	return nil
}

func readServicePID() int {
	b, err := os.ReadFile(pidFilePath())
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

// processAlive : le signal 0 ne tue rien, il teste juste l'existence du process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// tailFile renvoie les n dernières lignes d'un fichier (best-effort).
func tailFile(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n") + "\n"
}

// serviceLogTail renvoie les n dernières lignes du journal du service (pour
// l'UI web). Le fichier est le même en mode launchd et en mode utilisateur.
func serviceLogTail(n int) string { return tailFile(logFilePath(), n) }
