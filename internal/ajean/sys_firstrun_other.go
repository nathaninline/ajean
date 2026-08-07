//go:build !windows

package ajean

// appFirstRun : hors Windows, `ajean app` est lancé depuis un terminal par
// quelqu'un qui a déjà suivi l'installation — pas de double-clic à désambiguïser,
// donc pas de boîte de dialogue. On se contente de provisionner les données au
// premier lancement. Ne relance jamais (renvoie toujours false).
func appFirstRun() bool {
	if len(ReadConfig()) == 0 {
		_ = cmdInstall(nil)
	}
	return false
}
