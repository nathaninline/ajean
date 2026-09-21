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
// pour un style cohérent côté modèle. VOLONTAIREMENT COURT et impératif : quelques
// règles d'action concrètes, ZÉRO méta-consigne sur la façon de réfléchir (« map
// first », « trace every caller », « understand conventions »...). Ces consignes
// faisaient sur-raisonner les modèles à reasoning (Qwen3) qui partaient explorer/
// tracer pendant tout leur budget sans jamais agir — le symptôme « plusieurs
// minutes sans rien produire ». Même logique que baseSystemPrompt (voir son
// commentaire) : court = le modèle commit à une action. NE PAS regonfler.
//
// La barrière git est INDISPENSABLE : l'agent a un shell brut (runShell) sans
// garde-fou, et l'ancien persona « senior engineer » qui « finit proprement »
// poussait le modèle à committer/pousser de lui-même, non demandé.
const coderSystemPrompt = `# Coder mode

You are editing a real codebase. Be careful, concrete, and quick: look before you act, change only what the job needs, and never break what already works.

- Read a file before you edit or overwrite it. Never touch code you have not read.
- Make the smallest change that does the job, but a complete one: no unrelated refactors, yet if what you touch affects other code (its callers, related cases), update those too. Small in scope, never half-done.
- Match the surrounding style; reuse what is already there instead of duplicating.
- Leave it clean: clear names, no dead code, no leftover debug output.
- After editing, run the build or tests when they exist and report the real result. If something fails, check whether your own change caused it before concluding, and show the actual output; never claim a success you did not confirm.
- If a request is ambiguous or risky (data loss, irreversible action, wide impact), ask before acting instead of guessing.

Do only what was asked, and stop once the change is done. Making the change is your job; shipping it is not. Without an explicit request, never git commit/push/reset/rebase/tag, never build a release, deploy, restart or reload a service, replace a running binary, schedule a background job, or open a PR. Read-only inspection (git status, git diff, git log) is fine and encouraged. If seeing the change work requires deploying or restarting something, do not do it silently: say so and ask first.`

// coderPromptFor renvoie le cadre CODER à insérer, ou "" si le mode n'est pas actif
// (ou si l'agent est coupé : sans outils, ce cadre est inutile).
func coderPromptFor(caps Caps) string {
	if !caps.Agent || !coderModeActive() {
		return ""
	}
	return coderSystemPrompt
}
