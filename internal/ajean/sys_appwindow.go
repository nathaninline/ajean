package ajean

// sys_appwindow.go — ouvrir AJEAN dans sa PROPRE fenêtre, comme une app native.
//
// Plutôt qu'un onglet de plus dans le navigateur, on lance un navigateur
// Chromium (Edge, toujours présent sur Windows 10/11 ; Chrome, Brave, Chromium
// ailleurs) en mode application : `--app=URL` donne une fenêtre sans onglets ni
// barre d'adresse, avec sa propre entrée dans la barre des tâches / le Dock.
// Aucun CGO : les binaires Windows/Linux restent cross-compilés depuis ubuntu.
//
// Un profil dédié (--user-data-dir) sépare la fenêtre AJEAN du navigateur
// habituel : entrée distincte dans la barre des tâches, et relancer AJEAN
// rouvre la fenêtre au lieu d'un onglet dans une session existante.
// Sans navigateur Chromium, on retombe sur le navigateur par défaut.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// openAppWindow ouvre l'URL dans une fenêtre d'application dédiée, ou à défaut
// dans le navigateur par défaut. AJEAN_BROWSER=1 force le navigateur classique.
func openAppWindow(url string) error {
	if os.Getenv("AJEAN_BROWSER") == "1" {
		return openBrowser(url)
	}
	bin := findChromium()
	if bin == "" {
		return openBrowser(url)
	}
	args := []string{
		"--app=" + url,
		"--no-first-run",
		"--no-default-browser-check",
		"--window-size=1200,860",
	}
	if dir := appWindowProfileDir(); dir != "" {
		args = append(args, "--user-data-dir="+dir)
	}
	// Surtout pas hideCmd : HideWindow (SW_HIDE) est appliqué par Edge à sa
	// première fenêtre, qui reste alors un cadre gris inerte (#101).
	if err := exec.Command(bin, args...).Start(); err != nil {
		return openBrowser(url)
	}
	return nil
}

// envRestarted : posée par l'accompagnateur de relance après mise à jour (voir
// cmdRestartAfterUpdate) pour que la nouvelle instance réutilise la fenêtre.
const envRestarted = "AJEAN_RESTARTED"

// appWindowAlive : une fenêtre AJEAN (profil dédié) est-elle encore ouverte ?
// Chromium tient le fichier « lockfile » de son profil ouvert en exclusif tant
// qu'il tourne (Windows) : s'il résiste à la suppression, la fenêtre vit. Un
// verrou orphelin, lui, se supprime sans dommage. Hors Windows (lien symbolique
// SingletonLock, sémantique différente) on répond non : rouvrir reste sûr.
func appWindowAlive() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	dir := appWindowProfileDir()
	if dir == "" {
		return false
	}
	lock := filepath.Join(dir, "lockfile")
	if _, err := os.Stat(lock); err != nil {
		return false
	}
	return os.Remove(lock) != nil
}

// appWindowProfileDir : profil propre à la fenêtre AJEAN, dans le dossier de
// config de l'UTILISATEUR (le dossier de données peut être machine/root).
func appWindowProfileDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(base, "ajean", "appwindow")
	if os.MkdirAll(dir, 0o700) != nil {
		return ""
	}
	return dir
}

// findChromium cherche un navigateur qui sait faire `--app`, Edge en premier
// sous Windows (installé d'office), Chrome en premier ailleurs.
func findChromium() string {
	var cands []string
	switch runtime.GOOS {
	case "windows":
		var roots []string
		for _, e := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
			if v := os.Getenv(e); v != "" {
				roots = append(roots, v)
			}
		}
		for _, rel := range []string{
			`Microsoft\Edge\Application\msedge.exe`,
			`Google\Chrome\Application\chrome.exe`,
			`BraveSoftware\Brave-Browser\Application\brave.exe`,
			`Chromium\Application\chrome.exe`,
		} {
			for _, r := range roots {
				cands = append(cands, filepath.Join(r, rel))
			}
		}
	case "darwin":
		for _, app := range []string{
			"Google Chrome.app/Contents/MacOS/Google Chrome",
			"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"Brave Browser.app/Contents/MacOS/Brave Browser",
			"Chromium.app/Contents/MacOS/Chromium",
		} {
			cands = append(cands, "/Applications/"+app)
			if h, err := os.UserHomeDir(); err == nil {
				cands = append(cands, filepath.Join(h, "Applications", app))
			}
		}
	default:
		for _, name := range []string{
			"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
			"microsoft-edge", "microsoft-edge-stable", "brave-browser",
		} {
			if p, err := exec.LookPath(name); err == nil {
				return p
			}
		}
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}
