//go:build darwin

package jean

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// sys_service_darwin.go — gestion du service via launchd (LaunchDaemon), équivalent
// macOS de sys_service_linux.go (systemd). ⚠️ NON TESTÉ sur un vrai Mac : jusqu'ici
// le support macOS était absent (le code systemd/Linux était utilisé par erreur,
// cf. issue #4). Implémentation prudente basée sur launchctl load/unload/list.

// launchdLabel dérive le label launchd du service (ex. "com.jean.jean").
func launchdLabel(svc string) string { return "com.jean." + svc }

// launchdPlistPath : chemin du LaunchDaemon (domaine système, exécuté par root
// puis abaissé à l'utilisateur cible via la clé UserName du plist).
func launchdPlistPath(svc string) string {
	return "/Library/LaunchDaemons/" + launchdLabel(svc) + ".plist"
}

// launchdLogPath : sortie standard/erreur du service, sous JEAN_HOME (accessible
// en écriture par l'utilisateur du service après le chown de l'installation).
func launchdLogPath() string { return filepath.Join(JeanHome(), serviceName()+".log") }

// serviceAction mappe start/stop/restart/enable/disable sur launchctl. `load -w`
// (re)active le service ET le rend persistant au boot ; `unload -w` le désactive.
func serviceAction(action string) error {
	svc := serviceName()
	plist := launchdPlistPath(svc)
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
	fmt.Printf("→ jean logs   pour plus de détails\n→ jean edit   pour corriger config.env\n")
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
func serviceIsActive() bool {
	out, err := exec.Command("launchctl", "list", launchdLabel(serviceName())).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "\"PID\"")
}
