package ajean

// cu_autoshot.go — aperçu EN DIRECT du navigateur piloté (computer use). Après
// chaque action browser_*, on capture la page pour que l'UTILISATEUR voie ce que
// fait l'IA. C'est de l'interface seulement :
//   - jamais envoyé au modèle (zéro token, vision non requise) ;
//   - jamais écrit sur le disque : un petit anneau en mémoire vive ;
//   - retiré du journal en fin de tour (compactLogLocked), et l'UI efface la carte
//     à la fin du tour. Une capture à GARDER passe toujours par
//     browser_screenshot, qui l'enregistre dans le dossier de travail.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const cuShotRing = 8 // captures gardées en RAM (l'UI n'affiche que la dernière)

var (
	cuShotMu   sync.Mutex
	cuShotData = map[string][]byte{}
	cuShotIDs  []string
)

// cuAutoShotTool : outils après lesquels on capture (ceux qui changent la page).
func cuAutoShotTool(name string) bool {
	switch name {
	case "browser_open", "browser_click", "browser_click_xy", "browser_type", "browser_key", "browser_scroll", "browser_find":
		return true
	}
	return false
}

func cuLog(format string, a ...any) { fmt.Fprintf(os.Stderr, "[cu] "+format+"\n", a...) }

// cuAutoShot capture la page en pleine résolution (JPEG de bonne qualité) et
// renvoie l'identifiant de la capture en mémoire ("" si impossible : jamais
// bloquant pour l'outil).
func cuAutoShot() string {
	s, err := cdpGet()
	if err != nil {
		cuLog("aperçu : navigateur indisponible : %v", err)
		return ""
	}
	raw, err := s.screenshot()
	if err != nil {
		cuLog("aperçu : capture impossible : %v", err)
		return ""
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		cuLog("aperçu : image illisible (%d octets) : %v", len(raw), err)
		return ""
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return ""
	}
	id := fmt.Sprintf("%x", time.Now().UnixNano())
	cuShotMu.Lock()
	cuShotData[id] = buf.Bytes()
	cuShotIDs = append(cuShotIDs, id)
	for len(cuShotIDs) > cuShotRing {
		delete(cuShotData, cuShotIDs[0])
		cuShotIDs = cuShotIDs[1:]
	}
	cuShotMu.Unlock()
	return id
}

// handleCUShot (GET ?id=) : une capture en base64 (JSON, pour traverser aussi le
// tunnel chiffré de app.ajean.link). 404 si elle est déjà sortie de l'anneau.
func handleCUShot(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	cuShotMu.Lock()
	b := cuShotData[id]
	cuShotMu.Unlock()
	if b == nil {
		sendJSON(w, 404, map[string]any{"ok": false, "error": "capture expirée"})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "mime": "image/jpeg", "data": base64.StdEncoding.EncodeToString(b)})
}
