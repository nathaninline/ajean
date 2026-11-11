//go:build windows

package ajean

// Passage 0.18 → 0.19 sous Windows : l'ancien moteur (processus détaché,
// fichier .pid) peut encore tourner et tenir le port et la VRAM. On l'arrête
// une fois au démarrage. Les modèles deviennent des services Windows, créés
// avec une confirmation administrateur (UAC) au premier démarrage de chacun.

func retireLegacyEngine() {
	if svcIsFile(pidFilePath()) {
		_ = svcStop(false)
	}
}

func legacyWarning() string { return "" }
