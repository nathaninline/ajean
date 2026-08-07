package ajean

import "fmt"

// Le « mode agent » est l'unique interrupteur qui donne à l'IA l'accès à ses
// outils : le shell (run_shell), l'écriture de fichiers et sa mémoire
// (mem_search/mem_read/mem_add/mem_edit).

func agentEnabled() bool { return getBool(bkState, "agent") }

func setAgentEnabled(on bool) error { return putBool(bkState, "agent", on) }

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
