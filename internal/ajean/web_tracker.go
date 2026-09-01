// web_tracker.go — endpoints de gestion des TRACKERS (données datées qui s'accumulent).
// Voir tracker.go pour le modèle. L'UI charge la liste puis les événements d'UN tracker
// pour les afficher regroupés par année/mois côté client ; l'IA, elle, passe par
// l'outil `tracker` avec navigation par niveaux (jamais tout d'un coup).
package ajean

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleTracker (GET) : liste des trackers du projet actif (métadonnées).
func handleTracker(w http.ResponseWriter, r *http.Request) {
	list := trackerList()
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		row := map[string]any{"slug": m.Slug, "name": m.Name, "count": m.Count}
		if m.Count > 0 {
			row["first"] = fmtDay(m.FirstTS)
			row["last"] = fmtDay(m.LastTS)
			row["latest"] = m.LastText
		}
		out = append(out, row)
	}
	sendJSON(w, 200, map[string]any{"ok": true, "trackers": out})
}

// handleTrackerEvents (GET ?slug=) : tous les événements d'un tracker (pour l'UI, qui
// regroupe par année/mois). L'IA ne passe jamais par là (elle navigue par niveaux).
func handleTrackerEvents(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.URL.Query().Get("slug"))
	s, ok := trackerLoad(slug)
	if !ok {
		sendJSON(w, 404, map[string]any{"ok": false, "error": "tracker introuvable"})
		return
	}
	evs := make([]map[string]any, 0, len(s.Events))
	for _, e := range s.Events {
		evs = append(evs, map[string]any{"id": e.ID, "ts": e.TS, "when": fmtEvent(e), "date_only": e.DateOnly, "text": e.Text})
	}
	sendJSON(w, 200, map[string]any{"ok": true, "name": s.Name, "slug": slug, "events": evs})
}

// handleTrackerAdd (POST {name, when, text}) : ajoute un point (crée le tracker au besoin).
func handleTrackerAdd(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, When, Text string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	id, err := trackerAdd(body.Name, body.When, body.Text)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "id": id})
}

// handleTrackerEdit (POST {slug, id, when, text}) : modifie un point.
func handleTrackerEdit(w http.ResponseWriter, r *http.Request) {
	var body struct{ Slug, ID, When, Text string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := trackerEditEvent(strings.TrimSpace(body.Slug), strings.TrimSpace(body.ID), body.When, body.Text); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleTrackerRename (POST {slug, name}) : renomme un tracker (re-clé si besoin).
func handleTrackerRename(w http.ResponseWriter, r *http.Request) {
	var body struct{ Slug, Name string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := trackerRename(strings.TrimSpace(body.Slug), body.Name); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleTrackerMove (POST {slug, toSlug}) : déplace un tracker vers un autre projet.
func handleTrackerMove(w http.ResponseWriter, r *http.Request) {
	var body struct{ Slug, ToSlug string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := trackerMoveToProject(strings.TrimSpace(body.Slug), strings.TrimSpace(body.ToSlug)); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleTrackerDelete (POST {slug, id?}) : supprime un point, ou le tracker entier si id absent.
func handleTrackerDelete(w http.ResponseWriter, r *http.Request) {
	var body struct{ Slug, ID string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	slug := strings.TrimSpace(body.Slug)
	var err error
	if strings.TrimSpace(body.ID) == "" {
		err = trackerDelete(slug)
	} else {
		err = trackerDeleteEvent(slug, strings.TrimSpace(body.ID))
	}
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}
