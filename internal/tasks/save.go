package tasks

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"ajean/internal/icons"
	"ajean/internal/scripts"
)

// defaultIcon : l'icône d'une tâche à laquelle personne n'en a choisi.
const defaultIcon = "clock"

// Input : ce que l'utilisateur (interface) ou le modèle (outil) donne pour
// créer ou modifier une tâche. Pour une modification (ID non vide), un champ
// absent garde sa valeur.
type Input struct {
	ID        string         `json:"id"`
	Name      *string        `json:"name"`
	Prompt    *string        `json:"prompt"`
	Tool      *string        `json:"tool"`
	Args      map[string]any `json:"args"`
	Conv      *string        `json:"conv"`
	Quiet     *bool          `json:"quiet"`
	Icon      *string        `json:"icon"`
	Color     *string        `json:"color"`
	Enabled   *bool          `json:"enabled"`
	InMinutes int            `json:"in_minutes"` // ponctuelle : dans N minutes
	At        string         `json:"at"`         // ponctuelle : « HH:MM » (prochaine occurrence) ou « AAAA-MM-JJ HH:MM »
	Every     int            `json:"every"`      // récurrente
	Unit      string         `json:"unit"`
	Time      string         `json:"time"`
	Days      []string       `json:"days"`
	Day       int            `json:"day"`
}

// Save crée une tâche (ID vide) ou en modifie une, après avoir tout validé, et
// calcule sa prochaine exécution.
func Save(in Input) (Task, error) { return save(in, time.Now()) }

func save(in Input, now time.Time) (Task, error) {
	var saved Task
	err := update(func(f *file) error {
		t := Task{Enabled: true, Created: now, Icon: defaultIcon}
		slot := -1
		if in.ID != "" {
			if slot = f.index(in.ID); slot < 0 {
				return errNotFound
			}
			t = f.Tasks[slot]
		} else if len(f.Tasks) >= maxTasks {
			return fmt.Errorf("%d tâches au plus : supprime-en une (task_delete) avant d'en ajouter", maxTasks)
		}
		wasEnabled := slot >= 0 && t.Enabled

		if err := in.work(&t); err != nil {
			return err
		}
		schedule, rescheduled, err := in.schedule(t.Schedule, now)
		if err != nil {
			return err
		}
		if err := schedule.allowed(t.Prompt != ""); err != nil {
			return err
		}
		t.Schedule = schedule
		if in.Conv != nil {
			t.Conv = strings.TrimSpace(*in.Conv)
		}
		if in.Icon != nil {
			t.Icon = icons.Icon(*in.Icon)
		}
		if in.Color != nil {
			t.Color = icons.Color(*in.Color)
		}
		if in.Quiet != nil {
			t.Quiet = *in.Quiet
		}
		if in.Enabled != nil {
			t.Enabled = *in.Enabled
		}
		// Sans nouveau calendrier, une tâche active garde son échéance : la
		// renommer ne doit pas décaler son rythme.
		if rescheduled || (t.Enabled && !wasEnabled) {
			if t.Next, err = t.Schedule.start(now); err != nil {
				return err
			}
			t.Retried = false
		}
		if !t.Enabled {
			t.Next = time.Time{}
		}

		if slot >= 0 {
			f.Tasks[slot] = t
		} else {
			if t.ID, err = newID(); err != nil {
				return err
			}
			f.Tasks = append(f.Tasks, t)
		}
		saved = t
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	changed()
	return saved, nil
}

// work pose le nom de la tâche et ce qu'elle fait : une consigne pour le
// modèle OU un outil avec ses paramètres.
func (in Input) work(t *Task) error {
	prompt, tool := t.Prompt, t.Tool
	if in.Prompt != nil {
		prompt = strings.TrimSpace(*in.Prompt)
	}
	if in.Tool != nil {
		tool = strings.ToLower(strings.TrimSpace(*in.Tool))
	}
	// Donner l'un fait changer la tâche de nature : l'autre, s'il n'est pas
	// redonné, s'efface.
	if in.Prompt != nil && prompt != "" && in.Tool == nil {
		tool = ""
	}
	if in.Tool != nil && tool != "" && in.Prompt == nil {
		prompt = ""
	}
	switch {
	case prompt != "" && tool != "":
		return errors.New("donne 'prompt' (tâche du modèle) OU 'tool' (tâche d'outil), pas les deux")
	case prompt == "" && tool == "":
		return errors.New("dis ce que fait la tâche : 'prompt' (une consigne pour le modèle) ou 'tool' (le nom d'un de ses outils)")
	}
	if in.Tool != nil && tool != "" {
		if err := known(tool); err != nil {
			return err
		}
	}
	t.Prompt, t.Tool = prompt, tool
	if in.Args != nil {
		t.Args = in.Args
	}
	if tool == "" || len(t.Args) == 0 {
		t.Args = nil
	}

	if in.Name != nil {
		t.Name = strings.TrimSpace(*in.Name)
	}
	if t.Name == "" {
		t.Name = tool
		if prompt != "" {
			line, _, _ := strings.Cut(prompt, "\n")
			t.Name = strings.TrimSpace(line)
		}
	}
	if name := []rune(t.Name); len(name) > maxName {
		t.Name = strings.TrimSpace(string(name[:maxName-1])) + "…"
	}
	return nil
}

// known vérifie que l'outil existe : mieux vaut l'apprendre à la création
// qu'à la première exécution.
func known(tool string) error {
	var names []string
	for _, s := range scripts.List() {
		if s.Name == tool {
			return nil
		}
		names = append(names, s.Name)
	}
	if len(names) == 0 {
		return fmt.Errorf("outil « %s » introuvable : aucun outil n'est enregistré", tool)
	}
	return fmt.Errorf("outil « %s » introuvable. Outils existants : %s", tool, strings.Join(names, ", "))
}

// schedule rend le calendrier demandé, et dit s'il remplace l'ancien (old, vide
// pour une création). Les champs d'une récurrence non redonnés gardent leur
// valeur : « time » seul déplace l'heure d'une tâche hebdomadaire.
func (in Input) schedule(old Schedule, now time.Time) (Schedule, bool, error) {
	once := in.InMinutes != 0 || strings.TrimSpace(in.At) != ""
	recurring := in.Every != 0 || in.Unit != "" || in.Time != "" || len(in.Days) > 0 || in.Day != 0
	switch {
	case once && recurring:
		return Schedule{}, false, errors.New("donne soit in_minutes ou at (une seule fois), soit every + unit (régulièrement), pas les deux")
	case in.InMinutes < 0:
		return Schedule{}, false, errors.New("'in_minutes' : un nombre de minutes positif")
	case in.InMinutes > 0:
		return Schedule{At: now.Add(time.Duration(in.InMinutes) * time.Minute)}, true, nil
	case once:
		at, err := parseAt(in.At, now)
		return Schedule{At: at}, true, err
	case !recurring:
		if old.Every == 0 && old.At.IsZero() {
			return Schedule{}, false, errors.New("dis quand : in_minutes ou at (une seule fois), ou every + unit (régulièrement)")
		}
		return old, false, nil
	}
	s := Schedule{}
	if old.Every > 0 {
		s = old
	}
	if in.Every != 0 {
		s.Every = in.Every
	}
	if in.Unit != "" {
		s.Unit = in.Unit
	}
	if in.Time != "" {
		s.Time = in.Time
	}
	if len(in.Days) > 0 {
		s.Days = in.Days
	}
	if in.Day != 0 {
		s.Day = in.Day
	}
	if s.Every == 0 && s.Unit != "" {
		s.Every = 1 // « unit: day » seul : tous les jours
	}
	s, err := s.completed(now)
	return s, true, err
}
