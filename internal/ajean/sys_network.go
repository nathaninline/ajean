package ajean

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// sys_network.go — « est-ce que ma machine est joignable depuis le réseau ? »
//
// Le moteur écoute à l'adresse HOST. Sous Windows, l'installation pose
// HOST=127.0.0.1 : le chat du navigateur marche (il passe par le serveur web
// d'AJEAN, sur la même machine), mais l'endpoint OpenAI :8080/v1 est INVISIBLE
// depuis le reste du réseau. Or c'est très exactement ce que les gens viennent
// chercher : brancher un logiciel tiers sur le modèle qui tourne dans le salon.
//
// Deux choses manquaient, et pas une seule :
//  1. HOST n'était réglable NULLE PART dans l'interface — il fallait connaître
//     « ajean edit » et savoir quoi y écrire.
//  2. Même à 0.0.0.0, le pare-feu Windows bloque les connexions entrantes tant
//     qu'aucune règle n'autorise le port. AJEAN n'en posait aucune.
//
// D'où un seul interrupteur qui fait les deux, et qui DIT ce qu'il n'a pas pu
// faire (poser une règle de pare-feu exige les droits administrateur, que
// l'installation d'AJEAN ne réclame pas).

// firewallInert coupe tout pilotage du pare-feu. Positionné par les tests : une
// suite qui laisse une règle entrante derrière elle est un effet de bord
// inacceptable, et sur un runner élevé elle ouvrirait le port pour de bon.
// Déclaré ici, et non dans sys_firewall_windows.go, pour être LU sur les trois
// plateformes (sinon il n'est qu'écrit par les tests hors Windows, ce que
// staticcheck signale à juste titre comme une variable morte).
var firewallInert bool

// hostLocalOnly est l'adresse d'écoute « cette machine seulement ».
const hostLocalOnly = "127.0.0.1"

// hostAllInterfaces est l'adresse d'écoute « tout le réseau ».
const hostAllInterfaces = "0.0.0.0"

// engineHost renvoie l'adresse d'écoute configurée pour le moteur. Vide = le
// défaut de backend_serve.go, c'est-à-dire toutes les interfaces.
func engineHost() string {
	h := strings.TrimSpace(ReadConfig()["HOST"])
	if h == "" {
		return hostAllInterfaces
	}
	return h
}

// lanExposed : le moteur écoute-t-il ailleurs que sur la boucle locale ?
func lanExposed() bool {
	h := engineHost()
	return h != hostLocalOnly && h != "::1" && !strings.EqualFold(h, "localhost")
}

// netStatus est ce que l'interface affiche, et ce que renvoie `ajean network`.
type netStatus struct {
	Exposed  bool   `json:"exposed"`  // le moteur écoute-t-il sur le réseau
	Host     string `json:"host"`     // adresse d'écoute effective
	Port     int    `json:"port"`     // port du moteur
	URL      string `json:"url"`      // endpoint OpenAI à coller dans un logiciel tiers
	Firewall string `json:"firewall"` // "ouvert", "ferme", "inconnu" (aucun pare-feu piloté)
	Hint     string `json:"hint"`     // ce qu'il reste à faire à la main, ou ""
}

// networkStatus assemble l'état courant.
func networkStatus() netStatus {
	st := netStatus{Exposed: lanExposed(), Host: engineHost(), Port: LLMPort()}
	st.URL = fmt.Sprintf("http://%s:%d/v1", localIP(), st.Port)
	st.Firewall = firewallState(st.Port)
	if st.Exposed && st.Firewall == "ferme" {
		st.Hint = firewallManualHint(st.Port)
	}
	return st
}

// setLANExposure écrit HOST et, à l'ouverture, tente de poser la règle de
// pare-feu. Elle renvoie l'état obtenu ; une règle non posée n'est PAS une
// erreur bloquante (l'utilisateur peut la poser lui-même, Hint le lui dit).
//
// Le service n'est pas redémarré ici : llama-server ne lit --host qu'au
// lancement, et c'est à l'appelant de choisir le moment (l'interface propose le
// redémarrage, la CLI le rappelle).
func setLANExposure(on bool) (netStatus, error) {
	host := hostLocalOnly
	if on {
		host = hostAllInterfaces
	}
	if err := SetConfigKey("HOST", host); err != nil {
		return netStatus{}, err
	}
	port := LLMPort()
	switch {
	case firewallInert:
		// rien : voir la note sur firewallInert
	case on:
		_ = firewallOpen(port)
	default:
		_ = firewallClose(port)
	}
	return networkStatus(), nil
}

// cmdNetwork : `ajean network [on|off|status]`.
func cmdNetwork(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "on", "off":
		st, err := setLANExposure(sub == "on")
		if err != nil {
			return err
		}
		if st.Exposed {
			fmt.Printf("%s le moteur écoutera sur tout le réseau (%s)\n", green("[ok]"), st.URL)
		} else {
			fmt.Printf("%s le moteur n'écoutera plus que sur cette machine\n", green("[ok]"))
		}
		printNetHint(st)
		fmt.Printf("%s redémarre le moteur pour appliquer : %s\n", dim("[info]"), bold("ajean restart"))
		return nil
	case "status", "":
		st := networkStatus()
		state := dim("cette machine seulement")
		if st.Exposed {
			state = green("tout le réseau")
		}
		fmt.Printf("%s  écoute: %s (HOST=%s, PORT=%d)\n", cyan("Réseau"), state, st.Host, st.Port)
		switch st.Firewall {
		case "ouvert":
			fmt.Printf("  pare-feu : %s\n", green("port autorisé"))
		case "ferme":
			fmt.Printf("  pare-feu : %s\n", yellow("aucune règle pour ce port"))
		default:
			fmt.Printf("  pare-feu : %s\n", dim("non géré par AJEAN sur cette plateforme"))
		}
		if st.Exposed {
			fmt.Printf("  endpoint OpenAI : %s\n", bold(st.URL))
		}
		printNetHint(st)
		return nil
	}
	return fmt.Errorf("usage: ajean network [on|off|status]")
}

func printNetHint(st netStatus) {
	if st.Hint != "" {
		fmt.Printf("%s %s\n", yellow("[attention]"), st.Hint)
	}
}

// handleNetwork : GET l'état, POST {exposed:bool} pour basculer.
func handleNetwork(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Exposed *bool `json:"exposed"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if req.Exposed != nil {
			st, err := setLANExposure(*req.Exposed)
			if err != nil {
				sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			sendJSON(w, 200, map[string]any{"ok": true, "status": st})
			return
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "status": networkStatus()})
}
