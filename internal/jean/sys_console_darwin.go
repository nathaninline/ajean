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
		fixFinderPath()
		return false
	}
	return true
}

// appWarning signale une situation de lancement qui casse Jean de façon peu
// évidente. Aujourd'hui : l'App Translocation de Gatekeeper. Une app non signée
// ouverte depuis Téléchargements est exécutée depuis une copie en LECTURE SEULE
// sous /private/var/folders/…/AppTranslocation/<uuid>/, à un chemin différent à
// CHAQUE lancement. Conséquences vécues : plusieurs instances qui se marchent
// dessus (la seconde trouve le port 8090 occupé et se contente d'ouvrir l'UI de
// la première, souvent une version périmée), des process fantômes issus de
// copies précédentes, et une mise à jour en place impossible.
// Le remède est côté utilisateur : déplacer Jean.app dans /Applications.
func appWarning() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if !strings.Contains(exe, "/AppTranslocation/") {
		return ""
	}
	return "AJEAN tourne depuis une copie temporaire en lecture seule (App Translocation de macOS). " +
		"Fermez l'app, déplacez Jean.app dans le dossier Applications, puis rouvrez-la — " +
		"sinon chaque lancement crée une instance séparée et les mises à jour du moteur échouent."
}

// fixFinderPath complète le PATH famélique hérité du Finder. Une app lancée par
// LaunchServices reçoit /usr/bin:/bin:/usr/sbin:/sbin — pas les dossiers de
// Homebrew : sans ça, Jean ne voit ni `brew`, ni `cmake`, ni `git`, et la
// compilation d'un backend échoue alors que les outils sont bien installés.
func fixFinderPath() {
	extra := []string{
		"/opt/homebrew/bin", "/opt/homebrew/sbin", // Homebrew Apple Silicon
		"/usr/local/bin", "/usr/local/sbin", // Homebrew Intel
		"/opt/local/bin", // MacPorts
	}
	path := os.Getenv("PATH")
	have := map[string]bool{}
	for _, p := range strings.Split(path, ":") {
		have[p] = true
	}
	var add []string
	for _, p := range extra {
		if !have[p] {
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				add = append(add, p)
			}
		}
	}
	if len(add) > 0 {
		_ = os.Setenv("PATH", strings.Join(add, ":")+":"+path)
	}
}
