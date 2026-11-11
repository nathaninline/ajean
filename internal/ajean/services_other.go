//go:build !linux

package ajean

// Hors Linux : pas d'assistant root ni de gardien séparé (le chien de garde du
// serveur web s'en charge).

import (
	"errors"
	"time"
)

type GuardKill struct {
	Name  string `json:"name"`
	Bytes uint64 `json:"bytes"`
	At    int64  `json:"at"`
}

func GuardKills() []GuardKill { return nil }
func osHasRootGuard() bool    { return false }

func osSvcLastExit(s *Service) time.Time { return time.Time{} }

func osPortOwner(port string) (unit, program string) {
	if svcPortBusy(port) {
		// Le port appartient-il à un service géré en marche ?
		list, _ := LoadServices()
		for i := range list {
			if list[i].Local() && localPort(&list[i]) == port && osSvcState(&list[i]) == "active" {
				return ServiceUnitName(&list[i]), list[i].Name
			}
		}
		return "", "un autre programme"
	}
	return "", ""
}

// osSvcWaitOutcome : 8 s sans passer à l'arrêt.
func osSvcWaitOutcome(s *Service) bool {
	for i := 0; i < 16; i++ {
		time.Sleep(500 * time.Millisecond)
		if st := osSvcState(s); st == "failed" || st == "inactive" {
			return false
		}
	}
	return true
}

func osSvcFailure(s *Service) (string, string) {
	return failureFromLog(osSvcLogTail(s, 60), localPort(s))
}

func cmdSvcRoot(args []string) error       { return errors.New("réservé à Linux") }
func cmdInstallHelper(args []string) error { return errors.New("réservé à Linux") }
func cmdGuard(args []string) error         { return errors.New("réservé à Linux") }

// Démarrage avec la machine : mémorisé dans la définition, appliqué à l'unité
// (type de démarrage SCM, RunAtLoad launchd).
func osSvcAutostart(s *Service) bool { return s.AutostartWanted }

func osSvcSetAutostart(s *Service, on bool) error {
	s.AutostartWanted = on
	if err := PutService(*s); err != nil {
		return err
	}
	return osSvcInstall(s)
}
