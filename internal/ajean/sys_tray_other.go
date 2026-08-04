//go:build !windows && !darwin

package ajean

// sys_tray_other.go — pas d'icône de zone de notification sous Linux (serveur
// sans session graphique la plupart du temps). On se contente d'ouvrir l'UI dans
// le navigateur et de garder le serveur en vie. Aucun import systray ici → les
// builds Linux restent purs (CGO_ENABLED=0).

func runTray(url string) {
	select {} // garde le serveur en vie (Ctrl-C pour quitter) ; le navigateur est déjà ouvert par cmdApp
}
