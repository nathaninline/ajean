// web_projects.go — endpoints de gestion des PROJETS (mémoire + sessions
// cloisonnées). Voir projects.go pour le modèle. La bascule de projet passe par la
// conversation (SwitchProject) : la session courante est archivée dans son projet,
// puis une session vierge démarre dans le projet cible.
package ajean

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleProjects (GET) : liste des projets + slug du projet actif.
func handleProjects(w http.ResponseWriter, r *http.Request) {
	list := listProjects()
	out := make([]map[string]any, 0, len(list))
	for _, p := range list {
		out = append(out, map[string]any{"slug": p.Slug, "name": p.Name, "created_at": p.CreatedAt, "desc": p.Desc})
	}
	sendJSON(w, 200, map[string]any{"ok": true, "projects": out, "active": activeProjectSlug()})
}

// handleProjectCreate (POST {name}) : crée un projet.
func handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	p, err := createProject(body.Name)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "slug": p.Slug, "name": p.Name})
}

// handleProjectRename (POST {slug, name}) : renomme (libellé seulement).
func handleProjectRename(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := renameProject(strings.TrimSpace(body.Slug), body.Name); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleProjectDescribe (POST {slug, desc}) : enregistre la description du projet,
// fournie à l'IA en tête de conversation. desc vide = efface.
func handleProjectDescribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug string `json:"slug"`
		Desc string `json:"desc"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := setProjectDesc(strings.TrimSpace(body.Slug), body.Desc); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleProjectMoveSession (POST {id, slug}) : déplace une conversation archivée
// vers un autre projet (issue #55).
func handleProjectMoveSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := moveArchiveToProject(strings.TrimSpace(body.ID), strings.TrimSpace(body.Slug)); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleProjectMoveMem (POST {name, slug}) : déplace une page mémoire du projet
// ACTIF vers un autre projet (issue #55).
func handleProjectMoveMem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := moveMemPage(strings.TrimSpace(body.Name), activeProjectSlug(), strings.TrimSpace(body.Slug)); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleProjectDelete (POST {slug}) : supprime un projet (dossier + sessions). Si
// c'est le projet actif, la conversation rebascule sur un autre projet.
func handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug string `json:"slug"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	slug := strings.TrimSpace(body.Slug)
	wasActive := activeProjectSlug() == slug
	if err := deleteProject(slug); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Le projet actif a changé sous nos pieds : on démarre une session vierge dans le
	// nouveau projet actif pour que le fil affiché ne pointe plus vers un projet effacé.
	if wasActive {
		_ = conv.SwitchProject(activeProjectSlug())
	}
	sendJSON(w, 200, map[string]any{"ok": true, "active": activeProjectSlug()})
}

// handleProjectSwitch (POST {slug}) : bascule le projet actif (nouvelle session).
func handleProjectSwitch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug string `json:"slug"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := conv.SwitchProject(strings.TrimSpace(body.Slug)); err != nil {
		sendJSON(w, 404, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "active": activeProjectSlug()})
}
