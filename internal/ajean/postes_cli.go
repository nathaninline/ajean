// postes_cli.go — commande `ajean postes` : la face CLI de la gestion des postes
// distants, pour que l'IA (via bash) puisse préparer l'ajout d'une machine sans
// passer par les endpoints HTTP authentifiés de l'UI.
//
// Une seule sous-commande utile ici : `pair`, qui génère un code d'appairage et
// imprime la commande `ajean remote install ...` prête à coller sur la machine
// cible. Lister les postes et basculer la cible se font via les outils LLM
// machines_list / machines_use (voir node_server.go).
//
// Tourne en process séparé du serveur : la base bbolt est ouverte/refermée par
// opération (voir store.go), donc `ajean postes pair` cohabite avec le service.
package ajean

import (
	"fmt"
	"strings"
	"time"
)

func cmdPostes(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "pair":
		return postesPair(args)
	case "", "help", "-h", "--help":
		postesUsage()
		return nil
	default:
		postesUsage()
		return fmt.Errorf("sous-commande inconnue: %s", sub)
	}
}

func postesUsage() {
	fmt.Print(`ajean postes — préparer l'ajout d'une machine (poste distant)

  ajean postes pair [--allow shell,read,write,list] [--root DIR]
        génère un code d'appairage (10 min) et imprime la commande
        « ajean remote install ... » à lancer sur la machine cible.

Pour LISTER les postes ou BASCULER la cible d'exécution, l'IA utilise ses
outils machines_list / machines_use (mode agent + gestion des machines).
`)
}

func postesPair(args []string) error {
	f := parseRemoteFlags(args)
	caps := nodeSanitizeCaps(f.allow)
	if len(caps) == 0 {
		// Défaut prudent, identique à handleNodePair : lecture seule.
		caps = []string{nodeCapRead, nodeCapList}
	}
	p := nodePairPending{
		Code:    strings.ToUpper(nodeRandHex(4)),
		Caps:    caps,
		Root:    strings.TrimSpace(f.root),
		Expires: time.Now().Add(nodePairCodeTTL).Unix(),
	}
	if err := savePairPending(p); err != nil {
		return err
	}
	key := e2ePubHex()
	mid := machineID()
	fmt.Printf("code d'appairage : %s   (valable %d min, usage unique)\n", p.Code, int(nodePairCodeTTL.Minutes()))
	fmt.Printf("capacités        : %s\n", strings.Join(caps, ", "))
	if p.Root != "" {
		fmt.Printf("dossier          : %s\n", p.Root)
	}
	fmt.Printf("clé de l'agent   : %s\n", key)
	fmt.Printf("machine (id)     : %s\n", mid)
	fmt.Println()
	fmt.Println("Sur la MACHINE CIBLE (accessible par ssh), lance :")
	fmt.Printf("  ajean remote install <url-serveur> --code %s --key %s --machine %s --yes\n", p.Code, key, mid)
	fmt.Println()
	fmt.Println("<url-serveur> = l'adresse de CE serveur AJEAN (voir `ajean network`),")
	fmt.Println("ou https://ajean.link si le poste passe par le relais.")
	return nil
}
