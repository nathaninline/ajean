//go:build darwin

package ajean

// Services sous macOS : agents launchd de l'utilisateur (gui/<uid>).
// NON TESTÉ sur Mac.

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func svcLabel(s *Service) string { return "com.ajean.svc." + s.ID }

func svcDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func svcPlistPath(s *Service) string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Library", "LaunchAgents", svcLabel(s)+".plist")
}

func plistString(v string) string { return "<string>" + html.EscapeString(v) + "</string>" }

func launchdPlistText(s *Service) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
`)
	fmt.Fprintf(&b, "<key>Label</key>%s\n<key>ProgramArguments</key><array>%s", plistString(svcLabel(s)), "")
	for _, a := range s.argv() {
		b.WriteString(plistString(a))
	}
	b.WriteString("</array>\n")
	if s.Workdir != "" {
		fmt.Fprintf(&b, "<key>WorkingDirectory</key>%s\n", plistString(s.Workdir))
	}
	if len(s.Env) > 0 {
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("<key>EnvironmentVariables</key><dict>")
		for _, k := range keys {
			fmt.Fprintf(&b, "<key>%s</key>%s", html.EscapeString(k), plistString(s.Env[k]))
		}
		b.WriteString("</dict>\n")
	}
	lp := svcLogPath(s)
	fmt.Fprintf(&b, "<key>StandardOutPath</key>%s\n<key>StandardErrorPath</key>%s\n", plistString(lp), plistString(lp))
	fmt.Fprintf(&b, "<key>RunAtLoad</key><%t/>\n", s.AutostartWanted)
	b.WriteString("<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>\n<key>ThrottleInterval</key><integer>10</integer>\n</dict></plist>\n")
	return b.String()
}

func osSvcInstall(s *Service) error {
	_ = os.MkdirAll(filepath.Dir(svcLogPath(s)), 0o755)
	p := svcPlistPath(s)
	text := launchdPlistText(s)
	if old, err := os.ReadFile(p); err == nil && string(old) == text {
		return nil
	}
	_ = exec.Command("launchctl", "bootout", svcDomain()+"/"+svcLabel(s)).Run()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(text), 0o644)
}

func osSvcUninstall(s *Service) error {
	_ = exec.Command("launchctl", "bootout", svcDomain()+"/"+svcLabel(s)).Run()
	if err := os.Remove(svcPlistPath(s)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func osSvcStart(s *Service) error {
	_ = exec.Command("launchctl", "bootstrap", svcDomain(), svcPlistPath(s)).Run()
	out, err := exec.Command("launchctl", "kickstart", svcDomain()+"/"+svcLabel(s)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl kickstart : %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func osSvcStop(s *Service) error {
	_ = exec.Command("launchctl", "bootout", svcDomain()+"/"+svcLabel(s)).Run()
	return nil
}

func osSvcState(s *Service) string {
	out, err := exec.Command("launchctl", "print", svcDomain()+"/"+svcLabel(s)).Output()
	if err != nil {
		return "inactive"
	}
	txt := string(out)
	switch {
	case strings.Contains(txt, "state = running"):
		return "active"
	case strings.Contains(txt, "state = spawn scheduled"):
		return "activating"
	}
	return "inactive"
}

func osSvcLogTail(s *Service, n int) string {
	b, err := os.ReadFile(svcLogPath(s))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func cmdServiceHost(args []string) error { return fmt.Errorf("réservé à Windows") }

func osSvcInstallElevatedOrDirect(s *Service) error { return osSvcInstall(s) }
