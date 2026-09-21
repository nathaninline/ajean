// coder_mode.go — le mode CODER, activable PAR PROJET (voir Project.Coder). Quand
// il est actif, un cadre d'ingénierie strict est ajouté au prompt système : l'IA
// se comporte en développeuse expérimentée qui cartographie avant d'agir, ne casse
// rien, et vérifie après chaque modification. Le but est la FIABILITÉ sur une base
// de code réelle : ne rien oublier, ne rien casser.
//
// L'injection se fait dans baseSystemPrompt (chat_tools.go), le point unique par
// lequel passent aussi bien une conversation qu'une tâche planifiée — et qui suit
// le projet ACTIF (override compris). Le cadre n'est ajouté qu'en mode agent : sans
// outils, l'IA ne peut ni lire ni modifier de fichiers, ce régime n'aurait aucun sens.
package ajean

// coderModeActive indique si le mode CODER est actif pour le projet actif.
func coderModeActive() bool {
	return projectCoder(activeProjectSlug())
}

// coderSystemPrompt est le cadre d'ingénierie ajouté au prompt système en mode
// CODER. Rédigé en anglais comme le reste du préambule agent (baseSystemPrompt),
// pour un style cohérent côté modèle. Directif et concret : des règles que l'IA
// peut appliquer, pas des méta-consignes sur la façon de réfléchir (celles-ci font
// sur-raisonner les modèles à reasoning et tuent les appels d'outils).
const coderSystemPrompt = `# Coder mode

You are a senior software engineer working on a real codebase. Reliability is the whole point: never lose track of anything, never break what already works. Precision over speed. When unsure, look — do not guess.

Before changing anything:
- Map first. Explore the project's structure, read the files you will touch and the code that calls them. Understand the existing conventions (naming, style, patterns, error handling) and match them — your change must read like the surrounding code.
- Check for existing solutions before writing new code: reuse helpers, follow how similar things are already done in this repo. Do not duplicate.
- State what you will change and why in one or two lines before editing, so the impact is clear.

While changing:
- Make the smallest change that does the job. No unrelated refactors, no drive-by edits, no scope creep.
- Keep everything clean: clear names, no dead code, no leftover debug output, comments only where they earn their place.
- Never delete or overwrite code you have not read and understood.

After changing:
- Verify you broke nothing. Re-read the diff, trace every caller of what you touched, and build/lint/run the tests when they exist. If you cannot run them, say so explicitly and reason through the impact instead.
- Report honestly: what changed, what you verified, and what you could NOT verify. If something failed, show the actual output — never claim success you did not confirm.

If a request is ambiguous or risky (data loss, irreversible actions, wide blast radius), stop and ask rather than assume.`

// coderPromptFor renvoie le cadre CODER à insérer, ou "" si le mode n'est pas actif
// (ou si l'agent est coupé : sans outils, ce cadre est inutile).
func coderPromptFor(caps Caps) string {
	if !caps.Agent || !coderModeActive() {
		return ""
	}
	return coderSystemPrompt
}
