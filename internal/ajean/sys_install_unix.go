//go:build !windows

package ajean

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// sys_install_unix.go — installation et désinstallation, partie commune à Linux
// et macOS. Les deux plateformes font exactement la même chose, à un détail
// près : la façon de déclarer les services (systemd d'un côté, launchd de
// l'autre). Ce détail vit dans sys_install_linux.go / sys_install_darwin.go,
// derrière deux fonctions : installServices et uninstallServices.
//
// Avant, chaque plateforme avait sa copie intégrale du parcours : deux fichiers
// identiques à 60 %, donc toute correction à faire deux fois. Comme le support
// macOS n'est pas testé sur une vraie machine, c'est précisément là qu'une
// divergence passait inaperçue.

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

	fmt.Printf("Installation pour utilisateur %s\n", cyan(targetUser))
	fmt.Printf("  AJEAN_HOME = %s\n", ajeanHome)
	fmt.Printf("  services   = %s + %s\n", serviceName(), uiUnitName)

	// 0. Reprise d'une installation 0.7, si c'en est une. AVANT provisionDataDir :
	//    elle déplace des dossiers entiers, donc la destination ne doit pas
	//    encore exister. Code temporaire, voir migrate_07.go.
	if err := migrateFrom07(ajeanHome); err != nil {
		return fmt.Errorf("reprise de l'installation 0.7 : %w", err)
	}

	// 1. Arborescence + configuration de départ.
	if err := provisionDataDir(); err != nil {
		return err
	}
	fmt.Printf("  %s arborescence prête\n", green("✓"))

	// 2. Lien vers /usr/local/bin/ajean.
	if err := linkInstalledExe(); err != nil {
		return err
	}

	// 3. /etc/default/ajean, pour que les invocations root résolvent AJEAN_HOME.
	if err := os.MkdirAll("/etc/default", 0o755); err != nil {
		return err
	}
	defaults := fmt.Sprintf("# Généré par ajean install — racine des données\nAJEAN_HOME=%s\n", ajeanHome)
	if err := os.WriteFile("/etc/default/ajean", []byte(defaults), 0o644); err != nil {
		return err
	}
	fmt.Printf("  %s /etc/default/ajean\n", green("✓"))

	// 4. Déclaration des services (systemd ou launchd selon la plateforme).
	if err := installServices(targetUser, ajeanHome); err != nil {
		return err
	}

	// 5. AJEAN_HOME appartient à l'utilisateur cible : les deux services et les
	//    commandes qu'il tape doivent pouvoir écrire dans la base.
	chown(ajeanHome, u)

	fmt.Println()
	fmt.Printf("%s installation terminée.\n", green("[ok]"))
	fmt.Printf("\nProchaines étapes :\n")
	fmt.Printf("  1. édite la config :  %s   (renseigne BIN, MODEL)\n", bold("sudo -u "+targetUser+" ajean edit"))
	fmt.Printf("  2. démarre le moteur : %s\n", bold("sudo ajean start"))
	fmt.Printf("  3. démarre l'interface: %s\n", bold("sudo ajean ui start"))
	return nil
}

// linkInstalledExe pose /usr/local/bin/ajean sur le binaire courant.
//
// Garde-fou (issue #5) : si on tourne DÉJÀ depuis la cible — l'utilisateur a
// posé le binaire là puis lancé `sudo ajean install` — il ne faut surtout pas
// Remove+Symlink sur soi-même : ça effacerait le vrai binaire et créerait un
// lien vers lui-même (« Too many levels of symbolic links »). On compare les
// chemins réels, résolus.
func linkInstalledExe() error {
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
	if tp, e := filepath.EvalSymlinks(target); e == nil && tp == self {
		fmt.Printf("  %s %s est déjà le binaire installé (aucun lien à créer)\n", green("✓"), target)
		return nil
	}
	_ = os.Remove(target)
	if err := os.Symlink(self, target); err != nil {
		// Repli sur une copie si le lien échoue (volumes différents).
		data, rerr := os.ReadFile(self)
		if rerr != nil {
			return rerr
		}
		if werr := os.WriteFile(target, data, 0o755); werr != nil {
			return werr
		}
	}
	fmt.Printf("  %s %s -> %s\n", green("✓"), target, self)
	return nil
}

func cmdUninstall(args []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("ajean uninstall doit être exécuté en root")
	}
	uninstallServices()
	for _, p := range []string{"/etc/default/ajean", installedExePath()} {
		if err := os.Remove(p); err == nil {
			fmt.Printf("  %s %s\n", green("✓"), p)
		}
	}
	fmt.Println(dim("(données utilisateur conservées — supprime $AJEAN_HOME manuellement si tu veux purger)"))
	fmt.Println(green("[ok]") + " désinstallé")
	return nil
}

// chown donne récursivement path à l'utilisateur/groupe indiqué.
func chown(path string, u *user.User) {
	var uid, gid int
	fmt.Sscanf(u.Uid, "%d", &uid)
	fmt.Sscanf(u.Gid, "%d", &gid)
	_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			_ = os.Chown(p, uid, gid)
		}
		return nil
	})
}
