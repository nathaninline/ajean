//go:build !windows

package jean

// sys_splash_other.go — pas de fenêtre de démarrage hors Windows (no-op).

type splash struct{}

func showSplash(text string) *splash { return &splash{} }
func (s *splash) close()             {}
