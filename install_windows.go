//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// On Windows there's no systemd unit, sudoers, or /usr/local/bin to populate.
// `jean install` simply provisions the data directory and a starter config; the
// service itself is managed by the PID-file supervisor in service_windows.go
// (jean start / stop / status), which needs no admin rights.

const configTemplate = `# Configuration JEAN — édite-moi puis: jean restart
# jean serve lit ce fichier et lance ton binaire llama.cpp.

# Chemins (utilise des chemins Windows ; les antislashs ou slashs marchent)
BIN="C:/llama.cpp/build/bin/Release/llama-server.exe"
MODEL="C:/models/your-model.gguf"

# Serveur
PORT="8080"
HOST="127.0.0.1"

# Inference
CTX="32768"
BATCH="2048"
UBATCH="512"
NGL="999"

# Args supplémentaires passés à llama-server
EXTRA_ARGS=""
`

func cmdInstall(args []string) error {
	jeanHome := JeanHome()

	fmt.Printf("Installation (Windows)\n")
	fmt.Printf("  JEAN_HOME = %s\n", jeanHome)
	fmt.Printf("  service   = %s\n", serviceName())

	// 1. Create directories
	for _, d := range []string{jeanHome, filepath.Join(jeanHome, "configs"), filepath.Join(jeanHome, "SKILLS")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	// 2. Drop a config.env if none exists
	conf := filepath.Join(jeanHome, "config.env")
	if _, err := os.Stat(conf); os.IsNotExist(err) {
		if err := os.WriteFile(conf, []byte(configTemplate), 0o644); err != nil {
			return err
		}
		fmt.Printf("  %s écrit %s\n", green("✓"), conf)
	} else {
		fmt.Printf("  %s config.env déjà présent — inchangé\n", dim("•"))
	}

	fmt.Println()
	fmt.Printf("%s installation terminée.\n", green("[ok]"))
	fmt.Printf("\nProchaines étapes :\n")
	fmt.Printf("  1. édite la config :   %s   (renseigne BIN, MODEL)\n", bold("jean edit"))
	fmt.Printf("  2. démarre le service: %s\n", bold("jean start"))
	fmt.Printf("  3. UI web :            %s\n", bold("jean web"))
	fmt.Printf("\n%s pour exécuter 'jean' depuis n'importe où, ajoute son dossier au PATH.\n", dim("[info]"))
	return nil
}

func cmdUninstall(args []string) error {
	keepData := true
	for _, a := range args {
		if a == "--purge" {
			keepData = false
		}
		if a == "--keep-data" {
			keepData = true
		}
	}
	// Stop the background server if it's running.
	_ = svcStop(false)

	if !keepData {
		jeanHome := JeanHome()
		if err := os.RemoveAll(jeanHome); err != nil {
			return fmt.Errorf("suppression de %s: %w", jeanHome, err)
		}
		fmt.Printf("  %s %s supprimé\n", green("✓"), jeanHome)
	} else {
		fmt.Println(dim("(données utilisateur conservées — relance avec --purge pour tout supprimer)"))
	}
	fmt.Println(green("[ok]") + " désinstallé")
	return nil
}
