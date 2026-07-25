//go:build darwin

package jean

// sys_console_darwin.go — détection du lancement « par clic » sous macOS.
//
// Sur macOS il n'y a pas d'équivalent d'AttachConsole : un binaire lancé depuis
// le Finder hérite quand même de descripteurs stdout/stderr (vers les logs
// système). Le signal fiable, c'est l'emplacement de l'exécutable : la release
// publie un bundle Jean.app dont le binaire vit dans
// Jean.app/Contents/MacOS/jean. Si on tourne depuis là, c'est un double-clic
// dans le Finder → expérience « application » (UI web + navigateur), exactement
// comme le double-clic sur jean.exe sous Windows.
//
// Le même binaire copié dans /usr/local/bin garde évidemment le comportement CLI.

import (
	"os"
	"path/filepath"
	"strings"
)

func setupConsole() bool {
	exe, err := os.Executable()
	if err != nil {
		return true
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	// .../Jean.app/Contents/MacOS/jean
	if strings.HasSuffix(filepath.Dir(exe), "/Contents/MacOS") {
		return false
	}
	return true
}
