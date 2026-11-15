package tasks

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"slices"
	"strings"
	"time"

	"ajean/internal/scripts"
)

const (
	pollEvery  = 15 * time.Second
	retryAfter = 5 * time.Minute // avant la seconde chance d'une tâche ponctuelle en échec
	alertAfter = 3               // une tâche silencieuse récurrente ne signale que sa 3ᵉ erreur d'affilée
)

// maxRun : durée maximale d'une exécution. Une variable, pour que les tests
// n'attendent pas trente minutes.
var maxRun = 30 * time.Minute

// Hooks : ce que le reste du programme branche sur les tâches.
type Hooks struct {
	Run     func(ctx context.Context, t Task) (report string, err error) // exécute une tâche du modèle
	Deliver func(t Task, report string)                                  // remet le compte rendu à l'utilisateur
	Changed func()                                                       // la liste ou un état a changé (l'interface se rafraîchit)
}

// execution : la tâche en train de s'exécuter.
type execution struct {
	id      string
	cancel  context.CancelFunc
	stopped bool // arrêt demandé (Stop, ou suppression de la tâche)
}

// Start fait tourner l'échéancier jusqu'à la fin du contexte : à chaque tour,
// les tâches échues partent une par une, dans l'ordre de leur échéance.
func Start(ctx context.Context, h Hooks) {
	hooks.Store(&h)
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for ctx.Err() == nil {
		for ctx.Err() == nil && runDue(ctx, time.Now()) {
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		case <-wake:
		}
	}
}

// RunNow lance la tâche dès que possible (après celle qui est en cours), sans
// toucher à son calendrier.
func RunNow(id string) error {
	mu.Lock()
	defer mu.Unlock()
	f, err := load()
	switch {
	case err != nil:
		return err
	case f.find(id) == nil:
		return errNotFound
	case f.Paused:
		return errors.New("les tâches sont en pause : enlève la pause avant d'en lancer une")
	case current != nil && current.id == id:
		return errors.New("cette tâche est déjà en cours d'exécution")
	case slices.Contains(forced, id):
		return errors.New("cette tâche attend déjà son tour")
	}
	forced = append(forced, id)
	select {
	case wake <- struct{}{}:
	default:
	}
	return nil
}

// Stop arrête l'exécution en cours de la tâche, ou annule son lancement demandé
// par RunNow s'il n'a pas commencé.
func Stop(id string) {
	mu.Lock()
	defer mu.Unlock()
	halt(id)
}

// halt fait le travail de Stop, verrou tenu.
func halt(id string) {
	forced = slices.DeleteFunc(forced, func(queued string) bool { return queued == id })
	if current != nil && current.id == id {
		current.stopped = true
		current.cancel()
	}
}

// runDue exécute, s'il y en a une, la prochaine tâche à lancer à l'instant now,
// et dit s'il en a lancé une. Le verrou n'est tenu ni pendant l'exécution ni
// pendant la livraison : elles sont longues, et une tâche peut en créer,
// modifier ou supprimer d'autres.
func runDue(ctx context.Context, now time.Time) bool {
	mu.Lock()
	t, due, err := claim(now)
	if err != nil || t == nil {
		mu.Unlock()
		if err != nil {
			log.Printf("tâches : %v", err)
		}
		return false
	}
	runCtx, cancel := context.WithTimeout(ctx, maxRun)
	defer cancel()
	run := &execution{id: t.ID, cancel: cancel}
	current = run
	mu.Unlock()
	changed()

	started := time.Now()
	report, err := execute(runCtx, *t)
	elapsed := time.Since(started)

	mu.Lock()
	current = nil
	// Le programme qui s'arrête interrompt la tâche sans que personne l'ait voulu.
	interrupted := ctx.Err() != nil && !run.stopped
	status, text := outcome(*t, report, err, run.stopped || interrupted, errors.Is(runCtx.Err(), context.DeadlineExceeded))
	done := Run{At: now, Status: status, Report: clip(text), Seconds: math.Round(elapsed.Seconds()*10) / 10}
	var message string
	f, err := load()
	if err == nil {
		if stored := f.find(t.ID); stored != nil {
			message = stored.conclude(done, text, due, now.Add(elapsed), interrupted)
			*t = *stored
			err = store(f)
		}
	}
	mu.Unlock()
	if err != nil {
		log.Printf("tâches : résultat de « %s » non enregistré : %v", t.Name, err)
	}
	changed()

	if message != "" {
		if err := deliver(*t, message); err != nil {
			amend(t.ID, now, err.Error())
			changed()
		}
	}
	return true
}

// claim choisit la tâche à lancer (verrou tenu) : d'abord celles demandées par
// RunNow, puis la plus anciennement échue. due est l'échéance servie, zéro pour
// un lancement à la demande. Une tâche récurrente est reprogrammée ici, avant
// de s'exécuter et à partir de son échéance : une exécution longue ou
// interrompue ne décale pas son rythme.
func claim(now time.Time) (t *Task, due time.Time, err error) {
	f, err := load()
	if err != nil || f.Paused {
		return nil, time.Time{}, err
	}
	for len(forced) > 0 {
		id := forced[0]
		forced = forced[1:]
		if t = f.find(id); t != nil {
			return t, time.Time{}, nil
		}
	}
	for i := range f.Tasks {
		c := &f.Tasks[i]
		if c.Enabled && !c.Next.IsZero() && !c.Next.After(now) && (t == nil || c.Next.Before(t.Next)) {
			t = c
		}
	}
	if t == nil {
		return nil, time.Time{}, nil
	}
	due = t.Next
	if t.Schedule.Every > 0 {
		t.Next = t.Schedule.next(due, now)
		if err := store(f); err != nil {
			return nil, time.Time{}, err
		}
	}
	return t, due, nil
}

// execute fait le travail de la tâche. Une panique (d'un crochet, d'un outil)
// devient l'erreur de cette exécution, pas la mort du programme.
func execute(ctx context.Context, t Task) (report string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panique pendant l'exécution : %v", r)
		}
	}()
	if t.Tool != "" {
		return scripts.Run(t.Tool, t.Args)
	}
	h := hooks.Load()
	if h == nil || h.Run == nil {
		return "", errors.New("le modèle n'est pas branché sur les tâches")
	}
	return h.Run(ctx, t)
}

// outcome : le statut d'une exécution et le texte à en garder.
func outcome(t Task, report string, err error, stopped, timedOut bool) (status, text string) {
	report = strings.TrimSpace(report)
	switch {
	case stopped:
		return statusStopped, "exécution arrêtée avant la fin"
	case err != nil && timedOut:
		return statusError, fmt.Sprintf("durée maximale dépassée (%s)", maxRun)
	case err != nil:
		return statusError, err.Error()
	case t.Quiet && (report == "" || report == Nothing):
		return statusNothing, ""
	case report == "":
		// Un rappel qui ne dit rien est un rappel perdu : l'utilisateur doit le savoir.
		return statusError, "la tâche n'a rien renvoyé"
	}
	return statusOK, report
}

// clip borne un compte rendu gardé dans tasks.json.
func clip(s string) string {
	if r := []rune(s); len(r) > maxReport {
		return string(r[:maxReport]) + fmt.Sprintf("\n[… tronqué : %d caractères sur %d]", maxReport, len(r))
	}
	return s
}

// conclude range une exécution dans la tâche et rend ce qu'il faut dire à
// l'utilisateur ("" : rien). text est le compte rendu entier, due l'échéance
// servie (zéro : lancement à la demande, qui ne touche pas au calendrier), end
// l'instant de fin.
func (t *Task) conclude(run Run, text string, due, end time.Time, interrupted bool) string {
	t.Runs = append([]Run{run}, t.Runs...)
	t.Runs = t.Runs[:min(len(t.Runs), maxRuns)]

	// Une tâche ponctuelle a fait son temps, sauf si son calendrier a changé
	// pendant qu'elle tournait, ou si l'arrêt du programme l'a coupée (elle
	// repartira au prochain démarrage).
	retrying := false
	if !due.IsZero() && t.Schedule.Every == 0 && t.Next.Equal(due) && !interrupted {
		if run.Status == statusError && !t.Retried {
			t.Retried, t.Next, retrying = true, end.Add(retryAfter), true
		} else {
			t.Enabled, t.Next, t.Retried = false, time.Time{}, false
		}
	}

	switch run.Status {
	case statusOK:
		return text
	case statusError:
		switch {
		case t.Quiet && t.Schedule.Every > 0:
			// Une veille qui échoue une fois n'a pas à déranger.
			if t.failures() == alertAfter {
				return fmt.Sprintf("La tâche « %s » a échoué %d fois de suite : %s", t.Name, alertAfter, text)
			}
			return ""
		case retrying:
			return fmt.Sprintf("La tâche « %s » a échoué : %s\nNouvelle tentative dans %d minutes.", t.Name, text, int(retryAfter.Minutes()))
		}
		return fmt.Sprintf("La tâche « %s » a échoué : %s", t.Name, text)
	}
	return ""
}

// failures : le nombre d'erreurs d'affilée, la dernière exécution comprise.
func (t *Task) failures() int {
	n := 0
	for n < len(t.Runs) && t.Runs[n].Status == statusError {
		n++
	}
	return n
}

// deliver remet un compte rendu à l'utilisateur. Une panique du crochet est
// rendue comme une erreur.
func deliver(t Task, report string) (err error) {
	h := hooks.Load()
	if h == nil || h.Deliver == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("le compte rendu n'a pas pu être remis à l'utilisateur : %v", r)
		}
	}()
	h.Deliver(t, report)
	return nil
}

// amend transforme en erreur l'exécution du moment at : son compte rendu n'est
// jamais arrivé à l'utilisateur, l'interface doit le montrer.
func amend(id string, at time.Time, reason string) {
	err := update(func(f *file) error {
		t := f.find(id)
		if t == nil || len(t.Runs) == 0 || !t.Runs[0].At.Equal(at) {
			return errNotFound
		}
		t.Runs[0].Status, t.Runs[0].Report = statusError, reason
		return nil
	})
	if err != nil && !errors.Is(err, errNotFound) {
		log.Printf("tâches : %v", err)
	}
}

// changed prévient l'interface qu'il y a du nouveau.
func changed() {
	h := hooks.Load()
	if h == nil || h.Changed == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("tâches : panique du crochet Changed : %v", r)
		}
	}()
	h.Changed()
}
