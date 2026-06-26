package main

import (
	"fmt"
	"os"
)

// Le « mode agent » est l'unique interrupteur qui donne à l'IA l'accès à ses
// outils. Un skill est un outil comme un autre : quand le mode agent est actif,
// l'IA dispose à la fois du shell (run_shell) et de la gestion des skills.
// Plus de drapeaux machine/skills séparés — un seul fichier .agent_enabled.

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
		fmt.Println(green("[ok]") + " mode agent activé — l'IA dispose du shell complet et de ses skills")
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
		fmt.Printf("  outils : run_shell (timeout %ds, max %ds) + skill (read/write/append/delete)\n", toolDefaultTimeout, toolMaxTimeout)
		sk := ListSkills()
		if len(sk) == 0 {
			fmt.Printf("  skills : aucun — crée %s/<nom>/SKILL.md\n", skillsDir())
			return nil
		}
		fmt.Printf("  skills (%s) :\n", skillsDir())
		for _, s := range sk {
			fmt.Printf("    %s  %s\n", bold(s.Name), s.Desc)
		}
	default:
		return fmt.Errorf("usage: jean agent [on|off|status]")
	}
	return nil
}
