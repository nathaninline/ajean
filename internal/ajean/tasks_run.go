package ajean

// tasks_run.go — exécution d'une tâche, ISOLÉE de la conversation partagée.
//
// Une tâche ne doit ni apparaître dans le chat, ni casser le replay SSE, ni
// entrer en collision avec un tour utilisateur. On réutilise donc UN SEUL point
// partagé avec la conversation : son verrou de génération (conv.Generating), qui
// garantit qu'une seule inférence tourne à la fois (llama-server est souvent en
// -np 1). Tout le reste — messages, journal d'affichage — n'est jamais touché :
// la tâche construit son propre fil éphémère et jette tout à la fin, ne gardant
// que le texte final comme compte-rendu.

import (
	"context"
	"errors"
	"strings"
	"time"
)

// RunAutonomous exécute un prompt en arrière-plan, sans laisser de trace dans la
// conversation partagée, et renvoie le texte final produit par l'IA (compte-rendu).
// Prend le MÊME gate que StartTurn : renvoie ErrBusy si un tour (utilisateur ou
// une autre tâche) est déjà en cours, ou errModelLoading si le modèle n'est pas
// prêt. caps gouverne l'accès aux outils (mode agent, mémoire, internet).
func (c *Conversation) RunAutonomous(ctx context.Context, taskID, taskName, prompt string, caps Caps, temperature float64) (string, error) {
	if !healthCheck() {
		return "", errModelLoading
	}
	// Contexte annulable propre à la tâche, exposé via c.cancel : ainsi le bouton
	// stop du chat (/api/chat/stop → conv.Stop) interrompt VRAIMENT une tâche de
	// fond, au lieu de l'afficher « en cours » sans pouvoir l'arrêter.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	if c.Generating {
		c.mu.Unlock()
		return "", ErrBusy
	}
	c.Generating = true
	c.cancel = cancel
	c.runningTaskID = taskID
	c.runningTaskName = taskName
	c.mu.Unlock()
	// Libère le gate quoi qu'il arrive. On ne touche NI c.Messages NI c.Log NI
	// c.epoch : la conversation partagée est totalement à l'écart.
	defer func() {
		c.mu.Lock()
		c.Generating = false
		c.cancel = nil
		c.runningTaskID = ""
		c.runningTaskName = ""
		c.mu.Unlock()
	}()

	if temperature == 0 {
		temperature = 0.7
	}
	msgs := []Message{{Role: "user", Content: prompt}}
	// Même préambule que la vraie génération : sysprompt perso + préambule agent
	// + briefing machine (via InjectSkills), pour que l'IA ait le même contexte et
	// les mêmes outils qu'en chat.
	final := msgs
	if sp := readSysPrompt(); sp != "" {
		final = append([]Message{{Role: "system", Content: sp}}, msgs...)
	}

	var content strings.Builder
	extra, err := runChat(ctx, InjectSkills(final, caps), temperature, caps, func(ev StreamEvent) bool {
		if ev.Content != "" {
			content.WriteString(ev.Content)
		}
		return true
	})
	_ = extra // les messages d'outils ne sont pas conservés : la tâche est éphémère
	return strings.TrimSpace(content.String()), err
}

// reportMax borne la taille du compte-rendu conservé (le texte final peut être
// long ; on n'en garde qu'un aperçu pour l'UI).
const reportMax = 4000

// runTask exécute une tâche et met à jour son état persisté (dernier run, prochain
// run, succès/échec, compte-rendu). Relit la tâche juste avant d'écrire pour ne
// pas écraser une modification concurrente (toggle/édition depuis l'UI).
func runTask(t Task) {
	report, err := conv.RunAutonomous(context.Background(), t.ID, t.Name, t.Prompt, globalCaps(), 0)

	// Occupé ou modèle pas encore prêt : ce n'est pas un échec de la tâche, juste
	// un mauvais moment. On ne touche pas à son état (NextRun reste dans le passé)
	// pour qu'elle soit réessayée au prochain tick.
	if err == ErrBusy || err == errModelLoading {
		return
	}

	now := time.Now()
	cur, ok := getTask(t.ID)
	if !ok {
		return // supprimée entre-temps : rien à écrire
	}
	cur.LastRun = now.UnixMilli()
	cur.NextRun = computeNextRun(cur.Schedule, now)
	if errors.Is(err, context.Canceled) {
		// Arrêt volontaire (bouton stop) : ce n'est pas un échec. On garde la trace
		// « interrompue » sans allumer l'indicateur rouge d'erreur.
		cur.LastOK = true
		cur.LastError = ""
		cur.LastReport = "(interrompue manuellement)"
	} else if err != nil {
		cur.LastOK = false
		cur.LastError = err.Error()
	} else {
		cur.LastOK = true
		cur.LastError = ""
		if len(report) > reportMax {
			report = report[:reportMax] + "…"
		}
		cur.LastReport = report
	}
	_ = saveTask(cur)
}
