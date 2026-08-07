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

// setupConsole décide dans quel monde on tourne et renvoie true en usage CLI.
//
// Le signal est le nombre de processus rattachés à notre console : si nous
// sommes le SEUL, c'est que Windows l'a créée pour nous, donc qu'aucun terminal
// ne nous a lancés. On la fait disparaître et on bascule en mode application.
// Un shell rattaché (cmd, PowerShell, Windows Terminal) en fait au moins deux.
func setupConsole() bool {
	k := syscall.NewLazyDLL("kernel32.dll")

	// Combien de processus partagent notre console ? Nous seul (1) = personne ne
	// nous a lancés depuis un terminal, Windows a créé cette console pour nous :
	// c'est un double-clic. Un shell rattaché en fait au moins deux.
	//
	// Le compte vaut 0 quand il n'y a aucune console (parent détaché). On répond
	// alors « CLI », le cas contraire étant couvert autrement : les relances
	// internes passent « app » explicitement (voir launch, sys_firstrun_windows.go).
	// Mieux vaut afficher une aide inutile que démarrer un serveur en silence.
	var pids [8]uint32
	n, _, _ := k.NewProc("GetConsoleProcessList").Call(
		uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n == 1 {
		if hwnd, _, _ := k.NewProc("GetConsoleWindow").Call(); hwnd != 0 {
			// Masquer AVANT de libérer : sinon la fenêtre reste peinte le temps
			// que Windows la détruise.
			const swHide = 0
			_, _, _ = k.NewProc("ShowWindow").Call(hwnd, swHide)
		}
		_, _, _ = k.NewProc("FreeConsole").Call()
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
