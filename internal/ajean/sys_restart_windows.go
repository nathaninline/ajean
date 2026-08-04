//go:build windows

package ajean

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Redémarrage propre de l'application après une mise à jour, sous Windows.
//
// Un process ne peut pas se relancer lui-même : tant qu'il tourne, le nouveau
// binaire ne peut pas prendre sa place sur le port, et le dossier de données
// reste tenu. On délègue donc à un ACCOMPAGNATEUR détaché — une copie de nous
// lancée avec une sous-commande interne — qui attend notre disparition, profite
// de cette fenêtre pour terminer la migration, puis relance l'application.
//
// C'est cette fenêtre qui manquait. Une mise à jour en place laisse AJEAN
// tourner, et donc le dossier tenu : le renommage échouait avec « Accès refusé »,
// qui sous Windows ne distingue pas un verrou d'un manque de droits. D'où des
// postes qui restaient indéfiniment sur l'ancien nom.

const restartArg = "restart-after-update"

// scheduleAppRestart lance l'accompagnateur et annonce que le redémarrage est
// pris en charge. L'appelant doit ensuite rendre la main puis quitter, pour que
// la réponse HTTP parte AVANT que le process ne disparaisse.
func scheduleAppRestart() (bool, string) {
	exe, err := os.Executable()
	if err != nil {
		return false, ""
	}
	cmd := spawnDetached(exe, restartArg, strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return false, ""
	}
	// L'accompagnateur ne doit pas mourir avec nous : spawnDetached l'a déjà
	// sorti de notre groupe de processus, on se contente de l'oublier.
	_ = cmd.Process.Release()

	go func() {
		time.Sleep(1500 * time.Millisecond) // laisser la réponse atteindre le navigateur
		os.Exit(0)
	}()
	return true, "AJEAN redémarre — la page se reconnectera toute seule dans quelques secondes."
}

// cmdRestartAfterUpdate est exécuté par l'accompagnateur détaché.
//
// Il n'écrit rien à l'écran : personne ne le regarde, il n'a pas de console, et
// son seul travail visible est de faire réapparaître l'application.
func cmdRestartAfterUpdate(args []string) error {
	if len(args) > 0 {
		if pid, err := strconv.Atoi(args[0]); err == nil {
			waitForExit(pid, 30*time.Second)
		}
	}
	// L'application vient de se fermer : plus rien ne tient le dossier de
	// données, le renommage peut enfin aboutir. postUpdateMigrate ne fait rien
	// s'il n'y a pas lieu, et repointe raccourci et PATH s'il a migré.
	postUpdateMigrate()

	// installedExePath() est relu APRÈS la migration : le binaire a pu changer
	// de dossier avec elle, et relancer l'ancien chemin échouerait.
	target := installedExePath()
	if _, err := os.Stat(target); err != nil {
		// La cible a disparu : mieux vaut relancer ce qu'on a que rien du tout.
		if self, e := os.Executable(); e == nil {
			target = self
		}
	}
	if !launch(target) {
		return fmt.Errorf("relance de %s impossible", target)
	}
	return nil
}

// waitForExit attend la fin du process pid, sans dépasser le délai imparti.
// Un dépassement n'est pas bloquant : on poursuit quand même, quitte à ce que
// la migration échoue et soit retentée plus tard. Rester coincé ici serait pire
// — l'utilisateur se retrouverait sans application du tout.
func waitForExit(pid int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			time.Sleep(500 * time.Millisecond) // laisser les handles se fermer
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
