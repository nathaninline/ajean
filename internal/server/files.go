package server

// files.go — écran « Fichiers » : le WORKSPACE, et lui seul, parcouru, rempli
// et vidé depuis l'interface. Tous les accès passent par le paquet workspace
// (os.Root) : aucun chemin reçu ne peut en sortir.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"ajean/internal/workspace"
)

// handleFiles (GET ?path=) : le contenu d'un dossier du workspace.
func handleFiles(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	entries, err := workspace.Entries(p)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	reply(w, map[string]any{"path": strings.Trim(path.Clean("/"+strings.ReplaceAll(p, `\`, "/")), "/"), "entries": orEmpty(entries)})
}

// handleFileGet (GET ?path=&dl=1) : le fichier lui-même, affiché ou téléchargé.
func handleFileGet(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	f, info, err := workspace.OpenFile(p)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	defer f.Close()
	name := path.Base(strings.ReplaceAll(p, `\`, "/"))
	disp := "inline"
	if r.URL.Query().Get("dl") != "" {
		disp = "attachment"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disp, url.PathEscape(name)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Un fichier du workspace ne doit jamais s'exécuter comme une page de l'app.
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// handleFilesUpload (multipart : dir, file…) : dépose des fichiers dans un
// dossier ; un nom déjà pris devient « nom-2.ext ».
func handleFilesUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes*4)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("envoi illisible ou trop gros (%d Mo max)", maxUploadBytes*4>>20))
		return
	}
	dir := strings.Trim(strings.ReplaceAll(r.FormValue("dir"), `\`, "/"), "/")
	var saved []string
	for _, head := range r.MultipartForm.File["file"] {
		f, err := head.Open()
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		name := safeName(head.Filename)
		p := path.Join(dir, name)
		for n := 2; workspace.Exists(p); n++ {
			ext := filepath.Ext(name)
			p = path.Join(dir, fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, ext), n, ext))
		}
		if err := workspace.WriteBytes(p, data); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		saved = append(saved, p)
	}
	reply(w, map[string]any{"saved": orEmpty(saved)})
}

// cleanName : un nom tapé dans l'app, sans chemin ni caractère interdit.
func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\:*?"<>|`) {
		return "", errors.New(`nom non valide (sans / \ : * ? " < > |)`)
	}
	return name, nil
}

// handleFilesMkdir (POST {dir, name}).
func handleFilesMkdir(w http.ResponseWriter, r *http.Request) {
	var q struct{ Dir, Name string }
	if !body(w, r, &q) {
		return
	}
	name, err := cleanName(q.Name)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	done(w, workspace.Mkdir(path.Join(q.Dir, name)), http.StatusBadRequest)
}

// handleFilesRename (POST {path, name}) : renomme sur place.
func handleFilesRename(w http.ResponseWriter, r *http.Request) {
	var q struct{ Path, Name string }
	if !body(w, r, &q) {
		return
	}
	name, err := cleanName(q.Name)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	p := strings.ReplaceAll(q.Path, `\`, "/")
	done(w, workspace.Move(p, path.Join(path.Dir(p), name)), http.StatusBadRequest)
}

// handleFilesDelete (POST {path}).
func handleFilesDelete(w http.ResponseWriter, r *http.Request) {
	var q struct{ Path string }
	if body(w, r, &q) {
		done(w, workspace.Delete(q.Path), http.StatusBadRequest)
	}
}

