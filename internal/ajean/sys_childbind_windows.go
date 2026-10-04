//go:build windows

package ajean

// Lier un processus enfant (le Chrome du computer use) à la vie d'AJEAN : s'il
// meurt, même tué brutalement (arrêt forcé, plantage, redémarrage), Windows
// ferme le navigateur et tous ses sous-processus. Sans ça, chaque redémarrage
// laissait un Chrome headless orphelin ; vu le 2026-10-03 : 13 navigateurs
// (~150 processus) dont plusieurs décodaient encore une webcam, CPU à 100 %.
//
// Mécanisme : un « job object » avec KILL_ON_JOB_CLOSE. Seul AJEAN en détient
// le handle ; à sa mort le système le ferme et tue tout le job. L'enfant est
// rattaché juste après son lancement, SANS être créé suspendu : le couple
// CREATE_SUSPENDED + NtResumeProcess est la signature de l'injection de
// processus et faisait classer AJEAN en cheval de Troie par Defender
// (Bearfoos.A!ml). Les rares sous-processus nés avant le rattachement sont
// rattrapés par killProcessTree.

import (
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	childJobOnce sync.Once
	childJob     windows.Handle
)

func childJobHandle() windows.Handle {
	childJobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(h)
			return
		}
		childJob = h // jamais fermé : c'est la mort du process qui le ferme
	})
	return childJob
}

// prepareBoundChild : à appeler AVANT cmd.Start(). Rien à préparer sous
// Windows ; gardé pour la symétrie avec Linux.
func prepareBoundChild(cmd *exec.Cmd) {}

// bindChild : à appeler juste APRÈS cmd.Start(). Rattache l'enfant au job.
func bindChild(cmd *exec.Cmd) {
	if cmd.Process == nil || childJobHandle() == 0 {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.AssignProcessToJobObject(childJobHandle(), h)
}

// killProcessTree : tue le processus ET ses descendants. Process.Kill ne vise
// que le processus principal ; les sous-processus de Chrome lui survivaient.
func killProcessTree(cmd *exec.Cmd) {
	k := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	k.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if k.Run() != nil {
		_ = cmd.Process.Kill()
	}
}
