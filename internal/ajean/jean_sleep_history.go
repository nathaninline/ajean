package ajean

// jean_sleep_history.go — l'historique des sommeils, vu et réversible par la
// personne : ce que chaque sommeil a changé (fiches fusionnées, archivées,
// lignes ajoutées et retirées) et un retour à l'état d'avant. Les copies sont
// prises par jeanSnapMemory, une par sommeil, dans consolidation-avant/<id>/.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const jeanSleepKeep = 14 // copies datées gardées

var jeanLastSnapID string // copie prise au début du sommeil en cours

type jeanSleepMeta struct {
	ID      string `json:"id"`
	When    int64  `json:"when"` // epoch ms
	Kind    string `json:"kind"` // sommeil | restauration
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

const jeanSleepMetaFile = "meta.json"

func jeanSleepDir(id string) string { return filepath.Join(jeanDir(), jeanGuardDir, id) }

// jeanPruneSleepsLocked garde les jeanSleepKeep copies les plus récentes, et retire
// l'ancienne copie unique (fichiers posés à plat avant cette version).
func jeanPruneSleepsLocked() {
	root := filepath.Join(jeanDir(), jeanGuardDir)
	ents, _ := os.ReadDir(root)
	var ids []string
	for _, e := range ents {
		if e.IsDir() {
			ids = append(ids, e.Name())
		} else {
			_ = os.Remove(filepath.Join(root, e.Name()))
		}
	}
	sort.Strings(ids)
	for len(ids) >= jeanSleepKeep {
		_ = os.RemoveAll(filepath.Join(root, ids[0]))
		ids = ids[1:]
	}
}

// jeanSleepSaveMeta note le bilan d'une copie (sommeil fini, interrompu, restauration).
func jeanSleepSaveMeta(id, kind, status, summary string) {
	if id == "" {
		return
	}
	b, _ := json.Marshal(jeanSleepMeta{ID: id, When: time.Now().UnixMilli(), Kind: kind, Status: status, Summary: clipRunes(summary, 1200)})
	if out, err := encodeMemContent(b); err == nil {
		_ = os.WriteFile(filepath.Join(jeanSleepDir(id), jeanSleepMetaFile), out, 0o600)
	}
}

func jeanSleepLoadMeta(id string) jeanSleepMeta {
	m := jeanSleepMeta{ID: id, Kind: "sommeil"}
	if t, err := time.ParseInLocation("20060102-150405", id, time.Local); err == nil {
		m.When = t.UnixMilli()
	}
	raw, err := os.ReadFile(filepath.Join(jeanSleepDir(id), jeanSleepMetaFile))
	if err != nil {
		return m
	}
	if plain, err := decodeMemContent(raw); err == nil {
		_ = json.Unmarshal(plain, &m)
	}
	return m
}

// jeanSleepSnap relit une copie datée.
func jeanSleepSnap(id string) (jeanMemSnap, error) {
	dir := jeanSleepDir(id)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("copie « %s » introuvable", id)
	}
	snap := jeanMemSnap{}
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || n == jeanSleepMetaFile {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		plain, err := decodeMemContent(raw)
		if err != nil {
			return nil, err
		}
		snap[n] = string(plain)
	}
	return snap, nil
}

// jeanCurrentSnap : la mémoire telle qu'elle est (sans prendre de copie).
func jeanCurrentSnap() jeanMemSnap {
	jeanMu.Lock()
	defer jeanMu.Unlock()
	snap := jeanMemSnap{}
	ents, _ := os.ReadDir(jeanDir())
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !(n == jeanProfileFile || n == jeanLessonsFile || strings.HasPrefix(n, jeanFichePrefix) && strings.HasSuffix(n, ".md")) {
			continue
		}
		if s, err := jeanRead(n); err == nil {
			snap[n] = s
		}
	}
	return snap
}

type jeanFileChange struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"` // created | removed | changed
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	NAdd    int      `json:"n_add"`
	NDel    int      `json:"n_del"`
}

func jeanLineSet(s string) (out []string, set map[string]bool) {
	set = map[string]bool{}
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(l)
		if t == "" || set[t] {
			continue
		}
		set[t] = true
		out = append(out, t)
	}
	return
}

// jeanSnapDiff : ce qui a changé entre deux états, fichier par fichier.
func jeanSnapDiff(before, after jeanMemSnap) []jeanFileChange {
	names := map[string]bool{}
	for n := range before {
		names[n] = true
	}
	for n := range after {
		names[n] = true
	}
	var out []jeanFileChange
	for n := range names {
		a, inA := before[n]
		b, inB := after[n]
		if inA && inB && a == b {
			continue
		}
		c := jeanFileChange{Name: n, Status: "changed"}
		switch {
		case !inA:
			c.Status = "created"
		case !inB:
			c.Status = "removed"
		}
		la, sa := jeanLineSet(a)
		lb, sb := jeanLineSet(b)
		for _, l := range lb {
			if !sa[l] {
				c.NAdd++
				if c.Status == "changed" && len(c.Added) < 40 {
					c.Added = append(c.Added, clipRunes(l, 300))
				}
			}
		}
		for _, l := range la {
			if !sb[l] {
				c.NDel++
				if c.Status == "changed" && len(c.Removed) < 40 {
					c.Removed = append(c.Removed, clipRunes(l, 300))
				}
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func jeanSleepIDs() []string {
	ents, _ := os.ReadDir(filepath.Join(jeanDir(), jeanGuardDir))
	var ids []string
	for _, e := range ents {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// jeanSleepRestore remet la mémoire telle qu'avant le sommeil id. L'état
// actuel est d'abord copié : une restauration se défait comme un sommeil.
func jeanSleepRestore(id string) error {
	snap, err := jeanSleepSnap(id)
	if err != nil {
		return err
	}
	jeanReflect.mu.Lock()
	busy := jeanReflect.cancel != nil
	jeanReflect.mu.Unlock()
	if busy {
		return fmt.Errorf("AJEAN est en train de réfléchir ou de dormir, réessayer dans un moment")
	}
	jeanSnapMemory()
	jeanSleepSaveMeta(jeanLastSnapID, "restauration", "ok", "Copie prise avant de revenir à l'état du "+jeanSleepLoadMeta(id).whenText())
	if err := jeanRollback(snap); err != nil {
		return err
	}
	jeanMu.Lock()
	_ = jeanAppendLocked("consolidation", "Mémoire remise telle qu'avant le sommeil du "+jeanSleepLoadMeta(id).whenText()+" (à la demande de l'utilisateur)")
	jeanMu.Unlock()
	jeanMemDirty.Store(true)
	return nil
}

func (m jeanSleepMeta) whenText() string {
	return time.UnixMilli(m.When).In(loc(defaultTaskTZ())).Format("02/01 15:04")
}

// GET : derniers sommeils (récents d'abord) avec leurs changements, et fiches
// archivées. POST {restore:id} : revenir avant ce sommeil. POST {unarchive:name} :
// ressortir une fiche des archives.
func handleJeanSleeps(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var b struct {
			Restore   string `json:"restore"`
			Unarchive string `json:"unarchive"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		var err error
		switch {
		case b.Restore != "":
			if strings.ContainsAny(b.Restore, `/\.`) {
				err = fmt.Errorf("identifiant invalide")
			} else {
				err = jeanSleepRestore(b.Restore)
			}
		case b.Unarchive != "":
			_, err = JeanRead(b.Unarchive) // relire une fiche archivée la ramène
		default:
			err = fmt.Errorf("rien à faire")
		}
		if err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true})
		return
	}
	ids := jeanSleepIDs()
	var list []map[string]any
	for i := len(ids) - 1; i >= 0; i-- {
		before, err := jeanSleepSnap(ids[i])
		if err != nil {
			continue
		}
		after, err := jeanSleepSnap(ids[i] + string(filepath.Separator) + "apres")
		if err == nil && len(after) > 0 {
			// état noté à la fin du sommeil : ses changements à lui seul
		} else if i == len(ids)-1 {
			after = jeanCurrentSnap()
		} else if after, err = jeanSleepSnap(ids[i+1]); err != nil {
			continue
		}
		m := jeanSleepLoadMeta(ids[i])
		list = append(list, map[string]any{"meta": m, "changes": jeanSnapDiff(before, after), "latest": i == len(ids)-1})
	}
	var arch []map[string]any
	for _, f := range jeanArchivedFiches() {
		arch = append(arch, map[string]any{"name": f.Name, "when": f.When, "size": len([]rune(f.Content))})
	}
	sendJSON(w, 200, map[string]any{"ok": true, "sleeps": list, "archives": arch})
}

// jeanSleepSaveAfter garde l'état juste après le sommeil (sous-dossier apres/) :
// les changements montrés sont alors ceux du sommeil seul, pas ceux de la
// journée qui a suivi.
func jeanSleepSaveAfter(id string) {
	if id == "" {
		return
	}
	dir := filepath.Join(jeanSleepDir(id), "apres")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	for n, s := range jeanCurrentSnap() {
		if out, err := encodeMemContent([]byte(s)); err == nil {
			_ = os.WriteFile(filepath.Join(dir, n), out, 0o600)
		}
	}
}
