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
// lancé SUSPENDU, rattaché, puis relancé : aucun sous-processus ne peut naître
// avant le rattachement et s'échapper du job.

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
	ntResume     = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
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

// prepareBoundChild : à appeler AVANT cmd.Start().
func prepareBoundChild(cmd *exec.Cmd) {
	if childJobHandle() == 0 {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

// bindChild : à appeler juste APRÈS cmd.Start(). Rattache puis relance. Si le
// rattachement échoue, l'enfant est relancé quand même (mieux vaut un
// navigateur qui risque de rester orphelin qu'un navigateur figé).
func bindChild(cmd *exec.Cmd) {
	if cmd.Process == nil || cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED == 0 {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.AssignProcessToJobObject(childJobHandle(), h)
	ntResume.Call(uintptr(h))
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
