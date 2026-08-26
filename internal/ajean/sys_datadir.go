package ajean

import (
	"os"
	"runtime"
)

// sys_datadir.go — création du dossier de données, identique sur les trois
// plateformes. Avant, chaque installateur (Linux, macOS, Windows, plus le
// premier lancement de l'app) créait ses dossiers et écrivait son propre
// modèle de config.env : quatre copies qui divergeaient à la première
// modification. Il n'y en a plus qu'une.

// dataDirs est l'arborescence complète de $AJEAN_HOME. Rien d'autre n'y est
// créé : tout le reste vit dans ajean.db.
func dataDirs() []string {
	return []string{AjeanHome(), backendsDir(), binDir(), presetsDir(), memoryDir(), modelsDir(), workspaceDir(), scriptsDir()}
}

// defaultConfig est la configuration de départ d'une installation neuve. Les
// valeurs sont volontairement incomplètes (BIN et MODEL sont vides) : c'est
// l'écran d'accueil, ou « ajean llamacpp install », qui les renseigne.
func defaultConfig() map[string]string {
	host := "0.0.0.0"
	if runtime.GOOS == "windows" {
		host = "127.0.0.1"
	}
	return map[string]string{
		"PORT":   "8080",
		"HOST":   host,
		"CTX":    "32768",
		"BATCH":  "2048",
		"UBATCH": "512",
		"NGL":    "999",
	}
}

// provisionDataDir crée l'arborescence et, sur une installation neuve, pose la
// configuration de départ. Idempotente : une configuration existante n'est
// jamais écrasée.
func provisionDataDir() error {
	for _, d := range dataDirs() {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if len(ReadConfig()) > 0 {
		return nil
	}
	return WriteConfig(defaultConfig())
}
