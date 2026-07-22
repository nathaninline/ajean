package jean

import (
	"fmt"
	"os"
)

// Le « mode agent » est l'unique interrupteur qui donne à l'IA l'accès à ses
// outils. Quand il est actif, l'IA dispose du shell (run_shell) et de sa
// mémoire (mem_search/mem_read/mem_add/mem_edit) — les anciens « skills » ont
// été fondus dans la mémoire. Plus de drapeaux machine/skills séparés : un seul
// fichier .agent_enabled.

func agentEnabled() bool {
	if _, err := os.Stat(agentFlag()); err == nil {
		return true
	}
	// Migration : si l'un des anciens drapeaux séparés traîne encore, on
	// considère le mode agent comme actif (et on le matérialise au prochain set).
	if _, err := os.Stat(legacyToolsFlag()); err == nil {
		return true
	}
	if _, err := os.Stat(legacySkillsFlag()); err == nil {
		return true
	}
	return false
}

func setAgentEnabled(on bool) error {
	_ = os.MkdirAll(JeanHome(), 0o755)
	// On nettoie systématiquement les anciens drapeaux pour ne pas garder un
	// état fantôme « à moitié activé » hérité de l'ancien modèle à deux toggles.
	_ = os.Remove(legacyToolsFlag())
	_ = os.Remove(legacySkillsFlag())
	if on {
		f, err := os.Create(agentFlag())
		if err != nil {
			return err
		}
		return f.Close()
	}
	if err := os.Remove(agentFlag()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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
		fmt.Println(green("[ok]") + " mode agent activé — l'IA dispose du shell complet (bash) et de sa mémoire")
	case "off":
		if err := setAgentEnabled(false); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " mode agent désactivé")
	case "", "status", "list":
		state := dim("off")
		if agentEnabled() {
			state = green("on")
		}
		fmt.Printf("%s  état: %s\n", cyan("Mode agent"), state)
		fmt.Printf("  outils : bash (timeout %ds, max %ds) + mémoire (mem_search/mem_read/mem_add/mem_edit)\n", toolDefaultTimeout, toolMaxTimeout)
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
		return fmt.Errorf("usage: jean agent [on|off|status]")
	}
	return nil
}
