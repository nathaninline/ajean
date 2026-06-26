package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"time"
)

const (
	toolDefaultTimeout = 30
	toolMaxTimeout     = 300
	toolMaxOutput      = 8000 // characters of stdout/stderr returned to the model
)

// baseSystemPrompt is the always-on system preamble. Structured like pi's
// proven prompt (identity → method → guidelines → concision → date): a concrete,
// procedural prompt gives the model rails so it stops deliberating forever
// ("Wait, let me check… Wait, I'll just run it…") and commits to an action.
// The per-tool "Outil disponible" sections live in machine/skills prompts so
// they only appear when the matching feature is on.
func baseSystemPrompt(caps Caps) string {
	// No tool access → no agentic preamble. A plain chat model told to "call
	// tools immediately" hallucinates textual tool calls (e.g. default_api:bash)
	// that leak into the answer. Let the user's own system prompt stand alone.
	if !caps.Agent {
		return ""
	}
	var b strings.Builder
	b.WriteString("Tu es un assistant expert opérant dans jean, un agent qui agit directement sur cette machine. Tu aides l'utilisateur en inspectant le système, en exécutant des commandes et en gérant des skills réutilisables.\n\n")
	b.WriteString("Méthode :\n")
	for _, l := range []string{
		"Raisonne brièvement (1 à 2 phrases) puis AGIS. Dès qu'une action est possible, appelle l'outil immédiatement — ne l'annonce pas, fais-le.",
		"Ne répète JAMAIS deux fois la même étape de réflexion ni le même appel d'outil. Si tu hésites (« je devrais peut-être vérifier… »), arrête de réfléchir et appelle l'outil maintenant.",
		"Pour toute question système (fichiers, process, réseau, OS), utilise tes outils au lieu de supposer — tu as un accès réel, ne demande pas la permission pour inspecter.",
		"Quand la tâche est faite, donne une réponse finale courte et arrête-toi. N'enchaîne pas d'outils inutiles.",
		"Sois concis dans tes réponses.",
		"Affiche clairement les chemins de fichiers quand tu travailles dessus.",
	} {
		b.WriteString("- " + l + "\n")
	}
	b.WriteString("\nDate du jour : " + time.Now().Format("2006-01-02"))
	return b.String()
}

// machineSystemPrompt returns a short briefing about the host the model is
// running on, so that when machine access is enabled it knows *which* machine
// run_shell acts upon (and doesn't claim it has no access to "your PC").
// Returns "" when machine access is off.
func machineSystemPrompt(caps Caps) string {
	if !caps.Agent {
		return ""
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "inconnu"
	}
	who := ""
	if u, err := user.Current(); err == nil {
		who = u.Username
	}
	cwd, _ := os.Getwd()

	var b strings.Builder
	b.WriteString("Outil disponible — run_shell(command, timeout) : exécute une commande shell sur CETTE machine et renvoie stdout, stderr et le code de sortie. C'est ton accès réel ; ne dis jamais que tu n'y as pas accès.\n")
	b.WriteString("Conseils run_shell :\n")
	for _, l := range []string{
		"Sers-t'en pour toute question système (OS, disque, process, fichiers, réseau, lire un script) plutôt que de supposer.",
		"Préfère des commandes ciblées et non-interactives ; chaîne-les avec && quand elles sont dépendantes.",
		"Évite les commandes destructrices (rm -rf, mkfs, dd…) sauf demande explicite de l'utilisateur.",
		"Si une commande échoue, lis stderr et le code de sortie, puis corrige — ne relance pas la même commande à l'identique.",
	} {
		b.WriteString("- " + l + "\n")
	}
	b.WriteString(fmt.Sprintf("Machine : hôte=%s, %s/%s", host, runtime.GOOS, runtime.GOARCH))
	if who != "" {
		b.WriteString(", user=" + who)
	}
	if cwd != "" {
		b.WriteString(", cwd=" + cwd)
	}
	b.WriteString(".")
	return b.String()
}

// runShell executes a command via the platform shell (bash -c on Unix, cmd /C
// on Windows — see newShellCmd in platform_*.go) with a clamped timeout,
// returning a single string formatted "exit: N\n\nstdout:\n...\n\nstderr:\n..."
// truncated to keep tool output bounded.
func runShell(command string, timeoutSec int) string {
	if timeoutSec <= 0 {
		timeoutSec = toolDefaultTimeout
	}
	if timeoutSec > toolMaxTimeout {
		timeoutSec = toolMaxTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := newShellCmd(ctx, command)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("[timeout après %ds]", timeoutSec)
	}
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return fmt.Sprintf("[erreur: %v]", err)
		}
	}
	out := tailRunes(stdout.String(), toolMaxOutput)
	errOut := tailRunes(stderr.String(), toolMaxOutput)
	parts := []string{fmt.Sprintf("exit: %d", exit)}
	if out != "" {
		parts = append(parts, "stdout:\n"+out)
	}
	if errOut != "" {
		parts = append(parts, "stderr:\n"+errOut)
	}
	return strings.Join(parts, "\n\n")
}

// tailRunes returns the last n runes of s (used to cap tool output).
func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
