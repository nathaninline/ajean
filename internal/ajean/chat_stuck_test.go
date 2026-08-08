package ajean

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Le bouton stop doit VRAIMENT arrêter une commande shell. Elle naissait d'un
// contexte indépendant du tour : arrêter la génération ne l'arrêtait pas, et le
// tour restait suspendu jusqu'à l'expiration du délai (5 min au maximum).
func TestRunShellObeitAuStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("commande de veille propre à Unix")
	}
	testHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	out := runShell(ctx, "sleep 30", 300)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("la commande a survécu à l'annulation (%s) : %q", d, out)
	}
	if !strings.Contains(out, "interrompue") {
		t.Errorf("résultat attendu « interrompue », reçu %q", out)
	}
}

// Une commande qui laisse un process en arrière-plan garde les tubes de sortie
// ouverts. Sans WaitDelay, Wait attend leur fermeture — donc pour toujours — et
// le tour ne se terminait JAMAIS (seul un redémarrage du service débloquait).
func TestRunShellNeBloquePasSurUnProcessDetache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("syntaxe de détachement propre à Unix")
	}
	testHome(t)
	start := time.Now()
	out := runShell(context.Background(), "sleep 30 & echo lance", 2)
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("runShell est resté accroché aux tubes du process détaché (%s)", d)
	}
	if !strings.Contains(out, "lance") && !strings.Contains(out, "timeout") {
		t.Errorf("sortie inattendue : %q", out)
	}
}

// Le dossier de travail est résolu une fois par process : s'il disparaît, plus
// AUCUNE commande ne passait (« chdir : no such file or directory ») jusqu'au
// redémarrage du service. Il doit être recréé au besoin.
func TestRunShellRecreeLeWorkspaceDisparu(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("echo se comporte différemment sous cmd.exe")
	}
	testHome(t)
	ws := agentWorkspace()
	if ws == "" {
		t.Skip("pas de workspace résolu")
	}
	if err := os.RemoveAll(ws); err != nil {
		t.Fatal(err)
	}
	out := runShell(context.Background(), "echo vivant", 10)
	if !strings.Contains(out, "vivant") {
		t.Fatalf("commande cassée par la disparition du workspace : %q", out)
	}
}

// Reset doit TOUJOURS rendre la main, même sur un tour resté coincé : c'est le
// geste que l'on tente quand le chat est bloqué. Avant, Generating restait vrai
// et tout envoi suivant était refusé « génération en cours » jusqu'au
// redémarrage du service.
func TestResetDebloqueUnTourCoince(t *testing.T) {
	c := newTestConv()
	// Simule un tour parti et jamais terminé (commande accrochée, moteur disparu).
	c.mu.Lock()
	c.Generating = true
	c.mu.Unlock()

	c.Reset()

	if st := c.state(); st["generating"] != false {
		t.Fatal("après Reset, la conversation doit être déclarée libre")
	}
}

// Le tour abandonné se termine parfois APRÈS le démarrage du suivant. Sa fin ne
// doit pas déclarer « libre » une génération toute neuve.
func TestTourPerimeNeLiberePasLeTourCourant(t *testing.T) {
	c := newTestConv()
	oldEpoch := c.epoch
	c.Reset() // le tour d'epoch `oldEpoch` devient périmé
	c.mu.Lock()
	c.Generating = true // un nouveau tour démarre
	c.mu.Unlock()

	// Fin (tardive) du tour périmé : même code que le defer de generate.
	c.mu.Lock()
	if c.epoch == oldEpoch {
		c.Generating = false
	}
	c.mu.Unlock()

	if st := c.state(); st["generating"] != true {
		t.Fatal("un tour périmé a libéré le tour courant")
	}
}
