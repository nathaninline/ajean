// Package server sert l'interface web d'AJEAN et son API HTTP locale.
//
//	server.go         routes, garde d'accès, flux d'événements, utilitaires JSON
//	conversations.go  conversations : état, envoi, arrêt, pièces jointes
//	settings.go       fournisseurs d'API, configurations, dictée
//	embed.go          recherche par le sens : essai d'un modèle, moteur local
//	memory.go         fenêtre « Mémoire » : profil, fiches, leçons, règles, journal,
//	                  outils, sommeil
//	tasks.go          écran « Tâches »
//	apps.go           écran « Applications » et pont des apps
//	files.go          écran « Fichiers » : le WORKSPACE
//	ui/               l'interface (embarquée dans le binaire)
package server

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"ajean/internal/agent"
)

//go:embed ui
var uiFiles embed.FS

const maxJSONBody = 4 << 20

// New : le gestionnaire HTTP complet (interface + API).
func New() http.Handler {
	ui, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		panic(err) // le dossier est embarqué à la compilation : ne peut pas manquer
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", page(http.FileServerFS(ui)))

	api := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, guard(h)) }
	api("GET /api/events", handleEvents)

	api("GET /api/state", handleState)
	api("POST /api/convs/new", handleConvNew)
	api("POST /api/convs/delete", handleConvDelete)
	api("POST /api/convs/rename", handleConvRename)
	api("POST /api/upload", handleUpload)
	api("POST /api/send", handleSend)
	api("POST /api/stop", handleStop)

	api("GET /api/config", handleConfigGet)
	api("POST /api/config", handleConfigSave)
	api("POST /api/providers/save", handleProviderSave)
	api("POST /api/providers/delete", handleProviderDelete)
	api("POST /api/providers/models", handleProviderModels)
	api("POST /api/providers/test", handleProviderTest)
	api("POST /api/presets/save", handlePresetSave)
	api("POST /api/presets/delete", handlePresetDelete)
	api("POST /api/presets/select", handlePresetSelect)
	api("POST /api/effort", handleEffortChoose)
	api("POST /api/transcribe", handleTranscribe)
	api("POST /api/embed/test", handleEmbedTest)
	api("GET /api/embed/local", handleEmbedLocal)
	api("POST /api/embed/local/install", handleEmbedInstall)
	api("POST /api/embed/local/cancel", handleEmbedCancel)
	api("POST /api/embed/local/remove", handleEmbedRemove)

	api("GET /api/memory", handleMemory)
	api("POST /api/memory/profile", handleProfile)
	api("GET /api/memory/fiche", handleFicheGet)
	api("POST /api/memory/fiche", handleFicheSave)
	api("POST /api/memory/fiche/delete", handleFicheDelete)
	api("POST /api/memory/lesson/delete", handleLessonDelete)
	api("POST /api/memory/rule/delete", handleRuleDelete)
	api("POST /api/memory/journal/delete", handleJournalDelete)
	api("POST /api/tools/delete", handleToolDelete)
	api("POST /api/sleep", handleSleep)
	api("POST /api/sleep/restore", handleSleepRestore)

	api("GET /api/tasks", handleTasks)
	api("POST /api/tasks/save", handleTaskSave)
	api("POST /api/tasks/delete", handleTaskDelete)
	api("POST /api/tasks/run", handleTaskRun)
	api("POST /api/tasks/stop", handleTaskStop)
	api("POST /api/tasks/pause", handleTasksPause)

	api("GET /api/files", handleFiles)
	api("GET /api/files/get", handleFileGet)
	api("POST /api/files/upload", handleFilesUpload)
	api("POST /api/files/mkdir", handleFilesMkdir)
	api("POST /api/files/rename", handleFilesRename)
	api("POST /api/files/delete", handleFilesDelete)

	api("GET /api/apps", handleApps)
	api("GET /api/apps/page", handleAppPage)
	api("POST /api/apps/delete", handleAppDelete)
	api("GET /api/apps/versions", handleAppVersions)
	api("POST /api/apps/rollback", handleAppRollback)
	api("POST /api/apps/bridge", handleAppBridge)
	return mux
}

// page sert l'interface avec deux consignes pour le navigateur :
//   - ne pas resservir une ancienne version (l'interface est embarquée dans le
//     binaire et change avec lui) ;
//   - interdire à tout cadre de la page de charger une adresse. Les apps
//     s'affichent dans un cadre construit sur place, qui n'en a pas besoin ;
//     sans cela, une app pourrait faire naviguer son cadre vers un site et y
//     faire sortir des données dans l'adresse.
func page(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy", "frame-src 'none'")
		h.ServeHTTP(w, r)
	})
}

// guard protège l'API contre une page d'un autre site ouverte dans le même
// navigateur, qui ne doit pas pouvoir parler à l'assistant :
//   - sans clé d'accès, la requête doit viser la machine elle-même par son nom
//     local. Sinon un site pourrait faire pointer son propre nom vers 127.0.0.1
//     (DNS rebinding) et passer pour nous ;
//   - l'origine, quand le navigateur la donne, doit être la nôtre ;
//   - une écriture doit être en JSON, ou un envoi de fichier venant de notre page ;
//   - si AJEAN_WEB_KEY est définie (accès depuis le réseau), elle est exigée.
func guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := os.Getenv("AJEAN_WEB_KEY")
		if key == "" && !localHost(r.Host) {
			fail(w, http.StatusForbidden, errors.New("hôte refusé"))
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
				fail(w, http.StatusForbidden, errors.New("origine refusée"))
				return
			}
		}
		if r.Method == http.MethodPost {
			ct := r.Header.Get("Content-Type")
			isJSON := strings.HasPrefix(ct, "application/json")
			isUpload := strings.HasPrefix(ct, "multipart/form-data") && origin != ""
			if !isJSON && !isUpload {
				fail(w, http.StatusUnsupportedMediaType, errors.New("JSON attendu"))
				return
			}
		}
		if key != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got == "" {
				got = r.URL.Query().Get("key") // EventSource ne sait pas poser d'en-tête
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
				fail(w, http.StatusUnauthorized, errors.New("clé d'accès requise"))
				return
			}
		}
		next(w, r)
	}
}

// localHost : l'en-tête Host désigne-t-il la machine elle-même ?
func localHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// handleEvents : le flux en direct (Server-Sent Events) de tout ce qui se passe.
func handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, errors.New("flux non supporté"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	events, cancel := agent.Subscribe()
	defer cancel()
	fmt.Fprint(w, ": ok\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second) // garde la connexion ouverte à travers les proxys
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-events:
			fmt.Fprintf(w, "data: %s\n\n", ev)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		fl.Flush()
	}
}

// ---- utilitaires JSON -----------------------------------------------------------

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter) { reply(w, map[string]bool{"ok": true}) }

func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// done répond « ok », ou l'erreur avec le statut donné.
func done(w http.ResponseWriter, err error, code int) {
	if err != nil {
		fail(w, code, err)
		return
	}
	ok(w)
}

// body décode le corps JSON d'une requête dans v (false : la réponse d'erreur est partie).
func body(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, errors.New("requête illisible"))
		return false
	}
	return true
}
