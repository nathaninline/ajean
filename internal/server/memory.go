package server

import (
	"context"
	"net/http"
	"strings"

	"ajean/internal/agent"
	"ajean/internal/memory"
	"ajean/internal/scripts"
)

const journalPageSize = 200

// handleMemory (GET ?q=) : tout ce que montre la fenêtre « Mémoire ». q filtre le journal.
func handleMemory(w http.ResponseWriter, r *http.Request) {
	profile, _ := memory.Profile()
	journal, total := memory.Journal(r.URL.Query().Get("q"), journalPageSize)
	reply(w, map[string]any{
		"profile": orEmpty(profile), "lessons": orEmpty(memory.Lessons()), "rules": orEmpty(memory.Rules()),
		"fiches": orEmpty(memory.Fiches()), "journal": orEmpty(journal), "journal_total": total,
		"tools": orEmpty(scripts.List()), "sleeps": orEmpty(memory.Sleeps()),
	})
}

// orEmpty : une liste vide plutôt que « null » dans le JSON.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// handleProfile (POST {key, value, old_key}) : pose ou remplace une ligne du
// profil ; value vide = la retirer ; old_key différent de key = renommer.
func handleProfile(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Key, Value string
		OldKey     string `json:"old_key"`
	}
	if !body(w, r, &q) {
		return
	}
	if strings.TrimSpace(q.Value) == "" {
		done(w, memory.ForgetFact(q.Key), http.StatusBadRequest)
		return
	}
	done(w, memory.UpdateFact(q.OldKey, q.Key, q.Value), http.StatusBadRequest)
}

func handleFicheGet(w http.ResponseWriter, r *http.Request) {
	f, err := memory.GetFiche(r.URL.Query().Get("name"))
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	reply(w, f)
}

// handleFicheSave (POST {name, when, content, old_name}) : old_name différent = renommage.
func handleFicheSave(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Name, When, Content string
		OldName             string `json:"old_name"`
	}
	if body(w, r, &q) {
		done(w, memory.UpdateFiche(q.OldName, q.Name, q.When, q.Content), http.StatusBadRequest)
	}
}

func handleFicheDelete(w http.ResponseWriter, r *http.Request) {
	var q struct{ Name string }
	if body(w, r, &q) {
		done(w, memory.DeleteFiche(q.Name), http.StatusNotFound)
	}
}

func handleLessonDelete(w http.ResponseWriter, r *http.Request) {
	var q struct{ N int }
	if body(w, r, &q) {
		done(w, memory.ForgetLesson(q.N), http.StatusNotFound)
	}
}

func handleRuleDelete(w http.ResponseWriter, r *http.Request) {
	var q struct{ N int }
	if body(w, r, &q) {
		done(w, memory.ForgetRule(q.N), http.StatusNotFound)
	}
}

func handleJournalDelete(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID string }
	if body(w, r, &q) {
		done(w, memory.DeleteEntry(q.ID), http.StatusNotFound)
	}
}

func handleToolDelete(w http.ResponseWriter, r *http.Request) {
	var q struct{ Name string }
	if body(w, r, &q) {
		done(w, scripts.Delete(q.Name), http.StatusNotFound)
	}
}

// handleSleep lance un sommeil maintenant. Il continue en arrière-plan : sa fin
// arrive par les événements.
func handleSleep(w http.ResponseWriter, r *http.Request) {
	if err := agent.CanSleep(); err != nil {
		fail(w, status(err), err)
		return
	}
	go func() { _ = agent.Sleep(context.Background()) }()
	ok(w)
}

func handleSleepRestore(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID string }
	if body(w, r, &q) {
		done(w, memory.RestoreSleep(q.ID), http.StatusBadRequest)
	}
}
