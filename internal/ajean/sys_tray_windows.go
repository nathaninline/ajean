//go:build windows

package ajean

// sys_tray_windows.go — icône de AJEAN dans la zone de notification Windows (system
// tray). Quand on lance l'app (double-clic ou `ajean app`), l'UI web s'ouvre dans
// le navigateur ET une petite icône apparaît près de l'horloge : elle montre que
// AJEAN tourne et offre un menu « Ouvrir AJEAN » / « Quitter ».
//
// getlantern/systray est pur Go sur Windows (Win32 via x/sys, aucun CGO) — le
// binaire reste statique. L'équivalent macOS vit dans sys_tray_darwin.go (Cocoa,
// donc CGO) ; les builds Linux ne touchent jamais systray.

import (
	"os"

	"github.com/getlantern/systray"
)

// runTray ouvre l'UI dans le navigateur puis fait tourner l'icône de la zone de
// notification. Bloque jusqu'à ce que l'utilisateur choisisse « Quitter ».
func runTray(url string) {
	systray.Run(func() {
		systray.SetIcon(trayIcon())
		systray.SetTitle("AJEAN")
		systray.SetTooltip("AJEAN — votre IA locale")
		mOpen := systray.AddMenuItem("Ouvrir AJEAN", "Ouvrir l'interface")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quitter", "Arrêter AJEAN et décharger le modèle")

		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					_ = openAppWindow(url)
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, func() {
		// « Quitter » arrête aussi l'ancien moteur (0.18 et avant), s'il tourne
		// encore : il vivait dans un process DÉTACHÉ dont l'app était
		// propriétaire, et plus rien d'autre ne l'arrêterait. Les modèles
		// d'aujourd'hui sont des services Windows : fermer l'interface ne les
		// touche pas, comme sous Linux et macOS.
		_ = svcStop(false)
		os.Exit(0)
	})
}

// trayIcon : l'icône de marque en .ico, deux tailles pour rester nette selon
// l'échelle d'affichage. Le rendu vit dans sys_brand_icon.go, partagé avec
// l'icône du .exe.
func trayIcon() []byte { return BrandICO(16, trayIconSize) }
