package agent

// La révision : la boucle d'apprentissage. Pendant un échange, le modèle est
// occupé à répondre et pense rarement à noter ce qu'il apprend. On repasse donc
// APRÈS, quand l'utilisateur ne parle plus : un tour silencieux, à la suite de
// la conversation, dont la seule consigne est « qu'est-ce qui mérite d'être
// retenu ? ». Rien n'est affiché et le fil n'est pas modifié.
//
// Chaque révision est un appel de plus à l'API : elle ne part que s'il y a de
// quoi apprendre (une correction, un vrai travail, ou plusieurs échanges
// accumulés), et s'efface dès que l'utilisateur reprend la parole.

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"ajean/internal/config"
	"ajean/internal/llm"
	"ajean/internal/memory"
)

const (
	reviewDelay         = 45 * time.Second // silence attendu avant de réviser
	reviewDelayUrgent   = 10 * time.Second // après une correction : elle ne doit pas attendre
	reviewTimeout       = 3 * time.Minute
	reviewMaxIterations = 8
	reviewEvery         = 4 // échanges accumulés qui justifient une révision à eux seuls
	workCalls           = 5 // appels d'outils dans un tour : l'assistant a dû chercher son chemin
)

// reviewState : ce qui attend d'être révisé dans une conversation.
type reviewState struct {
	pending    int               // échanges pas encore révisés
	work       int               // appels d'outils du plus gros tour non révisé
	correction memory.Correction // pire correction reçue
	corrected  []string          // messages de correction de l'utilisateur, à citer
	timer      *time.Timer
	cancel     context.CancelFunc // révision en cours
}

// noteTurn enregistre un tour terminé et programme une révision s'il y a de
// quoi apprendre. saved : l'assistant a déjà écrit dans sa mémoire pendant le tour.
func (c *conversation) noteTurn(userText string, toolCalls int, saved bool) {
	if config.Get().ReviewOff {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r := &c.review
	r.pending++
	if !saved {
		r.work = max(r.work, toolCalls)
	}
	if k := memory.DetectCorrection(userText); k != memory.NoCorrection {
		r.correction = max(r.correction, k)
		r.corrected = append(r.corrected, userText)
	}
	delay := reviewDelay
	switch {
	case r.correction != memory.NoCorrection:
		delay = reviewDelayUrgent
	case r.work >= workCalls, r.pending >= reviewEvery:
	default:
		return
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	r.timer = time.AfterFunc(delay, c.runReview)
}

// cancelReview annule la révision programmée ou en cours : ce qu'elle devait
// regarder reste en attente et sera révisé après le prochain tour.
func (c *conversation) cancelReview() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.review.timer != nil {
		c.review.timer.Stop()
	}
	if c.review.cancel != nil {
		c.review.cancel()
	}
}

func (c *conversation) runReview() {
	ctx, cancel := context.WithTimeout(context.Background(), reviewTimeout)
	defer cancel()
	c.mu.Lock()
	if c.busy || c.review.pending == 0 {
		c.mu.Unlock()
		return
	}
	r := c.review
	c.review.cancel = cancel
	msgs, _ := c.contextLocked()
	msgs = append(msgs, llm.Message{Role: "user", Content: reviewPrompt(r)})
	c.mu.Unlock()

	writes, _, err := silent(ctx, msgs, reviewMaxIterations)

	c.mu.Lock()
	c.review.cancel = nil
	if err == nil {
		c.review = reviewState{}
	}
	c.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("révision non faite : %v", err)
		}
		return
	}
	if len(writes) > 0 {
		memory.Log("revision", "Révision : "+strings.Join(writes, " · "))
		emit(event{Type: "memory"})
	}
}

func reviewPrompt(r reviewState) string {
	var b strings.Builder
	if r.correction != memory.NoCorrection {
		b.WriteString("[PRIORITY] These messages from the user look like a correction of something you did. Read them first: if they are not about you (the user is talking about something else), ignore this block.\n")
		for _, m := range r.corrected {
			b.WriteString("> " + strings.Join(strings.Fields(m), " ") + "\n")
		}
		if r.correction == memory.CorrectedAgain {
			b.WriteString("If the user is saying you ALREADY made this mistake, the rule you had was not enough. Find it (your lessons, the related fiche, or memory_search) " +
				"and make it impossible to miss: one strict lesson with memory_lesson, in the user's own words, saying what to do and what never to do; " +
				"fix the fiche too with fiche_patch if one is involved; remove the weaker duplicate. " +
				"If the rule is mechanical (a banned word or character), add it with memory_rule: then it cannot be broken.\n\n")
		} else {
			b.WriteString("If they did correct you, make sure the rule is saved exactly as the user meant it (re-read their words, do not reinterpret), where you will see it next time: " +
				"in the fiche of that task (fiche_patch, or memory_lesson with fiche=<name>), else as a lesson. If you already saved it, check it matches their words. " +
				"If it is mechanical (a banned word or character), also add it with memory_rule.\n\n")
		}
	}
	if r.work >= workCalls {
		fmt.Fprintf(&b, "[PRIORITY] One of these requests took you %d tool calls: you had to find your way. If the user may ask it again, "+
			"save the DIRECT path as a fiche now (fiche_save, or fiche_patch the existing one): the exact steps, URLs, file names and values that worked, in order, "+
			"without the dead ends, plus a pitfalls section for what failed. Never store results that change (prices, counts): only how to get them. "+
			"If code did the job, say in the fiche which of your tools does it (or that one should be written). Next time you must answer in one or two calls.\n\n", r.work)
	}
	fmt.Fprintf(&b, `[MEMORY REVIEW. Silent: the user will not see this message nor your answer.]
Look back at the last %d exchange(s) of this conversation and save only what will still matter in a month:
1. A lasting fact about the user that is new or changed (person, date, preference, habit, project) and not already in your profile: memory_remember.
2. A lesson: the user corrected you, a method failed before the one that worked, or you hit a quirk worth knowing: memory_lesson (with fiche=<name> if it is about a fiche).
3. A procedure: steps you worked out, or how the user wants a recurring task done: fiche_save (steps and pitfalls), not a lesson. A fiche that turned out wrong or incomplete: fiche_patch.
4. A list the user keeps updating (todo, shopping list) that lives nowhere durable: put its CURRENT state in one fiche, to be updated in place from now on.
Write only what was actually said or shown, never a guess or a generalization (a sister visited on Saturday is not "visits her often"). When the user and a tool result disagree, note both as they are.
No password, key or token in the profile: those go in the service's fiche.
Do NOT save: small talk, one-off details, what is already in your profile, lessons or fiches (the journal already keeps every exchange). Most of the time there is nothing or one thing to save: that is fine.
Do not answer the user. When done, reply with just: OK`, r.pending)
	return b.String()
}
