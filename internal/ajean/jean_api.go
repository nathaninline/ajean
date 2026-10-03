package ajean

// jean_api.go — la fenêtre « Mémoire de Jean » de l'interface : corriger le profil,
// lire et modifier les fiches, parcourir, filtrer et nettoyer le journal. Passe par
// le serveur, qui seul a la clé : marche donc aussi avec la mémoire chiffrée.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

type jeanEntryJSON struct {
	ID   string `json:"id"`
	When int64  `json:"when"` // epoch ms
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// handleJeanMemory (GET ?q=&offset=&limit=) : profil, liste des fiches et journal
// (récent d'abord, filtré sans casse ni accents si q).
func handleJeanMemory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	jeanMu.Lock()
	facts, perr := jeanProfile()
	all := jeanJournalAll()
	jeanMu.Unlock()
	if perr != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": perr.Error()})
		return
	}
	needle := foldSearch(q.Get("q"))
	terms := uniqueTerms(needle)
	kept := all[:0:0]
	for _, e := range all {
		hay := foldSearch(e.Text + " " + e.Kind)
		ok := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				ok = false
				break
			}
		}
		if ok {
			kept = append(kept, e)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].When.After(kept[j].When) })
	total := len(kept)
	off, _ := strconv.Atoi(q.Get("offset"))
	lim, _ := strconv.Atoi(q.Get("limit"))
	if lim <= 0 || lim > 200 {
		lim = 50
	}
	off = max(0, min(off, total))
	page := kept[off:min(off+lim, total)]
	out := make([]jeanEntryJSON, 0, len(page))
	for _, e := range page {
		out = append(out, jeanEntryJSON{e.ID, e.When.UnixMilli(), e.Kind, e.Text})
	}
	prof := make([]map[string]string, 0, len(facts))
	used := 0
	for _, f := range facts {
		prof = append(prof, map[string]string{"key": f.Key, "value": f.Value})
		used += len("- " + f.Key + ": " + f.Value + "\n")
	}
	fiches := []map[string]any{}
	for _, f := range jeanFiches(false) {
		fiches = append(fiches, map[string]any{"name": f.Name, "when": f.When, "mod": f.ModTime})
	}
	lessons := []map[string]any{}
	lused := 0
	if ls, err := jeanLessons(); err == nil {
		for _, l := range ls {
			lessons = append(lessons, map[string]any{"n": l.N, "text": l.Text})
		}
		lused = len(renderJeanLessons(ls))
	}
	sendJSON(w, 200, map[string]any{"ok": true, "profile": prof, "profile_used": used, "fiches": fiches,
		"lessons": lessons, "lessons_used": lused, "lessons_max": jeanLessonsMaxChars,
		"profile_max": jeanProfileMaxChars, "journal": out, "total": total, "entries": len(all)})
}

// handleJeanProfile (POST {key, value, old_key}) : pose/remplace une ligne du
// profil ; value vide = la retirer ; old_key ≠ key = renommer. Le renommage écrit
// d'abord la nouvelle ligne : si elle est refusée, l'ancienne reste en place.
func handleJeanProfile(w http.ResponseWriter, r *http.Request) {
	var b map[string]string
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	key, value, oldKey := b["key"], b["value"], strings.TrimSpace(b["old_key"])
	var err error
	if strings.TrimSpace(value) == "" {
		_, err = jeanForgetFact(key)
	} else if _, err = JeanRemember(key, value); err == nil && oldKey != "" && normJeanKey(oldKey) != normJeanKey(key) {
		_, err = jeanForgetFact(oldKey)
	}
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleJeanJournalDelete (POST {id}) : supprime une entrée du journal.
func handleJeanJournalDelete(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := jeanDeleteEntry(strings.TrimSpace(b.ID)); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// jeanDeleteEntry retire l'entrée `id` du fichier de mois qui la contient.
func jeanDeleteEntry(id string) error {
	if id == "" {
		return fmt.Errorf("id manquant")
	}
	jeanMu.Lock()
	defer jeanMu.Unlock()
	ents, _ := os.ReadDir(jeanDir())
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, "journal-") || !strings.HasSuffix(n, ".md") {
			continue
		}
		s, err := jeanRead(n)
		if err != nil {
			continue
		}
		lines := strings.Split(s, "\n")
		start := -1
		for i, l := range lines {
			if m := jeanHeadRe.FindStringSubmatch(strings.TrimRight(l, "\r")); m != nil && m[2] == id {
				start = i
				break
			}
		}
		if start < 0 {
			continue
		}
		end := len(lines)
		for i := start + 1; i < len(lines); i++ {
			if jeanHeadRe.MatchString(strings.TrimRight(lines[i], "\r")) {
				end = i
				break
			}
		}
		// La ligne vide qui précède l'en-tête part avec l'entrée.
		if start > 0 && strings.TrimSpace(lines[start-1]) == "" {
			start--
		}
		out := append(append([]string{}, lines[:start]...), lines[end:]...)
		return jeanWrite(n, strings.Join(out, "\n"))
	}
	return fmt.Errorf("entrée introuvable")
}

// handleJeanFiche : GET ?name= renvoie une fiche ; POST {name, when, content,
// old_name} l'enregistre (old_name différent = renommage).
func handleJeanFiche(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		name := normFicheName(r.URL.Query().Get("name"))
		jeanMu.Lock()
		s, err := jeanRead(ficheFile(name))
		jeanMu.Unlock()
		if err != nil || s == "" {
			sendJSON(w, 404, map[string]any{"ok": false, "error": "fiche introuvable"})
			return
		}
		f := parseFiche(name, s)
		sendJSON(w, 200, map[string]any{"ok": true, "name": f.Name, "when": f.When, "content": f.Content})
		return
	}
	var b map[string]string
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if _, err := JeanSave(b["name"], b["when"], b["content"]); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if old := normFicheName(b["old_name"]); old != "" && old != normFicheName(b["name"]) {
		_ = jeanDeleteFiche(old)
	}
	sendJSON(w, 200, map[string]any{"ok": true, "name": normFicheName(b["name"])})
}

// handleJeanFicheDelete (POST {name}) : supprime une fiche.
func handleJeanFicheDelete(w http.ResponseWriter, r *http.Request) {
	var b map[string]string
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := jeanDeleteFiche(b["name"]); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleJeanLessonDelete (POST {n}) : retire la leçon lecon-N.
func handleJeanLessonDelete(w http.ResponseWriter, r *http.Request) {
	var b struct {
		N int `json:"n"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if _, _, err := jeanForgetLesson(fmt.Sprintf("lecon-%d", b.N)); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}
