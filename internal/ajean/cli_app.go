package ajean

// cli_app.go — expérience « application » de AJEAN.
//
// Quand on double-clique sur le binaire (aucun argument, console fraîche), au
// lieu d'afficher l'aide dans une console qui se ferme, AJEAN démarre son UI web,
// l'ouvre dans le navigateur ET pose une icône dans la zone de notification
// Windows (voir sys_tray_windows.go) : on voit que AJEAN tourne et on le pilote
// (« Ouvrir AJEAN » / « Quitter »).

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const appPort = 8090

// cmdApp lance l'UI web, l'ouvre dans le navigateur et fait tourner l'icône de
// la zone de notification (Windows). Sur une machine vierge, il crée d'abord le
// dossier de données + config.env pour que l'interface s'ouvre quand même
// (l'écran d'accueil prendra le relais pour le choix du modèle).
func cmdApp(args []string) error {
	url := fmt.Sprintf("http://localhost:%d", appPort)

	// Premier lancement : prépare le dossier de données et, sous Windows, propose
	// explicitement l'installation (voir sys_firstrun_windows.go). Si l'app a été
	// relancée depuis la copie installée, ce process n'a plus rien à faire.
	if appFirstRun() {
		return nil
	}

	addr := fmt.Sprintf("0.0.0.0:%d", appPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Le port est pris — mais par QUI ? On le demande au lieu de le
		// supposer. Annoncer « AJEAN est déjà lancé » à propos d'un autre
		// logiciel ouvrait sa page à sa place, et le retour anticipé privait
		// l'utilisateur jusqu'à l'icône de la zone de notification (observé en
		// usage réel le 2026-10-16 : un `python -m http.server` laissé par
		// l'aperçu d'un IDE ; la fenêtre montrait un listing de dossier).
		if ajeanAnswersOn(appPort) {
			// Une autre instance d'AJEAN sert déjà. Si sa fenêtre est ouverte, on
			// la ramène au premier plan : en ouvrir une autre donnait deux
			// fenêtres AJEAN côte à côte. Sinon on ouvre la sienne. Pas d'icône
			// de zone de notification ici, la sienne existe déjà.
			if focusAppWindow() {
				fmt.Println("AJEAN est déjà lancé — fenêtre ramenée au premier plan")
				return nil
			}
			fmt.Printf("AJEAN est déjà lancé — ouverture de %s\n", url)
			return openAppWindow(url)
		}
		qui := "un autre logiciel"
		if pid, name := pidOnPort(appPort); pid != 0 {
			qui = fmt.Sprintf("%s (PID %d)", name, pid)
		}
		fmt.Fprintln(os.Stderr, "AJEAN : port", appPort, "déjà utilisé par", qui)
		notifyUser("AJEAN ne peut pas démarrer",
			fmt.Sprintf("Le port %d est déjà utilisé par %s.\n\nAJEAN ne peut pas ouvrir son interface tant que ce processus tourne.\nFerme-le, puis relance AJEAN.", appPort, qui))
		return fmt.Errorf("port %d déjà utilisé par %s", appPort, qui)
	}

	// UN SEUL process propriétaire de la conversation, comme le service ajean-ui
	// sous Linux : l'app sert l'UI locale ET le tunnel avec le MÊME mux. Deux
	// process qui servent la conversation (objet en mémoire persisté dans
	// conversation.json) donnent deux fils divergents entre le local et
	// app.ajean.link — le bug qu'on a vécu avec le worker détaché.
	appOwnsLink = true
	appWebMux = newWebMux()
	go func() { _ = http.Serve(ln, appWebMux) }()

	go func() {
		if readLinkToken() == "" {
			return
		}
		// Worker détaché rescapé (version antérieure, autre copie de l'app) : il
		// possède SA propre conversation et écrase la nôtre. On le supprime avant
		// de prendre la main.
		killForeignUIWorker()
		startAppLink(appWebMux)
	}()

	// Relance après mise à jour : l'ancienne fenêtre est encore ouverte et se
	// reconnecte seule. En rouvrir une donnait deux fenêtres AJEAN côte à côte.
	// Si l'utilisateur l'a fermée entre-temps, on l'ouvre comme d'habitude.
	restarted := os.Getenv(envRestarted) == "1"
	os.Unsetenv(envRestarted)
	if (restarted && appWindowAlive()) || focusAppWindow() {
		// (ou une fenêtre AJEAN est restée ouverte après un arrêt : on la reprend)
		waitServerReady(url)
	} else {
		sp := showSplash("Lancement d'AJEAN en cours…")
		waitServerReady(url)
		_ = openAppWindow(url)
		time.Sleep(900 * time.Millisecond) // laisse le navigateur s'afficher par-dessus le splash
		sp.close()
	}

	go keepAppWindowIcon() // icône nette dans la barre des tâches (Windows)
	runTray(url)           // icône zone de notification ; bloque jusqu'à « Quitter »
	return nil
}

// waitServerReady attend que l'UI réponde (jusqu'à ~4 s) pour n'ouvrir le
// navigateur qu'une fois le serveur prêt.
func waitServerReady(url string) {
	c := &http.Client{Timeout: 400 * time.Millisecond}
	for i := 0; i < 20; i++ {
		if resp, err := c.Get(url); err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// ajeanAnswersOn : est-ce bien un AJEAN qui répond sur ce port ?
//
// On le sonde au lieu de le déduire — n'importe quel logiciel peut occuper 8090,
// et lui ouvrir la fenêtre à sa place est pire que d'échouer franchement.
//
// /api/ping est le discriminant : il répond {"ok":true,"service":"ajean",…}.
func ajeanAnswersOn(port int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/api/ping", port), nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// Instance protégée par une clé de pilotage : elle refuse le ping (le serveur
	// ne garde que l'empreinte de la clé, on n'a rien à présenter), mais son refus
	// porte la signature d'AJEAN (requireWebAuth).
	if resp.StatusCode == http.StatusUnauthorized {
		return strings.Contains(resp.Header.Get("WWW-Authenticate"), `realm="ajean"`)
	}
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return err == nil && bytes.Contains(body, []byte(`"service":"ajean"`))
}
