package ajean

// tasks_tools.go — outils task_* exposés au modèle pour qu'il gère LUI-MÊME ses
// tâches planifiées (voir tasks.go) : se donner un rappel récurrent, une veille,
// un ménage périodique… puis les lister, les ajuster ou les supprimer. Ils
// réutilisent exactement l'infra de l'UI (validateSchedule, computeNextRun,
// saveTask), donc une tâche créée par l'IA apparaît et se pilote comme les
// autres dans le panneau Tâches.
//
// Gate : réservés au mode agent (EnabledTools). Une tâche n'a de sens que si
// l'IA peut agir en arrière-plan ; sans agent elle n'aurait aucun outil pour
// livrer son résultat.

import (
	"fmt"
	"strings"
	"time"
)

// str extrait une chaîne d'un argument d'outil décodé en any ("" si absent ou
// d'un autre type).
func str(v any) string {
	s, _ := v.(string)
	return s
}

func taskListTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "task_list",
			Description: "List your scheduled tasks with their state, next run, and last result. Pass an id to get that task's full last report.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Show only this task, with its full last report"},
				},
			},
		},
	}
}

func taskCreateTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "task_create",
			Description: "Schedule work to run autonomously later, once or on a repeating schedule.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   map[string]any{"type": "string"},
					"prompt": map[string]any{"type": "string", "description": "Self-contained instruction to run each time"},
					// Le format du schedule est le seul point que le modèle ne peut pas
					// deviner : il vit ici, dans le paramètre, pas dans la phrase de l'outil.
					"schedule": map[string]any{"type": "string", "description": "\"@every 2h\" (interval), \"@every 1d@09:00\" (daily time), or a 5-field cron \"min hour dom mon dow\""},
					"enabled":  map[string]any{"type": "boolean", "description": "Start active (default true)"},
				},
				"required": []string{"name", "prompt", "schedule"},
			},
		},
	}
}

func taskUpdateTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "task_update",
			Description: "Change one or more fields of a scheduled task by id.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":       map[string]any{"type": "string"},
					"name":     map[string]any{"type": "string"},
					"prompt":   map[string]any{"type": "string"},
					"schedule": map[string]any{"type": "string"},
					"enabled":  map[string]any{"type": "boolean", "description": "false pauses, true resumes"},
				},
				"required": []string{"id"},
			},
		},
	}
}

func taskDeleteTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "task_delete",
			Description: "Delete a scheduled task by id.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required": []string{"id"},
			},
		},
	}
}

// defaultTaskTZ : le modèle n'a pas le fuseau du navigateur. On reprend celui
// d'une tâche existante (posé par l'UI depuis le navigateur) pour que « tous les
// jours à 9h » tombe à la bonne heure ; à défaut, fuseau vide = heure locale du
// serveur (souvent UTC), ce que la description du planificateur assume déjà.
func defaultTaskTZ() string {
	for _, t := range listTasks() {
		if strings.TrimSpace(t.TZ) != "" {
			return t.TZ
		}
	}
	return ""
}

// toolTaskList formate les tâches pour le modèle. Avec un id, on ne montre QUE
// cette tâche, compte-rendu COMPLET (le cas « donne-moi le compte-rendu de la
// tâche X ») ; sans id, la liste complète avec un aperçu court par tâche.
func toolTaskList(args map[string]any) string {
	tasks := listTasks()
	if len(tasks) == 0 {
		return "[aucune tâche planifiée]"
	}
	if id := strings.TrimSpace(str(args["id"])); id != "" {
		t, ok := getTask(id)
		if !ok {
			return "[erreur] tâche introuvable : " + id
		}
		return formatTask(t, true)
	}
	var b strings.Builder
	for _, t := range tasks {
		b.WriteString(formatTask(t, false) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatTask rend une tâche. full=true : compte-rendu entier (sur ses propres
// lignes) ; full=false : aperçu court d'une ligne, pour une liste scannable.
func formatTask(t Task, full bool) string {
	var b strings.Builder
	state := "off"
	if t.Enabled {
		state = "on"
	}
	fmt.Fprintf(&b, "- %s [%s] « %s » — %s", t.ID, state, t.Name, t.Schedule)
	if t.NextRun > 0 && t.Enabled {
		fmt.Fprintf(&b, " (prochaine : %s)", time.UnixMilli(t.NextRun).In(loc(t.TZ)).Format("2006-01-02 15:04"))
	}
	if t.LastRun > 0 {
		when := time.UnixMilli(t.LastRun).In(loc(t.TZ)).Format("2006-01-02 15:04")
		if t.LastOK {
			fmt.Fprintf(&b, " ✓ dernier run %s", when)
		} else {
			fmt.Fprintf(&b, " ✗ dernier run %s : %s", when, t.LastError)
		}
		if r := strings.TrimSpace(t.LastReport); r != "" {
			if full {
				// Compte-rendu ENTIER (déjà borné à 4000 car au stockage), tel quel,
				// sur ses propres lignes — c'est ce que l'utilisateur demande.
				b.WriteString("\n  compte-rendu complet :\n" + r)
			} else {
				b.WriteString("\n    compte-rendu : " + previewReport(r))
			}
		}
	}
	return b.String()
}

// previewReport aplati + tronque le compte-rendu pour l'aperçu de la liste (un
// rapport entier par tâche gonflerait la sortie). Le texte complet s'obtient via
// task_list avec l'id de la tâche.
func previewReport(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 300
	if len(s) > max {
		return s[:max] + " […] (id pour le compte-rendu complet)"
	}
	return s
}

// toolTaskCreate crée une tâche depuis un appel d'outil du modèle.
func toolTaskCreate(args map[string]any) string {
	name := strings.TrimSpace(str(args["name"]))
	prompt := strings.TrimSpace(str(args["prompt"]))
	schedule := strings.TrimSpace(str(args["schedule"]))
	if name == "" || prompt == "" || schedule == "" {
		return "[erreur] name, prompt et schedule sont obligatoires"
	}
	tz := defaultTaskTZ()
	if err := validateSchedule(schedule, tz); err != nil {
		return "[erreur] fréquence invalide : " + err.Error()
	}
	enabled := true
	if v, ok := args["enabled"].(bool); ok {
		enabled = v
	}
	t := Task{
		ID: newTaskID(), Name: name, Prompt: prompt, Schedule: schedule,
		TZ: tz, Enabled: enabled,
	}
	t.NextRun = computeNextRun(schedule, tz, time.Now())
	if err := saveTask(t); err != nil {
		return "[erreur] " + err.Error()
	}
	when := ""
	if t.NextRun > 0 && enabled {
		when = ", prochaine exécution " + time.UnixMilli(t.NextRun).In(loc(tz)).Format("2006-01-02 15:04")
	}
	return fmt.Sprintf("[ok] tâche '%s' créée (id %s)%s", name, t.ID, when)
}

// toolTaskUpdate modifie une tâche existante (partiel).
func toolTaskUpdate(args map[string]any) string {
	id := strings.TrimSpace(str(args["id"]))
	if id == "" {
		return "[erreur] id manquant"
	}
	t, ok := getTask(id)
	if !ok {
		return "[erreur] tâche introuvable : " + id
	}
	if v, ok := args["name"].(string); ok && strings.TrimSpace(v) != "" {
		t.Name = strings.TrimSpace(v)
	}
	if v, ok := args["prompt"].(string); ok && strings.TrimSpace(v) != "" {
		t.Prompt = strings.TrimSpace(v)
	}
	if v, ok := args["schedule"].(string); ok && strings.TrimSpace(v) != "" {
		sched := strings.TrimSpace(v)
		if err := validateSchedule(sched, t.TZ); err != nil {
			return "[erreur] fréquence invalide : " + err.Error()
		}
		t.Schedule = sched
		t.NextRun = computeNextRun(sched, t.TZ, time.Now())
	}
	if v, ok := args["enabled"].(bool); ok {
		t.Enabled = v
		if v && t.NextRun == 0 {
			t.NextRun = computeNextRun(t.Schedule, t.TZ, time.Now())
		}
	}
	if err := saveTask(t); err != nil {
		return "[erreur] " + err.Error()
	}
	return fmt.Sprintf("[ok] tâche '%s' (id %s) mise à jour", t.Name, t.ID)
}

// toolTaskDelete supprime une tâche.
func toolTaskDelete(args map[string]any) string {
	id := strings.TrimSpace(str(args["id"]))
	if id == "" {
		return "[erreur] id manquant"
	}
	t, ok := getTask(id)
	if !ok {
		return "[erreur] tâche introuvable : " + id
	}
	if err := deleteTask(id); err != nil {
		return "[erreur] " + err.Error()
	}
	return fmt.Sprintf("[ok] tâche '%s' (id %s) supprimée", t.Name, id)
}
