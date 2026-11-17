package agent

// Les conversations. Chacune est un fichier Markdown lisible dans
// MEMORY/conversations/ : le fil tel que l'utilisateur l'a lu. La mémoire, elle,
// est commune à toutes : ce qui est appris dans l'une sert dans les autres.
//
// Le contexte envoyé au modèle vit en mémoire vive, avec les appels d'outils du
// moment ; au démarrage il est reconstruit depuis le fil. Quand il dépasse le
// budget, les plus vieux messages en sortent : le journal les garde.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"ajean/internal/config"
	"ajean/internal/home"
	"ajean/internal/llm"
)

const (
	titleMax    = 48
	untitled    = "Nouvelle conversation"
	stampLayout = "2006-01-02 15:04:05 -0700" // en-tête d'un message dans le fichier
	filesPrefix = "_Fichiers joints : "       // dernière ligne d'un message avec pièces jointes
	filesSep    = " | "                       // sépare deux noms : ce caractère est interdit dans un nom de fichier déposé
	kindTask    = "task"                      // compte rendu d'une tâche, écrit par l'assistant de lui-même
	kindFailed  = "failed"                    // réponse interrompue ou impossible : ce qui en reste
)

var (
	ErrNotFound = errors.New("conversation introuvable")
	ErrBusy     = errors.New("une réponse est déjà en cours")

	idRe   = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}(-[0-9]+)?$`)
	headRe = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{4}) · (user|assistant)(?: · (\w+))?$`)
)

// Item : un message du fil affiché.
type Item struct {
	Role  string   `json:"role"` // user | assistant
	Text  string   `json:"text"`
	TS    int64    `json:"ts"`              // epoch ms
	Kind  string   `json:"kind,omitempty"`  // task (compte rendu d'une tâche) ou failed (réponse interrompue)
	Files []string `json:"files,omitempty"` // fichiers joints (chemins dans le WORKSPACE)
}

type conversation struct {
	mu      sync.Mutex
	id      string
	created int64 // epoch ms ; date d'une conversation encore vide dans la liste
	title   string
	log     []Item        // le fil affiché
	msgs    []llm.Message // le contexte du modèle : les messages récents
	summary string        // résumé de ce qui est sorti du contexte (voir context.go)
	busy    bool
	deleted bool               // supprimée : plus aucun tour ne doit y écrire
	cancel  context.CancelFunc // arrête la réponse en cours
	review  reviewState
	used    int // tokens du contexte à la dernière réponse (0 : inconnu), non conservé sur disque
}

var convs = struct {
	sync.Mutex
	byID map[string]*conversation
}{byID: map[string]*conversation{}}

func convDir() string { return filepath.Join(home.Memory(), "conversations") }

// ---- le fichier Markdown --------------------------------------------------------

func (c *conversation) render() string {
	var b strings.Builder
	title := c.title
	if title == "" {
		title = untitled
	}
	b.WriteString("# " + title + "\n")
	loc := config.Location()
	for _, it := range c.log {
		b.WriteString("\n## " + time.UnixMilli(it.TS).In(loc).Format(stampLayout) + " · " + it.Role)
		if it.Kind != "" {
			b.WriteString(" · " + it.Kind)
		}
		b.WriteString("\n" + escapeHeads(strings.TrimSpace(it.Text)) + "\n")
		if len(it.Files) > 0 {
			b.WriteString("\n" + filesPrefix + strings.Join(it.Files, filesSep) + "_\n")
		}
	}
	return b.String()
}

func parse(id, s string) *conversation {
	c := &conversation{id: id}
	var cur *Item
	var body []string
	flush := func() {
		if cur == nil {
			return
		}
		for len(body) > 0 && body[len(body)-1] == "" {
			body = body[:len(body)-1]
		}
		if n := len(body); n > 0 && strings.HasPrefix(body[n-1], filesPrefix) {
			cur.Files = strings.Split(strings.TrimSuffix(strings.TrimPrefix(body[n-1], filesPrefix), "_"), filesSep)
			body = body[:n-1]
		}
		cur.Text = strings.TrimSpace(unescapeHeads(strings.Join(body, "\n")))
		c.log = append(c.log, *cur)
	}
	for i, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if i == 0 && strings.HasPrefix(l, "# ") {
			if t := strings.TrimSpace(l[2:]); t != untitled {
				c.title = t
			}
			continue
		}
		if m := headRe.FindStringSubmatch(l); m != nil {
			flush()
			t, _ := time.Parse(stampLayout, m[1])
			cur, body = &Item{Role: m[2], Kind: m[3], TS: t.UnixMilli()}, nil
			continue
		}
		if cur != nil {
			body = append(body, l)
		}
	}
	flush()
	c.msgs = contextFromLog(c.log)
	c.trimLocked() // au démarrage, ce qui dépasse sort sans être résumé : pas d'appel au modèle ici
	c.summary = loadSummary(id)
	return c
}

// userContent : un message de l'utilisateur tel que le modèle le reçoit, daté.
func userContent(at time.Time, text string, files []string) string {
	s := "[" + config.Stamp(at) + "] " + text
	if len(files) > 0 {
		s += "\n\n[Files attached by the user, in your workspace: " + strings.Join(files, ", ") + "]"
	}
	return s
}

// contextFromLog reconstruit le contexte du modèle depuis le fil (au démarrage).
func contextFromLog(log []Item) []llm.Message {
	var out []llm.Message
	for _, it := range log {
		at := time.UnixMilli(it.TS)
		switch {
		case it.Role == "user":
			out = append(out, llm.Message{Role: "user", Content: userContent(at, it.Text, it.Files)})
		case it.Kind == kindTask:
			out = append(out, taskMessages(at, "", it.Text)...)
		default:
			out = append(out, llm.Message{Role: "assistant", Content: it.Text})
		}
	}
	return out
}

// Une ligne d'un message qui aurait la forme d'un en-tête couperait ce message
// en deux à la relecture : elle est écrite précédée d'une barre oblique inverse.
func escapeHeads(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if headRe.MatchString(strings.TrimLeft(l, `\`)) {
			lines[i] = `\` + l
		}
	}
	return strings.Join(lines, "\n")
}

func unescapeHeads(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if rest, ok := strings.CutPrefix(l, `\`); ok && headRe.MatchString(strings.TrimLeft(rest, `\`)) {
			lines[i] = rest
		}
	}
	return strings.Join(lines, "\n")
}

// saveLocked écrit le fil. Une conversation supprimée pendant un tour ne
// recrée pas son fichier.
func (c *conversation) saveLocked() {
	if c.deleted {
		return
	}
	if err := home.WriteFile(filepath.Join(convDir(), c.id+".md"), []byte(c.render())); err != nil {
		log.Printf("conversation %s non enregistrée : %v", c.id, err)
	}
}

// userTextsLocked : les derniers messages de l'utilisateur dans cette
// conversation. Le modèle les a sous les yeux, ou vient de les avoir : le
// rappel de mémoire n'a pas à les lui redonner.
func (c *conversation) userTextsLocked() []string {
	var out []string
	for i := len(c.log) - 1; i >= 0 && len(out) < 40; i-- {
		if c.log[i].Role == "user" {
			out = append(out, c.log[i].Text)
		}
	}
	return out
}

// ---- le registre ----------------------------------------------------------------

// Load lit les conversations enregistrées. À appeler une fois au démarrage.
func Load() {
	lastActivity.Store(time.Now().UnixMilli()) // l'utilisateur vient de lancer le programme
	convs.Lock()
	defer convs.Unlock()
	ents, _ := os.ReadDir(convDir())
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".md")
		if e.IsDir() || !ok || !idRe.MatchString(id) {
			continue
		}
		if raw, err := os.ReadFile(filepath.Join(convDir(), e.Name())); err == nil {
			convs.byID[id] = parse(id, string(raw))
		}
	}
}

func get(id string) *conversation {
	convs.Lock()
	defer convs.Unlock()
	return convs.byID[id]
}

// create donne une conversation vide : celle qui attend déjà son premier
// message s'il y en a une (sinon chaque clic sur « nouvelle conversation » en
// empilerait une), à défaut une nouvelle.
func create() *conversation {
	convs.Lock()
	defer convs.Unlock()
	now := time.Now()
	for _, c := range convs.byID {
		c.mu.Lock()
		empty := len(c.log) == 0 && !c.busy
		if empty {
			c.created = now.UnixMilli()
		}
		c.mu.Unlock()
		if empty {
			return c
		}
	}
	base := now.In(config.Location()).Format("20060102-150405")
	id := base
	for n := 2; convs.byID[id] != nil; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	c := &conversation{id: id, created: now.UnixMilli()}
	convs.byID[id] = c
	return c
}

// New ouvre une conversation vide et renvoie son identifiant. Son fichier
// n'apparaît qu'au premier message.
func New() string {
	id := create().id
	emit(event{Type: "convs"})
	return id
}

// Delete supprime une conversation et son fichier. La mémoire, elle, garde ce
// qui y a été appris.
func Delete(id string) error {
	convs.Lock()
	defer convs.Unlock()
	c := convs.byID[id]
	if c == nil {
		return ErrNotFound
	}
	c.mu.Lock()
	busy := c.busy
	c.deleted = !busy
	c.mu.Unlock()
	if busy {
		return ErrBusy
	}
	c.cancelReview()
	delete(convs.byID, id)
	forgetSummary(id)
	if err := os.Remove(filepath.Join(convDir(), id+".md")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	emit(event{Type: "convs"})
	return nil
}

// Rename change le titre d'une conversation.
func Rename(id, title string) error {
	c := get(id)
	title = clip(strings.Join(strings.Fields(title), " "), 80)
	if c == nil {
		return ErrNotFound
	}
	if title == "" {
		return errors.New("titre vide")
	}
	c.mu.Lock()
	c.title = title
	c.saveLocked()
	c.mu.Unlock()
	emit(event{Type: "convs"})
	return nil
}

// Info : une conversation dans la liste.
type Info struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	TS    int64  `json:"ts"` // dernier message (ou création), epoch ms
	Busy  bool   `json:"busy"`
	Used  int    `json:"used,omitempty"` // tokens du contexte à la dernière réponse
}

// List : les conversations, la plus récemment active d'abord.
func List() []Info {
	convs.Lock()
	defer convs.Unlock()
	out := make([]Info, 0, len(convs.byID))
	for _, c := range convs.byID {
		c.mu.Lock()
		in := Info{ID: c.id, Title: c.title, TS: c.created, Busy: c.busy}
		if n := len(c.log); n > 0 {
			in.TS = c.log[n-1].TS
		}
		c.mu.Unlock()
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TS != out[j].TS {
			return out[i].TS > out[j].TS
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// Thread : le fil d'une conversation. id vide ou inconnu → la plus récente ;
// ok=false s'il n'y en a aucune.
func Thread(id string) (info Info, log []Item, ok bool) {
	c := get(id)
	if c == nil {
		if l := List(); len(l) > 0 {
			c = get(l[0].ID)
		}
	}
	if c == nil {
		return Info{}, nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return Info{ID: c.id, Title: c.title, Busy: c.busy, Used: c.used}, append([]Item(nil), c.log...), true
}

// busyAnywhere : une réponse est-elle en cours dans une conversation ?
func busyAnywhere() bool {
	for _, in := range List() {
		if in.Busy {
			return true
		}
	}
	return false
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
