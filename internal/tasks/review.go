package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"ajean/internal/home"
	"ajean/internal/secrets"
)

// La validation des tâches : ce que le modèle crée, modifie ou supprime attend
// l'accord de l'utilisateur, donné une fois pour toute sa réponse. Une tâche non
// validée ne part pas. Ce que l'utilisateur fait lui-même dans l'interface est
// validé d'office.

func approvedPath() string { return home.State("approved", "tasks.json") }

func loadApproved() (map[string]Task, bool) {
	raw, err := os.ReadFile(approvedPath())
	if err != nil {
		return nil, false
	}
	out := map[string]Task{}
	if json.Unmarshal(raw, &out) != nil {
		return nil, false
	}
	return out, true
}

func storeApproved(m map[string]Task) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return home.WriteFile(approvedPath(), raw)
}

// Init reprend au premier démarrage les tâches déjà présentes.
func Init() error {
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(approvedPath()); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := load()
	if err != nil {
		return err
	}
	return storeApproved(snapshot(f))
}

func snapshot(f *file) map[string]Task {
	m := map[string]Task{}
	for _, t := range f.Tasks {
		m[t.ID] = t
	}
	return m
}

// same : deux tâches font-elles la même chose, au même moment ?
func same(a, b Task) bool {
	return a.Name == b.Name && a.Prompt == b.Prompt && a.Tool == b.Tool && a.Quiet == b.Quiet &&
		reflect.DeepEqual(jsonOf(a.Args), jsonOf(b.Args)) && jsonOf(a.Schedule) == jsonOf(b.Schedule) &&
		!(b.Enabled && !a.Enabled)
}

func jsonOf(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// validLocked : la tâche peut-elle partir ? (verrou tenu)
func validLocked(t Task) bool {
	m, ok := loadApproved()
	if !ok {
		return true // validation pas encore en place (premier démarrage, essais)
	}
	old, known := m[t.ID]
	return known && same(old, t)
}

// Change : une tâche qui attend la validation de l'utilisateur.
type Change struct {
	ID     string
	Name   string
	Kind   string // new, changed, deleted
	Detail string
}

// work : ce que fait la tâche, en clair.
func (t Task) work() string {
	if t.Tool != "" {
		return "lance l'outil « " + t.Tool + " » avec " + jsonOf(t.Args)
	}
	return t.Prompt
}

func (t Task) notice() string {
	if t.Quiet {
		return "seulement s'il y a quelque chose à signaler"
	}
	return "à chaque exécution"
}

// describe : tout ce que la tâche fera.
func describe(t Task, now time.Time) string {
	return "Quand : " + t.Schedule.text(now) + "\nCe qu'elle fera : " + t.work() + "\nVous prévient : " + t.notice()
}

// compare : seulement ce qui change.
func compare(a, b Task, now time.Time) string {
	var out []string
	add := func(label, x, y string) {
		if x != y {
			out = append(out, label+"\n  Avant : "+x+"\n  Après : "+y)
		}
	}
	add("Nom", a.Name, b.Name)
	add("Quand", a.Schedule.text(now), b.Schedule.text(now))
	add("Ce qu'elle fera", a.work(), b.work())
	add("Vous prévient", a.notice(), b.notice())
	if b.Enabled && !a.Enabled {
		out = append(out, "La tâche était suspendue : elle sera réactivée.")
	}
	return strings.Join(out, "\n\n")
}

// Changes : ce qui diffère de ce que l'utilisateur a validé.
func Changes() []Change {
	mu.Lock()
	defer mu.Unlock()
	m, ok := loadApproved()
	f, err := load()
	if !ok || err != nil {
		return nil
	}
	now := time.Now()
	var out []Change
	seen := map[string]bool{}
	for _, t := range f.Tasks {
		seen[t.ID] = true
		old, known := m[t.ID]
		switch {
		case !known:
			out = append(out, Change{t.ID, t.Name, "new", secrets.Redact(describe(t, now))})
		case !same(old, t):
			out = append(out, Change{t.ID, t.Name, "changed", secrets.Redact(compare(old, t, now))})
		}
	}
	for id, old := range m {
		if !seen[id] {
			out = append(out, Change{id, old.Name, "deleted", secrets.Redact("Cette tâche sera supprimée.\n" + describe(old, now))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Accept valide toutes les tâches telles qu'elles sont.
func Accept() error {
	mu.Lock()
	defer mu.Unlock()
	f, err := load()
	if err != nil {
		return err
	}
	return storeApproved(snapshot(f))
}

// Reject remet les tâches comme l'utilisateur les avait validées.
func Reject() error {
	now := time.Now()
	err := update(func(f *file) error {
		m, ok := loadApproved()
		if !ok {
			return nil
		}
		f.Tasks = slices.DeleteFunc(f.Tasks, func(t Task) bool {
			_, known := m[t.ID]
			if !known {
				halt(t.ID)
			}
			return !known
		})
		for id, old := range m {
			i := f.index(id)
			switch {
			case i < 0:
				if old.Enabled && old.Schedule.Every > 0 {
					old.Next = old.Schedule.next(old.Next, now)
				}
				f.Tasks = append(f.Tasks, old)
			case !same(old, f.Tasks[i]):
				runs := f.Tasks[i].Runs
				old.Runs = runs
				f.Tasks[i] = old
			}
		}
		return nil
	})
	if err == nil {
		changed()
	}
	return err
}

// SaveByUser : une tâche réglée par l'utilisateur lui-même est validée d'office,
// sans valider au passage une modification du modèle qui attendait.
func SaveByUser(in Input) (Task, error) {
	waiting := false
	if in.ID != "" {
		for _, c := range Changes() {
			waiting = waiting || c.ID == in.ID
		}
	}
	t, err := Save(in)
	if err != nil || waiting {
		return t, err
	}
	return t, approveOne(t.ID, &t)
}

// DeleteByUser : une suppression par l'utilisateur ne se valide pas.
func DeleteByUser(id string) error {
	if err := Delete(id); err != nil {
		return err
	}
	return approveOne(id, nil)
}

func approveOne(id string, t *Task) error {
	mu.Lock()
	defer mu.Unlock()
	m, ok := loadApproved()
	if !ok {
		return nil
	}
	if t == nil {
		delete(m, id)
	} else {
		m[id] = *t
	}
	if err := storeApproved(m); err != nil {
		return fmt.Errorf("validation de la tâche non enregistrée : %w", err)
	}
	return nil
}
