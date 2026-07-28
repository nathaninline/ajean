//go:build windows

package jean

// sys_tray_windows.go — icône de Jean dans la zone de notification Windows (system
// tray). Quand on lance l'app (double-clic ou `jean app`), l'UI web s'ouvre dans
// le navigateur ET une petite icône apparaît près de l'horloge : elle montre que
// Jean tourne et offre un menu « Ouvrir Jean » / « Quitter ».
//
// getlantern/systray est pur Go sur Windows (Win32 via x/sys, aucun CGO) — le
// binaire reste statique. L'équivalent macOS vit dans sys_tray_darwin.go (Cocoa,
// donc CGO) ; les builds Linux ne touchent jamais systray.

import (
	"bytes"
	"encoding/binary"
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

// trayIcon emballe le PNG de trayIconPNG() dans un conteneur .ico (ICONDIR +
// une ICONDIRENTRY) : Windows accepte un PNG (avec alpha pour les coins
// arrondis) comme image d'un .ico.
func trayIcon() []byte {
	p := trayIconPNG()
	const n = trayIconSize
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, uint16(0))      // réservé
	binary.Write(&ico, binary.LittleEndian, uint16(1))      // type = icône
	binary.Write(&ico, binary.LittleEndian, uint16(1))      // nombre d'images
	ico.WriteByte(n)                                        // largeur
	ico.WriteByte(n)                                        // hauteur
	ico.WriteByte(0)                                        // couleurs
	ico.WriteByte(0)                                        // réservé
	binary.Write(&ico, binary.LittleEndian, uint16(1))      // plans
	binary.Write(&ico, binary.LittleEndian, uint16(32))     // bits/pixel
	binary.Write(&ico, binary.LittleEndian, uint32(len(p))) // taille données
	binary.Write(&ico, binary.LittleEndian, uint32(6+16))   // offset données
	ico.Write(p)
	return ico.Bytes()
}
