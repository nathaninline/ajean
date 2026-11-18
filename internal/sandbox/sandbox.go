// Package sandbox décrit le mode test : une seconde instance d'AJEAN, vierge et
// jetable, lancée depuis l'écran Machine pour voir comment un modèle se comporte
// avant de l'adopter.
//
// L'isolement est celui d'AJEAN, pas celui d'un conteneur : l'instance a ses propres
// données, et tout ce qui agirait hors de ses dossiers est refusé. Un appel refusé
// reste visible dans la conversation : on voit ce que le modèle a tenté.
package sandbox

import (
	"io"
	"os"
	"strings"

	"ajean/internal/home"
)

// Env : posée à "1" dans l'instance de test.
const Env = "AJEAN_SANDBOX"

// Active : ce processus est-il une instance de test ?
func Active() bool { return os.Getenv(Env) == "1" }

// Refused : l'outil est-il interdit en mode test ? Restent permis la mémoire et le
// WORKSPACE, tous deux propres à l'instance.
func Refused(name string) bool {
	switch name {
	case "tools", "browser", "app", "task_add", "task_edit", "task_delete":
		return true
	}
	return false
}

// routes : l'API de l'instance de test. Tout le reste n'y existe pas (services,
// réglages, mot de passe, exécution de tâches et d'applications).
var routes = map[string]bool{
	"GET /api/auth": true, "GET /api/events": true, "GET /api/state": true, "GET /api/search": true,
	"POST /api/convs/new": true, "POST /api/convs/delete": true, "POST /api/convs/rename": true,
	"POST /api/upload": true, "POST /api/send": true, "POST /api/stop": true, "POST /api/review": true,
	"GET /api/config": true, "POST /api/effort": true,
	"GET /api/memory": true, "POST /api/memory/profile": true, "GET /api/memory/fiche": true,
	"POST /api/memory/fiche": true, "POST /api/memory/fiche/delete": true,
	"POST /api/memory/lesson/delete": true, "POST /api/memory/rule/delete": true,
	"POST /api/memory/journal/delete": true,
	"GET /api/tasks": true, "GET /api/apps": true,
	"GET /api/files": true, "GET /api/files/get": true, "POST /api/files/upload": true,
	"POST /api/files/mkdir": true, "POST /api/files/rename": true, "POST /api/files/delete": true,
}

// RouteAllowed : cette route existe-t-elle dans l'instance de test ?
func RouteAllowed(pattern string) bool { return routes[pattern] }

// WatchParent arrête l'instance et efface ses données quand AJEAN disparaît :
// l'entrée standard, tenue par le parent, se ferme alors, même après un plantage.
func WatchParent() {
	if !Active() {
		return
	}
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		if dir := home.Dir(); strings.Contains(dir, "ajean-sandbox-") {
			_ = os.RemoveAll(dir)
		}
		os.Exit(0)
	}()
}
