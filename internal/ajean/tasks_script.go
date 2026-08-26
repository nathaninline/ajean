package ajean

// tasks_script.go — exécution d'une tâche « script seul » : on lance un script du
// dossier scriptsDir(), SANS charger le modèle ni consommer de tokens. La sortie
// du script devient le compte-rendu, exactement comme pour une tâche IA, si bien
// que l'UI et les outils task_* l'affichent sans traitement particulier.
//
// Une tâche script ne passe PAS par le verrou de génération de la conversation
// (elle n'utilise pas le modèle), donc conv.runningTaskID ne la voit pas. On tient
// donc un petit registre à part (scriptRunning) pour que l'UI l'affiche « en
// cours » et puisse l'arrêter.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// scriptTaskTimeout borne l'exécution d'un script planifié. Généreux (un backup ou
// une synchro peut durer), mais fini : une tâche coincée ne doit pas bloquer le
// planificateur à vie.
const scriptTaskTimeout = 10 * time.Minute

// scriptRunning suit les tâches script en cours (id → cancel + nom), pour que
// handleTasks les signale « en cours » et que l'arrêt (/api/tasks/stop) puisse
// annuler leur contexte.
var scriptRunning = struct {
	mu     sync.Mutex
	cancel map[string]context.CancelFunc
	name   map[string]string
}{cancel: map[string]context.CancelFunc{}, name: map[string]string{}}

func scriptRunBegin(id, name string, cancel context.CancelFunc) {
	scriptRunning.mu.Lock()
	scriptRunning.cancel[id] = cancel
	scriptRunning.name[id] = name
	scriptRunning.mu.Unlock()
}

func scriptRunEnd(id string) {
	scriptRunning.mu.Lock()
	delete(scriptRunning.cancel, id)
	delete(scriptRunning.name, id)
	scriptRunning.mu.Unlock()
}

// scriptRunningAny renvoie l'id et le nom d'une tâche script en cours (la
// première trouvée), ou "","" si aucune. Le scheduler ne lance qu'une tâche à la
// fois, donc en pratique il y en a au plus une.
func scriptRunningAny() (string, string) {
	scriptRunning.mu.Lock()
	defer scriptRunning.mu.Unlock()
	for id, n := range scriptRunning.name {
		return id, n
	}
	return "", ""
}

// scriptRunStop annule la tâche script en cours d'id `id`. Renvoie true si une
// exécution correspondante a été trouvée et annulée.
func scriptRunStop(id string) bool {
	scriptRunning.mu.Lock()
	c := scriptRunning.cancel[id]
	scriptRunning.mu.Unlock()
	if c != nil {
		c()
		return true
	}
	return false
}

// runScriptTask exécute le script d'une tâche et enregistre son état (comme runTask
// pour une tâche IA), mais sans aucune inférence.
func runScriptTask(t Task) {
	start := time.Now()

	name := strings.TrimSpace(t.Script)
	if name == "" {
		recordTaskEnd(t.ID, start, "", fmt.Errorf("aucun script associé à la tâche"))
		return
	}
	full, err := scriptsPath(name)
	if err != nil {
		recordTaskEnd(t.ID, start, "", err)
		return
	}
	if _, err := os.Stat(full); err != nil {
		recordTaskEnd(t.ID, start, "", fmt.Errorf("script introuvable : %s", name))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), scriptTaskTimeout)
	defer cancel()
	// Registre : rend la tâche visible « en cours » dans l'UI et arrêtable via
	// /api/tasks/stop (qui appelle scriptRunStop → cancel).
	scriptRunBegin(t.ID, t.Name, cancel)
	defer scriptRunEnd(t.ID)

	// On réutilise runShell : garde-fous, dossier de travail, troncature de sortie
	// et gestion du timeout y sont déjà. La sortie formatée ("exit: N ...") sert à
	// la fois de compte-rendu et de signal de succès.
	out := runShell(ctx, scriptRunCommand(full), int(scriptTaskTimeout/time.Second))
	switch {
	case ctx.Err() == context.Canceled:
		// Arrêt volontaire (bouton stop) : recordTaskEnd l'affiche « interrompue »
		// sans allumer l'indicateur d'échec.
		recordTaskEnd(t.ID, start, "", context.Canceled)
	case strings.HasPrefix(out, "exit: 0"):
		recordTaskEnd(t.ID, start, out, nil)
	default:
		// Sortie non nulle, timeout ou erreur de lancement : échec, avec la sortie
		// complète comme message d'erreur.
		recordTaskEnd(t.ID, start, "", fmt.Errorf("%s", out))
	}
}

// scriptRunCommand construit la ligne de commande qui exécute le script `path`
// selon la plateforme et l'extension. Sous Unix, on lance le fichier directement
// (il est écrit en 0755, le shebang choisit l'interpréteur). Sous Windows, un .ps1
// passe par PowerShell ; le reste (.bat/.cmd/.exe) se lance tel quel via cmd. Le
// nom de script est déjà bridé par scriptsPath (pas de guillemet ni de métacaractère
// shell), donc l'entourer de guillemets suffit.
func scriptRunCommand(path string) string {
	q := `"` + path + `"`
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(path), ".ps1") {
		return `powershell -NoProfile -ExecutionPolicy Bypass -File ` + q
	}
	return q
}
