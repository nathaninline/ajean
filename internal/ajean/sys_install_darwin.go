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
// sur « systemctl: command not found » / « /etc/default/ajean », cf. issue #4).
// Implémentation prudente ; à valider sur une machine Apple.


// launchdPlistTemplate — champs formatés : Label, chemin du binaire, UserName,
// WorkingDirectory, AJEAN_HOME, StandardOutPath, StandardErrorPath.
// KeepAlive/SuccessfulExit=false ≈ Restart=on-failure ; RunAtLoad relance au
// boot une fois chargé avec `-w`.
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
  <dict><key>AJEAN_HOME</key><string>%s</string></dict>
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
	ajeanHome := defaultAjeanHome()
	if v := os.Getenv("AJEAN_HOME"); v != "" {
		ajeanHome = v
	}
	svc := serviceName()

	fmt.Printf("Installation pour utilisateur %s\n", cyan(targetUser))
	fmt.Printf("  AJEAN_HOME = %s\n", ajeanHome)
	fmt.Printf("  service   = %s (launchd)\n", svc)

	// 1. Arborescence + configuration de départ
	if err := provisionDataDir(); err != nil {
		return err
	}

	// 2. Symlink idempotent vers /usr/local/bin/ajean — même garde-fou que Linux
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

	// 3. /etc/default/ajean pour que les invocations CLI résolvent AJEAN_HOME.
	_ = os.MkdirAll("/etc/default", 0o755)
	defaults := fmt.Sprintf("# Généré par ajean install — racine des données\nAJEAN_HOME=%s\n", ajeanHome)
	if err := os.WriteFile("/etc/default/ajean", []byte(defaults), 0o644); err != nil {
		return err
	}
	fmt.Printf("  %s /etc/default/ajean\n", green("✓"))

	// 4. LaunchDaemon plist (le log va sous AJEAN_HOME, accessible au user cible)
	logPath := filepath.Join(ajeanHome, svc+".log")
	plist := fmt.Sprintf(launchdPlistTemplate, launchdLabel(svc), target, targetUser, ajeanHome, ajeanHome, logPath, logPath)
	plistPath := launchdPlistPath(svc)
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}
	fmt.Printf("  %s %s\n", green("✓"), plistPath)

	// 5. chown AJEAN_HOME au user cible
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
	} {
		if err := os.Remove(p); err == nil {
			fmt.Printf("  %s %s\n", green("✓"), p)
		}
	}
	// On ne supprime jamais AJEAN_HOME automatiquement (modèles, presets).
	fmt.Println(dim("(données utilisateur conservées — supprime $AJEAN_HOME manuellement si tu veux purger)"))
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
