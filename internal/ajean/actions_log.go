package ajean

// actions_log.go — le journal des actions.
//
// Chaque outil qui AGIT (shell, écriture de fichier, mémoire, tâche, navigateur,
// outil MCP…) est noté avant et après son exécution, en ajout seul, dans
// $AJEAN_HOME/actions/<espace>.jsonl. Deux usages :
//
//   - savoir ce que l'IA a fait sur la machine, y compris seule : une tâche
//     planifiée ne laisse dans son historique que son compte-rendu final, pas les
//     commandes qu'elle a lancées (« ajean actions », Paramètres > Sécurité) ;
//   - après une coupure en plein tour (crash, redémarrage, mise à jour), dire au
//     modèle ce qui avait DÉJÀ été fait, pour qu'un « réessaie » ne le refasse
//     pas à l'aveugle (actionsInterruptedNote).
//
// Les lectures ne sont pas notées. Ce qui ressemble à un secret est masqué avant
// l'écriture. Mémoire chiffrée : les lignes le sont aussi (même clé), et rien
// n'est écrit tant qu'elle est verrouillée — le journal ne doit pas devenir la
// copie en clair de conversations chiffrées.
//
// « ajean actions off » coupe le journal.

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	actionsMaxBytes  = 2 << 20 // au-delà, le fichier devient .old (un seul gardé)
	actionLabelMax   = 400
	actionResultMax  = 240
	actionEncPrefix  = "enc:"
	stActionsOff     = "actions_off"
	secretMaskedText = "[secret masqué]"
)

// actionEntry : une ligne du journal.
type actionEntry struct {
	TS     int64  `json:"ts"`              // epoch ms
	Turn   int64  `json:"turn"`            // identifiant du tour (un par exécution du modèle)
	Phase  string `json:"phase"`           // begin, start, done, end
	Task   string `json:"task,omitempty"`  // id de la tâche planifiée, le cas échéant
	Title  string `json:"title,omitempty"` // son nom
	Name   string `json:"name,omitempty"`  // outil
	Label  string `json:"label,omitempty"` // sa cible (commande, fichier…)
	Result string `json:"result,omitempty"`
}

var actionsMu sync.Mutex

func actionsDir() string { return filepath.Join(AjeanHome(), "actions") }

var actionScopeRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func actionsFile(scope string) string {
	if !actionScopeRe.MatchString(scope) {
		scope = "chat"
	}
	return filepath.Join(actionsDir(), scope+".jsonl")
}

// actionsEnabled : le journal est-il tenu ? (oui par défaut)
func actionsEnabled() bool { return !getBool(bkState, stActionsOff) }

// ---- masquage des secrets --------------------------------------------------------

var (
	redactLabelled = regexp.MustCompile("(?i)\\b(password|passwd|passphrase|pwd|mot de passe|mdp|api[ _-]?key|cl[ée] api|access[ _-]?token|auth[ _-]?token|token|secret)\\b([^\\n:=]{0,24}[:=]\\s*[`\"']?)([^\\s`\"',;)]{4,})")
	redactBearer   = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	redactPiped    = regexp.MustCompile(`(?i)\becho\s+(['"]?)([^\s'"|]{3,})(['"]?)\s*\|\s*sudo\s+-S`)
	redactTokens   = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{20,}|ajean-web-[a-f0-9]{16,}|jl_[A-Za-z0-9_-]{16,})\b`)
)

// redactPlaceholder : une valeur qui désigne un secret sans le contenir
// (variable, exemple, code qui va le chercher ailleurs).
func redactPlaceholder(v string) bool {
	if strings.ContainsAny(v, "[](){}<>$%*") || strings.HasPrefix(v, secretRefPrefix) {
		return true
	}
	switch strings.ToLower(strings.TrimRight(v, ".")) {
	case "none", "null", "true", "false", "vide", "empty", "absent", "aucun", "aucune", "required", "requis", "obligatoire",
		"masqué", "masque", "xxxx", "xxxxx", "xxxxxx", "exemple", "example", "changeme", "password", "secret", "token", "bearer", "basic":
		return true
	}
	return strings.Contains(v, ".") && !strings.ContainsAny(v, "0123456789") // cfg.password, os.environ.x
}

// redactSecrets remplace ce qui ressemble à un secret (mot de passe annoncé,
// jeton de forme connue, mot de passe passé à sudo par un tube).
func redactSecrets(s string) string {
	s = redactLabelled.ReplaceAllStringFunc(s, func(m string) string {
		p := redactLabelled.FindStringSubmatch(m)
		if redactPlaceholder(p[3]) {
			return m
		}
		return p[1] + p[2] + secretMaskedText
	})
	s = redactBearer.ReplaceAllString(s, "$1 "+secretMaskedText)
	s = redactPiped.ReplaceAllString(s, "echo "+secretMaskedText+" | sudo -S")
	return redactTokens.ReplaceAllString(s, secretMaskedText)
}

// ---- écriture ----------------------------------------------------------------------

// actionLog ajoute une ligne. Ne renvoie rien : le journal ne doit jamais faire
// échouer l'action qu'il note.
func actionLog(scope string, e actionEntry) {
	if !actionsEnabled() {
		return
	}
	e.TS = time.Now().UnixMilli()
	e.Label = redactSecrets(clipRunes(strings.TrimSpace(e.Label), actionLabelMax))
	e.Result = redactSecrets(clipRunes(strings.TrimSpace(e.Result), actionResultMax))
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	if memEncActive() {
		dek, err := currentDEK()
		if err != nil {
			return // mémoire verrouillée : on n'écrit rien en clair à côté
		}
		box, err := encPage(dek, raw)
		if err != nil {
			return
		}
		raw = []byte(actionEncPrefix + base64.StdEncoding.EncodeToString(box))
	}
	actionsMu.Lock()
	defer actionsMu.Unlock()
	path := actionsFile(scope)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	if st, err := os.Stat(path); err == nil && st.Size() > actionsMaxBytes {
		_ = os.Remove(path + ".old")
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
	_ = f.Sync() // l'entrée doit survivre à une coupure juste après
}

// readActions relit un espace du journal (lignes illisibles ou chiffrées sans la
// clé : ignorées).
func readActions(scope string) []actionEntry {
	actionsMu.Lock()
	defer actionsMu.Unlock()
	var out []actionEntry
	for _, path := range []string{actionsFile(scope) + ".old", actionsFile(scope)} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Bytes()
			if strings.HasPrefix(string(line), actionEncPrefix) {
				dek, err := currentDEK()
				if err != nil {
					continue
				}
				box, err := base64.StdEncoding.DecodeString(string(line[len(actionEncPrefix):]))
				if err != nil {
					continue
				}
				if line, err = decPage(dek, box); err != nil {
					continue
				}
			}
			var e actionEntry
			if json.Unmarshal(line, &e) == nil && e.Phase != "" {
				out = append(out, e)
			}
		}
		f.Close()
	}
	return out
}

// ---- un tour ---------------------------------------------------------------------

type actionCtxKey struct{}

type actionCtx struct{ scope, task, title string }

// withActionScope range dans le contexte l'espace du journal où noter ce tour
// (« tasks » + la tâche pour une exécution planifiée).
func withActionScope(ctx context.Context, scope, task, title string) context.Context {
	return context.WithValue(ctx, actionCtxKey{}, actionCtx{scope, task, title})
}

// actionTurn : le journal d'UN tour du modèle. Rien n'est écrit tant qu'aucune
// action n'a lieu : une simple réponse ne laisse pas de trace.
type actionTurn struct {
	scope, task, title string
	id                 int64
	on, begun          bool
}

// newActionTurn prépare le journal d'un tour. Les révisions internes de la
// mémoire de Jean ne sont pas journalisées : ce ne sont pas des actions sur la
// machine.
func newActionTurn(ctx context.Context, caps Caps) *actionTurn {
	t := &actionTurn{scope: "chat", id: time.Now().UnixNano(), on: caps.Agent || caps.Jean}
	if caps.Jean {
		t.scope = "jean"
	}
	if a, ok := ctx.Value(actionCtxKey{}).(actionCtx); ok && a.scope != "" {
		t.scope, t.task, t.title = a.scope, a.task, a.title
	}
	if isJeanReflecting(ctx) || isJeanSleepCtx(ctx) {
		t.on = false
	}
	return t
}

func (t *actionTurn) log(phase, name, label, result string) {
	actionLog(t.scope, actionEntry{Turn: t.id, Phase: phase, Task: t.task, Title: t.title, Name: name, Label: label, Result: result})
}

// start note une action AVANT son exécution.
func (t *actionTurn) start(name, label string) {
	if t == nil || !t.on {
		return
	}
	if !t.begun {
		t.begun = true
		t.log("begin", "", "", "")
	}
	t.log("start", name, label, "")
}

// done note son résultat.
func (t *actionTurn) done(name, label, result string) {
	if t == nil || !t.on || !t.begun {
		return
	}
	t.log("done", name, label, result)
}

// end clôt le tour : sans cette ligne, le tour est tenu pour coupé.
func (t *actionTurn) end() {
	if t == nil || !t.on || !t.begun {
		return
	}
	t.log("end", "", "", "")
}

// actionMutating : l'appel peut-il changer quelque chose ? Les lectures ne sont
// pas notées.
func actionMutating(name string, args map[string]any) bool {
	switch name {
	case "mem_search", "mem_read", "recall", "recall_search", "task_list", "see_image", "see_video",
		"web_search", "web_images", "web_open", "web_read", "web_grep",
		"browser_snapshot", "browser_find", "browser_screenshot", "browser_scroll",
		"jean_search", "jean_read", "jean_projects", "jean_project_mem", "jean_project_file":
		return false
	case "tracker":
		switch strings.ToLower(strings.TrimSpace(str(args["action"]))) {
		case "add", "edit", "delete":
			return true
		}
		return false
	case "app":
		switch strings.ToLower(strings.TrimSpace(str(args["action"]))) {
		case "check", "list", "read", "view":
			return false
		}
		return true
	}
	return true
}

// describeActions : ce qu'un tour a réellement fait, pour le modèle.
func describeActions(entries []actionEntry, turn int64) string {
	var lines []string
	open := map[string]int{}
	for _, e := range entries {
		if e.Turn != turn {
			continue
		}
		key := e.Name + "|" + e.Label
		switch e.Phase {
		case "start":
			open[key] = len(lines)
			lines = append(lines, "- "+strings.TrimSpace(e.Name+" "+e.Label)+" : result unknown (cut off while running, check before redoing)")
		case "done":
			if i, ok := open[key]; ok {
				lines[i] = "- " + strings.TrimSpace(e.Name+" "+e.Label) + " : " + e.Result
				delete(open, key)
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "[Actions already carried out during your previous turn, which was cut off before it finished. Do not repeat them without checking:\n" + strings.Join(lines, "\n") + "]\n\n"
}

// actionsInterruptedNote : le dernier tour de cet espace (et de cette tâche)
// n'a jamais fini — programme coupé en plein travail ? Alors ce qu'il avait
// fait, UNE fois : le tour est ensuite clos dans le journal.
func actionsInterruptedNote(scope, task string) string {
	if !actionsEnabled() {
		return ""
	}
	entries := readActions(scope)
	var last int64
	for _, e := range entries {
		if e.Task != task {
			continue
		}
		switch e.Phase {
		case "begin":
			last = e.Turn
		case "end":
			if e.Turn == last {
				last = 0
			}
		}
	}
	if last == 0 {
		return ""
	}
	title := ""
	for _, e := range entries {
		if e.Turn == last && e.Title != "" {
			title = e.Title
		}
	}
	actionLog(scope, actionEntry{Turn: last, Phase: "end", Task: task, Title: title})
	return describeActions(entries, last)
}

// ---- lecture pour l'utilisateur ---------------------------------------------------

// actionView : une action, telle qu'on la montre.
type actionView struct {
	TS     int64  `json:"ts"`
	Scope  string `json:"scope"`
	Task   string `json:"task,omitempty"`
	Name   string `json:"name"`
	Label  string `json:"label"`
	Result string `json:"result,omitempty"`
	Done   bool   `json:"done"`
}

var actionScopes = []string{"chat", "jean", "tasks"}

// recentActions : les dernières actions, tous espaces confondus, la plus récente
// d'abord.
func recentActions(limit int) []actionView {
	if limit <= 0 {
		limit = 50
	}
	var all []actionView
	for _, scope := range actionScopes {
		byKey := map[string]int{}
		for _, e := range readActions(scope) {
			key := strconv.FormatInt(e.Turn, 10) + "|" + e.Name + "|" + e.Label
			switch e.Phase {
			case "start":
				byKey[key] = len(all)
				all = append(all, actionView{TS: e.TS, Scope: scope, Task: e.Title, Name: e.Name, Label: e.Label})
			case "done":
				if i, ok := byKey[key]; ok {
					all[i].Result, all[i].Done = e.Result, true
					delete(byKey, key)
				}
			}
		}
	}
	// Tri par date décroissante (insertion : les listes sont courtes et presque triées).
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].TS > all[j-1].TS; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	if len(all) > limit {
		all = all[:limit]
	}
	if all == nil {
		all = []actionView{}
	}
	return all
}

// cmdActions : « ajean actions [N|on|off|status] ».
func cmdActions(args []string) error {
	n := 30
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "off":
			if err := putBool(bkState, stActionsOff, true); err != nil {
				return err
			}
			fmt.Printf("%s journal des actions coupé (le journal existant reste dans %s)\n", yellow("[info]"), actionsDir())
			return nil
		case "on":
			if err := putBool(bkState, stActionsOff, false); err != nil {
				return err
			}
			fmt.Printf("%s journal des actions tenu\n", green("[ok]"))
			return nil
		case "status":
			if actionsEnabled() {
				fmt.Printf("%s journal des actions tenu — %s\n", green("[ok]"), actionsDir())
			} else {
				fmt.Printf("%s journal des actions coupé\n", yellow("[info]"))
			}
			return nil
		default:
			v, err := strconv.Atoi(args[0])
			if err != nil || v <= 0 {
				return fmt.Errorf("usage: ajean actions [N|on|off|status]")
			}
			n = v
		}
	}
	list := recentActions(n)
	if len(list) == 0 {
		fmt.Println(dim("[info] aucune action notée"))
		if memLocked() {
			fmt.Println(dim("       (mémoire chiffrée et verrouillée : le journal chiffré n'est pas lisible ici)"))
		}
		return nil
	}
	for i := len(list) - 1; i >= 0; i-- {
		a := list[i]
		who := a.Scope
		if a.Task != "" {
			who += " « " + a.Task + " »"
		}
		res := a.Result
		if !a.Done {
			res = "(pas de résultat noté : coupé ou en cours)"
		}
		fmt.Printf("%s  %-10s %s %s\n      %s\n", time.UnixMilli(a.TS).Format("2006-01-02 15:04:05"), who, bold(a.Name), a.Label, dim(strings.ReplaceAll(res, "\n", " ")))
	}
	return nil
}

// handleActions : GET → les dernières actions (?limit=) et l'état du journal ;
// POST {on} → le tenir ou le couper.
func handleActions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			On *bool `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.On == nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": "requête illisible"})
			return
		}
		if err := putBool(bkState, stActionsOff, !*req.On); err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	sendJSON(w, 200, map[string]any{"ok": true, "on": actionsEnabled(), "locked": memLocked(), "actions": recentActions(limit)})
}
