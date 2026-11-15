package embedder

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

const (
	createNoWindow       = 0x08000000
	processTerminate     = 0x0001
	processSetQuota      = 0x0100
	jobExtendedLimitInfo = 9
	jobKillOnClose       = 0x2000
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	globalMemoryStatusEx     = kernel32.NewProc("GlobalMemoryStatusEx")
	createJobObject          = kernel32.NewProc("CreateJobObjectW")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
)

// prepare masque la fenêtre de console que Windows ouvrirait pour le moteur.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// jobLimits : JOBOBJECT_EXTENDED_LIMIT_INFORMATION, dont seul le drapeau sert.
type jobLimits struct {
	perProcessUserTime, perJobUserTime int64
	flags                              uint32
	minWorkingSet, maxWorkingSet       uintptr
	activeProcesses                    uint32
	affinity                           uintptr
	priorityClass, schedulingClass     uint32
	ioCounters                         [6]uint64
	processMemory, jobMemory           uintptr
	peakProcessMemory, peakJobMemory   uintptr
}

// killOnExit : un « job » Windows qui tue ses membres quand AJEAN se termine, de
// quelque façon que ce soit. Sans lui, fermer la fenêtre d'AJEAN laisserait le
// moteur tourner sans fin, avec sa mémoire vive.
var killOnExit = sync.OnceValue(func() syscall.Handle {
	h, _, _ := createJobObject.Call(0, 0)
	if h == 0 {
		return 0
	}
	limits := jobLimits{flags: jobKillOnClose}
	if ok, _, _ := setInformationJobObject.Call(h, jobExtendedLimitInfo, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); ok == 0 {
		_ = syscall.CloseHandle(syscall.Handle(h))
		return 0
	}
	return syscall.Handle(h)
})

// adopt lie le moteur lancé à la vie d'AJEAN. Si Windows refuse, le moteur
// reste arrêté par la voie normale (fin du programme, inactivité).
func adopt(cmd *exec.Cmd) {
	group := killOnExit()
	if group == 0 {
		return
	}
	h, err := syscall.OpenProcess(processTerminate|processSetQuota, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	defer syscall.CloseHandle(h)
	_, _, _ = assignProcessToJobObject.Call(uintptr(group), uintptr(h))
}

// freeRAM : la mémoire vive disponible, en Mo (known=false si Windows ne
// répond pas : on ne bloque alors rien).
func freeRAM() (mb int, known bool) {
	var m struct {
		length                       uint32
		load                         uint32
		totalPhys, availPhys         uint64
		totalPageFile, availPageFile uint64
		totalVirtual, availVirtual   uint64
		availExtendedVirtual         uint64
	}
	m.length = uint32(unsafe.Sizeof(m))
	if ok, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); ok == 0 {
		return 0, false
	}
	return int(m.availPhys >> 20), true
}
