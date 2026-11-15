package tasks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ajean/internal/config"
	"ajean/internal/home"
)

// recorder tient lieu du reste du programme : il note ce que les crochets reçoivent.
type recorder struct {
	mu        sync.Mutex
	run       func(ctx context.Context, t Task) (string, error) // ce que fait une tâche du modèle (par défaut : « fait »)
	ran       []string                                          // noms des tâches du modèle exécutées
	delivered []string                                          // « nom: compte rendu »
	convs     []string                                          // conversations des livraisons
	changes   int
}

func (r *recorder) hooks() Hooks {
	return Hooks{
		Run: func(ctx context.Context, t Task) (string, error) {
			r.mu.Lock()
			r.ran = append(r.ran, t.Name)
			run := r.run
			r.mu.Unlock()
			if run == nil {
				return "fait", nil
			}
			return run(ctx, t)
		},
		Deliver: func(t Task, report string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.delivered = append(r.delivered, t.Name+": "+report)
			r.convs = append(r.convs, t.Conv)
		},
		Changed: func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.changes++
		},
	}
}

// seen rend une copie de ce qui a été livré et le nombre de changements signalés.
func (r *recorder) seen() (delivered []string, changes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.delivered...), r.changes
}

// setup installe un dossier d'AJEAN vide, le fuseau Europe/Paris et des
// crochets qui notent tout.
func setup(t *testing.T) *recorder {
	t.Helper()
	t.Setenv("AJEAN_HOME", t.TempDir())
	if _, err := time.LoadLocation("Europe/Paris"); err != nil {
		t.Skipf("fuseaux indisponibles : %v", err)
	}
	if err := config.Update(func(c *config.Config) error { c.TZ = "Europe/Paris"; return nil }); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	current, forced = nil, nil
	mu.Unlock()
	select {
	case <-wake:
	default:
	}
	r := &recorder{}
	h := r.hooks()
	hooks.Store(&h)
	t.Cleanup(func() {
		hooks.Store(nil)
		_ = config.Update(func(c *config.Config) error { c.TZ = ""; return nil })
	})
	return r
}

// local : une date dans le fuseau de l'utilisateur.
func local(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, config.Location())
}

// now : l'instant de référence des tests, un dimanche.
func now() time.Time { return local(2026, 11, 15, 14, 30) }

func ptr[T any](v T) *T { return &v }

func mustSave(t *testing.T, in Input, at time.Time) Task {
	t.Helper()
	task, err := save(in, at)
	if err != nil {
		t.Fatalf("save : %v", err)
	}
	return task
}

// get rend la tâche telle que List la montre.
func get(t *testing.T, id string) View {
	t.Helper()
	for _, v := range List() {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("tâche %s absente de la liste", id)
	return View{}
}

// tick fait un tour d'échéancier à l'instant donné.
func tick(at time.Time) bool { return runDue(context.Background(), at) }

// addScript enregistre un outil au format du paquet scripts.
func addScript(t *testing.T, name, code string) {
	t.Helper()
	src := `/* TOOL {"description":"outil de test","params":{}} */` + "\n" + code + "\n"
	if err := home.WriteFile(filepath.Join(home.Tools(), name+".js"), []byte(src)); err != nil {
		t.Fatal(err)
	}
}

// ---- créer et modifier ----------------------------------------------------------------

func TestSaveCreates(t *testing.T) {
	r := setup(t)
	task := mustSave(t, Input{Prompt: ptr(" Rappeler à l'utilisateur d'appeler sa mère \nDeuxième ligne"), InMinutes: 45, Conv: ptr("c1")}, now())
	if task.ID == "" || task.Name != "Rappeler à l'utilisateur d'appeler sa mère" || !task.Enabled || task.Conv != "c1" || task.Quiet {
		t.Errorf("tâche créée : %+v", task)
	}
	if !task.Next.Equal(now().Add(45*time.Minute)) || !task.Schedule.At.Equal(task.Next) || !task.Created.Equal(now()) {
		t.Errorf("échéance : %+v", task)
	}
	if v := get(t, task.ID); v.ScheduleText != task.Schedule.text(time.Now()) || v.Running || v.ID != task.ID {
		t.Errorf("vue : %+v", v)
	}
	if _, changes := r.seen(); changes != 1 {
		t.Errorf("%d changements signalés, 1 attendu", changes)
	}

	// « HH:MM » déjà passé : demain. Une date complète est prise telle quelle.
	if task := mustSave(t, Input{Name: ptr("demain"), Prompt: ptr("p"), At: "09:00"}, now()); !task.Next.Equal(local(2026, 11, 16, 9, 0)) {
		t.Errorf("HH:MM passé : %v", task.Next)
	}
	if task := mustSave(t, Input{Prompt: ptr("p"), At: "2026-12-24T20:00"}, now()); !task.Next.Equal(local(2026, 12, 24, 20, 0)) {
		t.Errorf("date complète : %v", task.Next)
	}

	// Récurrente : ce qui n'est pas précisé vient de l'instant de création.
	weekly := mustSave(t, Input{Prompt: ptr("p"), Unit: "Weeks", Quiet: ptr(true)}, now())
	if s := weekly.Schedule; s.Every != 1 || s.Unit != "week" || s.Time != "14:30" || len(s.Days) != 1 || s.Days[0] != "sun" || !s.At.IsZero() {
		t.Errorf("calendrier hebdomadaire par défaut : %+v", s)
	}
	if !weekly.Next.Equal(local(2026, 11, 22, 14, 30)) || !weekly.Quiet {
		t.Errorf("tâche hebdomadaire : %+v", weekly)
	}
	monthly := mustSave(t, Input{Prompt: ptr("p"), Every: 2, Unit: "month", Time: "9h", Days: []string{"mon"}}, now())
	if s := monthly.Schedule; s.Day != 15 || s.Time != "09:00" || s.Days != nil || !monthly.Next.Equal(local(2026, 12, 15, 9, 0)) {
		t.Errorf("tâche mensuelle : %+v", monthly)
	}
	days := mustSave(t, Input{Prompt: ptr("p"), Unit: "week", Time: "08:00", Days: []string{"Vendredi", "monday", "fri", " WED "}}, now())
	if got := strings.Join(days.Schedule.Days, ","); got != "mon,wed,fri" {
		t.Errorf("jours normalisés : %s", got)
	}
}

func TestSaveRefused(t *testing.T) {
	setup(t)
	addScript(t, "meteo", `function run() { return "beau" }`)
	cases := map[string]Input{
		"ni consigne ni outil":   {InMinutes: 5},
		"consigne vide":          {Prompt: ptr("  "), InMinutes: 5},
		"consigne et outil":      {Prompt: ptr("p"), Tool: ptr("meteo"), InMinutes: 5},
		"outil inconnu":          {Tool: ptr("absent"), InMinutes: 5},
		"aucun moment":           {Prompt: ptr("p")},
		"deux moments":           {Prompt: ptr("p"), InMinutes: 5, Every: 1, Unit: "day"},
		"minutes négatives":      {Prompt: ptr("p"), InMinutes: -5},
		"date passée":            {Prompt: ptr("p"), At: "2026-11-15 14:00"},
		"date = instant":         {Prompt: ptr("p"), At: "2026-11-15 14:30"},
		"at illisible":           {Prompt: ptr("p"), At: "bientôt"},
		"every sans unit":        {Prompt: ptr("p"), Every: 2},
		"unit inconnu":           {Prompt: ptr("p"), Every: 2, Unit: "year"},
		"every négatif":          {Prompt: ptr("p"), Every: -1, Unit: "day"},
		"every démesuré":         {Prompt: ptr("p"), Every: 5000, Unit: "day"},
		"modèle chaque minute":   {Prompt: ptr("p"), Every: 4, Unit: "minute"},
		"heure illisible":        {Prompt: ptr("p"), Unit: "day", Time: "25:00"},
		"jour de semaine faux":   {Prompt: ptr("p"), Unit: "week", Days: []string{"mon", "xyz"}},
		"jour du mois faux":      {Prompt: ptr("p"), Unit: "month", Day: 32},
		"modification inconnue":  {ID: "t00000000", Name: ptr("x")},
		"désactivée mais passée": {Prompt: ptr("p"), At: "2026-11-01 08:00", Enabled: ptr(false)},
	}
	for label, in := range cases {
		if task, err := save(in, now()); err == nil {
			t.Errorf("%s : accepté (%+v), refus attendu", label, task)
		}
	}
	if list := List(); len(list) != 0 {
		t.Errorf("des tâches refusées ont été enregistrées : %+v", list)
	}

	// Une tâche d'outil peut tourner chaque minute, une tâche du modèle toutes les 5.
	mustSave(t, Input{Tool: ptr("Meteo"), Every: 1, Unit: "minute"}, now())
	mustSave(t, Input{Prompt: ptr("p"), Every: 5, Unit: "minutes"}, now())
}

func TestSaveLimit(t *testing.T) {
	setup(t)
	for i := range maxTasks {
		mustSave(t, Input{Name: ptr(fmt.Sprint("tâche ", i)), Prompt: ptr("p"), InMinutes: 5}, now())
	}
	if _, err := save(Input{Prompt: ptr("p"), InMinutes: 5}, now()); err == nil || !strings.Contains(err.Error(), "50") {
		t.Errorf("51ᵉ tâche : %v", err)
	}
	// La limite ne gêne pas une modification.
	mustSave(t, Input{ID: List()[0].ID, Name: ptr("renommée")}, now())
}

func TestSaveEdits(t *testing.T) {
	setup(t)
	addScript(t, "meteo", `function run() { return "beau" }`)
	task := mustSave(t, Input{Name: ptr("Résumé"), Prompt: ptr("Préparer le résumé"), Unit: "week", Days: []string{"mon"}, Time: "09:00", Conv: ptr("c1"), Quiet: ptr(true)}, now())
	monday := local(2026, 11, 16, 9, 0)
	if !task.Next.Equal(monday) {
		t.Fatalf("échéance de départ : %v", task.Next)
	}
	later := now().Add(time.Hour)

	// Un champ absent garde sa valeur, et l'échéance ne bouge pas.
	got := mustSave(t, Input{ID: task.ID, Name: ptr("Résumé de la semaine")}, later)
	if got.Name != "Résumé de la semaine" || got.Prompt != "Préparer le résumé" || got.Conv != "c1" || !got.Quiet || !got.Next.Equal(monday) || !got.Created.Equal(now()) {
		t.Errorf("renommer : %+v", got)
	}
	// « time » seul déplace l'heure et garde le reste du calendrier.
	got = mustSave(t, Input{ID: task.ID, Time: "10:15"}, later)
	if s := got.Schedule; s.Unit != "week" || len(s.Days) != 1 || s.Days[0] != "mon" || s.Time != "10:15" || !got.Next.Equal(local(2026, 11, 16, 10, 15)) {
		t.Errorf("changer l'heure : %+v", got)
	}
	// Changer d'unité écarte ce qui ne lui sert pas.
	got = mustSave(t, Input{ID: task.ID, Unit: "day"}, later)
	if s := got.Schedule; s.Unit != "day" || s.Days != nil || s.Time != "10:15" || !got.Next.Equal(local(2026, 11, 16, 10, 15)) {
		t.Errorf("changer d'unité : %+v", got)
	}
	// Suspendre puis reprendre : l'échéance repart de l'instant de la reprise.
	if got = mustSave(t, Input{ID: task.ID, Enabled: ptr(false)}, later); got.Enabled || !got.Next.IsZero() {
		t.Errorf("suspendre : %+v", got)
	}
	resumed := local(2026, 11, 20, 12, 0)
	if got = mustSave(t, Input{ID: task.ID, Enabled: ptr(true)}, resumed); !got.Enabled || !got.Next.Equal(local(2026, 11, 21, 10, 15)) {
		t.Errorf("reprendre : %+v", got)
	}
	// Donner un outil en fait une tâche d'outil ; donner une consigne, l'inverse.
	got = mustSave(t, Input{ID: task.ID, Tool: ptr("meteo"), Args: map[string]any{"city": "Lyon"}}, resumed)
	if got.Tool != "meteo" || got.Prompt != "" || got.Args["city"] != "Lyon" {
		t.Errorf("passer à un outil : %+v", got)
	}
	got = mustSave(t, Input{ID: task.ID, Prompt: ptr("Regarder la météo")}, resumed)
	if got.Tool != "" || got.Prompt != "Regarder la météo" || got.Args != nil {
		t.Errorf("revenir à une consigne : %+v", got)
	}
	// Passer à une tâche ponctuelle.
	got = mustSave(t, Input{ID: task.ID, InMinutes: 30}, resumed)
	if got.Schedule.Every != 0 || got.Schedule.Unit != "" || !got.Next.Equal(resumed.Add(30*time.Minute)) {
		t.Errorf("devenir ponctuelle : %+v", got)
	}
	// Une modification refusée ne laisse rien derrière elle.
	if _, err := save(Input{ID: task.ID, Name: ptr("cassée"), Every: 1, Unit: "minute"}, resumed); err == nil {
		t.Error("tâche du modèle chaque minute : refus attendu")
	}
	if v := get(t, task.ID); v.Name != "Résumé de la semaine" || v.Schedule.Every != 0 {
		t.Errorf("après une modification refusée : %+v", v.Task)
	}
	// Réactiver une ponctuelle dont le moment est passé demande un nouveau moment.
	mustSave(t, Input{ID: task.ID, Enabled: ptr(false)}, resumed)
	if _, err := save(Input{ID: task.ID, Enabled: ptr(true)}, resumed.Add(time.Hour)); err == nil {
		t.Error("réactiver une tâche ponctuelle passée : refus attendu")
	}
	if got = mustSave(t, Input{ID: task.ID, Enabled: ptr(true), At: "18:00"}, resumed.Add(time.Hour)); !got.Next.Equal(local(2026, 11, 20, 18, 0)) {
		t.Errorf("réactiver avec un nouveau moment : %+v", got)
	}
}

func TestListOrderAndDelete(t *testing.T) {
	r := setup(t)
	far := mustSave(t, Input{Name: ptr("loin"), Prompt: ptr("p"), InMinutes: 90}, now())
	off := mustSave(t, Input{Name: ptr("suspendue"), Prompt: ptr("p"), InMinutes: 5, Enabled: ptr(false)}, now())
	near := mustSave(t, Input{Name: ptr("proche"), Prompt: ptr("p"), InMinutes: 10}, now())
	offLater := mustSave(t, Input{Name: ptr("suspendue récente"), Prompt: ptr("p"), InMinutes: 5, Enabled: ptr(false)}, now().Add(time.Minute))

	var names []string
	for _, v := range List() {
		names = append(names, v.Name)
	}
	if got := strings.Join(names, ","); got != "proche,loin,suspendue récente,suspendue" {
		t.Errorf("ordre de la liste : %s", got)
	}
	if far.ID == near.ID || near.ID == off.ID || off.ID == offLater.ID {
		t.Error("identifiants en double")
	}

	_, before := r.seen()
	if err := Delete(near.ID); err != nil {
		t.Fatal(err)
	}
	if err := Delete(near.ID); err == nil {
		t.Error("supprimer deux fois : erreur attendue")
	}
	if _, after := r.seen(); after != before+1 {
		t.Errorf("suppression : %d changements signalés, 1 attendu", after-before)
	}
	if len(List()) != 3 {
		t.Errorf("après suppression : %d tâches", len(List()))
	}
}

func TestCorruptFileIsNotOverwritten(t *testing.T) {
	setup(t)
	if err := home.WriteFile(home.State("tasks.json"), []byte("{pas du json")); err != nil {
		t.Fatal(err)
	}
	if _, err := save(Input{Prompt: ptr("p"), InMinutes: 5}, now()); err == nil {
		t.Error("création sur un fichier illisible : erreur attendue")
	}
	if err := SetPaused(true); err == nil {
		t.Error("pause sur un fichier illisible : erreur attendue")
	}
	if err := RunNow("t1"); err == nil {
		t.Error("RunNow sur un fichier illisible : erreur attendue")
	}
	if tick(now()) {
		t.Error("un tour d'échéancier a lancé une tâche")
	}
	if list := List(); list == nil || len(list) != 0 {
		t.Errorf("List sur un fichier illisible : %v", list)
	}
	if raw, _ := os.ReadFile(home.State("tasks.json")); string(raw) != "{pas du json" {
		t.Errorf("le fichier illisible a été écrasé : %q", raw)
	}
}

// Une tâche porte une icône du jeu commun et une couleur : celles qu'on lui
// donne si elles sont valables, sinon l'horloge et la teinte neutre.
func TestIconAndColor(t *testing.T) {
	setup(t)
	plain := mustSave(t, Input{Name: ptr("Simple"), Prompt: ptr("p"), InMinutes: 30}, now())
	if plain.Icon != defaultIcon || plain.Color != "" {
		t.Errorf("tâche sans habillage : icône %q, couleur %q", plain.Icon, plain.Color)
	}
	dressed := mustSave(t, Input{Name: ptr("Vol"), Prompt: ptr("p"), InMinutes: 30, Icon: ptr("Plane"), Color: ptr("#2F7D5B")}, now())
	if dressed.Icon != "plane" || dressed.Color != "#2f7d5b" {
		t.Errorf("tâche habillée : icône %q, couleur %q", dressed.Icon, dressed.Color)
	}
	bad := mustSave(t, Input{ID: dressed.ID, Icon: ptr("✈️"), Color: ptr("bleu")}, now())
	if bad.Icon != "app" || bad.Color != "" {
		t.Errorf("habillage invalide : icône %q, couleur %q", bad.Icon, bad.Color)
	}
}
