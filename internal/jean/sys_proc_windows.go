//go:build windows

package jean

// sys_proc_windows.go — au double-clic, on relance Jean en processus DÉTACHÉ, sans
// console. L'original (qui, lui, possède la console fraîche créée par Explorer)
// se termine aussitôt, fermant sa console. Le nouveau process n'a aucune fenêtre
// de console : il n'y a donc plus de « terminal noir » et plus rien à fermer qui
// couperait le serveur. Seuls restent le splash puis l'icône de la zone de
// notification.

import (
	"os"
	"os/exec"
	"syscall"
)

func relaunchDetachedApp() {
	const (
		detachedProcess  = 0x00000008
		createNoWindow   = 0x08000000
		createNewProcGrp = 0x00000200
	)
	exe, err := os.Executable()
	if err != nil {
		_ = cmdApp(nil) // repli : on lance quand même en place
		return
	}
	cmd := exec.Command(exe, "app")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | createNoWindow | createNewProcGrp,
	}
	if err := cmd.Start(); err != nil {
		_ = cmdApp(nil)
		return
	}
	os.Exit(0) // ferme l'original → sa console disparaît ; le détaché continue
}
