//go:build windows

package jean

// sys_console_windows.go — gestion de la console sous Windows.
//
// Le binaire est compilé en sous-système GUI (`-H=windowsgui`) : au double-clic,
// Windows n'alloue AUCUNE console → plus jamais de « fenêtre noire de terminal »
// qui s'ouvre et se ferme. En contrepartie, un binaire GUI lancé depuis un
// terminal (cmd/PowerShell) n'écrit nulle part par défaut : on se rattache alors
// explicitement à la console du parent et on réouvre stdout/stderr/stdin dessus,
// pour que l'usage CLI (`jean web`, `jean status`, …) reste lisible.

import (
	"os"
	"syscall"
	"unsafe"
)

// setupConsole rattache le process à la console de son parent si elle existe.
// Retourne true si on dispose d'une console (→ usage CLI), false sinon (double-
// clic sans console → expérience « application »).
func setupConsole() bool {
	const attachParentProcess = ^uintptr(0) // (DWORD)-1 = ATTACH_PARENT_PROCESS
	k := syscall.NewLazyDLL("kernel32.dll")
	r, _, _ := k.NewProc("AttachConsole").Call(attachParentProcess)
	if r == 0 {
		return false // aucune console parente = lancé par double-clic (mode app)
	}
	// Réouvre les flux standard sur la console fraîchement rattachée : sans ça,
	// os.Stdout/Stderr pointent sur des handles invalides (sous-système GUI).
	if con, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = con
		os.Stderr = con
	}
	if cin, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = cin
	}
	configureConsole()
	return true
}

// configureConsole passe la console en UTF-8 + traitement des séquences ANSI
// (couleurs/curseur), comme le faisait l'init() au chargement — mais APRÈS le
// rattachement, sinon ça ciblait une console encore inexistante.
func configureConsole() {
	const (
		cpUTF8                          = 65001
		enableVirtualTerminalProcessing = 0x0004
		stdOutputHandle                 = ^uintptr(10) // -11
	)
	k := syscall.NewLazyDLL("kernel32.dll")
	_, _, _ = k.NewProc("SetConsoleOutputCP").Call(uintptr(cpUTF8))
	_, _, _ = k.NewProc("SetConsoleCP").Call(uintptr(cpUTF8))
	h, _, _ := k.NewProc("GetStdHandle").Call(stdOutputHandle)
	var mode uint32
	if r, _, _ := k.NewProc("GetConsoleMode").Call(h, uintptr(unsafe.Pointer(&mode))); r != 0 {
		_, _, _ = k.NewProc("SetConsoleMode").Call(h, uintptr(mode|enableVirtualTerminalProcessing))
	}
}
