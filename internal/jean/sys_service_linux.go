//go:build linux

package jean

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// serviceAction wraps `systemctl <action> <svc>` with passwordless sudo where
// it makes sense, and prints a follow-up status check after start/restart.
func serviceAction(action string) error {
	svc := serviceName()
	needsRoot := action == "start" || action == "stop" || action == "restart" || action == "enable" || action == "disable"
	args := []string{}
	bin := "systemctl"
	if needsRoot && os.Geteuid() != 0 {
		bin = "sudo"
		args = append(args, "-n", "systemctl")
	}
	args = append(args, action, svc)
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	switch action {
	case "start", "restart":
		return checkStarted(svc)
	case "stop":
		fmt.Println(green("[ok]") + " arrêté")
	case "enable":
		fmt.Println(green("[ok]") + " démarrage auto activé")
	case "disable":
		fmt.Println(green("[ok]") + " démarrage auto désactivé")
	}
	return nil
}

func checkStarted(svc string) error {
	time.Sleep(2 * time.Second)
	out, _ := exec.Command("systemctl", "is-active", svc).Output()
	state := strings.TrimSpace(string(out))
	if state == "active" || state == "activating" {
		fmt.Printf("%s %s: %s\n", green("[ok]"), svc, state)
		return nil
	}
	fmt.Printf("%s %s: %s — derniers logs :\n", red("[ERREUR]"), svc, state)
	fmt.Println("------------------------------------------------")
	logs, _ := exec.Command("journalctl", "-u", svc, "-n", "20", "--no-pager").Output()
	fmt.Print(string(logs))
	fmt.Println("------------------------------------------------")
	fmt.Printf("→ jean logs   pour plus de détails\n→ jean edit   pour corriger config.env\n")
	return fmt.Errorf("service %s non démarré", svc)
}

func serviceLogs() error {
	cmd := exec.Command("journalctl", "-u", serviceName(), "-n", "80", "-f")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// serviceIsActive reports whether the systemd unit is currently running.
func serviceIsActive() bool {
	out, _ := exec.Command("systemctl", "is-active", serviceName()).Output()
	return strings.TrimSpace(string(out)) == "active"
}
