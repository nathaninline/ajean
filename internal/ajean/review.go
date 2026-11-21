package ajean

// review.go — la validation de ce que l'IA planifie.
//
// L'IA a la main libre pendant une réponse : l'utilisateur est là, il voit ce
// qu'elle fait. Ce qui tourne ENSUITE, sans personne — une tâche planifiée
// qu'elle a créée ou modifiée, le script qu'une tâche lance — est autre chose :
// une page web piégée qui lui ferait poser une tâche récurrente s'installerait
// sur la machine pour de bon.
//
// Validation activée (« ajean review on », Paramètres > Sécurité), tout ce que le
// modèle crée, modifie ou supprime parmi les tâches — et le contenu des scripts
// qu'elles lancent — attend l'accord de l'utilisateur, donné UNE fois pour toute
// la réponse, à la fin. Une tâche non validée ne part pas. Ce que l'utilisateur
// règle lui-même dans l'interface est validé d'office.
//
// Éteinte (le défaut), rien ne change : les tâches partent comme en 0.18.
//
// État (bucket état) : "review_on", "review_tasks" = {id → ce qui a été validé}.
// À l'activation, les tâches déjà présentes sont reprises telles quelles : elles
// tournaient avant, elles ne doivent pas s'arrêter.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	stReviewOn    = "review_on"
	stReviewTasks = "review_tasks"
	stReviewNote  = "review_note"

	reviewScriptMax = 256 << 10 // au-delà, on ne garde que l'empreinte du script
)

// reviewTaskDef : ce qu'une tâche FAIT, tel que l'utilisateur l'a validé. L'état
// d'exécution (dernier passage, historique…) n'en fait pas partie.
type reviewTaskDef struct {
	Name     string `json:"name"`
	Prompt   string `json:"prompt,omitempty"`
	Schedule string `json:"schedule"`
	TZ       string `json:"tz,omitempty"`
	Preset   string `json:"preset,omitempty"`
	Project  string `json:"project,omitempty"`
	Jean     bool   `json:"jean,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Script   string `json:"script,omitempty"`
	NoMem    bool   `json:"no_mem,omitempty"`
	NoWeb    bool   `json:"no_web,omitempty"`
	Enabled  bool   `json:"enabled"`
	// Tâche script : le CONTENU du script fait partie de ce qui est validé. Sans
	// cela, réécrire le script d'une tâche déjà validée passerait inaperçu.
	ScriptSum  string `json:"script_sum,omitempty"`
	ScriptText string `json:"script_text,omitempty"` // pour montrer la différence et revenir en arrière
}

var reviewMu sync.Mutex

// reviewOn : la validation est-elle activée ?
func reviewOn() bool { return getBool(bkState, stReviewOn) }

// reviewScriptState lit le script d'une tâche script : empreinte et texte (texte
// omis s'il est trop gros). Script absent : empreinte vide.
func reviewScriptState(t Task) (sum, text string) {
	if t.Kind != "script" || strings.TrimSpace(t.Script) == "" {
		return "", ""
	}
	full, err := scriptsPathIn(scriptsDirFor(t.Jean), t.Script)
	if err != nil {
		return "", ""
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return "", ""
	}
	h := sha256.Sum256(raw)
	if len(raw) <= reviewScriptMax {
		text = string(raw)
	}
	return hex.EncodeToString(h[:]), text
}

func reviewDefOf(t Task) reviewTaskDef {
	d := reviewTaskDef{
		Name: t.Name, Prompt: t.Prompt, Schedule: t.Schedule, TZ: t.TZ, Preset: t.Preset,
		Project: t.Project, Jean: t.Jean, Kind: t.Kind, Script: t.Script,
		NoMem: t.NoMem, NoWeb: t.NoWeb, Enabled: t.Enabled,
	}
	if d.Kind == "agent" {
		d.Kind = "" // "" et "agent" désignent la même chose (tasks.go)
	}
	d.ScriptSum, d.ScriptText = reviewScriptState(t)
	return d
}

// reviewSame : la tâche fait-elle toujours ce qui a été validé ? La SUSPENDRE
// ne demande pas d'accord (c'est le sens sûr) ; la réactiver, si.
func reviewSame(old, cur reviewTaskDef) bool {
	return old.Name == cur.Name && old.Prompt == cur.Prompt && old.Schedule == cur.Schedule &&
		old.TZ == cur.TZ && old.Preset == cur.Preset && old.Project == cur.Project && old.Jean == cur.Jean &&
		old.Kind == cur.Kind && old.Script == cur.Script && old.NoMem == cur.NoMem && old.NoWeb == cur.NoWeb &&
		old.ScriptSum == cur.ScriptSum && !(cur.Enabled && !old.Enabled)
}

// reviewLoad lit ce qui a été validé. ok=false : illisible ou absent — l'appelant
// en tire la conclusion PRUDENTE (rien n'est validé).
func reviewLoad() (map[string]reviewTaskDef, bool) {
	raw, err := getBytesErr(bkState, stReviewTasks)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	m := map[string]reviewTaskDef{}
	if json.Unmarshal(raw, &m) != nil {
		return nil, false
	}
	return m, true
}

func reviewStore(m map[string]reviewTaskDef) error { return putJSON(bkState, stReviewTasks, m) }

func reviewSnapshot() map[string]reviewTaskDef {
	m := map[string]reviewTaskDef{}
	for _, t := range listTasks() {
		m[t.ID] = reviewDefOf(t)
	}
	return m
}

// reviewTaskValid : cette tâche peut-elle partir seule ? Validation éteinte :
// toujours. Allumée : seulement si elle fait exactement ce qui a été validé.
// Un état de validation illisible ne laisse RIEN partir.
func reviewTaskValid(t Task) bool {
	if !reviewOn() {
		return true
	}
	m, ok := reviewLoad()
	if !ok {
		return false
	}
	old, known := m[t.ID]
	return known && reviewSame(old, reviewDefOf(t))
}

// reviewEnable active la validation en reprenant les tâches présentes.
func reviewEnable() error {
	reviewMu.Lock()
	defer reviewMu.Unlock()
	if reviewOn() {
		if _, ok := reviewLoad(); ok {
			return nil
		}
	}
	if err := reviewStore(reviewSnapshot()); err != nil {
		return err
	}
	return putBool(bkState, stReviewOn, true)
}

// reviewDisable éteint la validation : les tâches repartent comme en 0.18, y
// compris celles qui attendaient.
func reviewDisable() error {
	reviewMu.Lock()
	defer reviewMu.Unlock()
	if err := putBool(bkState, stReviewOn, false); err != nil {
		return err
	}
	_ = putStr(bkState, stReviewNote, "")
	return putBytes(bkState, stReviewTasks, nil)
}

// ReviewItem : un changement à valider.
type ReviewItem struct {
	Type   string `json:"type"`   // task
	Change string `json:"change"` // new, changed, deleted
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// Review : la liste à valider. ID change dès que la liste change : une réponse
// donnée sur une liste périmée est refusée.
type Review struct {
	ID    string       `json:"id"`
	Items []ReviewItem `json:"items"`
}

func (d reviewTaskDef) work() string {
	if d.Kind == "script" {
		return "lance le script « " + d.Script + " » (sans modèle)"
	}
	return d.Prompt
}

func (d reviewTaskDef) access() string {
	var p []string
	if d.Kind != "script" {
		if d.NoMem {
			p = append(p, "sans mémoire")
		}
		if d.NoWeb {
			p = append(p, "sans web")
		}
		if d.Preset != "" {
			p = append(p, "preset "+d.Preset)
		}
	}
	if d.Jean {
		p = append(p, "mode Jean")
	} else if d.Project != "" {
		p = append(p, "projet "+d.Project)
	}
	return strings.Join(p, ", ")
}

func reviewDescribe(d reviewTaskDef) string {
	var b strings.Builder
	b.WriteString("Quand : " + d.Schedule)
	if d.TZ != "" {
		b.WriteString(" (" + d.TZ + ")")
	}
	b.WriteString("\nCe qu'elle fera : " + d.work())
	if a := d.access(); a != "" {
		b.WriteString("\nCadre : " + a)
	}
	if !d.Enabled {
		b.WriteString("\nÉtat : suspendue")
	}
	if d.Kind == "script" {
		switch {
		case d.ScriptSum == "":
			b.WriteString("\nScript introuvable.")
		case d.ScriptText != "":
			b.WriteString("\n\nScript exécuté :\n" + d.ScriptText)
		default:
			b.WriteString("\n(script trop gros pour être affiché ici)")
		}
	}
	return b.String()
}

func reviewCompare(a, b reviewTaskDef) string {
	var out []string
	add := func(label, x, y string) {
		if x != y {
			out = append(out, label+"\n  Avant : "+x+"\n  Après : "+y)
		}
	}
	add("Nom", a.Name, b.Name)
	add("Quand", strings.TrimSpace(a.Schedule+" "+a.TZ), strings.TrimSpace(b.Schedule+" "+b.TZ))
	add("Ce qu'elle fera", a.work(), b.work())
	add("Cadre", a.access(), b.access())
	if b.Enabled && !a.Enabled {
		out = append(out, "La tâche était suspendue : elle sera réactivée.")
	}
	if a.ScriptSum != b.ScriptSum && a.Script == b.Script {
		switch {
		case b.ScriptSum == "":
			out = append(out, "Le script « "+b.Script+" » a été supprimé.")
		case a.ScriptText != "" && b.ScriptText != "":
			lines, added, removed := lineDiff(a.ScriptText, b.ScriptText)
			var d strings.Builder
			fmt.Fprintf(&d, "Le script « %s » a changé (+%d −%d) :", b.Script, added, removed)
			for _, l := range lines {
				d.WriteString("\n" + l.Op + " " + l.Text)
			}
			out = append(out, d.String())
		default:
			out = append(out, "Le script « "+b.Script+" » a changé.")
		}
	}
	return strings.Join(out, "\n\n")
}

// reviewChanges : ce qui diffère de ce que l'utilisateur a validé.
func reviewChanges() []ReviewItem {
	if !reviewOn() {
		return nil
	}
	m, ok := reviewLoad()
	if !ok {
		m = map[string]reviewTaskDef{} // état perdu : tout est à revalider
	}
	var out []ReviewItem
	seen := map[string]bool{}
	for _, t := range listTasks() {
		seen[t.ID] = true
		cur := reviewDefOf(t)
		old, known := m[t.ID]
		switch {
		case !known:
			out = append(out, ReviewItem{"task", "new", t.ID, t.Name, redactSecrets(reviewDescribe(cur))})
		case !reviewSame(old, cur):
			out = append(out, ReviewItem{"task", "changed", t.ID, t.Name, redactSecrets(reviewCompare(old, cur))})
		}
	}
	for id, old := range m {
		if !seen[id] {
			out = append(out, ReviewItem{"task", "deleted", id, old.Name, redactSecrets("Cette tâche a été supprimée.\n" + reviewDescribe(old))})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func reviewOf(items []ReviewItem) *Review {
	if len(items) == 0 {
		return nil
	}
	raw, _ := json.Marshal(items)
	sum := sha256.Sum256(raw)
	return &Review{ID: hex.EncodeToString(sum[:8]), Items: items}
}

// reviewPending : ce qui attend, une fois le travail du modèle terminé. Pendant
// qu'il génère, rien : il modifie peut-être encore, on valide tout à la fin.
func reviewPending() *Review {
	if !reviewOn() || conv.isGenerating() {
		return nil
	}
	return reviewOf(reviewChanges())
}

// reviewTaskPending : cette tâche attend-elle une validation ?
func reviewTaskPending(id string) bool {
	for _, it := range reviewChanges() {
		if it.ID == id {
			return true
		}
	}
	return false
}

var errReviewChanged = errors.New("la liste à valider a changé : relis-la")

// reviewAnswer applique la réponse de l'utilisateur à TOUTE la liste.
func reviewAnswer(id string, accept bool) error {
	reviewMu.Lock()
	defer reviewMu.Unlock()
	r := reviewPending()
	if r == nil || r.ID != id {
		return errReviewChanged
	}
	if accept {
		return reviewStore(reviewSnapshot())
	}
	if err := reviewRejectLocked(); err != nil {
		return err
	}
	return putStr(bkState, stReviewNote, "[The user refused your latest changes to scheduled tasks and their scripts: they were put back as before. Do not redo them without asking.]")
}

// reviewKeepRejected met de côté la version refusée d'un script avant de
// remettre la version validée : un refus ne doit rien détruire.
func reviewKeepRejected(full string) {
	raw, err := os.ReadFile(full)
	if err != nil {
		return
	}
	dst := filepath.Join(AjeanHome(), "approved", "rejected", time.Now().Format("20060102-150405")+"-"+filepath.Base(full))
	_ = memWriteFileAtomic(dst, raw, 0o600)
}

// reviewRejectLocked remet les tâches (et leurs scripts) comme l'utilisateur les
// avait validés.
func reviewRejectLocked() error {
	m, ok := reviewLoad()
	if !ok {
		return errors.New("état de la validation illisible : rien n'a été changé")
	}
	now := time.Now()
	seen := map[string]bool{}
	for _, t := range listTasks() {
		seen[t.ID] = true
		old, known := m[t.ID]
		if !known { // créée par le modèle
			scriptRunStop(t.ID)
			if err := deleteTask(t.ID); err != nil {
				return err
			}
			continue
		}
		cur := reviewDefOf(t)
		if reviewSame(old, cur) {
			continue
		}
		// Script modifié ou supprimé : la version validée revient, la refusée est gardée.
		if old.Kind == "script" && old.ScriptSum != cur.ScriptSum && old.ScriptText != "" {
			if full, err := scriptsPathIn(scriptsDirFor(old.Jean), old.Script); err == nil {
				reviewKeepRejected(full)
				mode := os.FileMode(0o755)
				if fi, err := os.Stat(full); err == nil {
					mode = fi.Mode().Perm()
				}
				if err := memWriteFileVerified(full, []byte(old.ScriptText), mode); err != nil {
					return fmt.Errorf("script « %s » non remis : %w", old.Script, err)
				}
			}
		}
		if t.Schedule != old.Schedule || t.TZ != old.TZ {
			t.NextRun = computeNextRun(old.Schedule, old.TZ, now)
		}
		t.Name, t.Prompt, t.Schedule, t.TZ, t.Preset = old.Name, old.Prompt, old.Schedule, old.TZ, old.Preset
		t.Project, t.Jean, t.Kind, t.Script = old.Project, old.Jean, old.Kind, old.Script
		t.NoMem, t.NoWeb = old.NoMem, old.NoWeb
		if t.Enabled && !old.Enabled {
			t.Enabled = false
		}
		if err := saveTask(t); err != nil {
			return err
		}
	}
	for id, old := range m { // supprimée par le modèle : elle revient
		if seen[id] {
			continue
		}
		t := Task{
			ID: id, Name: old.Name, Prompt: old.Prompt, Schedule: old.Schedule, TZ: old.TZ, Preset: old.Preset,
			Project: old.Project, Jean: old.Jean, Kind: old.Kind, Script: old.Script,
			NoMem: old.NoMem, NoWeb: old.NoWeb, Enabled: old.Enabled,
		}
		t.NextRun = computeNextRun(t.Schedule, t.TZ, now)
		if err := saveTask(t); err != nil {
			return err
		}
	}
	return nil
}

// reviewUserSaved : une tâche réglée par l'utilisateur lui-même est validée
// d'office — sauf si une modification du modèle y attendait (wasPending) : on ne
// la valide pas au passage, la liste reste à lire.
func reviewUserSaved(id string, wasPending bool) {
	if !reviewOn() || wasPending {
		return
	}
	reviewMu.Lock()
	defer reviewMu.Unlock()
	m, ok := reviewLoad()
	if !ok {
		return
	}
	if t, found := getTask(id); found {
		m[id] = reviewDefOf(t)
		_ = reviewStore(m)
	}
}

// reviewUserDeleted : une suppression par l'utilisateur ne se valide pas.
func reviewUserDeleted(id string) {
	if !reviewOn() {
		return
	}
	reviewMu.Lock()
	defer reviewMu.Unlock()
	if m, ok := reviewLoad(); ok {
		delete(m, id)
		_ = reviewStore(m)
	}
}

// reviewTakeNote : ce que le modèle doit savoir d'un refus, une seule fois.
func reviewTakeNote() string {
	s := getStr(bkState, stReviewNote)
	if s == "" {
		return ""
	}
	_ = putStr(bkState, stReviewNote, "")
	return s + "\n\n"
}

// reviewToolNote : ajouté au résultat d'un outil de tâche quand la validation
// est activée, pour que le modèle ne promette pas une exécution qui attend.
func reviewToolNote() string {
	if !reviewOn() {
		return ""
	}
	return " — en attente de validation par l'utilisateur (demandée à la fin de ta réponse) : ne promets pas qu'elle s'exécutera avant"
}

// ---- commande et API -----------------------------------------------------------

func printReview(r *Review) {
	if r == nil {
		fmt.Println(dim("[info] rien à valider"))
		return
	}
	for _, it := range r.Items {
		kind := map[string]string{"new": "nouvelle tâche", "changed": "tâche modifiée", "deleted": "tâche supprimée"}[it.Change]
		fmt.Printf("%s %s « %s »\n", yellow("•"), kind, it.Name)
		for _, l := range strings.Split(it.Detail, "\n") {
			fmt.Println("    " + l)
		}
	}
}

// cmdReview : « ajean review [status|on|off|accept|reject] ».
func cmdReview(args []string) error {
	sub := "status"
	if len(args) > 0 && args[0] != "" {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "on":
		if err := reviewEnable(); err != nil {
			return err
		}
		fmt.Printf("%s validation activée : ce que l'IA planifie attend votre accord (les tâches déjà présentes sont reprises)\n", green("[ok]"))
		return nil
	case "off":
		if err := reviewDisable(); err != nil {
			return err
		}
		fmt.Printf("%s validation désactivée : les tâches partent sans accord, comme avant\n", yellow("[info]"))
		return nil
	case "status":
		if !reviewOn() {
			fmt.Printf("%s validation désactivée\n", yellow("[info]"))
			return nil
		}
		fmt.Printf("%s validation activée\n", green("[ok]"))
		printReview(reviewOf(reviewChanges()))
		return nil
	case "accept", "reject":
		if !reviewOn() {
			return errors.New("la validation n'est pas activée (ajean review on)")
		}
		// Depuis la ligne de commande, la liste est lue directement (le process de
		// l'interface seul sait si le modèle travaille encore).
		r := reviewOf(reviewChanges())
		if r == nil {
			fmt.Println(dim("[info] rien à valider"))
			return nil
		}
		printReview(r)
		reviewMu.Lock()
		defer reviewMu.Unlock()
		if sub == "accept" {
			if err := reviewStore(reviewSnapshot()); err != nil {
				return err
			}
			fmt.Printf("%s validé\n", green("[ok]"))
			return nil
		}
		if err := reviewRejectLocked(); err != nil {
			return err
		}
		_ = putStr(bkState, stReviewNote, "[The user refused your latest changes to scheduled tasks and their scripts: they were put back as before. Do not redo them without asking.]")
		fmt.Printf("%s refusé : tout est revenu comme validé\n", yellow("[info]"))
		return nil
	default:
		return fmt.Errorf("usage: ajean review [status|on|off|accept|reject]")
	}
}

// handleReview : GET → {on, review} ; POST {on} active ou éteint, POST {id,
// accept} répond à la liste affichée.
func handleReview(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			On     *bool  `json:"on"`
			ID     string `json:"id"`
			Accept *bool  `json:"accept"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": "requête illisible"})
			return
		}
		var err error
		switch {
		case req.On != nil && *req.On:
			err = reviewEnable()
		case req.On != nil:
			err = reviewDisable()
		case req.Accept != nil && req.ID != "":
			err = reviewAnswer(req.ID, *req.Accept)
		default:
			err = errors.New("requête incomplète")
		}
		if err != nil {
			code := 400
			if errors.Is(err, errReviewChanged) {
				code = http.StatusConflict
			}
			sendJSON(w, code, map[string]any{"ok": false, "error": err.Error(), "on": reviewOn(), "review": reviewPending()})
			return
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "on": reviewOn(), "review": reviewPending(), "busy": conv.isGenerating()})
}
