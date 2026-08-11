// ajean-remote — client LÉGER « poste distant ». Binaire séparé et minuscule
// (protocole partagé + WebSocket, sans UI ni moteur) : on l'installe sur un PC
// pour que l'IA d'un serveur AJEAN puisse agir dessus.
//
//	ajean-remote install <url-serveur> --code CODE [--allow shell,read,write,list] [--root DIR]
//	                                 appaire ce PC et l'installe en service (démarrage auto)
//	ajean-remote connect <url> --code CODE [...]   même chose mais en avant-plan (test)
//	ajean-remote run                   boucle client (utilisé par le service)
//	ajean-remote status | uninstall | logout
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/nathaninline/ajean/internal/nodeclient"
)

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	var err error
	switch cmd {
	case "install":
		err = doConnect(args, true)
	case "connect":
		err = doConnect(args, false)
	case "run":
		err = doRun()
	case "uninstall":
		err = nodeclient.Uninstall()
		if err == nil {
			fmt.Println("[ok] service retiré")
		}
	case "logout":
		_ = nodeclient.Uninstall()
		err = os.Remove(nodeclient.ConfigPath())
		if err == nil || os.IsNotExist(err) {
			err = nil
			fmt.Println("[ok] configuration oubliée")
		}
	case "status":
		err = doStatus()
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "[err]", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`ajean-remote — client léger « poste distant » pour AJEAN

  ajean-remote install <url-serveur> --code CODE [options]   appaire + installe en service (démarrage auto, invisible)
  ajean-remote connect <url-serveur> --code CODE [options]   idem en avant-plan (pour tester)
  ajean-remote status                                        état de la configuration
  ajean-remote uninstall                                     retire le service (garde la config)
  ajean-remote logout                                        retire le service ET oublie la clé

Options :
  --code CODE     code d'appairage (généré dans l'UI AJEAN → Postes distants)
  --allow LISTE   capacités acceptées : shell,read,write,list (défaut : ce que le serveur autorise)
  --root DIR      dossier autorisé (confine lecture/écriture)
  --yes           auto-approuve les actions (obligatoire pour le service, sans terminal)
`)
}

// doConnect appaire au besoin, sauvegarde, puis installe (service) ou lance
// (avant-plan) selon install.
func doConnect(args []string, install bool) error {
	f := parseFlags(args)
	cfg, lerr := nodeclient.LoadConfig()
	needEnroll := lerr != nil || cfg.Priv == ""
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
			return fmt.Errorf("url du serveur requise (ex: http://192.168.1.127:8090)")
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
	allow, root, yes := f.allow, f.root, f.yes
	if len(allow) > 0 {
		cfg.Caps = nodeclient.SanitizeCaps(allow)
	}
	if root != "" {
		cfg.Root = root
	}
	if cfg.Root == "" {
		cfg.Root = nodeclient.DataDir() + string(os.PathSeparator) + "workspace"
	}
	if yes || install {
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

func doRun() error {
	cfg, err := nodeclient.LoadConfig()
	if err != nil {
		return fmt.Errorf("aucune configuration — lance d'abord « ajean-remote install … »")
	}
	return nodeclient.RunService(cfg)
}

func doStatus() error {
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

type flags struct {
	url, code, root, key, machine string
	allow                         []string
	yes                           bool
}

func parseFlags(args []string) flags {
	var f flags
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
