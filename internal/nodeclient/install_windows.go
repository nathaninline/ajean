package nodeclient

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const serviceName = "AjeanRemote"

// RunService lance la boucle client : en vrai service Windows si on est démarré
// par le gestionnaire de services, sinon en avant-plan (utile pour `run` lancé
// à la main).
func RunService(cfg Config) error {
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isSvc {
		Run(context.Background(), cfg, false)
		return nil
	}
	return svc.Run(serviceName, &handler{cfg: cfg})
}

type handler struct{ cfg Config }

func (h *handler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	s <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, h.cfg, true) // quiet : un service n'a pas de console
	s <- svc.Status{State: svc.Running, Accepts: accepted}
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			s <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			s <- svc.Status{State: svc.StopPending}
			return false, 0
		}
	}
	return false, 0
}

// Install copie le binaire à un emplacement stable, crée le service (démarrage
// automatique) et le lance. Nécessite les droits administrateur.
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
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("gestionnaire de services (droits admin requis ?): %w", err)
	}
	defer m.Disconnect()
	if s, err := m.OpenService(serviceName); err == nil {
		s.Close()
		return fmt.Errorf("service déjà installé — « ajean remote uninstall » d'abord")
	}
	s, err := m.CreateService(serviceName, dst, mgr.Config{
		StartType:   mgr.StartAutomatic,
		DisplayName: "AJEAN — poste distant",
		Description: "Pont léger permettant à votre serveur IA AJEAN d'agir sur ce PC.",
	}, "remote", "run")
	if err != nil {
		return fmt.Errorf("création du service: %w", err)
	}
	defer s.Close()
	if err := s.Start(); err != nil {
		return fmt.Errorf("service créé mais démarrage échoué: %w", err)
	}
	return nil
}

// Uninstall arrête et supprime le service.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service non installé")
	}
	defer s.Close()
	_, _ = s.Control(svc.Stop)
	time.Sleep(700 * time.Millisecond)
	return s.Delete()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
