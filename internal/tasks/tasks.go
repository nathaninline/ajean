// Package tasks tient les tâches d'AJEAN : ce qu'il fait tout seul, à un moment
// donné ou régulièrement, et dont il rend compte à l'utilisateur. Un rappel,
// une veille, un travail récurrent sont la même chose : une tâche.
//
// Une tâche travaille de l'une de ces deux façons :
//
//   - tâche du modèle (Prompt) : l'assistant reçoit la consigne et travaille
//     seul, avec ses outils, dans un fil isolé des conversations (crochet Run) ;
//   - tâche d'outil (Tool, Args) : un des outils que l'assistant s'est
//     fabriqués est lancé directement, sans le modèle. Ce qu'il renvoie est le
//     compte rendu, et l'exécution ne coûte aucun jeton.
//
// Le compte rendu est ensuite remis à l'utilisateur (crochet Deliver). Une
// tâche silencieuse (Quiet) ne le remet que s'il y a quelque chose à dire.
//
// Tout est rangé dans .ajean/tasks.json : la liste des tâches, leurs dernières
// exécutions et l'interrupteur général.
package tasks

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"ajean/internal/home"
)

// Nothing : ce que répond une tâche silencieuse quand elle n'a rien à signaler.
const Nothing = "NOTHING"

const (
	maxTasks  = 50
	maxRuns   = 10   // exécutions gardées par tâche
	maxReport = 4000 // caractères d'un compte rendu gardé
	maxName   = 80   // caractères d'un nom
)

// Ce qu'a donné une exécution.
const (
	statusOK      = "ok"
	statusNothing = "rien"
	statusError   = "erreur"
	statusStopped = "arrêtée"
)

// Schedule : quand une tâche s'exécute, une fois (At) ou régulièrement.
type Schedule struct {
	At    time.Time `json:"at"`              // tâche ponctuelle : l'instant (zéro pour une tâche récurrente)
	Every int       `json:"every,omitempty"` // tâche récurrente : tous les Every…
	Unit  string    `json:"unit,omitempty"`  // …minute | hour | day | week | month
	Time  string    `json:"time,omitempty"`  // « HH:MM », heure de l'utilisateur (unités day, week, month)
	Days  []string  `json:"days,omitempty"`  // unité week : mon, tue, wed, thu, fri, sat, sun
	Day   int       `json:"day,omitempty"`   // unité month : jour du mois voulu (un 31 reste un 31 après février)
}

// Run : une exécution passée.
type Run struct {
	At      time.Time `json:"at"`
	Status  string    `json:"status"` // ok | rien | erreur | arrêtée
	Report  string    `json:"report"` // compte rendu ou message d'erreur, borné
	Seconds float64   `json:"seconds"`
}

// Task : une tâche.
type Task struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Prompt   string         `json:"prompt,omitempty"` // tâche du modèle : ce qu'il doit faire
	Tool     string         `json:"tool,omitempty"`   // OU tâche d'outil : le nom de l'outil…
	Args     map[string]any `json:"args,omitempty"`   // …et ses paramètres
	Schedule Schedule       `json:"schedule"`
	Conv     string         `json:"conv,omitempty"` // conversation où livrer le compte rendu
	Quiet    bool           `json:"quiet,omitempty"`
	Icon     string         `json:"icon"`  // nom d'une icône du jeu commun (paquet icons)
	Color    string         `json:"color"` // couleur choisie pour la tâche, #rrggbb ("" : teinte neutre)
	Enabled  bool           `json:"enabled"`
	Next     time.Time      `json:"next"`              // prochaine exécution (zéro : plus aucune)
	Retried  bool           `json:"retried,omitempty"` // ponctuelle en échec : sa seconde chance est déjà programmée
	Runs     []Run          `json:"runs,omitempty"`    // les 10 dernières, la plus récente d'abord
	Created  time.Time      `json:"created"`
}

// View : une tâche telle que l'interface l'affiche.
type View struct {
	Task
	ScheduleText string `json:"schedule_text"` // en français : « tous les lundis à 09:00 »
	Running      bool   `json:"running"`
}

// file : ce que contient tasks.json.
type file struct {
	Paused bool   `json:"paused"`
	Tasks  []Task `json:"tasks"`
}

var (
	// mu protège tasks.json et l'état en mémoire ci-dessous : l'échéancier,
	// l'interface et les outils du modèle y touchent en même temps.
	mu      sync.Mutex
	current *execution // l'exécution en cours
	forced  []string   // tâches à lancer dès que possible (RunNow), dans l'ordre des demandes

	hooks atomic.Pointer[Hooks]
	wake  = make(chan struct{}, 1) // réveille l'échéancier sans attendre son prochain tour
)

var errNotFound = errors.New("tâche introuvable (task_list donne les ids)")

func path() string { return home.State("tasks.json") }

// load lit tasks.json (verrou tenu). Un fichier illisible est une erreur et non
// une liste vide : l'écriture suivante effacerait toutes les tâches.
func load() (*file, error) {
	f := &file{}
	raw, err := os.ReadFile(path())
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, f); err != nil {
		return nil, fmt.Errorf("tasks.json illisible (%w) : corrige-le ou supprime-le", err)
	}
	return f, nil
}

// store écrit tasks.json (verrou tenu).
func store(f *file) error {
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return home.WriteFile(path(), raw)
}

// update applique une modification et l'enregistre, sauf si fn la refuse.
func update(fn func(f *file) error) error {
	mu.Lock()
	defer mu.Unlock()
	f, err := load()
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		return err
	}
	return store(f)
}

func (f *file) index(id string) int {
	return slices.IndexFunc(f.Tasks, func(t Task) bool { return t.ID == id })
}

func (f *file) find(id string) *Task {
	if i := f.index(id); i >= 0 {
		return &f.Tasks[i]
	}
	return nil
}

func newID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("t%x", b), nil
}

func view(t Task, now time.Time, running bool) View {
	return View{Task: t, ScheduleText: t.Schedule.text(now), Running: running}
}

// List : les prochaines d'abord, les tâches sans prochaine exécution à la fin
// (les plus récentes en tête).
func List() []View {
	mu.Lock()
	f, err := load()
	running := ""
	if current != nil {
		running = current.id
	}
	mu.Unlock()
	if err != nil {
		log.Printf("tâches : %v", err)
		return []View{}
	}
	slices.SortStableFunc(f.Tasks, func(a, b Task) int {
		switch {
		case a.Next.IsZero() && b.Next.IsZero():
			return b.Created.Compare(a.Created)
		case a.Next.IsZero():
			return 1
		case b.Next.IsZero():
			return -1
		}
		return a.Next.Compare(b.Next)
	})
	now := time.Now()
	views := make([]View, len(f.Tasks))
	for i, t := range f.Tasks {
		views[i] = view(t, now, t.ID == running)
	}
	return views
}

// Delete supprime une tâche, et arrête son exécution si elle est en cours.
func Delete(id string) error {
	err := update(func(f *file) error {
		i := f.index(id)
		if i < 0 {
			return errNotFound
		}
		f.Tasks = slices.Delete(f.Tasks, i, i+1)
		halt(id)
		return nil
	})
	if err == nil {
		changed()
	}
	return err
}

// Paused : l'interrupteur général est-il mis ? Plus rien ne part tant qu'il l'est.
func Paused() bool {
	mu.Lock()
	defer mu.Unlock()
	f, err := load()
	return err == nil && f.Paused
}

// SetPaused met ou enlève l'interrupteur général.
func SetPaused(paused bool) error { return setPaused(paused, time.Now()) }

func setPaused(paused bool, now time.Time) error {
	err := update(func(f *file) error {
		if paused {
			forced = nil
		}
		if f.Paused && !paused {
			// Sans cela, toutes les tâches récurrentes échues pendant la pause
			// partiraient en rafale à la reprise. Les ponctuelles, elles, restent
			// dues : un rappel en retard vaut mieux qu'un rappel perdu.
			for i := range f.Tasks {
				t := &f.Tasks[i]
				if t.Enabled && t.Schedule.Every > 0 && !t.Next.IsZero() {
					t.Next = t.Schedule.next(t.Next, now)
				}
			}
		}
		f.Paused = paused
		return nil
	})
	if err == nil {
		changed()
	}
	return err
}
