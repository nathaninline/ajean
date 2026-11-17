package agent

import (
	"context"
	"encoding/base64"
	"errors"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"ajean/internal/config"
	"ajean/internal/llm"
	"ajean/internal/memory"
	"ajean/internal/tool"
	"ajean/internal/workspace"
)

const (
	maxTurnIterations = 30              // allers-retours modèle ↔ outils dans un même tour
	recallTimeout     = 4 * time.Second // au-delà, le tour part sans rappel de mémoire
	maxImageBytes     = 8 << 20         // une image plus grosse n'est pas montrée au modèle
)

// lastActivity : fin du dernier tour, toutes conversations confondues (epoch ms).
// Le sommeil s'en sert pour ne pas partir pendant que l'utilisateur est là.
var lastActivity atomic.Int64

// Send ajoute le message de l'utilisateur à la conversation et lance la réponse
// en arrière-plan. La suite arrive par les événements.
func Send(id, text string, files []string) error {
	c := get(id)
	if c == nil {
		return ErrNotFound
	}
	if !config.Get().Chat().Ready() {
		return llm.ErrNotConfigured
	}
	if !c.begin() {
		return ErrBusy
	}
	go c.turn(text, files)
	return nil
}

// Stop interrompt la réponse en cours d'une conversation.
func Stop(id string) {
	if c := get(id); c != nil {
		c.mu.Lock()
		if c.cancel != nil {
			c.cancel()
		}
		c.mu.Unlock()
	}
}

// begin réserve la conversation pour un tour (false si une réponse est en
// cours, ou si la conversation vient d'être supprimée).
func (c *conversation) begin() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.busy || c.deleted {
		return false
	}
	c.busy = true
	c.started = time.Now().UnixMilli()
	return true
}

// turn déroule un tour : le message de l'utilisateur, puis la boucle modèle ↔
// outils jusqu'à la réponse. begin() doit avoir réussi. Tout est diffusé aux
// interfaces.
func (c *conversation) turn(text string, files []string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = llm.WithPhase(ctx, func(p string) { emit(event{Type: "phase", Conv: c.id, Text: p}) })
	ctx = llm.WithUsage(ctx, func(u llm.Usage) {
		used := u.Prompt + u.Completion
		c.mu.Lock()
		c.used = used
		c.mu.Unlock()
		emit(event{Type: "usage", Conv: c.id, Used: used, Max: ContextWindow(), Out: u.Completion})
	})
	c.cancelReview() // l'utilisateur reprend la main : la révision attendra
	now := time.Now()

	msgs, lead, recent := c.open(text, files, now, cancel)
	user := userMessage(ctx, text, files, now, recent)
	base := len(msgs)
	msgs = append(msgs, user)

	calls, saved := 0, false
	specs, described := withToolDesc(allTools.specs)
	exec := func(tc llm.ToolCall) string {
		cl, ok := decode(tc)
		desc := takeToolDesc(described, &cl)
		emit(event{Type: "tool", Conv: c.id, Name: cl.name, Label: cl.label(), Desc: desc})
		if !ok {
			return tool.Err("arguments illisibles (JSON invalide)")
		}
		calls++
		res := allTools.run(cl, c.id)
		if _, isMemory := memoryTools.byName[cl.name]; isMemory && tool.IsOK(res) {
			saved = true
		}
		return res
	}
	onText := func(delta string, end bool) {
		if end {
			delta += "\n\n" // un autre message du modèle peut suivre, après ses outils
		}
		if delta != "" {
			emit(event{Type: "delta", Conv: c.id, Text: delta})
		}
	}
	out, texts, err := loop(ctx, msgs, specs, maxTurnIterations, onText, exec)
	if llm.Rejected(err) && len(user.Images) > 0 && len(out) == base+1 {
		// La requête elle-même est refusée dès le premier appel : le modèle
		// n'accepte sans doute pas les images. On recommence sans elles.
		msgs[base].Images = nil
		msgs[base].Content += "\n[The attached images could not be shown to you: this model does not accept images.]"
		out, texts, err = loop(ctx, msgs, allTools.specs, maxTurnIterations, onText, exec)
	}
	answer := strings.Join(texts, "\n\n") // un tour raté garde tout ce qui a été dit
	if err == nil {
		answer = visibleAnswer(texts)
	}
	if err == nil && answer == "" {
		err = errors.New("le modèle n'a rien répondu")
	}
	stopped := errors.Is(ctx.Err(), context.Canceled)

	done := time.Now()
	lastActivity.Store(done.UnixMilli())
	if err != nil {
		c.closeFailed(msgs[base], answer, err, stopped, done)
		if stopped {
			emit(event{Type: "stopped", Conv: c.id})
		} else {
			emit(event{Type: "error", Conv: c.id, Error: err.Error()})
		}
		return
	}
	c.closeDone(out, lead, base, answer, done)
	memory.LogExchange(text, answer)
	c.noteTurn(text, calls, saved)
	WakeSearchIndex()
	emit(event{Type: "done", Conv: c.id, Text: answer, TS: done.UnixMilli(), Model: modelName()})
}

// modelName : le nom de la configuration active (sinon le modèle), affiché sous
// chaque réponse pour savoir qui l'a écrite.
func modelName() string {
	if p, ok := config.Get().Preset(); ok {
		if p.Name != "" {
			return p.Name
		}
		return p.Model
	}
	return ""
}

// open inscrit le message dans le fil et l'annonce aux interfaces. Renvoie une
// copie du contexte du modèle (dont les lead premiers messages sont le résumé de
// ce qui en est sorti) et les derniers messages de l'utilisateur.
func (c *conversation) open(text string, files []string, now time.Time, cancel context.CancelFunc) (msgs []llm.Message, lead int, recent []string) {
	c.mu.Lock()
	c.cancel = cancel
	recent = c.userTextsLocked()
	msgs, lead = c.contextLocked()
	titled := c.title == ""
	if titled {
		c.title = clip(strings.Join(strings.Fields(text), " "), titleMax)
	}
	c.log = append(c.log, Item{Role: "user", Text: text, TS: now.UnixMilli(), Files: files})
	c.saveLocked()
	c.mu.Unlock()

	if titled {
		emit(event{Type: "convs"})
	}
	emit(event{Type: "user", Conv: c.id, Text: text, TS: now.UnixMilli(), Files: files})
	emit(event{Type: "start", Conv: c.id})
	return msgs, lead, recent
}

// userMessage : le message tel que le modèle le reçoit, daté et accompagné de
// ce que la mémoire a de pertinent pour lui (le fil affiché et le journal, eux,
// gardent le texte brut).
func userMessage(ctx context.Context, text string, files []string, now time.Time, recent []string) llm.Message {
	ctx, cancel := context.WithTimeout(ctx, recallTimeout)
	defer cancel()
	return llm.Message{
		Role:    "user",
		Content: userContent(now, text, files) + memory.Recall(ctx, text, recent),
		Images:  images(files),
	}
}

// images : les images jointes, prêtes à être montrées au modèle.
func images(files []string) []string {
	var out []string
	for _, f := range files {
		typ := mime.TypeByExtension(strings.ToLower(filepath.Ext(f)))
		if !strings.HasPrefix(typ, "image/") {
			continue
		}
		abs, err := workspace.Abs(f)
		if err != nil {
			continue
		}
		if raw, err := os.ReadFile(abs); err == nil && len(raw) <= maxImageBytes {
			out = append(out, "data:"+typ+";base64,"+base64.StdEncoding.EncodeToString(raw))
		}
	}
	return out
}

// closeDone enregistre un tour réussi : la réponse dans le fil, les messages du
// tour dans le contexte du modèle.
func (c *conversation) closeDone(out []llm.Message, lead, base int, answer string, at time.Time) {
	// Les images (lourdes) et la réflexion du modèle ne servent qu'au tour qui les a vues.
	for i := base; i < len(out); i++ {
		out[i].Images, out[i].Reasoning = nil, ""
	}
	c.mu.Lock()
	c.cancel, c.busy = nil, false
	c.msgs = out[lead:] // sans le résumé placé en tête pour ce tour
	c.log = append(c.log, Item{Role: "assistant", Text: answer, TS: at.UnixMilli(), Model: modelName()})
	dropped := c.trimLocked()
	c.saveLocked()
	c.mu.Unlock()
	c.summarizeLater(dropped)
	emit(event{Type: "convs"})
}

// closeFailed enregistre un tour interrompu ou en erreur. Le message de
// l'utilisateur et ce qui reste de la réponse entrent quand même dans le
// contexte : sinon, à « réessaie », le modèle ne saurait pas de quoi on parle.
func (c *conversation) closeFailed(user llm.Message, partial string, err error, stopped bool, at time.Time) {
	note := partial
	switch {
	case note != "":
	case stopped:
		note = "Réponse interrompue."
	default:
		note = "Réponse impossible : " + err.Error()
	}
	user.Images = nil
	c.mu.Lock()
	c.cancel, c.busy = nil, false
	c.msgs = append(c.msgs, user, llm.Message{Role: "assistant", Content: note})
	c.log = append(c.log, Item{Role: "assistant", Text: note, TS: at.UnixMilli(), Kind: kindFailed})
	dropped := c.trimLocked()
	c.saveLocked()
	c.mu.Unlock()
	c.summarizeLater(dropped)
	emit(event{Type: "convs"})
}

// shortFollowUp : en dessous, un dernier message n'est pas une réponse mais un
// mot de la fin (« C'est noté. »).
const shortFollowUp = 150

// visibleAnswer : ce que le fil garde d'un tour. En chemin, le modèle commente
// ce qu'il fait (« je cherche… », « ce site bloque, j'essaie ailleurs ») : utile
// en direct, du bruit une fois la réponse donnée. La réponse est son dernier
// message. Sauf s'il a répondu PUIS rangé sa mémoire et conclu d'un mot : la
// réponse est alors le plus long message, suivi de ce mot.
func visibleAnswer(texts []string) string {
	if len(texts) == 0 {
		return ""
	}
	last := texts[len(texts)-1]
	longest := last
	for _, t := range texts {
		if len(t) > len(longest) {
			longest = t
		}
	}
	if longest == last || len([]rune(last)) >= shortFollowUp {
		return last
	}
	return longest + "\n\n" + last
}
