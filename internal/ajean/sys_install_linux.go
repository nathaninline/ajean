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

const configTemplate = `# Configuration JEAN — édite-moi puis: ajean restart
# Le service systemd lit ce fichier et lance ton binaire llama.cpp.

# Chemins
BIN="/usr/local/bin/llama-server"
MODEL="/home/USER/models/your-model.gguf"

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

const sudoersTemplate = `# Allow %s to manage the %s systemd unit without a password (installed by jean).
%s ALL=(root) NOPASSWD: /bin/systemctl start %s, /bin/systemctl stop %s, /bin/systemctl restart %s, /bin/systemctl enable %s, /bin/systemctl disable %s
`

const serviceUnitTemplate = `[Unit]
Description=JEAN llama.cpp server
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
	fmt.Printf("  service   = %s\n", svc)

	// 1. Create directories
	for _, d := range []string{ajeanHome, filepath.Join(ajeanHome, "configs"), filepath.Join(ajeanHome, "SKILLS")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	// 2. Drop a config.env if none exists
	conf := filepath.Join(ajeanHome, "config.env")
	if _, err := os.Stat(conf); os.IsNotExist(err) {
		body := strings.ReplaceAll(configTemplate, "USER", targetUser)
		if err := os.WriteFile(conf, []byte(body), 0o644); err != nil {
			return err
		}
		fmt.Printf("  %s écrit %s\n", green("✓"), conf)
	}

	// 3. Symlink current binary to /usr/local/bin/jean
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if rp, e := filepath.EvalSymlinks(self); e == nil {
		self = rp
	}
	target := installedExePath()
	// Garde-fou (issue #5) : si on tourne DÉJÀ depuis la cible (l'utilisateur a
	// posé le binaire dans /usr/local/bin/jean puis lancé `sudo ajean install`),
	// ne surtout pas Remove+Symlink sur soi-même — ça effacerait le vrai binaire
	// et créerait /usr/local/bin/jean -> /usr/local/bin/jean (« Too many levels
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

	// 3 bis. Alias hérité : `ajean` doit rester tapable. Des utilisateurs ont des
	// alias shell, des cron, des scripts de déploiement et des tutos qui appellent
	// « jean » — on ne contrôle rien de tout ça, et un binaire renommé qui casse
	// la commande d'hier est une régression pour eux. Le lien pointe sur le
	// nouveau nom, donc il suit les mises à jour sans entretien.
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

	// 4. Drop /etc/default/ajean so root invocations resolve AJEAN_HOME correctly.
	// On écrit les DEUX clés : des scripts d'utilisateurs sourcent ce fichier et
	// lisent $JEAN_HOME. L'ancien /etc/default/jean est laissé en place — le
	// supprimer casserait ces mêmes scripts, et AjeanHome() sait déjà le lire.
	defaults := fmt.Sprintf("# Generated by ajean install — racine des configs/memoire/presets\nAJEAN_HOME=%s\nJEAN_HOME=%s\n", ajeanHome, ajeanHome)
	if err := os.WriteFile("/etc/default/ajean", []byte(defaults), 0o644); err != nil {
		return err
	}
	fmt.Printf("  %s /etc/default/ajean\n", green("✓"))

	// 5. Write the systemd unit (ExecStart = `ajean serve`, no start.sh needed)
	unit := fmt.Sprintf(serviceUnitTemplate, targetUser, ajeanHome, installedExePath()+" serve")
	unitPath := "/etc/systemd/system/" + svc + ".service"
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	fmt.Printf("  %s %s\n", green("✓"), unitPath)

	// 5. Sudoers drop-in
	sudoers := fmt.Sprintf(sudoersTemplate, targetUser, svc, targetUser, svc, svc, svc, svc, svc)
	sudoersPath := "/etc/sudoers.d/ajean-" + svc
	if err := os.WriteFile(sudoersPath, []byte(sudoers), 0o440); err != nil {
		return err
	}
	fmt.Printf("  %s %s\n", green("✓"), sudoersPath)

	// 6. chown JEAN_HOME contents to target user
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
	_ = exec.Command("systemctl", "stop", svc).Run()
	_ = exec.Command("systemctl", "disable", svc).Run()
	for _, p := range []string{
		"/etc/systemd/system/" + svc + ".service",
		"/etc/sudoers.d/ajean-" + svc,
		"/etc/default/jean",
		installedExePath(),
	} {
		if err := os.Remove(p); err == nil {
			fmt.Printf("  %s %s\n", green("✓"), p)
		}
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	if !keepData {
		fmt.Println(dim("(données utilisateur conservées — supprime $JEAN_HOME manuellement si tu veux purger)"))
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
