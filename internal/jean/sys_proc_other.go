//go:build !windows

package jean

// sys_proc_other.go — hors Windows, pas de détachement (le mode app double-clic est
// propre à Windows). Défini pour la compilation ; non appelé en pratique car
// launchedByDoubleClick renvoie false sur Unix.

func relaunchDetachedApp() { _ = cmdApp(nil) }
