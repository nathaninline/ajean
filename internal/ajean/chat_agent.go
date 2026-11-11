package ajean

import (
	"fmt"
)

// Le « mode agent » est l'unique interrupteur qui donne à l'IA l'accès à ses
// outils : le shell (run_shell), l'écriture de fichiers et sa mémoire
// (mem_search/mem_read/mem_add/mem_edit).

// Il est ACTIF D'OFFICE : tous les modes d'AJEAN (Projet, Jean) reposent sur ces
// outils, et sans eux le modèle ne peut rien faire d'utile ici. Seul un arrêt
// explicite (« ajean agent off ») le coupe ; il est noté "0", l'absence de
// réglage valant « actif ».
func agentEnabled() bool { return getStr(bkState, "agent") != "0" }

func setAgentEnabled(on bool) error {
	if on {
		return putStr(bkState, "agent", "1")
	}
	return putStr(bkState, "agent", "0")
}

func cmdAgent(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "on":
		if err := setAgentEnabled(true); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " mode agent activé — l'IA dispose du shell complet (" + shellName() + "), de l'écriture de fichiers et de sa mémoire")
	case "off":
		if err := setAgentEnabled(false); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " mode agent désactivé")
	case "", "status":
		state := dim("off")
		if agentEnabled() {
			state = green("on")
		}
		fmt.Printf("%s  état: %s\n", cyan("Mode agent"), state)
		fmt.Printf("  outils : %s (timeout %ds, max %ds) + write/edit + mémoire (mem_search/mem_read/mem_add/mem_edit)\n", shellName(), toolDefaultTimeout, toolMaxTimeout)
		mem := MemList()
		if len(mem) == 0 {
			fmt.Printf("  mémoire : aucune page — crée %s/<nom>.md\n", memoryDir())
			return nil
		}
		fmt.Printf("  mémoire (%s) :\n", memoryDir())
		for _, p := range mem {
			fmt.Printf("    %s  %s\n", bold(p.Name), p.Title)
		}
	default:
		return fmt.Errorf("usage: ajean agent [on|off|status]")
	}
	return nil
}
