package ajean

// tasks.go — les tâches planifiées : des consignes que l'IA exécute toute seule,
// en arrière-plan, sur une fréquence réglable (intervalle simple ou cron). Chaque
// tâche tourne ISOLÉE de la conversation partagée (voir RunAutonomous dans
// tasks_run.go) : elle ne pollue pas le chat et livre son résultat via les propres
// outils de l'IA (mail via MCP, shell, web…). L'utilisateur peut désactiver une
// tâche, ou tout suspendre d'un coup via l'interrupteur maître (tasks_paused).

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Task est une tâche planifiée persistée dans le bucket bkTasks (clé = ID).
type Task struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Prompt   string `json:"prompt"`
	Schedule string `json:"schedule"` // "@every 2h" | "@every 1d@23:00" | expr cron 5 champs
	TZ       string `json:"tz"`       // fuseau IANA du navigateur (ex. "Europe/Paris") pour l'heure
	Preset   string `json:"preset"`   // id du preset à activer avant l'exécution (vide = preset actif)
	// Accès de la tâche. On stocke la NÉGATION (« pas de… ») pour que le zéro JSON
	// (tâches créées avant ces champs) garde le comportement historique : accès.
	NoMem      bool   `json:"no_mem"` // true = pas d'accès à la mémoire pour cette tâche
	NoWeb      bool   `json:"no_web"` // true = pas d'accès au web pour cette tâche
	Enabled    bool   `json:"enabled"`
	LastRun    int64  `json:"last_run"`    // epoch ms, 0 = jamais
	NextRun    int64  `json:"next_run"`    // epoch ms, 0 = à (re)calculer
	LastDurMs  int64  `json:"last_dur_ms"` // durée du dernier passage en ms
	LastOK     bool   `json:"last_ok"`
	LastError  string `json:"last_error"`
	LastReport string `json:"last_report"` // dernier texte final de l'IA (compte-rendu court)

	Running bool `json:"-"` // transitoire : une exécution est en cours
}

// newTaskID génère un identifiant court et unique.
func newTaskID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// listTasks renvoie toutes les tâches, triées par nom pour un affichage stable.
func listTasks() []Task {
	var out []Task
	_ = view(bkTasks, func(b *bolt.Bucket) error {
		return b.ForEach(func(_, v []byte) error {
			var t Task
			if json.Unmarshal(v, &t) == nil {
				out = append(out, t)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// getTask lit une tâche par ID. Renvoie false si absente.
func getTask(id string) (Task, bool) {
	var t Task
	ok := getJSON(bkTasks, id, &t)
	return t, ok
}

// saveTask enregistre (crée ou remplace) une tâche.
func saveTask(t Task) error { return putJSON(bkTasks, t.ID, t) }

// deleteTask supprime une tâche par ID.
func deleteTask(id string) error { return putBytes(bkTasks, id, nil) }

// tasksPaused indique si l'interrupteur maître suspend TOUTES les tâches. On
// stocke « en pause » plutôt que « actif » pour que l'état par défaut (base
// neuve) soit « actif » sans dépendre d'une écriture : c'est un frein, pas un
// moteur.
func tasksPaused() bool { return getBool(bkState, "tasks_paused") }

func setTasksPaused(on bool) error { return putBool(bkState, "tasks_paused", on) }

// computeNextRun calcule le prochain déclenchement d'une tâche à partir de son
// Schedule (dans le fuseau tz), relatif à `from`. Renvoie 0 si le schedule est
// invalide (la tâche ne se déclenchera alors jamais — l'UI refuse déjà un
// schedule invalide à la sauvegarde, ceci n'est qu'un garde-fou).
func computeNextRun(schedule, tz string, from time.Time) int64 {
	next, err := nextAfter(schedule, tz, from)
	if err != nil {
		return 0
	}
	return next.UnixMilli()
}
