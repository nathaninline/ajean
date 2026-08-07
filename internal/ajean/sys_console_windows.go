//go:build windows

package ajean

// sys_console_windows.go — gestion de la console sous Windows.
//
// Le binaire est compilé en sous-système GUI (`-H=windowsgui`) : au double-clic,
// Windows n'alloue AUCUNE console → plus jamais de « fenêtre noire de terminal »
// qui s'ouvre et se ferme. En contrepartie, un binaire GUI lancé depuis un
// terminal (cmd/PowerShell) n'écrit nulle part par défaut : on se rattache alors
// explicitement à la console du parent et on réouvre stdout/stderr/stdin dessus,
// pour que l'usage CLI (`ajean web`, `ajean status`, …) reste lisible.

import (
	"os"
	"syscall"
	"unsafe"
)

// setupConsole rattache le process à la console de son parent si elle existe.
// Retourne true si on dispose d'une console (→ usage CLI), false sinon (double-
// clic sans console → expérience « application »).
func setupConsole() bool {
	// Relevé AVANT le rattachement, et c'est tout l'enjeu : AttachConsole
	// REMPLACE lui-même les handles standard du processus par ceux de la
	// console. Interrogé après, on ne voit donc plus jamais qu'une console, et
	// la trace de la redirection posée par le shell est déjà perdue.
	outRedirected := stdRedirected(stdOutputHandle)
	errRedirected := stdRedirected(stdErrorHandle)
	inRedirected := stdRedirected(stdInputHandle)

	const attachParentProcess = ^uintptr(0) // (DWORD)-1 = ATTACH_PARENT_PROCESS
	k := syscall.NewLazyDLL("kernel32.dll")
	r, _, _ := k.NewProc("AttachConsole").Call(attachParentProcess)
	if r == 0 {
		return false // aucune console parente = lancé par double-clic (mode app)
	}
	// Réouvre les flux standard sur la console fraîchement rattachée : sans ça,
	// os.Stdout/Stderr pointent sur des handles invalides (sous-système GUI).
	//
	// MAIS seulement ceux que le shell n'a pas déjà branchés ailleurs. Réouvrir
	// systématiquement sur CONOUT$ écrasait la redirection : « ajean where >
	// fichier.txt » n'écrivait rien dans le fichier, et « ajean where | findstr »
	// ne transmettait rien au tube — tout partait sur la console. On ne touche
	// donc qu'aux flux réellement inutilisables.
	if !outRedirected {
		if con, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stdout = con
		}
	}
	if !errRedirected {
		if con, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stderr = con
		}
	}
	if !inRedirected {
		if cin, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
			os.Stdin = cin
		}
	}
	configureConsole()
	return true
}

// Handles standard, tels que les nomme GetStdHandle.
const (
	stdInputHandle  = ^uintptr(9)  // -10
	stdOutputHandle = ^uintptr(10) // -11
	stdErrorHandle  = ^uintptr(11) // -12
)

// stdRedirected dit si un flux standard est DÉJÀ branché sur un fichier ou un
// tube par le processus parent. Dans ce cas il ne faut surtout pas le remplacer
// par la console : c'est ce qui cassait « > fichier » et « | commande ».
//
// Un flux de type « caractère » (la console elle-même) ou inutilisable n'est pas
// considéré comme redirigé : on le réouvre alors sur CONOUT$/CONIN$, ce dont un
// binaire GUI a besoin pour écrire quelque part.
func stdRedirected(which uintptr) bool {
	const (
		invalidHandle = ^uintptr(0) // INVALID_HANDLE_VALUE
		fileTypeDisk  = 1
		fileTypePipe  = 3
	)
	k := syscall.NewLazyDLL("kernel32.dll")
	h, _, _ := k.NewProc("GetStdHandle").Call(which)
	if h == 0 || h == invalidHandle {
		return false
	}
	t, _, _ := k.NewProc("GetFileType").Call(h)
	return t == fileTypeDisk || t == fileTypePipe
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

// appWarning : rien à signaler sous Windows (pas d'App Translocation). Voir
// sys_console_darwin.go.
func appWarning() string { return "" }
