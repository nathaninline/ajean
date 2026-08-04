package ajean

import "os"

// Nom des unités système, pendant et après le renommage jean → ajean.
//
// Le problème : une mise à jour remplace le BINAIRE, pas les unités systemd.
// Un serveur déjà installé continue donc de tourner sous « jean.service » et
// « jean-link.service », qui pointent sur /usr/local/bin/jean. Si le binaire
// renommé partait du principe que les unités s'appellent désormais « ajean* »,
// le premier `systemctl restart` échouerait — et sur un serveur distant, ça veut
// dire l'accès coupé, sans terminal pour réparer.
//
// La solution retenue n'est PAS de migrer les unités au démarrage (il faudrait
// root, et un échec laisserait la machine sans service). C'est de résoudre le
// nom à l'usage : on utilise l'unité qui existe réellement. Une machine ancienne
// reste sur ses unités « jean* » et fonctionne ; une machine fraîchement
// installée — ou réinstallée via `ajean install` — utilise les « ajean* ».
// Aucune fenêtre pendant laquelle plus rien ne tourne.

// unitExists dit si un service de ce nom est installé sur la machine : unité
// systemd sous Linux, daemon launchd sous macOS (repéré par son plist, sous
// l'un ou l'autre des deux préfixes de label). Toujours faux sous Windows, où
// le « service » est un simple processus détaché suivi par un fichier PID.
func unitExists(name string) bool {
	paths := []string{
		"/etc/systemd/system/" + name + ".service",
		"/Library/LaunchDaemons/com.ajean." + name + ".plist",
		"/Library/LaunchDaemons/com.jean." + name + ".plist",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// resolveUnitName renvoie le nom d'unité à utiliser : le nouveau s'il est
// installé, sinon l'ancien s'il l'est, sinon le nouveau (installation à venir).
func resolveUnitName(modern, legacy string) string {
	if unitExists(modern) {
		return modern
	}
	if unitExists(legacy) {
		return legacy
	}
	return modern
}
