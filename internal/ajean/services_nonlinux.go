//go:build !linux

package ajean

import (
	"path/filepath"
	"runtime"
	"strings"
)

// svcLogPath : journal fichier (Windows et macOS ; Linux passe par journalctl).
func svcLogPath(s *Service) string {
	return filepath.Join(AjeanHome(), "services", s.ID+".log")
}

// argv : le programme à lancer (Windows et macOS ; Linux passe par l'unité).
// Une commande sur une ligne passe par le shell, comme dans un terminal.
func (s *Service) argv() []string {
	if c := strings.TrimSpace(s.Command); c != "" {
		if runtime.GOOS == "windows" {
			return []string{"cmd.exe", "/C", c}
		}
		return []string{"/bin/bash", "-lc", c}
	}
	return append([]string{s.Exec}, s.Args...)
}
