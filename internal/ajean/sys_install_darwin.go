//go:build darwin

package ajean

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// sys_install_darwin.go — la part launchd de l'installation. Le parcours commun
// (dossiers, lien du binaire, /etc/default, chown) vit dans sys_install_unix.go.
//
// ⚠️ NON TESTÉ sur un vrai Mac. Le support macOS a été ajouté parce que le code
// systemd tournait par erreur et échouait sur « systemctl: command not found ».
// Implémentation prudente, à valider sur une machine Apple.

// launchdPlistTemplate — champs : Label, binaire, argument, UserName,
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
    <string>%s</string>
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

// unitArgs associe chaque service à la sous-commande qu'il exécute.
func unitArgs() map[string]string {
	return map[string]string{serviceName(): "serve", uiUnitName: "web"}
}

func installServices(targetUser, ajeanHome string) error {
	exe := installedExePath()
	for name, arg := range unitArgs() {
		logPath := filepath.Join(ajeanHome, name+".log")
		plist := fmt.Sprintf(launchdPlistTemplate,
			launchdLabel(name), exe, arg, targetUser, ajeanHome, ajeanHome, logPath, logPath)
		path := launchdPlistPath(name)
		if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
			return err
		}
		fmt.Printf("  %s %s\n", green("✓"), path)
	}
	return nil
}

// uninstallServices décharge et efface les deux daemons. L'interface passe
// AVANT le moteur : elle sait le redémarrer, l'ordre inverse pourrait le
// relancer sous nos pieds.
func uninstallServices() {
	for _, name := range []string{uiUnitName, serviceName()} {
		_ = exec.Command("launchctl", "unload", "-w", launchdPlistPath(name)).Run()
		if err := os.Remove(launchdPlistPath(name)); err == nil {
			fmt.Printf("  %s %s\n", green("✓"), launchdPlistPath(name))
		}
	}
}
