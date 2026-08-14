package ajean

// cli_app.go — expérience « application » de AJEAN.
//
// Quand on double-clique sur le binaire (aucun argument, console fraîche), au
// lieu d'afficher l'aide dans une console qui se ferme, AJEAN démarre son UI web,
// l'ouvre dans le navigateur ET pose une icône dans la zone de notification
// Windows (voir sys_tray_windows.go) : on voit que AJEAN tourne et on le pilote
// (« Ouvrir AJEAN » / « Quitter »).

import (
	"fmt"
	"net"
	"net/http"
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
		// AJEAN tourne déjà sur ce port : on ouvre juste l'UI sur l'instance
		// existante plutôt que d'échouer.
		fmt.Printf("AJEAN est déjà lancé — ouverture de %s\n", url)
		return openBrowser(url)
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

	sp := showSplash("Lancement d'AJEAN en cours…")
	waitServerReady(url)
	_ = openBrowser(url)
	time.Sleep(900 * time.Millisecond) // laisse le navigateur s'afficher par-dessus le splash
	sp.close()

	runTray(url) // icône zone de notification ; bloque jusqu'à « Quitter »
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
