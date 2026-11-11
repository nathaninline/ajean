package ajean

import (
	"os"
)

// sys_datadir.go — création du dossier de données, identique sur les trois
// plateformes. Avant, chaque installateur (Linux, macOS, Windows, plus le
// premier lancement de l'app) créait ses dossiers et écrivait son propre
// modèle de config.env : quatre copies qui divergeaient à la première
// modification. Il n'y en a plus qu'une.

// dataDirs est l'arborescence de $AJEAN_HOME. Rien d'autre n'y est créé : les
// réglages vivent dans ajean.db, les modèles dans services.json. Les dossiers
// presets/ et backends/ d'une 0.18 restent en place s'ils existent, mais une
// installation neuve ne les reçoit plus.
func dataDirs() []string {
	return []string{AjeanHome(), binDir(), memoryDir(), modelsDir(), workspaceDir(), scriptsDir()}
}

// defaultConfig est la configuration de départ d'une installation neuve. Elle
// reste minimale : le modèle du chat se choisit dans l'interface (Modèles).
func defaultConfig() map[string]string {
	return map[string]string{
		"PORT": "8080",
		"CTX":  "32768",
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
