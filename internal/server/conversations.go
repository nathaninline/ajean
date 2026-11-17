package server

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"ajean/internal/agent"
	"ajean/internal/config"
	"ajean/internal/llm"
	"ajean/internal/workspace"
)

const (
	maxUploadBytes = 30 << 20
	maxThreadItems = 500       // messages renvoyés à l'interface
	uploadsDir     = "uploads" // où tombent les pièces jointes, dans le WORKSPACE
)

// status : le statut HTTP d'une erreur de l'agent.
func status(err error) int {
	switch {
	case errors.Is(err, agent.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, agent.ErrBusy), errors.Is(err, agent.ErrSleeping):
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

// handleState (GET ?conv=) : l'état général, la liste des conversations et le
// fil de celle demandée (à défaut la plus récente).
func handleState(w http.ResponseWriter, r *http.Request) {
	c := config.Get()
	info, log, _ := agent.Thread(r.URL.Query().Get("conv"))
	if n := len(log); n > maxThreadItems {
		log = log[n-maxThreadItems:]
	}
	if log == nil {
		log = []agent.Item{}
	}
	state := chatState(c)
	state["convs"], state["conv"], state["title"] = agent.List(), info.ID, info.Title
	state["busy"], state["log"] = info.Busy, log
	state["used"], state["max"] = info.Used, agent.ContextWindow()
	reply(w, state)
}

func handleConvNew(w http.ResponseWriter, r *http.Request) {
	reply(w, map[string]string{"conv": agent.New()})
}

func handleConvDelete(w http.ResponseWriter, r *http.Request) {
	var q struct{ Conv string }
	if body(w, r, &q) {
		err := agent.Delete(q.Conv)
		done(w, err, status(err))
	}
}

func handleConvRename(w http.ResponseWriter, r *http.Request) {
	var q struct{ Conv, Title string }
	if body(w, r, &q) {
		err := agent.Rename(q.Conv, q.Title)
		done(w, err, status(err))
	}
}

func handleSend(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Conv, Text, TZ string
		Files          []string
	}
	if !body(w, r, &q) {
		return
	}
	text := strings.TrimSpace(q.Text)
	if text == "" && len(q.Files) == 0 {
		fail(w, http.StatusBadRequest, errors.New("message vide"))
		return
	}
	// Le fuseau du navigateur : c'est l'heure de l'utilisateur, pas celle de la machine.
	if tz := strings.TrimSpace(q.TZ); tz != "" && tz != config.Get().TZ {
		if _, err := time.LoadLocation(tz); err == nil {
			_ = config.Update(func(c *config.Config) error { c.TZ = tz; return nil })
		}
	}
	err := agent.Send(q.Conv, text, q.Files)
	if errors.Is(err, llm.ErrNotConfigured) {
		fail(w, http.StatusBadRequest, err)
		return
	}
	done(w, err, status(err))
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	var q struct{ Conv string }
	if body(w, r, &q) {
		agent.Stop(q.Conv)
		ok(w)
	}
}

// handleUpload range un fichier déposé par l'utilisateur dans WORKSPACE/uploads
// (le seul endroit où l'assistant peut le lire) et renvoie son chemin.
func handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	f, head, err := r.FormFile("file")
	if err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("fichier illisible ou trop gros (%d Mo max)", maxUploadBytes>>20))
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	name := safeName(head.Filename)
	path := uploadsDir + "/" + name
	for n := 2; workspace.Exists(path); n++ {
		ext := filepath.Ext(name)
		path = fmt.Sprintf("%s/%s-%d%s", uploadsDir, strings.TrimSuffix(name, ext), n, ext)
	}
	if err := workspace.WriteBytes(path, data); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	typ := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	reply(w, map[string]any{"path": path, "name": name, "size": len(data), "image": strings.HasPrefix(typ, "image/")})
}

// safeName : le nom d'un fichier déposé, sans dossier ni caractère gênant.
func safeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	if name = strings.Trim(name, ". "); name == "" {
		name = "fichier"
	}
	return name
}
