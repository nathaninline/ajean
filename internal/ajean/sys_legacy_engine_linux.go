//go:build linux

package ajean

// Passage 0.18 → 0.19 sous Linux. L'ancien moteur (unité ajean-engine, qui
// lançait `ajean serve`) n'existe plus : laissé actif, il gardait le port et
// la VRAM du modèle chargé avant la mise à jour, puis plantait en boucle au
// démarrage suivant (la commande serve a disparu). La règle sudoers posée par
// la 0.18 autorise toujours stop/disable sans mot de passe : on s'en sert.
//
// Les modèles, eux, passent par l'assistant root (services_root_linux.go). Une
// mise à jour par `ajean update` ne peut pas l'installer (il faut root) : on le
// dit dans l'interface, avec la commande exacte.

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func legacyEngineUnitPath() string {
	return filepath.Join(systemUnitDir, serviceName()+".service")
}

// retireLegacyEngine arrête et désactive l'ancienne unité du moteur si elle est
// encore là. Sans effet sur une installation neuve.
func retireLegacyEngine() {
	if !svcIsFile(legacyEngineUnitPath()) {
		return
	}
	name := serviceName()
	en, _ := exec.Command("systemctl", "is-enabled", name).Output()
	ac, _ := exec.Command("systemctl", "is-active", name).Output()
	enabled := strings.TrimSpace(string(en)) == "enabled"
	active := strings.TrimSpace(string(ac)) == "active" || strings.TrimSpace(string(ac)) == "activating"
	if !enabled && !active {
		return
	}
	// L'ancien moteur démarrait avec la machine : le modèle du chat converti le
	// fera aussi (legacyAutostartLoop), sinon l'utilisateur le perdrait au
	// prochain redémarrage.
	if enabled {
		_ = putStr(bkState, legacyAutostartKey, "1")
	}
	run := func(op string) error {
		if os.Geteuid() == 0 {
			return exec.Command("systemctl", op, name).Run()
		}
		// Chemin exact de la règle sudoers 0.18 (/bin/systemctl).
		return exec.Command("sudo", "-n", "/bin/systemctl", op, name).Run()
	}
	if active {
		if err := run("stop"); err != nil {
			fmt.Fprintf(os.Stderr, "[ajean] arrêt de l'ancien moteur %s : %v\n", name, err)
		}
	}
	if enabled {
		if err := run("disable"); err != nil {
			fmt.Fprintf(os.Stderr, "[ajean] désactivation de l'ancien moteur %s : %v\n", name, err)
		}
	}
}

// removeLegacyEngineRoot retire l'ancienne unité et sa règle sudoers (root,
// depuis install-helper / install).
func removeLegacyEngineRoot() {
	name := serviceName()
	p := legacyEngineUnitPath()
	if !svcIsFile(p) {
		return
	}
	_ = rootSystemctl("stop", "--", name)
	_ = rootSystemctl("disable", "--", name)
	for _, f := range []string{p, "/etc/sudoers.d/" + name} {
		if err := os.Remove(f); err == nil {
			fmt.Printf("  %s ancien moteur retiré : %s\n", green("✓"), f)
		}
	}
	_ = rootSystemctl("daemon-reload")
}

// legacyWarning : ce qui empêche les modèles locaux de démarrer après une mise
// à jour, à afficher dans l'interface ("" si tout va bien).
func legacyWarning() string {
	if os.Geteuid() == 0 {
		return ""
	}
	v := helperProtoCached()
	if v >= helperProto {
		return ""
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "ajean"
	}
	who := os.Getenv("USER")
	if u, err := user.Current(); err == nil && u.Username != "" {
		who = u.Username
	}
	cmd := "sudo " + exe + " install-helper " + who
	if v >= 0 {
		return "L'assistant root d'AJEAN date d'une version précédente : certains modèles peuvent refuser de démarrer. " +
			"Pour le mettre à jour, dans un terminal de cette machine : " + cmd
	}
	return "Les modèles locaux ne peuvent pas démarrer : l'assistant root d'AJEAN n'est pas installé (étape nouvelle de la version 0.19). " +
		"Dans un terminal de cette machine : " + cmd
}

// helperProtoCached : /api/status est interrogé toutes les 5 s, on ne relance
// pas sudo à chaque fois (1 min de cache, et tout de suite après un échec).
var helperProtoCache struct {
	sync.Mutex
	v  int
	at time.Time
}

func helperProtoCached() int {
	helperProtoCache.Lock()
	defer helperProtoCache.Unlock()
	if !helperProtoCache.at.IsZero() && time.Since(helperProtoCache.at) < time.Minute && helperProtoCache.v >= helperProto {
		return helperProtoCache.v
	}
	if !helperProtoCache.at.IsZero() && time.Since(helperProtoCache.at) < 10*time.Second {
		return helperProtoCache.v
	}
	helperProtoCache.v, helperProtoCache.at = helperProtoInstalled(), time.Now()
	return helperProtoCache.v
}
