package agent

// Le sommeil : la consolidation de la mémoire. La révision ajoute au fil de
// l'eau ; sans ménage, au bout de quelques mois la mémoire devient un tas (un
// même fait à deux endroits, une leçon contredite par une plus récente, des
// fiches qui se recouvrent). La nuit, quand personne ne parle, le modèle relit
// TOUTE sa mémoire à froid et la remet d'aplomb.
//
// Deux filets : une copie de la mémoire est prise avant chaque sommeil (on peut
// y revenir depuis l'interface), et un garde-fou déterministe remet en place
// tout ce que le ménage aurait perdu sans le vouloir (paquet memory).

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"ajean/internal/config"
	"ajean/internal/llm"
	"ajean/internal/memory"
)

const (
	sleepHour          = 3 // début de la fenêtre de sommeil, heure de l'utilisateur
	sleepWindowHours   = 4
	sleepCheckEvery    = 10 * time.Minute
	sleepMinInterval   = 12 * time.Hour // jamais deux sommeils automatiques rapprochés
	sleepCatchUpAfter  = 36 * time.Hour // au-delà, un sommeil de jour rattrape la nuit manquée
	sleepCatchUpIdle   = 30 * time.Minute
	sleepTimeout       = 20 * time.Minute
	sleepMaxIterations = 60
	sleepDumpChars     = 150_000 // part de la mémoire donnée en entier au modèle
)

var (
	ErrSleeping = errors.New("un sommeil est déjà en cours")
	sleeping    atomic.Bool
)

var sleepPrompt = `[MEMORY CONSOLIDATION. Silent maintenance while the user is away: nobody reads your answer.]
Below is your whole long-term memory. Tidy it so that future you finds the right thing at once and is never misled:
1. Duplicates: the same information in two places: keep it only where it belongs (facts about the user: profile; a rule learned from a mistake: lessons; how to do a task: that task's fiche) and remove the other copy (memory_forget, or fiche_patch). Never move a lesson into the profile.
2. Contradictions: the most recent wins. Replace the outdated value; never leave the new one next to the old.
3. A lesson that only concerns one fiche: memory_lesson with fiche=<name>, then memory_forget lecon-N. A lesson that is only a fact about the user: memory_remember, then memory_forget that lesson.
4. Profile lines that are guessed, vague or outdated: fix (memory_remember with the same key) or memory_forget.
5. Fiches: make unclear or patched-up steps clean and precise with fiche_patch. Keep every step and pitfall that is still true.
6. Overlapping fiches (start with the section "` + memory.RelatedFichesHeading + `" when it is there): the newer one is the reference. Pour the other into it with fiche_merge, then tighten the result with fiche_patch. A fiche must never send you to an outdated method.
7. A fiche section "## Restauré (à réintégrer)" holds lines a past tidy-up lost by mistake: put each back where it belongs in that fiche, then remove the section.
8. Dead weight: a fiche never opened for 60+ days about something that is over or replaced: memory_forget it (archived, not lost: reading it brings it back). Keep fiches for things that come back from time to time.
Rules: never invent anything; never delete something true and useful; prefer few precise edits. Take a line out only with memory_forget or fiche_patch: a line that disappears any other way is put back by the memory guard. If everything is already clean, change nothing.
When done, reply with one short line saying what you changed (or just OK).`

// RunSleepSchedule tourne jusqu'à la fin du contexte : la nuit, si la mémoire a
// changé depuis le dernier sommeil et que personne ne parle, il la consolide.
func RunSleepSchedule(ctx context.Context) {
	tick := time.NewTicker(sleepCheckEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			if sleepDue(now) {
				_ = Sleep(ctx)
			}
		}
	}
}

func sleepDue(now time.Time) bool {
	c := config.Get()
	if c.SleepOff || !c.Chat().Ready() || busyAnywhere() {
		return false
	}
	last := memory.LastSleep()
	if !memory.LastChange().After(last) {
		return false // rien de nouveau à ranger
	}
	if h := now.In(config.Location()).Hour(); (h-sleepHour+24)%24 < sleepWindowHours {
		return now.Sub(last) >= sleepMinInterval
	}
	// Hors de la nuit : rattrapage pour une machine éteinte la nuit, quand le
	// dernier sommeil date et que l'utilisateur s'est éloigné.
	idle := now.Sub(time.UnixMilli(lastActivity.Load()))
	// Tant qu'il n'a jamais dormi, seule la nuit compte : pas de ménage le jour même
	// où la mémoire commence à se remplir.
	return !last.IsZero() && now.Sub(last) >= sleepCatchUpAfter && idle >= sleepCatchUpIdle
}

// CanSleep : un sommeil peut-il partir maintenant ? (nil si oui)
func CanSleep() error {
	switch {
	case !config.Get().Chat().Ready():
		return llm.ErrNotConfigured
	case busyAnywhere():
		return ErrBusy
	case sleeping.Load():
		return ErrSleeping
	}
	return nil
}

// Sleep consolide la mémoire maintenant. Bloque jusqu'à la fin du sommeil.
func Sleep(ctx context.Context) error {
	if err := CanSleep(); err != nil {
		return err
	}
	if !sleeping.CompareAndSwap(false, true) {
		return ErrSleeping
	}
	defer sleeping.Store(false)

	s, err := memory.BeginSleep()
	if err != nil {
		return err
	}
	emit(event{Type: "sleep", State: "start"})
	defer emit(event{Type: "sleep", State: "end"})

	ctx, cancel := context.WithTimeout(ctx, sleepTimeout)
	defer cancel()
	msgs := []llm.Message{{Role: "user", Content: sleepPrompt + "\n\n" + memory.Dump(sleepDumpChars)}}
	writes, last, err := silent(ctx, msgs, sleepMaxIterations)

	status, summary := "ok", strings.TrimSpace(last)
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		status, summary = "interrompu", "Sommeil interrompu avant la fin."
	case err != nil:
		status, summary = "erreur", err.Error()
	case len(writes) == 0:
		summary = "Rien à ranger."
	}
	restored := s.Finish(status, summary)
	if len(writes) > 0 || len(restored) > 0 {
		memory.Log("sommeil", "Sommeil : "+summary)
		emit(event{Type: "memory"})
	}
	return err
}
