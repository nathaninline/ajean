// Package browser donne à l'assistant un navigateur web. Il pilote un vrai
// Chrome, Chromium, Edge ou Brave par le protocole DevTools (CDP), sans vision :
// la page lui est rendue en texte, chaque élément cliquable porte un numéro, et
// il agit en donnant ce numéro.
//
// Le navigateur est enfermé comme le reste de l'assistant : uniquement http(s),
// jamais la machine locale (l'interface de l'assistant y vit), aucun envoi de
// fichier, aucun JavaScript venu du modèle. Tout son trafic sort par un proxy
// intégré (proxy.go) qui refuse les adresses locales au moment de la connexion ;
// le contrôle des adresses côté pilotage (guard.go) écarte les schémas sans
// réseau et explique un refus au modèle.
//
// Sur le disque : le navigateur a son propre profil, .ajean/browser/ (jamais
// celui de l'utilisateur), et ses téléchargements vont dans WORKSPACE/downloads/.
package browser

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"ajean/internal/home"
	"ajean/internal/workspace"
)

const (
	callTimeout = 10 * time.Second // une commande CDP ordinaire
	startMax    = 20 * time.Second // le temps laissé au navigateur pour ouvrir son port
	startPoll   = 200 * time.Millisecond
	stopMax     = 3 * time.Second
	portFile    = "DevToolsActivePort" // écrit par le navigateur dans son profil
	blank       = "about:blank"        // la seule page non http(s) tolérée : la page vide de départ
)

// instance : un navigateur en marche et l'onglet qu'on y pilote.
type instance struct {
	conn     *conn
	proxy    *proxy            // sa seule sortie vers le réseau
	cmd      *exec.Cmd         // le processus du navigateur
	exited   chan struct{}     // fermé à la fin de ce processus
	target   string            // l'onglet piloté
	session  string            // sa session CDP
	known    map[string]bool   // onglets déjà vus : un inconnu est un onglet qui vient de s'ouvrir
	mu       sync.Mutex        // protège sessions et notes, touchés par les événements
	sessions map[string]string // onglet → session CDP, une fois l'onglet préparé (voir equip)
	notes    []string          // ce qui s'est passé en marge (dialogue, blocage, téléchargement)
}

// Un seul navigateur pour tout le programme. mu sérialise les appels : deux
// conversations ne peuvent pas tirer la même page chacune de son côté.
var (
	mu      sync.Mutex
	current *instance
)

// Shutdown ferme le navigateur s'il est en marche (arrêt du programme, ou
// action close).
func Shutdown() {
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		current.stop()
		current = nil
	}
}

// get rend le navigateur, lancé au premier besoin et relancé s'il a été fermé
// à la main ou s'il a planté. mu doit être tenu.
func get() (*instance, error) {
	if current != nil {
		select {
		case <-current.conn.done:
			current.stop()
			current = nil
		default:
			return current, nil
		}
	}
	b, err := start()
	if err != nil {
		return nil, err
	}
	current = b
	return b, nil
}

func start() (*instance, error) {
	profile := home.State("browser")
	downloads, err := workspace.Abs("downloads")
	if err != nil {
		return nil, err
	}
	closeLeftover(profile)
	px, err := startProxy()
	if err != nil {
		return nil, fmt.Errorf("proxy du navigateur impossible : %v", err)
	}
	b := &instance{proxy: px, known: map[string]bool{}, sessions: map[string]string{}}
	if err := b.launch(profile); err != nil {
		px.close()
		return nil, err
	}
	// Le reste du confinement se pose ici, avant toute navigation : téléchargements
	// dans le workspace, et chaque onglet, présent ou à venir, remis à equip. Si
	// l'un des deux échoue, on ne navigue pas du tout.
	err = os.MkdirAll(downloads, 0o755)
	if err == nil {
		err = b.conn.call("", "Browser.setDownloadBehavior", map[string]any{"behavior": "allow", "downloadPath": downloads, "eventsEnabled": true}, nil, callTimeout)
	}
	if err == nil {
		err = b.conn.call("", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true, "filter": []map[string]any{{"type": "page"}}}, nil, callTimeout)
	}
	if err != nil {
		b.stop()
		return nil, fmt.Errorf("confinement du navigateur impossible : %v", err)
	}
	return b, nil
}

// closeLeftover ferme un navigateur resté ouvert sur ce profil (arrêt brutal du
// programme). Son proxy n'existe plus, il n'a donc plus de réseau ; et en lancer
// un second ne ferait que lui passer la main.
func closeLeftover(profile string) {
	c, err := attach(profile, func(*conn, message) {})
	if err != nil {
		return
	}
	_ = c.call("", "Browser.close", nil, nil, stopMax)
	select {
	case <-c.done:
	case <-time.After(stopMax):
	}
	c.close()
}

// launch démarre le navigateur sur notre profil, derrière notre proxy, et
// attend qu'il soit pilotable.
func (b *instance) launch(profile string) error {
	bin, err := binary()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(profile, 0o755); err != nil {
		return err
	}
	// Le port d'un lancement précédent ferait croire que celui-ci est déjà prêt.
	_ = os.Remove(filepath.Join(profile, portFile))
	args := []string{
		"--remote-debugging-port=0", "--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check", "--window-size=1280,900",
		"--proxy-server=http://" + b.proxy.addr,
		"--proxy-bypass-list=<-loopback>", // sans cela, localhost et 127.0.0.1 évitent le proxy
		"--disable-quic",                  // HTTP/3 voyage en UDP, hors du proxy
		// WebRTC ouvrirait sinon des connexions UDP directes, hors du proxy.
		"--webrtc-ip-handling-policy=disable_non_proxied_udp",
	}
	if os.Getenv("AJEAN_BROWSER_HEADLESS") != "" {
		args = append(args, "--headless=new")
	}
	b.cmd, b.exited = exec.Command(bin, append(args, blank)...), make(chan struct{})
	if err := b.cmd.Start(); err != nil {
		return fmt.Errorf("lancement du navigateur impossible : %v", err)
	}
	go func() { _ = b.cmd.Wait(); close(b.exited) }()
	for deadline := time.Now().Add(startMax); ; time.Sleep(startPoll) {
		if b.conn, err = attach(profile, b.onEvent); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			_ = b.cmd.Process.Kill()
			return errors.New("le navigateur n'a pas ouvert son port de pilotage à temps")
		}
	}
}

// attach se branche sur le navigateur qui a écrit son port dans le profil. La
// connexion part d'ici droit vers lui : le proxy ne sert qu'à ses sorties.
func attach(profile string, onEvent func(*conn, message)) (*conn, error) {
	raw, err := os.ReadFile(filepath.Join(profile, portFile))
	if err != nil {
		return nil, err
	}
	port, path, ok := strings.Cut(strings.TrimSpace(string(raw)), "\n") // port, puis chemin WebSocket
	if !ok {
		return nil, errors.New("port du navigateur pas encore écrit")
	}
	return dial("ws://127.0.0.1:"+strings.TrimSpace(port)+strings.TrimSpace(path), onEvent)
}

// stop ferme le navigateur puis son proxy : dans cet ordre, le navigateur ne
// tourne jamais sans lui.
func (b *instance) stop() {
	_ = b.conn.call("", "Browser.close", nil, nil, stopMax)
	select {
	case <-b.exited:
	case <-time.After(stopMax):
		_ = b.cmd.Process.Kill()
	}
	b.conn.close()
	b.proxy.close()
}

// binary trouve le navigateur : AJEAN_BROWSER, sinon Chrome, Chromium, Edge
// puis Brave à leurs emplacements habituels.
func binary() (string, error) {
	if p := strings.TrimSpace(os.Getenv("AJEAN_BROWSER")); p != "" {
		if !isFile(p) {
			return "", fmt.Errorf("AJEAN_BROWSER ne désigne pas un programme : %s", p)
		}
		return p, nil
	}
	switch runtime.GOOS {
	case "windows":
		for _, rel := range []string{`Google\Chrome\Application\chrome.exe`, `Chromium\Application\chrome.exe`, `Microsoft\Edge\Application\msedge.exe`, `BraveSoftware\Brave-Browser\Application\brave.exe`} {
			for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
				if root := os.Getenv(env); root != "" && isFile(filepath.Join(root, rel)) {
					return filepath.Join(root, rel), nil
				}
			}
		}
	case "darwin":
		userHome, _ := os.UserHomeDir()
		for _, app := range []string{"Google Chrome.app/Contents/MacOS/Google Chrome", "Chromium.app/Contents/MacOS/Chromium", "Microsoft Edge.app/Contents/MacOS/Microsoft Edge", "Brave Browser.app/Contents/MacOS/Brave Browser"} {
			for _, p := range []string{"/Applications/" + app, filepath.Join(userHome, "Applications", app)} {
				if isFile(p) {
					return p, nil
				}
			}
		}
	default: // noms de commande, cherchés dans le PATH
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "microsoft-edge-stable", "brave-browser"} {
			if p, err := exec.LookPath(name); err == nil {
				return p, nil
			}
		}
	}
	return "", errors.New("aucun navigateur trouvé (Chrome, Chromium, Edge ou Brave) : installe-en un, ou donne son chemin complet dans la variable AJEAN_BROWSER")
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func (b *instance) note(s string) {
	b.mu.Lock()
	b.notes = append(b.notes, s)
	b.mu.Unlock()
}

// takeNotes rend les notes accumulées et les oublie : chacune n'est dite qu'une fois.
func (b *instance) takeNotes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	notes := b.notes
	b.notes = nil
	return notes
}

// clip coupe un texte trop long pour le modèle (une adresse, un titre, une vue).
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
