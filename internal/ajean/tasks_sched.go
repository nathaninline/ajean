package ajean

// tasks_sched.go — la boucle de planification. Une seule goroutine, un tick par
// minute, dans le process qui possède la conversation et l'UI (ajean-ui /
// `ajean link`). Voir StartTaskScheduler, câblée dans newWebMux (web_server.go).

import (
	"sync"
	"time"
)

var taskSchedOnce sync.Once

// StartTaskScheduler démarre la boucle de planification (idempotent : sûr à
// appeler à chaque construction du mux, ne lance qu'une goroutine).
func StartTaskScheduler() {
	taskSchedOnce.Do(func() { go taskSchedLoop() })
}

// taskSchedLoop réveille les tâches dues, une par tick (elles sont de toute façon
// sérialisées par le gate de génération). Le tick d'une minute suffit : c'est la
// résolution de la planification (voir tasks_cron.go).
func taskSchedLoop() {
	// Premier passage immédiat pour amorcer les NextRun manquants (après un
	// redémarrage du process, ou pour une tâche fraîchement créée sans NextRun).
	primeNextRuns()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		tickTasks(time.Now())
	}
}

// primeNextRuns renseigne NextRun de toute tâche activée qui n'en a pas (0),
// relativement à maintenant — sinon une tâche à NextRun=0 serait considérée « due »
// en permanence, ou jamais planifiée selon le sens de la comparaison.
func primeNextRuns() {
	now := time.Now()
	for _, t := range listTasks() {
		if t.Enabled && t.NextRun == 0 {
			t.NextRun = computeNextRun(t.Schedule, t.TZ, now)
			_ = saveTask(t)
		}
	}
}

// tickTasks lance la première tâche due (si l'interrupteur maître n'est pas en
// pause). Une seule par tick : lancer plusieurs inférences en parallèle est de
// toute façon impossible (gate de génération), et étaler leur départ évite qu'une
// rafale de tâches monopolise le modèle d'un coup.
func tickTasks(now time.Time) {
	if tasksPaused() {
		return
	}
	nowMs := now.UnixMilli()
	for _, t := range listTasks() {
		if !t.Enabled {
			continue
		}
		if t.NextRun == 0 {
			// Priming raté (schedule invalide) ou tâche neuve : on tente de le poser,
			// sans la déclencher ce tour-ci.
			t.NextRun = computeNextRun(t.Schedule, t.TZ, now)
			_ = saveTask(t)
			continue
		}
		if t.NextRun <= nowMs {
			runTask(t) // met à jour LastRun/NextRun/… et persiste
			return
		}
	}
}
