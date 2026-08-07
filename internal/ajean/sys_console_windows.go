//go:build windows

package ajean

// sys_console_windows.go — gestion de la console sous Windows.
//
// Le binaire est compilé en sous-système CONSOLE. C'est ce qui fait que `cmd`
// ATTEND la fin du programme : avec un binaire graphique, il rendait la main
// aussitôt et réaffichait son invite AVANT la sortie d'AJEAN, ce qui donnait
//
//	C:\Users\Alice>ajean
//
//	C:\Users\Alice>ajean 0.8.0 — manager llama.cpp + UI web
//
// La même cause cassait la redirection et les tubes, et rendait `ajean chat`
// inutilisable depuis un terminal (le shell et AJEAN se disputaient l'entrée).
//
// EN CONTREPARTIE, Windows alloue une console à tout lancement sans terminal —
// un double-clic. On la referme alors immédiatement : voir setupConsole. Les
// raccourcis créés par l'installation demandent en plus un démarrage minimisé,
// si bien que cette console n'est jamais peinte à l'écran.

import (
	"syscall"
	"unsafe"
)

// win appelle une procédure Win32 et renvoie (résultat, trouvée).
//
// LazyProc.Call PANIQUE quand la procédure est introuvable. Or tout ce fichier
// s'exécute AVANT le reste du programme : une panique ici et il ne reste rien à
// l'écran pour la comprendre, l'application ne démarre tout simplement plus.
// C'est exactement ce qui est arrivé en cherchant ShowWindow dans kernel32
// alors qu'elle vit dans user32. Aucun réglage de confort ne mérite ça.
func win(dll *syscall.LazyDLL, name string, a ...uintptr) (uintptr, bool) {
	p := dll.NewProc(name)
	if err := p.Find(); err != nil {
		return 0, false
	}
	r, _, _ := p.Call(a...)
	return r, true
}

var kernel32 = syscall.NewLazyDLL("kernel32.dll")

// setupConsole décide dans quel monde on tourne et renvoie true en usage CLI.
//
// Le signal est le nombre de processus rattachés à notre console : si nous
// sommes le SEUL, c'est que Windows l'a créée pour nous, donc qu'aucun terminal
// ne nous a lancés. On la fait disparaître et on bascule en mode application.
// Un shell rattaché (cmd, PowerShell, Windows Terminal) en fait au moins deux.
func setupConsole() bool {
	// Le compte vaut 0 quand il n'y a aucune console (parent détaché). On répond
	// alors « CLI », le cas contraire étant couvert autrement : les relances
	// internes passent « app » explicitement (voir launch, sys_firstrun_windows.go).
	// Mieux vaut afficher une aide inutile que démarrer un serveur en silence.
	var pids [8]uint32
	n, ok := win(kernel32, "GetConsoleProcessList",
		uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if ok && n == 1 {
		// Masquer AVANT de libérer : sinon la fenêtre reste peinte le temps que
		// Windows la détruise. ShowWindow est dans user32, pas kernel32.
		if hwnd, ok := win(kernel32, "GetConsoleWindow"); ok && hwnd != 0 {
			const swHide = 0
			win(u32s, "ShowWindow", hwnd, swHide)
		}
		win(kernel32, "FreeConsole")
		return false
	}

	// Lancé depuis un terminal : les flux standard sont déjà ceux du shell,
	// redirections et tubes compris. Il n'y a rien à rebrancher, seulement à
	// régler l'encodage et les couleurs.
	configureConsole()
	return true
}

// configureConsole passe la console en UTF-8 + traitement des séquences ANSI
// (couleurs/curseur).
func configureConsole() {
	const (
		cpUTF8                          = 65001
		enableVirtualTerminalProcessing = 0x0004
		stdOutputHandle                 = ^uintptr(10) // -11
	)
	win(kernel32, "SetConsoleOutputCP", uintptr(cpUTF8))
	win(kernel32, "SetConsoleCP", uintptr(cpUTF8))
	h, ok := win(kernel32, "GetStdHandle", stdOutputHandle)
	if !ok {
		return
	}
	var mode uint32
	if r, ok := win(kernel32, "GetConsoleMode", h, uintptr(unsafe.Pointer(&mode))); ok && r != 0 {
		win(kernel32, "SetConsoleMode", h, uintptr(mode|enableVirtualTerminalProcessing))
	}
}

// appWarning : rien à signaler sous Windows (pas d'App Translocation). Voir
// sys_console_darwin.go.
func appWarning() string { return "" }
