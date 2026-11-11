//go:build linux

package ajean

// Services sous Linux : de vraies unités SYSTÈME dans
// /etc/systemd/system. AJEAN tourne sous le compte de l'utilisateur ; tout ce
// qui demande root (écrire une unité, systemctl start/stop/enable…) passe par
// l'assistant root `ajean svc-root`, lancé via sudo sans mot de passe et qui
// n'agit QUE sur les services gérés (services_root_linux.go).
//
// Chaque service local a son unité, ajean-svc-<id>.service, écrite depuis sa
// commande. AJEAN ne pilote aucune autre unité.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const systemUnitDir = "/etc/systemd/system"

// unitFile : nom du fichier d'unité (avec .service).
func unitFile(s *Service) string {
	n := ServiceUnitName(s)
	if !strings.HasSuffix(n, ".service") {
		n += ".service"
	}
	return n
}

func svcUnitPath(s *Service) string { return filepath.Join(systemUnitDir, unitFile(s)) }

// systemctlRead : lecture seule, sans droits.
func systemctlRead(args ...string) (string, error) {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// systemdUnitText : l'unité d'un service (la commande passe
// par bash -lc, comme tapée dans un terminal).
func systemdUnitText(s *Service) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Écrit par AJEAN : modifier ce service depuis AJEAN.\n")
	fmt.Fprintf(&b, "[Unit]\nDescription=%s\nAfter=network.target\nStartLimitIntervalSec=300\nStartLimitBurst=3\n\n", strings.ReplaceAll(s.Name, "%", "%%"))
	b.WriteString("[Service]\n")
	user := s.User
	if user == "" {
		user = svcDefaultUser()
	}
	if user != "" && user != "root" {
		fmt.Fprintf(&b, "User=%s\n", user)
	}
	if s.Workdir != "" {
		fmt.Fprintf(&b, "WorkingDirectory=%s\n", strings.ReplaceAll(s.Workdir, "%", "%%"))
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "Environment=%s\n", systemdQuote(k+"="+s.Env[k]))
	}
	fmt.Fprintf(&b, "ExecStart=/bin/bash -lc '%s'\n", systemdEscape(s.commandLine()))
	if s.MemoryMaxGB > 0 {
		fmt.Fprintf(&b, "MemoryMax=%dM\nMemorySwapMax=0\n", int(s.MemoryMaxGB*1024))
	}
	if !s.NoRestart {
		b.WriteString("Restart=on-failure\nRestartSec=5\n")
	}
	// Le noyau tue le moteur plutôt qu'AJEAN si la mémoire vient à manquer.
	b.WriteString("KillMode=mixed\nTimeoutStopSec=30\nOOMScoreAdjust=500\nLimitMEMLOCK=infinity\n\n")
	b.WriteString("[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

// systemdQuote : un argument pour Environment=.
func systemdQuote(a string) string {
	a = strings.ReplaceAll(a, "%", "%%")
	a = strings.ReplaceAll(a, "$", "$$")
	if a != "" && !strings.ContainsAny(a, " \t\"'\\;") {
		return a
	}
	a = strings.ReplaceAll(a, `\`, `\\`)
	a = strings.ReplaceAll(a, `"`, `\"`)
	return `"` + a + `"`
}

// systemdEscape prépare une commande pour bash -lc '…' dans ExecStart.
func systemdEscape(cmd string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`, `%`, `%%`, `$`, `$$`).Replace(cmd)
}

func svcDefaultUser() string {
	if u := os.Getenv("SUDO_USER"); u != "" {
		return u
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return ""
}

// osSvcInstall écrit l'unité du service (inchangée et déjà inscrite = rien).
func osSvcInstall(s *Service) error {
	text := systemdUnitText(s)
	if old, err := os.ReadFile(svcUnitPath(s)); err == nil && string(old) == text && rootIsManaged(unitFile(s)) {
		return nil
	}
	return rootWriteUnit(unitFile(s), text)
}

// unitInstalled : l'unité existe. Un modèle jamais démarré n'en a pas encore.
func unitInstalled(s *Service) bool {
	_, err := os.Stat(svcUnitPath(s))
	return err == nil
}

func osSvcUninstall(s *Service) error {
	if !unitInstalled(s) && !rootIsManaged(unitFile(s)) {
		return nil
	}
	return rootRun("remove", unitFile(s))
}

func osSvcStart(s *Service) error {
	_ = rootRun("reset-failed", unitFile(s))
	return rootRun("start", unitFile(s))
}

func osSvcStop(s *Service) error {
	if !unitInstalled(s) {
		return nil
	}
	return rootRun("stop", unitFile(s))
}

func osSvcSetAutostart(s *Service, on bool) error {
	if on {
		return rootRun("enable", unitFile(s))
	}
	if !unitInstalled(s) {
		return nil
	}
	return rootRun("disable", unitFile(s))
}

func osSvcAutostart(s *Service) bool {
	out, _ := systemctlRead("is-enabled", "--", unitFile(s))
	return out == "enabled"
}

func osSvcState(s *Service) string {
	out, _ := systemctlRead("show", "-p", "ActiveState", "-p", "SubState", "-p", "LoadState", "--", unitFile(s))
	kv := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			kv[k] = v
		}
	}
	if kv["LoadState"] == "not-found" {
		return "inactive"
	}
	// Entre deux relances après un plantage, systemd dit « activating » : c'est un échec.
	if kv["SubState"] == "auto-restart" {
		return "failed"
	}
	switch kv["ActiveState"] {
	case "active", "reloading":
		return "active"
	case "activating":
		return "activating"
	case "failed":
		return "failed"
	case "":
		return "unknown"
	}
	return "inactive"
}

// osSvcLastExit : instant où le dernier processus principal de l'unité s'est
// terminé (zéro s'il tourne ou n'a jamais tourné).
func osSvcLastExit(s *Service) time.Time {
	out, _ := systemctlRead("show", "-p", "ExecMainExitTimestamp", "--value", "--", unitFile(s))
	return parseUnitTime(out)
}

func osSvcLogTail(s *Service, n int) string {
	out, _ := exec.Command("journalctl", "-u", unitFile(s), "-n", strconv.Itoa(n), "--no-pager", "-o", "cat").CombinedOutput()
	return strings.TrimRight(string(out), "\n")
}

// osSvcFailure : une phrase claire tirée de la fin du journal (dernier lancement).
func osSvcFailure(s *Service) (reason, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "journalctl", "-u", unitFile(s), "-n", "60", "--no-pager", "-o", "cat").CombinedOutput()
	return failureFromLog(string(out), localPort(s))
}

// osSvcWaitOutcome : attend un échec immédiat (8 s). Un service qui plante puis
// se relance seul n'est jamais « failed » : on compte aussi ses relances.
func osSvcWaitOutcome(s *Service) bool {
	for i := 0; i < 16; i++ {
		time.Sleep(500 * time.Millisecond)
		out, _ := systemctlRead("show", "-p", "ActiveState", "-p", "SubState", "-p", "NRestarts", "--", unitFile(s))
		for _, bad := range []string{"ActiveState=failed", "ActiveState=inactive", "SubState=auto-restart"} {
			if strings.Contains(out, bad) {
				return false
			}
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "NRestarts=") && l != "NRestarts=0" {
				return false
			}
		}
	}
	return true
}

// osPortOwner : l'unité systemd (ou le programme) qui écoute sur ce port.
func osPortOwner(port string) (unit, program string) {
	out, _ := exec.Command("ss", "-ltnpH", "sport = :"+port).Output()
	s := string(out)
	i := strings.Index(s, "pid=")
	if i < 0 {
		if strings.TrimSpace(s) != "" {
			return "", "un autre programme"
		}
		return "", ""
	}
	pid := s[i+4:]
	if j := strings.IndexAny(pid, ",)"); j > 0 {
		pid = pid[:j]
	}
	cg, _ := os.ReadFile("/proc/" + pid + "/cgroup")
	for _, l := range strings.Split(string(cg), "\n") {
		for _, part := range strings.Split(l, "/") {
			if strings.HasSuffix(part, ".service") {
				unit = part
			}
		}
	}
	comm, _ := os.ReadFile("/proc/" + pid + "/comm")
	program = strings.TrimSpace(string(comm))
	if unit != "" {
		program = strings.TrimSuffix(unit, ".service")
	}
	if program == "" {
		program = "un autre programme"
	}
	return unit, program
}

// cmdServiceHost n'existe que sous Windows.
func cmdServiceHost(args []string) error { return fmt.Errorf("réservé à Windows") }

func osSvcInstallElevatedOrDirect(s *Service) error { return osSvcInstall(s) }
