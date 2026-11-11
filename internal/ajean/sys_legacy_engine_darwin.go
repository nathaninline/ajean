//go:build darwin

package ajean

// Passage 0.18 → 0.19 sous macOS : l'ancien moteur était un LaunchDaemon root
// (/Library/LaunchDaemons). AJEAN, lancé sans droits, ne peut pas l'arrêter :
// on le signale avec la commande à lancer. Les modèles sont des agents
// utilisateur (launchd), sans droits particuliers.

import "os"

func retireLegacyEngine() {}

func legacyWarning() string {
	if !fileExists(launchdPlistPath(serviceName())) {
		return ""
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "ajean"
	}
	return "L'ancien moteur d'AJEAN (version 0.18) est encore installé et peut occuper la mémoire du modèle. " +
		"Dans un terminal : sudo launchctl bootout system/" + launchdLabel(serviceName()) + " && sudo rm " + launchdPlistPath(serviceName())
}
