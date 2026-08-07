//go:build linux

package ajean

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// sudoersTemplate autorise l'utilisateur à piloter UNE unité sans mot de passe.
// Posé pour les deux unités : l'interface, qui tourne sans privilèges, doit
// pouvoir redémarrer le moteur (bascule de preset) comme elle-même (nouveau
// jeton de liaison).
const sudoersTemplate = `# Permet à %s de piloter l'unité %s sans mot de passe (posé par ajean install).
%s ALL=(root) NOPASSWD: /bin/systemctl start %s, /bin/systemctl stop %s, /bin/systemctl restart %s, /bin/systemctl enable %s, /bin/systemctl disable %s
`

// uiUnitTemplate — le service d'interface : UI web locale, tunnel du relais et
// endpoint OpenAI, servis par un seul process (donc une seule conversation).
// Champs formatés : User, WorkingDirectory, ExecStart.
const uiUnitTemplate = `[Unit]
Description=AJEAN interface web + accès distant
After=network-online.target %s.service
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

const serviceUnitTemplate = `[Unit]
Description=AJEAN llama.cpp server
After=network.target

[Service]
Type=simple
User=%s
WorkingDirectory=%s
ExecStart=%s
Restart=on-failure
RestartSec=3

# Priorité CPU : on remonte le process pour qu'il ne soit pas dépriorisé face
# aux tâches de fond (sampling/orchestration côté CPU pèsent sur le débit même
# en inference GPU). Nice négatif + scheduling normal réactif.
Nice=-10
CPUSchedulingPolicy=other
CPUAccounting=yes

[Install]
WantedBy=multi-user.target
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
	fmt.Printf("  service   = %s\n", svc)

	// 1. Arborescence + configuration de départ
	if err := provisionDataDir(); err != nil {
		return err
	}
	fmt.Printf("  %s arborescence prête\n", green("✓"))

	// 2. Symlink current binary to /usr/local/bin/ajean
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if rp, e := filepath.EvalSymlinks(self); e == nil {
		self = rp
	}
	target := installedExePath()
	// Garde-fou (issue #5) : si on tourne DÉJÀ depuis la cible (l'utilisateur a
	// posé le binaire dans /usr/local/bin/ajean puis lancé `sudo ajean install`),
	// ne surtout pas Remove+Symlink sur soi-même — ça effacerait le vrai binaire
	// et créerait /usr/local/bin/ajean -> /usr/local/bin/ajean (« Too many levels
	// of symbolic links »). On compare les chemins réels (résolus).
	alreadyInPlace := false
	if tp, e := filepath.EvalSymlinks(target); e == nil && tp == self {
		alreadyInPlace = true
	}
	if alreadyInPlace {
		fmt.Printf("  %s %s est déjà le binaire installé (aucun lien à créer)\n", green("✓"), target)
	} else {
		_ = os.Remove(target)
		if err := os.Symlink(self, target); err != nil {
			// fall back to copy if symlink fails (e.g. cross-fs)
			if data, err := os.ReadFile(self); err == nil {
				_ = os.WriteFile(target, data, 0o755)
			}
		}
		fmt.Printf("  %s %s -> %s\n", green("✓"), target, self)
	}

	// 3. /etc/default/ajean, pour que les invocations root résolvent AJEAN_HOME.
	defaults := fmt.Sprintf("# Généré par ajean install — racine des données\nAJEAN_HOME=%s\n", ajeanHome)
	if err := os.WriteFile("/etc/default/ajean", []byte(defaults), 0o644); err != nil {
		return err
	}
	fmt.Printf("  %s /etc/default/ajean\n", green("✓"))

	// 4. Les deux unités : le moteur (exec llama-server) et l'interface.
	units := map[string]string{
		svc:        fmt.Sprintf(serviceUnitTemplate, targetUser, ajeanHome, installedExePath()+" serve"),
		uiUnitName: fmt.Sprintf(uiUnitTemplate, svc, targetUser, ajeanHome, installedExePath()+" web"),
	}
	for name, body := range units {
		path := "/etc/systemd/system/" + name + ".service"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
		fmt.Printf("  %s %s\n", green("✓"), path)
	}

	// 5. Sudoers : une règle par unité, pour que l'interface (sans privilèges)
	//    puisse redémarrer le moteur ET elle-même.
	for _, name := range []string{svc, uiUnitName} {
		sudoers := fmt.Sprintf(sudoersTemplate, targetUser, name, targetUser, name, name, name, name, name)
		path := "/etc/sudoers.d/ajean-" + name
		if err := os.WriteFile(path, []byte(sudoers), 0o440); err != nil {
			return err
		}
		fmt.Printf("  %s %s\n", green("✓"), path)
	}

	// 6. chown AJEAN_HOME contents to target user
	chown(ajeanHome, u)

	// 7. systemd reload
	_ = exec.Command("systemctl", "daemon-reload").Run()

	fmt.Println()
	fmt.Printf("%s installation terminée.\n", green("[ok]"))
	fmt.Printf("\nProchaines étapes :\n")
	fmt.Printf("  1. édite la config :   %s\n", bold("sudo -u "+targetUser+" ajean edit"))
	fmt.Printf("     (renseigne BIN, MODEL, etc.)\n")
	fmt.Printf("  2. démarre le service: %s\n", bold("sudo -u "+targetUser+" ajean start"))
	fmt.Printf("  3. UI web :            %s\n", bold("sudo -u "+targetUser+" ajean web"))
	return nil
}

func cmdUninstall(args []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("ajean uninstall doit être exécuté en root")
	}
	svc := serviceName()
	keepData := false
	for _, a := range args {
		if a == "--purge" {
			keepData = false
		}
		if a == "--keep-data" {
			keepData = true
		}
	}
	for _, name := range []string{uiUnitName, svc} {
		_ = exec.Command("systemctl", "stop", name).Run()
		_ = exec.Command("systemctl", "disable", name).Run()
	}
	for _, p := range []string{
		"/etc/systemd/system/" + svc + ".service",
		"/etc/systemd/system/" + uiUnitName + ".service",
		"/etc/sudoers.d/ajean-" + svc,
		"/etc/sudoers.d/ajean-" + uiUnitName,
		"/etc/default/ajean",
		installedExePath(),
	} {
		if err := os.Remove(p); err == nil {
			fmt.Printf("  %s %s\n", green("✓"), p)
		}
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	if !keepData {
		fmt.Println(dim("(données utilisateur conservées — supprime $AJEAN_HOME manuellement si tu veux purger)"))
	}
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
