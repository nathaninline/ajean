//go:build darwin

package ajean

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// sys_install_darwin.go — installation macOS via launchd (LaunchDaemon), équivalent
// de sys_install_linux.go (systemd). ⚠️ NON TESTÉ sur un vrai Mac : le support macOS
// était totalement absent (le code systemd/Linux tournait par erreur et échouait
// sur « systemctl: command not found » / « /etc/default/jean », cf. issue #4).
// Implémentation prudente ; à valider sur une machine Apple.

const configTemplate = `# Configuration JEAN — édite-moi puis: ajean restart
# Le service launchd lit ce fichier et lance ton binaire llama.cpp.

# Chemins
BIN="/usr/local/bin/llama-server"
MODEL="/Users/USER/models/your-model.gguf"

# Serveur
PORT="8080"
HOST="0.0.0.0"

# Inference
CTX="32768"
BATCH="2048"
UBATCH="512"
NGL="999"

# Args supplémentaires passés à llama-server
EXTRA_ARGS=""
`

// launchdPlistTemplate — champs formatés : Label, chemin du binaire, UserName,
// WorkingDirectory, AJEAN_HOME, JEAN_HOME, StandardOutPath, StandardErrorPath.
// KeepAlive/SuccessfulExit=false ≈ Restart=on-failure ; RunAtLoad relance au
// boot une fois chargé avec `-w`.
//
// Le chemin du binaire est injecté et non codé en dur : il a changé avec le
// renommage, et un plist figé sur l'ancien chemin donnerait un daemon qui refuse
// de démarrer. Les deux variables d'environnement sont posées car le code lit
// encore JEAN_HOME en héritage.
const launchdPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>serve</string>
  </array>
  <key>UserName</key><string>%s</string>
  <key>WorkingDirectory</key><string>%s</string>
  <key>EnvironmentVariables</key>
  <dict><key>AJEAN_HOME</key><string>%s</string><key>JEAN_HOME</key><string>%s</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key>
  <dict><key>SuccessfulExit</key><false/></dict>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`

func cmdInstall(args []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("ajean install doit être exécuté en root (sudo ajean install)")
	}
	targetUser := os.Getenv("SUDO_USER")
	if targetUser == "" {
		targetUser = "root"
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--user=") {
			targetUser = strings.TrimPrefix(a, "--user=")
		}
	}
	u, err := user.Lookup(targetUser)
	if err != nil {
		return fmt.Errorf("utilisateur '%s' introuvable: %w", targetUser, err)
	}
	// Migration de l'agencement jean -> ajean AVANT de résoudre les chemins :
	// `install` est root et délibéré, c'est le bon moment. Ne fait rien si la
	// machine est déjà en ajean, ou si l'utilisateur a imposé un chemin.
	if err := migrateLayout(defaultLayoutPlan()); err != nil {
		return err
	}
	resetResolvedHome() // le dossier a pu changer à l'instant

	ajeanHome := defaultAjeanHome()
	if v := os.Getenv("JEAN_HOME"); v != "" {
		ajeanHome = v
	}
	svc := serviceName()

	fmt.Printf("Installation pour utilisateur %s\n", cyan(targetUser))
	fmt.Printf("  JEAN_HOME = %s\n", ajeanHome)
	fmt.Printf("  service   = %s (launchd)\n", svc)

	// 1. Répertoires
	for _, d := range []string{ajeanHome, filepath.Join(ajeanHome, "configs"), filepath.Join(ajeanHome, "SKILLS")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	// 2. config.env si absent
	conf := filepath.Join(ajeanHome, "config.env")
	if _, err := os.Stat(conf); os.IsNotExist(err) {
		body := strings.ReplaceAll(configTemplate, "USER", targetUser)
		if err := os.WriteFile(conf, []byte(body), 0o644); err != nil {
			return err
		}
		fmt.Printf("  %s écrit %s\n", green("✓"), conf)
	}

	// 3. Symlink idempotent vers /usr/local/bin/ajean — même garde-fou que Linux
	//    (issue #5 : ne pas se lier sur soi-même si on tourne déjà depuis la cible).
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if rp, e := filepath.EvalSymlinks(self); e == nil {
		self = rp
	}
	target := installedExePath()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	alreadyInPlace := false
	if tp, e := filepath.EvalSymlinks(target); e == nil && tp == self {
		alreadyInPlace = true
	}
	if alreadyInPlace {
		fmt.Printf("  %s %s est déjà le binaire installé (aucun lien à créer)\n", green("✓"), target)
	} else {
		_ = os.Remove(target)
		if err := os.Symlink(self, target); err != nil {
			if data, err := os.ReadFile(self); err == nil {
				_ = os.WriteFile(target, data, 0o755)
			}
		}
		fmt.Printf("  %s %s -> %s\n", green("✓"), target, self)
	}

	// 3 bis. Alias hérité : `jean` doit rester tapable (voir sys_paths.go).
	if alias := legacyExePath(); alias != target {
		if rp, e := filepath.EvalSymlinks(alias); e != nil || rp != self {
			_ = os.Remove(alias)
			if err := os.Symlink(target, alias); err != nil {
				fmt.Printf("  %s alias %s non créé (%v) — « ajean » reste disponible\n", yellow("[info]"), alias, err)
			} else {
				fmt.Printf("  %s %s -> %s (alias hérité)\n", green("✓"), alias, target)
			}
		}
	}

	// 4. /etc/default/ajean pour que les invocations CLI résolvent AJEAN_HOME.
	// Les deux clés sont écrites : des scripts d'utilisateurs sourcent ce fichier
	// et lisent encore $JEAN_HOME.
	_ = os.MkdirAll("/etc/default", 0o755)
	defaults := fmt.Sprintf("# Generated by ajean install — racine des configs/memoire/presets\nAJEAN_HOME=%s\nJEAN_HOME=%s\n", ajeanHome, ajeanHome)
	if err := os.WriteFile("/etc/default/ajean", []byte(defaults), 0o644); err != nil {
		return err
	}
	fmt.Printf("  %s /etc/default/ajean\n", green("✓"))

	// 5. LaunchDaemon plist (le log va sous JEAN_HOME, accessible au user cible)
	logPath := filepath.Join(ajeanHome, svc+".log")
	plist := fmt.Sprintf(launchdPlistTemplate, launchdLabel(svc), target, targetUser, ajeanHome, ajeanHome, ajeanHome, logPath, logPath)
	plistPath := launchdPlistPath(svc)
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}
	fmt.Printf("  %s %s\n", green("✓"), plistPath)

	// 6. chown JEAN_HOME au user cible
	chown(ajeanHome, u)

	fmt.Println()
	fmt.Printf("%s installation terminée.\n", green("[ok]"))
	fmt.Printf("\nProchaines étapes :\n")
	fmt.Printf("  1. édite la config :   %s\n", bold("sudo -u "+targetUser+" ajean edit"))
	fmt.Printf("     (renseigne BIN, MODEL, etc.)\n")
	fmt.Printf("  2. démarre le service: %s\n", bold("sudo ajean start"))
	fmt.Printf("  3. UI web :            %s\n", bold("sudo -u "+targetUser+" ajean web"))
	return nil
}

func cmdUninstall(args []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("ajean uninstall doit être exécuté en root")
	}
	svc := serviceName()
	_ = exec.Command("launchctl", "unload", "-w", launchdPlistPath(svc)).Run()
	for _, p := range []string{
		launchdPlistPath(svc),
		"/etc/default/ajean",
		installedExePath(),
		legacyExePath(),
	} {
		if err := os.Remove(p); err == nil {
			fmt.Printf("  %s %s\n", green("✓"), p)
		}
	}
	// On ne supprime jamais JEAN_HOME automatiquement (modèles, configs).
	fmt.Println(dim("(données utilisateur conservées — supprime $JEAN_HOME manuellement si tu veux purger)"))
	fmt.Println(green("[ok]") + " désinstallé")
	return nil
}

// chown recursively changes ownership of path to the given user/group.
func chown(path string, u *user.User) {
	var uid, gid int
	fmt.Sscanf(u.Uid, "%d", &uid)
	fmt.Sscanf(u.Gid, "%d", &gid)
	filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			_ = os.Chown(p, uid, gid)
		}
		return nil
	})
}
