//go:build windows

package ajean

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// sys_firewall_windows.go — règles de pare-feu entrantes pour le moteur.
//
// Sur une installation Windows fraîche, même avec HOST=0.0.0.0, le pare-feu
// bloque tout ce qui arrive du réseau : l'utilisateur voyait « ça écoute
// partout » et se prenait quand même un délai d'attente depuis son autre
// machine. AJEAN pose donc la règle lui-même, quand il en a le droit.
//
// `ajean install` ne réclame PAS les droits administrateur (voir
// sys_install_windows.go) : netsh échouera donc souvent. On ne fait pas semblant
// que ça a marché — firewallState relit la règle, et firewallManualHint donne la
// commande exacte à coller dans un terminal administrateur.

// firewallRuleName identifie nos règles. Une par port : changer PORT dans la
// configuration ne doit pas laisser une règle ouverte sur l'ancien.
func firewallRuleName(port int) string {
	return "AJEAN moteur (port " + strconv.Itoa(port) + ")"
}

// netsh exécute netsh sans faire clignoter de console (hideCmd) et renvoie sa
// sortie combinée.
//
// firewallInert coupe tout appel à netsh. Positionné par les tests : une suite
// qui laisse une règle entrante derrière elle est un effet de bord inacceptable,
// et sur un runner élevé elle ouvrirait le port pour de bon.
var firewallInert bool

func netsh(args ...string) (string, error) {
	if firewallInert {
		return "", fmt.Errorf("pare-feu non piloté")
	}
	out, err := hideCmd(exec.Command("netsh", args...)).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// firewallOpen autorise le port en entrée (TCP), pour les profils privé et
// domaine seulement : ouvrir un modèle non authentifié sur un réseau public
// (café, hôtel) n'est pas un défaut qu'on pose au nom de l'utilisateur.
func firewallOpen(port int) error {
	_ = firewallClose(port) // idempotence : pas d'empilement de règles homonymes
	out, err := netsh("advfirewall", "firewall", "add", "rule",
		"name="+firewallRuleName(port), "dir=in", "action=allow",
		"protocol=TCP", "localport="+strconv.Itoa(port), "profile=private,domain")
	if err != nil {
		return fmt.Errorf("règle de pare-feu refusée (droits administrateur requis) : %s", out)
	}
	return nil
}

// firewallClose retire la règle. Absente = rien à faire, et surtout pas une
// erreur.
func firewallClose(port int) error {
	_, _ = netsh("advfirewall", "firewall", "delete", "rule", "name="+firewallRuleName(port))
	return nil
}

// firewallState relit l'état RÉEL de la règle plutôt que de croire au succès
// supposé d'une commande passée.
func firewallState(port int) string {
	out, err := netsh("advfirewall", "firewall", "show", "rule", "name="+firewallRuleName(port))
	if err != nil || strings.TrimSpace(out) == "" {
		return "ferme"
	}
	// netsh répond « Aucune règle ne correspond aux critères » (localisé) avec un
	// code de sortie non nul : le err ci-dessus suffit. Ici la règle existe.
	return "ouvert"
}

// firewallManualHint : la commande à coller dans un terminal ADMINISTRATEUR
// quand AJEAN n'a pas pu poser la règle lui-même.
func firewallManualHint(port int) string {
	return fmt.Sprintf("le pare-feu Windows bloque encore le port %d. Ouvre un terminal ADMINISTRATEUR et lance :\n"+
		`  netsh advfirewall firewall add rule name="%s" dir=in action=allow protocol=TCP localport=%d profile=private,domain`,
		port, firewallRuleName(port), port)
}
