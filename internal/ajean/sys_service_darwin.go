//go:build darwin

package ajean

import "os"

// sys_service_darwin.go : chemins des LaunchDaemons d'AJEAN (interface, et unité
// de l'ancien moteur 0.18 et avant, que la désinstallation retire encore). Les
// modèles sont des services, voir services_darwin.go.

// launchdLabel dérive le label launchd du service (ex. "com.ajean.ajean"), en
// conservant le préfixe hérité « com.ajean. » tant que c'est LUI qui est
// réellement installé : le plist d'un daemon déjà chargé ne se renomme pas tout
// seul, et viser le mauvais label reviendrait à ne plus voir le service.
func launchdLabel(svc string) string {
	modern := "com.ajean." + svc
	if _, err := os.Stat("/Library/LaunchDaemons/" + modern + ".plist"); err == nil {
		return modern
	}
	if legacy := "com.ajean." + svc; fileExists("/Library/LaunchDaemons/" + legacy + ".plist") {
		return legacy
	}
	return modern
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// launchdPlistPath : chemin du LaunchDaemon (domaine système, exécuté par root
// puis abaissé à l'utilisateur cible via la clé UserName du plist).
func launchdPlistPath(svc string) string {
	return "/Library/LaunchDaemons/" + launchdLabel(svc) + ".plist"
}
