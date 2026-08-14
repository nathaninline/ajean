// Sous-commande « ajean remote » — client « poste distant » intégré au binaire
// principal. On l'installe sur un PC pour que l'IA d'un serveur AJEAN puisse agir
// dessus (shell/fichiers), chiffré de bout en bout via ajean.link (relais aveugle)
// ou en direct sur le réseau local.
//
//	ajean remote install <url-serveur> --machine ID --key CLÉ --code CODE [--allow shell,read,write,list] [--root DIR]
//	                                    appaire ce PC et l'installe en service (démarrage auto)
//	ajean remote connect <url> ...      idem mais en avant-plan (test)
//	ajean remote run                    boucle client (utilisé par le service)
//	ajean remote status | uninstall | logout
//
// Historiquement un binaire séparé « ajean-remote » ; fusionné dans ce binaire
// pour n'avoir qu'une seule application.
package ajean

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/nathaninline/ajean/internal/nodeclient"
)

func cmdRemote(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "install":
		return remoteConnect(args, true)
	case "connect":
		return remoteConnect(args, false)
	case "run":
		return remoteRun()
	case "uninstall":
		if err := nodeclient.Uninstall(); err != nil {
			return err
		}
		fmt.Println("[ok] service retiré")
		return nil
	case "logout":
		_ = nodeclient.Uninstall()
		err := os.Remove(nodeclient.ConfigPath())
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("[ok] configuration oubliée")
		return nil
	case "status":
		return remoteStatus()
	default:
		remoteUsage()
		return nil
	}
}

func remoteUsage() {
	fmt.Print(`ajean remote — piloter CE PC depuis l'IA d'un serveur AJEAN

  ajean remote install <url-serveur> --code CODE [options]   appaire + installe en service (démarrage auto, invisible)
  ajean remote connect <url-serveur> --code CODE [options]   idem en avant-plan (pour tester)
  ajean remote status                                        état de la configuration
  ajean remote uninstall                                     retire le service (garde la config)
  ajean remote logout                                        retire le service ET oublie la clé

Options :
  --machine ID   machine à joindre via ajean.link (fournie par l'UI → Postes distants)
  --key CLÉ      clé publique de l'agent (le poste scelle vers elle)
  --code CODE    code d'appairage (10 min, généré dans l'UI AJEAN → Postes distants)
  --allow LISTE  capacités acceptées : shell,read,write,list (défaut : ce que le serveur autorise)
  --root DIR     dossier autorisé (confine lecture/écriture)
  --yes          auto-approuve les actions (obligatoire pour le service, sans terminal)
`)
}

// remoteConnect appaire au besoin, sauvegarde, puis installe (service) ou lance
// (avant-plan) selon install.
func remoteConnect(args []string, install bool) error {
	f := parseRemoteFlags(args)
	cfg, lerr := nodeclient.LoadConfig()
	// Un --code est un jeton d'appairage à usage unique : le fournir veut TOUJOURS
	// dire « (ré)appaire-moi maintenant ». Sans ce « || f.code != "" », une config
	// résiduelle (test précédent, ancien binaire ajean-remote au même emplacement
	// %ProgramData%\ajean-remote\config.json) faisait sauter l'appairage en silence :
	// le service démarrait avec de vieilles clés que le serveur ignore et le poste
	// ne remontait jamais, alors que tout affichait « [ok] ».
	needEnroll := lerr != nil || cfg.Priv == "" || f.code != ""
	if f.url != "" {
		cfg.ServerURL = f.url
	}
	if f.key != "" {
		cfg.AgentPub = f.key
	}
	if f.machine != "" {
		cfg.MachineID = f.machine
	}
	if needEnroll {
		if cfg.ServerURL == "" {
			return fmt.Errorf("url du serveur requise (ex: https://ajean.link)")
		}
		if f.code == "" {
			return fmt.Errorf("code d'appairage requis : --code CODE")
		}
		if cfg.AgentPub == "" {
			return fmt.Errorf("clé de l'agent requise : --key <clé> (fournie par la commande d'appairage)")
		}
		if err := nodeclient.Enroll(&cfg, f.code); err != nil {
			return err
		}
		fmt.Printf("[ok] appairé (id %s)\n", cfg.ID)
	}
	if len(f.allow) > 0 {
		cfg.Caps = nodeclient.SanitizeCaps(f.allow)
	}
	if f.root != "" {
		cfg.Root = f.root
	}
	if cfg.Root == "" {
		cfg.Root = nodeclient.DataDir() + string(os.PathSeparator) + "workspace"
	}
	if f.yes || install {
		cfg.AutoYes = true // un service n'a pas de terminal pour confirmer
	}
	if cfg.Name == "" {
		cfg.Name, _ = os.Hostname()
	}
	if err := nodeclient.SaveConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("[ok] poste « %s » → %s\n", cfg.Name, cfg.ServerURL)
	fmt.Printf("[ok] capacités : %s | dossier : %s\n", strings.Join(cfg.Caps, ", "), cfg.Root)

	if install {
		if err := nodeclient.Install(); err != nil {
			return err
		}
		fmt.Println("[ok] service installé et démarré — il tournera en tâche de fond et au redémarrage")
		return nil
	}
	nodeclient.Run(context.Background(), cfg, false)
	return nil
}

func remoteRun() error {
	cfg, err := nodeclient.LoadConfig()
	if err != nil {
		return fmt.Errorf("aucune configuration — lance d'abord « ajean remote install … »")
	}
	return nodeclient.RunService(cfg)
}

func remoteStatus() error {
	cfg, err := nodeclient.LoadConfig()
	if err != nil {
		fmt.Println("aucun poste configuré")
		return nil
	}
	fmt.Printf("serveur   : %s\n", cfg.ServerURL)
	fmt.Printf("nom       : %s\n", cfg.Name)
	fmt.Printf("capacités : %s\n", strings.Join(cfg.Caps, ", "))
	fmt.Printf("dossier   : %s\n", cfg.Root)
	fmt.Printf("auto-oui  : %v\n", cfg.AutoYes)
	fmt.Printf("os        : %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return nil
}

type remoteFlags struct {
	url, code, root, key, machine string
	allow                         []string
	yes                           bool
}

func parseRemoteFlags(args []string) remoteFlags {
	var f remoteFlags
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "--code":
			f.code = next()
		case a == "--key":
			f.key = next()
		case a == "--machine":
			f.machine = next()
		case a == "--allow":
			for _, p := range strings.Split(next(), ",") {
				if p = strings.TrimSpace(p); p != "" {
					f.allow = append(f.allow, p)
				}
			}
		case a == "--root":
			f.root = next()
		case a == "--yes" || a == "-y":
			f.yes = true
		case !strings.HasPrefix(a, "-") && f.url == "":
			f.url = a
		}
	}
	return f
}
