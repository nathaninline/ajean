// web_tasks.go — endpoints de pilotage des tâches planifiées (voir tasks.go).
// Tous derrière la clé de pilotage (enregistrés via api(...) dans newWebMux).
package ajean

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// handleTasks : GET → liste des tâches + état de l'interrupteur maître + si le
// mode agent est actif (sans lui, une tâche n'a aucun outil pour agir).
//
// Les horodatages (next_run, last_run) partent en epoch ms BRUTS : c'est le
// navigateur qui les met en forme, dans le fuseau de l'utilisateur. Les formater
// ici les figerait dans le fuseau du SERVEUR (souvent UTC), d'où un décalage
// horaire à l'écran.
func handleTasks(w http.ResponseWriter, r *http.Request) {
	tasks := listTasks()
	conv.mu.Lock()
	runningID := conv.runningTaskID
	conv.mu.Unlock()
	// Une tâche script tourne hors du verrou de génération : conv ne la voit pas,
	// on complète donc avec le registre des scripts en cours (tasks_script.go).
	if runningID == "" {
		if sid, _ := scriptRunningAny(); sid != "" {
			runningID = sid
		}
	}
	// Presets (id + nom + actif) pour peupler le sélecteur du formulaire de tâche.
	presets := []map[string]any{}
	if list, err := ListPresets(); err == nil {
		for _, p := range list {
			presets = append(presets, map[string]any{"id": p.ID, "name": p.Name, "active": p.Active})
		}
	}
	// Scripts durables disponibles, pour le sélecteur d'une tâche « script seul ».
	scripts, _ := listScripts()
	if scripts == nil {
		scripts = []scriptInfo{}
	}
	// Projets, pour le sélecteur « mémoire du projet » du formulaire de tâche.
	projects := []map[string]any{}
	for _, p := range listProjects() {
		projects = append(projects, map[string]any{"slug": p.Slug, "name": p.Name})
	}
	sendJSON(w, 200, map[string]any{
		"ok":         true,
		"tasks":      tasks,
		"paused":     tasksPaused(),
		"agent":      agentEnabled(),
		"running_id": runningID,
		"presets":    presets,
		"scripts":    scripts,
		"projects":   projects,
		// État global mémoire/web, pour proposer des défauts cohérents à la création.
		"mem_on": memMode() != MemOff,
		"web_on": internetEnabled() && crawlReachable(),
	})
}

// handleTaskSave crée ou met à jour une tâche. Valide le schedule (400 sinon) et
// recalcule NextRun. Un ID vide = création.
func handleTaskSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Prompt   string `json:"prompt"`
		Schedule string `json:"schedule"`
		TZ       string `json:"tz"`
		Preset   string `json:"preset"`
		Project  string `json:"project"`
		NoMem    bool   `json:"no_mem"`
		NoWeb    bool   `json:"no_web"`
		Enabled  bool   `json:"enabled"`
		Kind     string `json:"kind"`   // "" / "agent" = consigne IA ; "script" = script seul
		Script   string `json:"script"` // nom du script (dossier protégé) pour kind=="script"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.Schedule = strings.TrimSpace(req.Schedule)
	req.Kind = strings.TrimSpace(req.Kind)
	req.Script = strings.TrimSpace(req.Script)
	if req.Name == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "nom obligatoire"})
		return
	}
	// Une tâche script exige un script existant ; une tâche IA exige une consigne.
	if req.Kind == "script" {
		if req.Script == "" {
			sendJSON(w, 400, map[string]any{"ok": false, "error": "script obligatoire pour une tâche script"})
			return
		}
		if err := scriptExists(req.Script); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	} else if req.Prompt == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "consigne obligatoire"})
		return
	}
	if err := validateSchedule(req.Schedule, req.TZ); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "fréquence invalide : " + err.Error()})
		return
	}

	var t Task
	if req.ID != "" {
		if cur, ok := getTask(req.ID); ok {
			t = cur // conserve l'historique d'exécution (LastRun/LastReport…)
		} else {
			t.ID = req.ID
		}
	}
	if t.ID == "" {
		t.ID = newTaskID()
	}
	t.Name, t.Prompt, t.Enabled = req.Name, req.Prompt, req.Enabled
	t.TZ, t.Preset = req.TZ, req.Preset
	t.Project = req.Project
	t.NoMem, t.NoWeb = req.NoMem, req.NoWeb
	t.Kind, t.Script = req.Kind, req.Script
	// Recalcule NextRun si la fréquence a changé (ou à la création).
	if t.Schedule != req.Schedule || t.NextRun == 0 {
		t.NextRun = computeNextRun(req.Schedule, req.TZ, time.Now())
	}
	t.Schedule = req.Schedule
	if err := saveTask(t); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "id": t.ID})
}

// handleTaskDelete supprime une tâche.
func handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.ID == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "id manquant"})
		return
	}
	if err := deleteTask(req.ID); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleTaskToggle active/désactive UNE tâche. En l'activant, on (re)pose son
// NextRun pour qu'elle reparte proprement.
func handleTaskToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
		On bool   `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	t, ok := getTask(req.ID)
	if !ok {
		sendJSON(w, 404, map[string]any{"ok": false, "error": "tâche introuvable"})
		return
	}
	t.Enabled = req.On
	if req.On {
		t.NextRun = computeNextRun(t.Schedule, t.TZ, time.Now())
	}
	if err := saveTask(t); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleTasksPause pilote l'interrupteur maître ({on:true} = suspendre tout).
func handleTasksPause(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	was := tasksPaused()
	if err := setTasksPaused(req.On); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Reprise (pause → actif) : on replanifie les tâches à leur prochaine échéance
	// pour ne PAS rejouer d'un coup toutes celles dont l'heure est passée pendant la
	// pause. Elles repartent à leur horaire habituel, pas en rafale.
	if was && !req.On {
		rescheduleEnabledFromNow()
	}
	sendJSON(w, 200, map[string]any{"ok": true, "paused": tasksPaused()})
}

// handleTaskRun lance une tâche MAINTENANT (bouton « tester »). Exécution
// détachée : on rend la main tout de suite et on rafraîchit l'état à la fin.
// 409 si une génération est déjà en cours.
func handleTaskRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	t, ok := getTask(req.ID)
	if !ok {
		sendJSON(w, 404, map[string]any{"ok": false, "error": "tâche introuvable"})
		return
	}
	// Une tâche script ne touche pas au modèle : on peut la lancer même si le
	// modèle n'est pas chargé ou qu'une génération est en cours. Seules les tâches
	// IA passent par le gate de génération (409/503).
	if t.Kind != "script" {
		conv.mu.Lock()
		busy := conv.Generating
		conv.mu.Unlock()
		if busy {
			sendJSON(w, 409, map[string]any{"ok": false, "error": ErrBusy.Error()})
			return
		}
		if !healthCheck() {
			sendJSON(w, 503, map[string]any{"ok": false, "error": errModelLoading.Error()})
			return
		}
	}
	go runTask(t)
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleTaskStop interrompt la tâche en cours. Une tâche script tourne hors du
// verrou de génération : on l'annule via son registre. Sinon (tâche IA), on
// retombe sur conv.Stop, qui annule le contexte de la génération autonome.
func handleTaskStop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.ID != "" && scriptRunStop(req.ID) {
		sendJSON(w, 200, map[string]any{"ok": true})
		return
	}
	conv.Stop()
	sendJSON(w, 200, map[string]any{"ok": true})
}
