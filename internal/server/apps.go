package server

import (
	"net/http"

	"ajean/internal/apps"
)

func handleApps(w http.ResponseWriter, r *http.Request) {
	reply(w, map[string]any{"apps": orEmpty(apps.List())})
}

// handleAppPage (GET ?id=) : le document complet de l'app. La page d'AJEAN le
// met dans une iframe sans origine : l'app ne peut appeler aucune de nos routes.
func handleAppPage(w http.ResponseWriter, r *http.Request) {
	app, html, err := apps.Page(r.URL.Query().Get("id"))
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	reply(w, map[string]any{"app": app, "html": html})
}

func handleAppDelete(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID string }
	if body(w, r, &q) {
		done(w, apps.Delete(q.ID), http.StatusNotFound)
	}
}

func handleAppVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := apps.Versions(r.URL.Query().Get("id"))
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	reply(w, map[string]any{"versions": orEmpty(versions)})
}

func handleAppRollback(w http.ResponseWriter, r *http.Request) {
	var q struct {
		ID      string
		Version int
	}
	if body(w, r, &q) {
		done(w, apps.Rollback(q.ID, q.Version), http.StatusBadRequest)
	}
}

// handleAppBridge relaie une demande de l'app ouverte (stockage, outil, modèle,
// erreur). L'identifiant de l'app vient de la page d'AJEAN, pas de l'app : une
// app ne peut pas se faire passer pour une autre.
func handleAppBridge(w http.ResponseWriter, r *http.Request) {
	var q struct {
		App, Op string
		Args    map[string]any
	}
	if !body(w, r, &q) {
		return
	}
	result, err := apps.Bridge(r.Context(), q.App, q.Op, q.Args)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	reply(w, map[string]any{"result": result})
}
