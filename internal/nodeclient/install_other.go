//go:build !windows

package nodeclient

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// RunService : hors Windows, pas de gestionnaire de services intégré — on tourne
// simplement en avant-plan (le service est géré par systemd/launchd, cf Install).
func RunService(cfg Config) error {
	Run(context.Background(), cfg, false)
	return nil
}

// Install installe un service de démarrage automatique : systemd --user (Linux)
// ou launchd (macOS). Le binaire est copié à un emplacement stable.
func Install() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dst := InstalledExePath()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if abs, _ := filepath.Abs(self); abs != dst {
		if err := copyFile(self, dst); err != nil {
			return fmt.Errorf("copie du binaire: %w", err)
		}
	}
	switch runtime.GOOS {
	case "linux":
		return installSystemd(dst)
	case "darwin":
		return installLaunchd(dst)
	}
	return fmt.Errorf("installation auto non gérée sur %s — lance « ajean-remote connect » via ton propre gestionnaire de démarrage", runtime.GOOS)
}

func Uninstall() error {
	switch runtime.GOOS {
	case "linux":
		_ = exec.Command("systemctl", "--user", "disable", "--now", "ajean-remote.service").Run()
		return os.Remove(filepath.Join(os.Getenv("HOME"), ".config/systemd/user/ajean-remote.service"))
	case "darwin":
		p := filepath.Join(os.Getenv("HOME"), "Library/LaunchAgents/link.ajean.remote.plist")
		_ = exec.Command("launchctl", "unload", p).Run()
		return os.Remove(p)
	}
	return fmt.Errorf("désinstallation non gérée sur %s", runtime.GOOS)
}

func installSystemd(exe string) error {
	unit := "[Unit]\nDescription=AJEAN poste distant\nAfter=network-online.target\n\n" +
		"[Service]\nExecStart=" + exe + " run\nRestart=always\nRestartSec=5\n\n" +
		"[Install]\nWantedBy=default.target\n"
	dir := filepath.Join(os.Getenv("HOME"), ".config/systemd/user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "ajean-remote.service"), []byte(unit), 0o644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	if err := exec.Command("systemctl", "--user", "enable", "--now", "ajean-remote.service").Run(); err != nil {
		return fmt.Errorf("systemctl --user enable: %w (astuce : « loginctl enable-linger %s » pour survivre à la déconnexion)", err, os.Getenv("USER"))
	}
	return nil
}

func installLaunchd(exe string) error {
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>link.ajean.remote</string>
  <key>ProgramArguments</key><array><string>` + exe + `</string><string>run</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>
`
	dir := filepath.Join(os.Getenv("HOME"), "Library/LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, "link.ajean.remote.plist")
	if err := os.WriteFile(p, []byte(plist), 0o644); err != nil {
		return err
	}
	return exec.Command("launchctl", "load", p).Run()
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}
