package jean

// cli_app.go — expérience « application » de Jean.
//
// Quand on double-clique sur le binaire (aucun argument, console fraîche), au
// lieu d'afficher l'aide dans une console qui se ferme, Jean démarre son UI web,
// l'ouvre dans le navigateur ET pose une icône dans la zone de notification
// Windows (voir sys_tray_windows.go) : on voit que Jean tourne et on le pilote
// (« Ouvrir Jean » / « Quitter »).

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

const appPort = 8090

// cmdApp lance l'UI web, l'ouvre dans le navigateur et fait tourner l'icône de
// la zone de notification (Windows). Sur une machine vierge, il crée d'abord le
// dossier de données + config.env pour que l'interface s'ouvre quand même
// (l'écran d'accueil prendra le relais pour le choix du modèle).
func cmdApp(args []string) error {
	url := fmt.Sprintf("http://localhost:%d", appPort)

	if _, err := os.Stat(confPath()); err != nil {
		// Première fois : installe le minimum (dossiers + config de départ).
		_ = cmdInstall(nil)
	}

	addr := fmt.Sprintf("0.0.0.0:%d", appPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Jean tourne déjà sur ce port : on ouvre juste l'UI sur l'instance
		// existante plutôt que d'échouer.
		fmt.Printf("Jean est déjà lancé — ouverture de %s\n", url)
		return openBrowser(url)
	}

	// Sert l'UI en tâche de fond ; l'icône tray tient le premier plan.
	go func() { _ = http.Serve(ln, newWebMux()) }()

	sp := showSplash("Lancement de Jean en cours…")
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
