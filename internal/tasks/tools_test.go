package tasks

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"ajean/internal/tool"
)

// defs rend les outils par leur nom.
func defs(t *testing.T) map[string]tool.Def {
	t.Helper()
	out := map[string]tool.Def{}
	for _, d := range Tools() {
		if d.Description == "" || d.Params["type"] != "object" || d.Run == nil {
			t.Errorf("outil %s incomplet", d.Name)
		}
		out[d.Name] = d
	}
	if len(out) != 4 {
		t.Fatalf("4 outils attendus : %d", len(out))
	}
	return out
}

func TestToolDescriptions(t *testing.T) {
	add := defs(t)["task_add"]
	for _, must := range []string{Nothing, "zero cost", "does NOT see this conversation", "Remind the user to call their mother"} {
		if !strings.Contains(add.Description, must) {
			t.Errorf("la description de task_add ne dit pas %q", must)
		}
	}
	props := add.Params["properties"].(map[string]any)
	for _, name := range []string{"name", "prompt", "tool", "args", "in_minutes", "at", "every", "unit", "time", "days", "day", "quiet"} {
		if _, ok := props[name]; !ok {
			t.Errorf("task_add : paramètre %s absent", name)
		}
	}
	if _, ok := props["id"]; ok {
		t.Error("task_add ne prend pas d'id")
	}
}

func TestTools(t *testing.T) {
	setup(t)
	addScript(t, "meteo", `function run(args) { return "beau à " + args.city }`)
	d := defs(t)
	call := func(name string, args map[string]any) string {
		return d[name].Run(tool.Call{Args: args, Conv: "c42"})
	}

	if got := call("task_list", nil); got != "(aucune tâche)" {
		t.Errorf("liste vide : %q", got)
	}

	// task_add : une tâche du modèle, récurrente. Les nombres arrivent parfois en texte.
	got := call("task_add", map[string]any{
		"name": "Résumé", "prompt": "Prepare the weekly summary for the user", "every": "1", "unit": "week", "days": []any{"mon"}, "time": "09:00", "quiet": true,
	})
	if !tool.IsOK(got) || !strings.Contains(got, "tâche créée") || !strings.Contains(got, "tous les lundis à 09:00") || !strings.Contains(got, "jamais exécutée") {
		t.Fatalf("task_add : %s", got)
	}
	id := regexp.MustCompile(`t[0-9a-f]{8}`).FindString(got)
	weekly := get(t, id)
	if weekly.Conv != "c42" || !weekly.Quiet || !weekly.Enabled || weekly.Prompt != "Prepare the weekly summary for the user" || weekly.Schedule.Unit != "week" {
		t.Errorf("tâche créée par l'outil : %+v", weekly.Task)
	}

	// task_add : une tâche d'outil ponctuelle, les jours donnés comme un texte sont lus aussi.
	got = call("task_add", map[string]any{"tool": "meteo", "args": map[string]any{"city": "Lyon"}, "in_minutes": 30.0})
	if !tool.IsOK(got) || !strings.Contains(got, "· meteo · une fois, le ") {
		t.Fatalf("task_add d'un outil : %s", got)
	}
	toolID := regexp.MustCompile(`t[0-9a-f]{8}`).FindString(got)
	if v := get(t, toolID); v.Tool != "meteo" || v.Args["city"] != "Lyon" || v.Conv != "c42" || v.Next.Before(time.Now().Add(29*time.Minute)) {
		t.Errorf("tâche d'outil créée : %+v", v.Task)
	}
	if got := call("task_add", map[string]any{"prompt": "p", "unit": "week", "days": "mon, thu", "time": "8h"}); !strings.Contains(got, "tous les lundis et jeudis à 08:00") {
		t.Errorf("jours en texte : %s", got)
	}

	for label, args := range map[string]map[string]any{
		"sans rien":        {},
		"sans moment":      {"prompt": "p"},
		"outil inconnu":    {"tool": "absent", "in_minutes": 5.0},
		"consigne + outil": {"prompt": "p", "tool": "meteo", "in_minutes": 5.0},
		"trop fréquent":    {"prompt": "p", "every": 1.0, "unit": "minute"},
	} {
		if got := call("task_add", args); !tool.IsErr(got) {
			t.Errorf("task_add %s : %q, refus attendu", label, got)
		}
	}
	if got := call("task_add", map[string]any{"tool": "absent", "in_minutes": 5.0}); !strings.Contains(got, "meteo") {
		t.Errorf("le refus d'un outil inconnu doit donner les noms existants : %q", got)
	}

	// task_list : la liste, puis le détail avec les comptes rendus.
	if !tick(time.Now().Add(31 * time.Minute)) {
		t.Fatal("la tâche d'outil n'est pas partie")
	}
	list := call("task_list", nil)
	if lines := strings.Split(list, "\n"); len(lines) != 3 || !strings.HasPrefix(lines[2], "- "+toolID+" · meteo · ") || !strings.Contains(lines[2], "désactivée · dernière : ok le ") {
		t.Errorf("task_list : %q", list)
	}
	if !strings.Contains(list, "- "+id+" · Résumé · tous les lundis à 09:00 · prochaine : ") {
		t.Errorf("task_list : %q", list)
	}
	detail := call("task_list", map[string]any{"id": toolID})
	if !strings.Contains(detail, `outil : meteo {"city":"Lyon"}`) || !strings.Contains(detail, "dernières exécutions :") || !strings.HasSuffix(detail, "\nbeau à Lyon") {
		t.Errorf("détail d'une tâche d'outil : %q", detail)
	}
	detail = call("task_list", map[string]any{"id": id})
	if !strings.Contains(detail, "consigne : Prepare the weekly summary for the user") || !strings.Contains(detail, "silencieuse") || strings.Contains(detail, "dernières exécutions") {
		t.Errorf("détail d'une tâche du modèle : %q", detail)
	}
	if got := call("task_list", map[string]any{"id": "t00000000"}); !tool.IsErr(got) {
		t.Errorf("détail d'une tâche inconnue : %q", got)
	}

	// task_edit : seuls les champs donnés changent.
	got = call("task_edit", map[string]any{"id": id, "time": "10:30", "days": []any{"mon", "fri"}})
	if !tool.IsOK(got) || !strings.Contains(got, "tous les lundis et vendredis à 10:30") {
		t.Errorf("task_edit : %s", got)
	}
	if v := get(t, id); v.Name != "Résumé" || !v.Quiet || v.Conv != "c42" || v.Prompt != weekly.Prompt {
		t.Errorf("task_edit a touché à d'autres champs : %+v", v.Task)
	}
	if got := call("task_edit", map[string]any{"id": id, "enabled": false, "quiet": false, "name": "Bilan"}); !tool.IsOK(got) || !strings.Contains(got, "Bilan") || !strings.Contains(got, "désactivée") {
		t.Errorf("task_edit enabled=false : %s", got)
	}
	if v := get(t, id); v.Enabled || v.Quiet || !v.Next.IsZero() {
		t.Errorf("après task_edit enabled=false : %+v", v.Task)
	}
	if got := call("task_edit", map[string]any{"id": id, "enabled": "true"}); !tool.IsOK(got) || !strings.Contains(got, "prochaine : ") {
		t.Errorf("task_edit enabled=true : %s", got)
	}
	for label, args := range map[string]map[string]any{
		"sans id":         {"name": "x"},
		"id inconnu":      {"id": "t00000000", "name": "x"},
		"calendrier faux": {"id": id, "unit": "siècle"},
	} {
		if got := call("task_edit", args); !tool.IsErr(got) {
			t.Errorf("task_edit %s : %q, refus attendu", label, got)
		}
	}
	if len(List()) != 3 {
		t.Errorf("task_edit sans id a créé une tâche : %d tâches", len(List()))
	}

	// La pause générale est dite au modèle.
	if err := SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if got := call("task_list", nil); !strings.Contains(got, "en pause") {
		t.Errorf("task_list pendant la pause : %q", got)
	}

	// task_delete.
	if got := call("task_delete", map[string]any{"id": id}); !tool.IsOK(got) {
		t.Errorf("task_delete : %s", got)
	}
	if got := call("task_delete", map[string]any{"id": id}); !tool.IsErr(got) {
		t.Errorf("task_delete deux fois : %s", got)
	}
	if got := call("task_delete", map[string]any{}); !tool.IsErr(got) {
		t.Errorf("task_delete sans id : %s", got)
	}
	if len(List()) != 2 {
		t.Errorf("après task_delete : %d tâches", len(List()))
	}
}
