package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
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

func editConfig() error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nano"
	}
	cmd := exec.Command(editor, confPath())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	fmt.Println(dim("[info] jean restart pour appliquer"))
	return nil
}

// showVram parses `nvidia-smi --query-gpu=...` and renders a colored bar.
func showVram() error {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=name,memory.used,memory.total,utilization.gpu,temperature.gpu",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return fmt.Errorf("nvidia-smi indisponible: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) != 5 {
			continue
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		name := parts[0]
		used, _ := strconv.Atoi(parts[1])
		total, _ := strconv.Atoi(parts[2])
		util, _ := strconv.Atoi(parts[3])
		temp, _ := strconv.Atoi(parts[4])
		pct := 0
		if total > 0 {
			pct = used * 100 / total
		}
		full := pct / 5
		bar := strings.Repeat("█", full) + strings.Repeat("░", 20-full)
		fmt.Printf("\n  %s\n", cyan(name))
		fmt.Printf("  VRAM  %s  %3d%%   %.1f / %.1f GiB\n", green(bar), pct, float64(used)/1024, float64(total)/1024)
		fmt.Printf("  GPU   %3d%%      Temp  %d°C\n\n", util, temp)
	}
	return nil
}
