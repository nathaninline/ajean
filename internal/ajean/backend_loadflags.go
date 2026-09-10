package ajean

import (
	"os"
	"path/filepath"
	"strings"
)

// backend_loadflags.go — aide à la migration des anciens drapeaux de chargement
// mémoire de llama.cpp. Les versions récentes ont REMPLACÉ --mlock / --no-mmap
// par --load-mode ; AJEAN traduit déjà au lancement (voir reconcileLoadMode),
// donc le serveur démarre toujours. Ce fichier ajoute le volet VISIBLE : détecter
// qu'une config porte encore les anciens drapeaux sur un moteur qui attend la
// nouvelle syntaxe, et proposer un bouton pour nettoyer la config / le preset.

// loadFlagsNeedMigration : la config active contient encore --mlock / --no-mmap
// ALORS que le moteur actif attend --load-mode. C'est le signal pour l'UI
// (bandeau + bouton « Mettre à jour les flags »). La vérification du binaire
// (backendUsesLoadMode) n'est faite QUE si les anciens drapeaux sont présents,
// et son résultat est mémoïsé : pas de « --help » à chaque sondage de statut.
func loadFlagsNeedMigration() bool {
	cfg := ReadConfig()
	if !containsAny(splitArgs(cfg["EXTRA_ARGS"]), "--mlock", "--no-mmap") {
		return false
	}
	return backendUsesLoadMode(cfg["BIN"])
}

// migrateLoadFlags réécrit --mlock / --no-mmap en --load-mode dans la config
// active ET dans le preset d'origine (pour que le nettoyage tienne au prochain
// switch). Renvoie la nouvelle valeur d'EXTRA_ARGS. Idempotent.
func migrateLoadFlags() (string, error) {
	cfg := ReadConfig()
	args := splitArgs(cfg["EXTRA_ARGS"])
	if !containsAny(args, "--mlock", "--no-mmap") {
		return cfg["EXTRA_ARGS"], nil // déjà propre
	}
	// supported=true : l'UI n'appelle ceci que pour un moteur qui attend
	// --load-mode (loadFlagsNeedMigration l'a vérifié).
	joined := joinArgs(translateLoadMode(args, true))
	if err := SetConfigKey("EXTRA_ARGS", joined); err != nil {
		return "", err
	}
	if id := activePresetID(); id != "" {
		_ = migratePresetLoadFlags(id) // best-effort : la config active fait foi
	}
	return joined, nil
}

// migratePresetLoadFlags applique la même réécriture au fichier .env du preset.
func migratePresetLoadFlags(id string) error {
	path := filepath.Join(presetsDir(), id+".env")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m := parseEnv(string(b))
	args := splitArgs(m["EXTRA_ARGS"])
	if !containsAny(args, "--mlock", "--no-mmap") {
		return nil
	}
	m["EXTRA_ARGS"] = joinArgs(translateLoadMode(args, true))
	return os.WriteFile(path, []byte(formatEnv(m)), 0o644)
}

// joinArgs recolle des arguments en une ligne relisible par splitArgs : un
// argument contenant une espace est ré-entouré de guillemets (splitArgs retire
// les guillemets entourants, un simple join les perdrait et casserait un chemin
// avec espace au prochain parse).
func joinArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t") && !strings.ContainsRune(a, '"') {
			parts[i] = `"` + a + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
