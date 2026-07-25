//go:build darwin

package jean

// sys_tray_darwin.go — icône de Jean dans la barre de menus macOS (en haut à
// droite, à côté de l'horloge), pendant exact du system tray Windows : quand on
// ouvre Jean.app, l'UI web s'ouvre dans le navigateur ET l'icône apparaît, avec
// le menu « Ouvrir Jean » / « Quitter ».
//
// Contrairement à Windows, systray passe ici par Cocoa → CGO obligatoire : les
// binaires macOS sont donc compilés sur un runner macOS (voir release.yml), et
// le bundle est marqué LSUIElement (pas d'icône dans le Dock, uniquement la
// barre de menus).

import (
	"os"

	"github.com/getlantern/systray"
)

// runTray fait tourner l'icône de la barre de menus. Bloque jusqu'à « Quitter ».
// À appeler depuis la goroutine principale : Cocoa exige le thread main.
func runTray(url string) {
	systray.Run(func() {
		systray.SetIcon(trayIconPNG())
		systray.SetTooltip("Jean — votre IA locale")
		mOpen := systray.AddMenuItem("Ouvrir Jean", "Ouvrir l'interface")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quitter", "Arrêter Jean")

		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					_ = openBrowser(url)
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, func() {
		os.Exit(0) // « Quitter » → arrête tout le process (serveur inclus)
	})
}
