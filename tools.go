package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"time"
)

const (
	toolDefaultTimeout = 30
	toolMaxTimeout     = 300
	toolMaxOutput      = 8000 // characters of stdout/stderr returned to the model
)

func toolsEnabled() bool {
	_, err := os.Stat(toolsFlag())
	return err == nil
}

func setToolsEnabled(on bool) error {
	_ = os.MkdirAll(JeanHome(), 0o755)
	if on {
		f, err := os.Create(toolsFlag())
		if err != nil {
			return err
		}
		return f.Close()
	}
	if err := os.Remove(toolsFlag()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// machineSystemPrompt returns a short briefing about the host the model is
// running on, so that when machine access is enabled it knows *which* machine
// run_shell acts upon (and doesn't claim it has no access to "your PC").
// Returns "" when machine access is off.
func machineSystemPrompt() string {
	if !toolsEnabled() {
		return ""
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "inconnu"
	}
	who := ""
	if u, err := user.Current(); err == nil {
		who = u.Username
	}
	cwd, _ := os.Getwd()

	var b strings.Builder
	b.WriteString("Accès machine actif : le tool run_shell exécute des commandes sur cette machine — c'est ton accès réel, ne dis jamais que tu n'y as pas accès. Pour toute question système (OS, disque, process, fichiers, réseau), utilise-le au lieu de supposer.\n")
	b.WriteString(fmt.Sprintf("Machine : hôte=%s, %s/%s", host, runtime.GOOS, runtime.GOARCH))
	if who != "" {
		b.WriteString(", user=" + who)
	}
	if cwd != "" {
		b.WriteString(", cwd=" + cwd)
	}
	b.WriteString(".")
	return b.String()
}

// runShell executes a command via the platform shell (bash -c on Unix, cmd /C
// on Windows — see newShellCmd in platform_*.go) with a clamped timeout,
// returning a single string formatted "exit: N\n\nstdout:\n...\n\nstderr:\n..."
// truncated to keep tool output bounded.
func runShell(command string, timeoutSec int) string {
	if timeoutSec <= 0 {
		timeoutSec = toolDefaultTimeout
	}
	if timeoutSec > toolMaxTimeout {
		timeoutSec = toolMaxTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := newShellCmd(ctx, command)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("[timeout après %ds]", timeoutSec)
	}
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return fmt.Sprintf("[erreur: %v]", err)
		}
	}
	out := tailRunes(stdout.String(), toolMaxOutput)
	errOut := tailRunes(stderr.String(), toolMaxOutput)
	parts := []string{fmt.Sprintf("exit: %d", exit)}
	if out != "" {
		parts = append(parts, "stdout:\n"+out)
	}
	if errOut != "" {
		parts = append(parts, "stderr:\n"+errOut)
	}
	return strings.Join(parts, "\n\n")
}

// tailRunes returns the last n runes of s (used to cap tool output).
func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

func cmdTools(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "on":
		if err := setToolsEnabled(true); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " accès machine activé — l'IA dispose d'un shell complet sur le serveur")
	case "off":
		if err := setToolsEnabled(false); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " accès machine désactivé")
	case "", "status":
		state := dim("off")
		if toolsEnabled() {
			state = green("on")
		}
		fmt.Printf("%s  état: %s\n", cyan("Accès machine"), state)
		fmt.Printf("  timeout défaut: %ds, max: %ds\n", toolDefaultTimeout, toolMaxTimeout)
	default:
		return fmt.Errorf("usage: jean machine [on|off|status]")
	}
	return nil
}
