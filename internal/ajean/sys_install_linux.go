//go:build linux

package ajean

import (
	"fmt"
	"os"
	"os/exec"
)

// sys_install_linux.go — la part systemd de l'installation. Le parcours commun
// (dossiers, lien du binaire, /etc/default, chown) vit dans sys_install_unix.go.

// sudoersTemplate autorise l'utilisateur à piloter UNE unité sans mot de passe :
// l'interface, qui tourne sans privilèges, doit pouvoir se redémarrer elle-même
// (nouveau jeton, mise à jour). Les modèles passent par l'assistant root
// (services_root_linux.go), pas par sudo.
const sudoersTemplate = `# Permet à %[1]s de piloter l'unité %[2]s sans mot de passe (posé par ajean install).
%[1]s ALL=(root) NOPASSWD: /bin/systemctl start %[2]s, /bin/systemctl stop %[2]s, /bin/systemctl restart %[2]s, /bin/systemctl enable %[2]s, /bin/systemctl disable %[2]s
`

// uiUnitTemplate — l'interface : UI web locale, tunnel du relais et endpoint
// OpenAI, servis par un SEUL process (donc une seule conversation).
// Champs : User, WorkingDirectory, ExecStart.
const uiUnitTemplate = `[Unit]
Description=AJEAN : interface web et accès distant
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
WorkingDirectory=%s
ExecStart=%s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`

func installServices(targetUser, ajeanHome string) error {
	exe := installedExePath()
	// Les modèles sont des unités systemd SYSTÈME, pilotées par l'assistant root
	// (services_root_linux.go) : il se pose avec l'installation, comme le
	// gardien RAM. Un échec n'empêche pas l'interface de s'installer.
	if err := installRootHelper(targetUser); err != nil {
		fmt.Printf("  %s assistant root des services : %v\n", yellow("[warn]"), err)
	}
	units := map[string]string{
		uiUnitName: fmt.Sprintf(uiUnitTemplate, targetUser, ajeanHome, exe+" web"),
	}
	for name, body := range units {
		path := "/etc/systemd/system/" + name + ".service"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
		fmt.Printf("  %s %s\n", green("✓"), path)

		sudoers := fmt.Sprintf(sudoersTemplate, targetUser, name)
		sudoersPath := "/etc/sudoers.d/" + name
		if err := os.WriteFile(sudoersPath, []byte(sudoers), 0o440); err != nil {
			return err
		}
		fmt.Printf("  %s %s\n", green("✓"), sudoersPath)
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	return nil
}

// uninstallServices arrête, désactive et efface l'unité de l'interface et
// l'ancienne unité du moteur (versions 0.18 et avant) si elle existe encore,
// puis retire l'assistant root et le gardien RAM.
func uninstallServices() {
	svc := serviceName()
	for _, name := range []string{uiUnitName, svc} {
		_ = exec.Command("systemctl", "stop", name).Run()
		_ = exec.Command("systemctl", "disable", name).Run()
	}
	for _, p := range []string{
		"/etc/systemd/system/" + svc + ".service",
		"/etc/systemd/system/" + uiUnitName + ".service",
		"/etc/sudoers.d/" + svc,
		"/etc/sudoers.d/" + uiUnitName,
	} {
		if err := os.Remove(p); err == nil {
			fmt.Printf("  %s %s\n", green("✓"), p)
		}
	}
	uninstallRootHelper()
	_ = exec.Command("systemctl", "daemon-reload").Run()
}
