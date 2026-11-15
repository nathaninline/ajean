package server

import (
	"net/http"

	"ajean/internal/tasks"
)

// handleTasks : les tâches et l'interrupteur général.
func handleTasks(w http.ResponseWriter, r *http.Request) {
	reply(w, map[string]any{"paused": tasks.Paused(), "tasks": orEmpty(tasks.List())})
}

// handleTaskSave crée une tâche (sans id) ou en modifie une (avec id : seuls les
// champs donnés changent).
func handleTaskSave(w http.ResponseWriter, r *http.Request) {
	var in tasks.Input
	if !body(w, r, &in) {
		return
	}
	t, err := tasks.Save(in)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	reply(w, map[string]string{"id": t.ID})
}

func handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID string }
	if body(w, r, &q) {
		done(w, tasks.Delete(q.ID), http.StatusNotFound)
	}
}

// handleTaskRun lance une tâche maintenant, sans toucher à son calendrier.
func handleTaskRun(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID string }
	if body(w, r, &q) {
		done(w, tasks.RunNow(q.ID), http.StatusBadRequest)
	}
}

func handleTaskStop(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID string }
	if body(w, r, &q) {
		tasks.Stop(q.ID)
		ok(w)
	}
}

// handleTasksPause met ou lève l'interrupteur général : plus aucune tâche ne
// part tant qu'il est mis.
func handleTasksPause(w http.ResponseWriter, r *http.Request) {
	var q struct{ Paused bool }
	if body(w, r, &q) {
		done(w, tasks.SetPaused(q.Paused), http.StatusInternalServerError)
	}
}
