package ajean

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Ouverture des liens externes cliqués dans l'interface.
//
// Pourquoi le serveur s'en mêle : l'interface tourne dans un navigateur lancé en
// `--app=` (voir sys_appwindow.go), et un lien ouvert par window.open hérite du
// moteur de cette fenêtre — donc Edge sous Windows, quel que soit le navigateur
// par défaut de la machine. Seul le serveur peut en lancer un autre.
//
// Le réglage ne déplace QUE les liens : la fenêtre AJEAN reste où elle est.

// maxOpenURLLen : au-delà, ce n'est pas un lien qu'un humain a cliqué.
const maxOpenURLLen = 4096

// openURLExternally lance le navigateur et dit lequel a servi. Variable de paquet
// pour que les tests n'ouvrent jamais de vraie fenêtre.
var openURLExternally = launchExternalBrowser

// linkBrowserPref renvoie le navigateur choisi pour les liens. « auto » par
// défaut : on suit le gestionnaire de l'OS. Une valeur inconnue retombe sur
// « auto » — saveWebPrefs filtre déjà, mais le bucket peut avoir été écrit à la
// main.
func linkBrowserPref() string {
	v := strings.ToLower(strings.TrimSpace(loadWebPrefs()["link_browser"]))
	for _, n := range linkBrowserNames {
		if v == n {
			return n
		}
	}
	return "auto"
}

// validExternalURL n'accepte que http et https — la seule chose qu'un lien de
// conversation puisse être.
//
// Le contrôle du schéma fait trois métiers d'un coup, et c'est pour ça qu'il est
// ici plutôt qu'ailleurs :
//   - il écarte file:, javascript:, data:, vbscript: — ouvrir un fichier local
//     depuis un lien de conversation n'a aucune raison d'être ;
//   - il écarte une URL relative ou sans schéma, que url.Parse accepte sans
//     broncher ;
//   - il rend impossible un argument commençant par « - », donc l'injection de
//     drapeau dans la ligne de commande du navigateur (un --user-data-dir=… que
//     Windows consommerait comme une option). Sans ce contrôle, l'URL est un
//     argument parmi d'autres devant un binaire qui interprète tout ce qui
//     commence par un tiret.
func validExternalURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("lien vide")
	}
	if len(raw) > maxOpenURLLen {
		return "", fmt.Errorf("lien trop long (%d caractères)", len(raw))
	}
	u, err := url.Parse(raw)
	if err != nil {
		// couvre aussi les caractères de contrôle (retour à la ligne, etc.)
		return "", fmt.Errorf("lien illisible")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("seuls les liens http et https sont ouverts")
	}
	if u.Host == "" {
		return "", fmt.Errorf("lien sans adresse")
	}
	return raw, nil
}

// launchExternalBrowser ouvre l'URL et renvoie le navigateur employé
// (« system » pour le gestionnaire de l'OS).
//
// L'URL est passée en argument, jamais par un shell : c'est ce qui rend le
// contrôle de schéma suffisant.
func launchExternalBrowser(raw string) (string, error) {
	// Linux sans écran (serveur joint par un tunnel sur localhost) : xdg-open
	// « réussit » sans rien ouvrir, et le client croirait le lien ouvert. On
	// échoue franchement pour qu'il retombe sur window.open, sur son appareil.
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return "", fmt.Errorf("pas d'écran sur cette machine")
	}
	if pref := linkBrowserPref(); pref != "auto" {
		bin := resolveNamedBrowser(pref)
		if bin == "" {
			return "", fmt.Errorf("navigateur %s introuvable sur cette machine", pref)
		}
		if err := startDetached(exec.Command(bin, raw)); err != nil {
			return "", err
		}
		return pref, nil
	}
	// « auto » : le gestionnaire de l'OS d'abord. Sur une machine dont le
	// navigateur par défaut est Chrome, c'est déjà la bonne réponse sans rien
	// deviner — et c'est le seul moyen de couvrir Firefox ou Safari, que nous ne
	// savons pas nommer.
	//
	// Sur macOS il n'existe d'ailleurs pas d'autre voie : le gestionnaire par
	// défaut s'interroge par une API CoreServices joignable seulement en CGO (et,
	// sur disque, par un plist binaire protégé par SIP). `open` EST la réponse —
	// ne pas chercher à « améliorer » ceci plus tard.
	if err := openBrowser(raw); err == nil {
		return "system", nil
	}
	for _, n := range []string{"chrome", "edge"} {
		if bin := resolveNamedBrowser(n); bin != "" {
			if err := startDetached(exec.Command(bin, raw)); err == nil {
				return n, nil
			}
		}
	}
	return "", fmt.Errorf("aucun navigateur n'a pu être lancé")
}

// startDetached lance le navigateur et le récupère quand il se termine : sans
// Wait, chaque lien cliqué laissait un processus zombie jusqu'à l'arrêt d'AJEAN
// (Linux, macOS). Le plus souvent le lancement rend la main tout de suite à une
// instance déjà ouverte.
func startDetached(c *exec.Cmd) error {
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	return nil
}

// handleOpenURL ouvre un lien externe dans le navigateur choisi.
//
//	GET  → {ok, pref, available:{chrome:true,…}}
//	POST {url} → {ok, browser}
//
// Le GET sert à la ligne de réglage : il dit quel navigateur est retenu et
// lesquels sont réellement installés sur la machine.
func handleOpenURL(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		avail := map[string]bool{}
		for _, n := range linkBrowserNames {
			avail[n] = resolveNamedBrowser(n) != ""
		}
		sendJSON(w, 200, map[string]any{"ok": true, "pref": linkBrowserPref(), "available": avail})
	case http.MethodPost:
		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": "requête illisible"})
			return
		}
		clean, err := validExternalURL(req.URL)
		if err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		browser, err := openURLExternally(clean)
		if err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true, "browser": browser})
	default:
		sendJSON(w, 405, map[string]any{"ok": false, "error": "méthode inconnue"})
	}
}
