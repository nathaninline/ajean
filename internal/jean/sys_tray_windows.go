//go:build windows

package jean

// sys_tray_windows.go — icône de Jean dans la zone de notification Windows (system
// tray). Quand on lance l'app (double-clic ou `jean app`), l'UI web s'ouvre dans
// le navigateur ET une petite icône apparaît près de l'horloge : elle montre que
// Jean tourne et offre un menu « Ouvrir Jean » / « Quitter ».
//
// getlantern/systray est pur Go sur Windows (Win32 via x/sys, aucun CGO) — le
// binaire reste statique. L'import est confiné à ce fichier windows pour que les
// builds Linux/macOS (CGO_ENABLED=0) ne le touchent jamais.

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"

	"github.com/getlantern/systray"
)

// runTray ouvre l'UI dans le navigateur puis fait tourner l'icône de la zone de
// notification. Bloque jusqu'à ce que l'utilisateur choisisse « Quitter ».
func runTray(url string) {
	systray.Run(func() {
		systray.SetIcon(trayIcon())
		systray.SetTitle("Jean")
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

// trayIcon génère l'icône (.ico) de Jean, RÉPLIQUE EXACTE du favicon de l'UI
// web : carré bleu arrondi #1f6feb + « j » blanc (rects (6,3) (6,5) (4,7) sur
// une grille 12x12). Générée à la volée — pas d'asset binaire à embarquer.
// Windows accepte un PNG (avec alpha pour les coins arrondis) dans le .ico.
func trayIcon() []byte {
	const n = 32
	blue := color.RGBA{0x1f, 0x6f, 0xeb, 0xff}
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	clear := color.RGBA{0, 0, 0, 0}
	const r = 2.0 // rayon des coins, en unités de la grille 12
	// rects blancs du favicon (x, y, w, h en unités 12)
	rects := [][4]float64{{6, 3, 2, 2}, {6, 5, 2, 2}, {4, 7, 2, 2}}
	// hors du rectangle à coins arrondis (grille 12) → pixel transparent
	outside := func(gx, gy float64) bool {
		corner := func(cx, cy float64) bool {
			dx, dy := gx-cx, gy-cy
			return dx*dx+dy*dy > r*r
		}
		switch {
		case gx < r && gy < r:
			return corner(r, r)
		case gx > 12-r && gy < r:
			return corner(12-r, r)
		case gx < r && gy > 12-r:
			return corner(r, 12-r)
		case gx > 12-r && gy > 12-r:
			return corner(12-r, 12-r)
		}
		return false
	}
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	scale := float64(n) / 12
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			gx := (float64(x) + 0.5) / scale
			gy := (float64(y) + 0.5) / scale
			if outside(gx, gy) {
				img.Set(x, y, clear)
				continue
			}
			c := blue
			for _, rc := range rects {
				if gx >= rc[0] && gx < rc[0]+rc[2] && gy >= rc[1] && gy < rc[1]+rc[3] {
					c = white
				}
			}
			img.Set(x, y, c)
		}
	}

	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	p := pngBuf.Bytes()

	// Conteneur ICO : ICONDIR (6) + 1 ICONDIRENTRY (16) + PNG.
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
