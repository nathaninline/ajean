//go:build !windows

package ajean

import "os"

// appFirstRun : hors Windows, `jean app` est lancé depuis un terminal par
// quelqu'un qui a déjà suivi l'installation — pas de double-clic à désambiguïser,
// donc pas de boîte de dialogue. On se contente de provisionner les données au
// premier lancement, comme avant. Ne relance jamais (renvoie toujours false).
func appFirstRun() bool {
	if _, err := os.Stat(confPath()); err != nil {
		_ = cmdInstall(nil)
	}
	return false
}
