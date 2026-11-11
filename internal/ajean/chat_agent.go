package ajean

import (
	"fmt"
)

// L'IA a toujours accès à ses outils (shell, fichiers, mémoire) : il n'y a
// plus de « mode agent » à activer, AJEAN a forcément accès au système.

func cmdAgent(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "status":
		fmt.Printf("%s  toujours actifs (AJEAN a accès au système)\n", bold("Outils de l'IA"))
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
		return fmt.Errorf("usage: ajean agent [status]")
	}
	return nil
}
