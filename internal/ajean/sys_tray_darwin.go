//go:build darwin

package ajean

// sys_tray_darwin.go — icône de AJEAN dans la barre de menus macOS (en haut à
// droite, à côté de l'horloge), pendant exact du system tray Windows : quand on
// ouvre AJEAN.app, l'UI web s'ouvre dans le navigateur ET l'icône apparaît, avec
// le menu « Ouvrir AJEAN » / « Quitter ».
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
		// Icône « template » : macOS n'en garde que la forme (couche alpha) et la
		// colore selon le thème. Indispensable depuis que la marque est noire —
		// une icône noire « en dur » est illisible sur une barre de menus sombre.
		systray.SetTemplateIcon(brandTemplatePNG(trayIconSize), BrandIconPNG(trayIconSize))
		systray.SetTooltip("AJEAN — votre IA locale")
		mOpen := systray.AddMenuItem("Ouvrir AJEAN", "Ouvrir l'interface")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quitter", "Arrêter AJEAN")

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
