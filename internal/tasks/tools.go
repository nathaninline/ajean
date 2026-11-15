package tasks

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ajean/internal/config"
	"ajean/internal/icons"
	"ajean/internal/tool"
)

// Tools : les outils par lesquels le modèle gère ses tâches.
func Tools() []tool.Def {
	when := map[string]any{
		"in_minutes": tool.Integer("One-time task: delay from now, in minutes"),
		"at":         tool.String("One-time task, user's local time: \"HH:MM\" (next occurrence) or \"YYYY-MM-DD HH:MM\""),
		"every":      tool.Integer("Recurring task: every N units (default 1)"),
		"unit":       tool.Enum("Recurring task: the unit of 'every'", "minute", "hour", "day", "week", "month"),
		"time":       tool.String("Recurring task, units day/week/month: user's local time \"HH:MM\" (default: the current time)"),
		"days": map[string]any{
			"type": "array", "items": tool.Enum("Weekday", weekdays...),
			"description": "Recurring task, unit week: the weekdays it runs on (default: today's weekday)",
		},
		"day": tool.Integer("Recurring task, unit month: day of the month, 1-31 (31 means the last day of shorter months; default: today's)"),
	}
	what := map[string]any{
		"name":   tool.String("Short name shown to the user"),
		"prompt": tool.String("Model task: what you will do at that moment, self-contained"),
		"tool":   tool.String("Tool task: name of one of your own tools (see tools action=list)"),
		"args":   tool.Object("Tool task: the parameters passed to the tool"),
		"quiet":  tool.Boolean("true: tell the user only when there is something to report"),
		"icon":   tool.String("The task's icon, a name from the list in task_add"),
		"color":  tool.String("The task's color, #rrggbb"),
	}
	add, edit := map[string]any{}, map[string]any{
		"id":      tool.String("Task id (from task_list)"),
		"enabled": tool.Boolean("false suspends the task, true resumes it"),
	}
	for _, group := range []map[string]any{what, when} {
		for name, schema := range group {
			add[name], edit[name] = schema, schema
		}
	}
	return []tool.Def{
		{
			Name: "task_add",
			Description: "Schedule something you will do on your own, once or on a regular basis, and report to the user in this conversation: a reminder, a watch, a recurring job. " +
				"Model task (prompt): at that moment you receive the prompt and work alone with all your tools, in a separate thread that does NOT see this conversation. " +
				"So write the prompt as what you will do then, with every detail needed, e.g. \"Remind the user to call their mother\" or \"Check the price of flight X on site Y; tell the user only if it is under 300 €\". " +
				"Tool task (tool + args): one of your own tools is run directly, without you, at zero cost, and what it returns is the report. Choose it for repetitive work that code does without any thinking. " +
				"When: in_minutes or at for a one-time task, every + unit (+ time, days, day) for a recurring one. " +
				"quiet=true suits a watch: the user hears about it only when there is something to say. In a quiet model task, answer exactly " + Nothing + " when there is nothing to report; a quiet tool task returns nothing. " +
				"Look: give the task an icon and a color that fit it. " + icons.Help(),
			Params: tool.Params(add),
			Run: func(c tool.Call) string {
				in := input(c)
				in.ID, in.Conv = "", &c.Conv
				return saved(in, "tâche créée")
			},
		},
		{
			Name:        "task_list",
			Description: "List your scheduled tasks: id, name, schedule, next run, last status. With an id: that task in full, with its latest reports.",
			Params:      tool.Params(map[string]any{"id": tool.String("Show only this task, in full")}),
			Run:         func(c tool.Call) string { return listAll(c.Str("id")) },
		},
		{
			Name:        "task_edit",
			Description: "Change a scheduled task: give its id and only the fields to change (same fields as task_add). enabled=false suspends it, enabled=true resumes it.",
			Params:      tool.Params(edit, "id"),
			Run: func(c tool.Call) string {
				in := input(c)
				if in.ID == "" {
					return tool.Err("paramètre id manquant (task_list donne les ids)")
				}
				return saved(in, "tâche modifiée")
			},
		},
		{
			Name:        "task_delete",
			Description: "Delete a scheduled task.",
			Params:      tool.Params(map[string]any{"id": tool.String("Task id (from task_list)")}, "id"),
			Run:         func(c tool.Call) string { return tool.Done(Delete(c.Str("id")), "tâche supprimée") },
		},
	}
}

// input lit ce que le modèle a donné. Seuls les champs présents dans l'appel
// sont posés : pour une modification, les autres gardent leur valeur.
func input(c tool.Call) Input {
	in := Input{ID: c.Str("id"), Args: c.Obj("args"), At: c.Str("at"), Unit: c.Str("unit"), Time: c.Str("time"), Days: days(c.Args["days"])}
	in.InMinutes, _ = c.Int("in_minutes")
	in.Every, _ = c.Int("every")
	in.Day, _ = c.Int("day")
	for key, field := range map[string]**string{"name": &in.Name, "prompt": &in.Prompt, "tool": &in.Tool, "icon": &in.Icon, "color": &in.Color} {
		if _, given := c.Args[key]; given {
			v := c.Str(key)
			*field = &v
		}
	}
	for key, field := range map[string]**bool{"quiet": &in.Quiet, "enabled": &in.Enabled} {
		if _, given := c.Args[key]; given {
			v := c.Bool(key)
			*field = &v
		}
	}
	return in
}

// days lit la liste des jours. Les modèles l'envoient parfois comme un texte
// (« mon, wed »).
func days(v any) []string {
	switch v := v.(type) {
	case []any:
		out := make([]string, len(v))
		for i, d := range v {
			out[i] = fmt.Sprint(d)
		}
		return out
	case string:
		return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	}
	return nil
}

func saved(in Input, done string) string {
	t, err := Save(in)
	if err != nil {
		return tool.Err("%v", err)
	}
	return tool.OK("%s\n%s", done, line(view(t, time.Now(), false)))
}

func listAll(id string) string {
	all := List()
	if id != "" {
		for _, v := range all {
			if v.ID == id {
				return detail(v)
			}
		}
		return tool.Err("%v", errNotFound)
	}
	if len(all) == 0 {
		return "(aucune tâche)"
	}
	lines := make([]string, len(all))
	for i, v := range all {
		lines[i] = line(v)
	}
	if Paused() {
		lines = append(lines, "(les tâches sont en pause : aucune ne part tant que l'utilisateur ne les relance pas)")
	}
	return strings.Join(lines, "\n")
}

func stamp(t time.Time) string { return t.In(config.Location()).Format("2006-01-02 15:04") }

// line : une tâche sur une ligne.
func line(v View) string {
	state := "désactivée"
	switch {
	case v.Running:
		state = "en cours d'exécution"
	case v.Enabled && !v.Next.IsZero():
		state = "prochaine : " + stamp(v.Next)
	}
	last := "jamais exécutée"
	if len(v.Runs) > 0 {
		last = "dernière : " + v.Runs[0].Status + " le " + stamp(v.Runs[0].At)
	}
	return fmt.Sprintf("- %s · %s · %s · %s · %s", v.ID, v.Name, v.ScheduleText, state, last)
}

// detail : une tâche en entier, avec ses derniers comptes rendus.
func detail(v View) string {
	var b strings.Builder
	b.WriteString(line(v) + "\n")
	if v.Tool != "" {
		args, err := json.Marshal(v.Args)
		if err != nil || v.Args == nil {
			args = []byte("{}")
		}
		fmt.Fprintf(&b, "outil : %s %s\n", v.Tool, args)
	} else {
		fmt.Fprintf(&b, "consigne : %s\n", v.Prompt)
	}
	if v.Quiet {
		b.WriteString("silencieuse : ne prévient que s'il y a quelque chose à dire\n")
	}
	if len(v.Runs) == 0 {
		return strings.TrimSpace(b.String())
	}
	b.WriteString("dernières exécutions :")
	for _, r := range v.Runs {
		fmt.Fprintf(&b, "\n\n%s · %s · %g s", stamp(r.At), r.Status, r.Seconds)
		if r.Report != "" {
			b.WriteString("\n" + r.Report)
		}
	}
	return b.String()
}
