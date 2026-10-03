package ajean

// jean_conv.go — la conversation UNIQUE et sans fin du mode Jean. Choisir le mode
// Jean rouvre toujours ce même fil (id fixe), jamais une conversation neuve : le
// compactage (mémoire longue) le garde léger, le journal garde tout le reste.
//
// Jean peut aussi y écrire DE LUI-MÊME : le compte-rendu d'une tâche créée en
// mode Jean (rappel, veille…) arrive dans ce fil comme un message de Jean, avec
// une notification push, et son travail s'y affiche en direct pendant qu'elle
// tourne.

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	jeanConvID = "jean"
	// jeanTaskSlug : « projet » des tâches de Jean (pseudo-projet, jamais une vraie
	// mémoire de projet).
	jeanTaskSlug = "_jean"
	// jeanSilence : réponse d'une tâche Jean qui n'a rien d'utile à dire (une
	// veille sans nouveauté). Rien n'est alors posté.
	jeanSilence = "SILENCE"
)

// ---- ouvrir / vider la conversation ------------------------------------------

// OpenJean fait de la conversation Jean la conversation active (créée au besoin).
func (c *Conversation) OpenJean() error {
	if c.currentID() == jeanConvID {
		return nil
	}
	// Une tâche de Jean tourne déjà : on l'affiche dans le fil qu'on ouvre.
	defer c.jeanTaskReplay()
	if _, ok := loadArchive(jeanConvID); ok {
		return c.OpenSession(jeanConvID)
	}
	c.upsertSession()
	c.Reset()
	c.mu.Lock()
	c.ID = jeanConvID
	c.Mode = "jean"
	c.ActiveTitle = "Jean"
	c.mu.Unlock()
	c.persist()
	return nil
}

// handleChatJean (POST) : ouvre la conversation Jean.
func handleChatJean(w http.ResponseWriter, r *http.Request) {
	if err := conv.OpenJean(); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "active": jeanConvID})
}

// ClearJeanContext vide le contexte du MODÈLE dans le fil Jean, sans toucher au
// fil affiché : Jean repart léger (profil réinjecté au prochain message), et tout
// ce qui a été dit reste dans le journal et à l'écran.
func (c *Conversation) ClearJeanContext() error {
	c.mu.Lock()
	if c.ID != jeanConvID {
		c.mu.Unlock()
		return fmt.Errorf("la conversation Jean n'est pas ouverte")
	}
	if c.Generating && c.runningTaskID == "" {
		c.mu.Unlock()
		return ErrBusy
	}
	old := c.Messages
	c.Messages = nil
	c.CtxUsed = 0
	epoch := c.epoch
	c.mu.Unlock()
	jeanReflectBeforeClear(old) // rien de non révisé ne doit partir avec le contexte
	c.appendDelta(epoch, map[string]any{"ctx_cleared": true})
	c.appendDelta(epoch, map[string]any{"ctx_used": 0})
	c.persist()
	return nil
}

// handleChatJeanClear (POST) : « Vider le contexte » du mode Jean.
func handleChatJeanClear(w http.ResponseWriter, r *http.Request) {
	if err := conv.ClearJeanContext(); err != nil {
		sendJSON(w, 409, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// ---- messages spontanés ------------------------------------------------------

// JeanPost dépose un message de Jean dans sa conversation, qu'elle soit affichée
// ou non. Si un tour y tourne, on attend qu'il finisse (le message ne doit pas
// s'intercaler au milieu d'une réponse).
func (c *Conversation) JeanPost(text string) {
	text = strings.TrimSpace(jeanApplyRules(text)) // règles appliquées aussi aux messages des tâches
	if text == "" || strings.EqualFold(strings.Trim(text, " .\n"), jeanSilence) {
		return
	}
	deadline := time.Now().Add(30 * time.Minute)
	for {
		c.mu.Lock()
		active := c.ID == jeanConvID
		if active && c.Generating && c.runningTaskID == "" && time.Now().Before(deadline) {
			c.mu.Unlock()
			time.Sleep(2 * time.Second)
			continue
		}
		if active {
			c.Messages = append(c.Messages, Message{Role: "assistant", Content: text})
			epoch := c.epoch
			c.mu.Unlock()
			c.appendDelta(epoch, map[string]any{"jean_post": text})
			c.persist()
			c.upsertSessionMeta()
		} else {
			c.mu.Unlock()
			if err := jeanPostToArchive(text); err != nil {
				fmt.Fprintf(os.Stderr, "[jean] message non déposé : %v\n", err)
				return
			}
		}
		break
	}
	jeanMu.Lock()
	_ = jeanAppendLocked("message", "Jean (de lui-même) : "+clipRunes(text, jeanAutoExcerpt))
	jeanMu.Unlock()
	// Corps générique : la notification transite par Apple/Google.
	if hasPushSubs() {
		go sendPushToAll("Jean", "Jean t'a envoyé un message")
	}
}

// jeanPostToArchive ajoute le message à la conversation Jean archivée (pas
// affichée en ce moment). Elle est créée si elle n'existe pas encore.
func jeanPostToArchive(text string) error {
	a, ok := loadArchive(jeanConvID)
	if !ok {
		a = &convArchive{ID: jeanConvID, Project: jeanTaskSlug, Title: "Jean", Mode: "jean"}
	}
	now := time.Now().UnixMilli()
	a.Messages = append(a.Messages, Message{Role: "assistant", Content: text})
	a.Seq++
	a.Log = append(a.Log, LogEvent{Seq: a.Seq, TS: now, Delta: map[string]any{"jean_post": text}})
	a.SavedAt = now
	return saveArchive(a)
}

// ---- tâches de Jean ----------------------------------------------------------

// taskScope : à quelles tâches s'appliquent les outils task_* de ce tour. En mode
// Jean, celles de Jean ; sinon celles du projet actif.
func taskScope(args map[string]any) string {
	if args["_jean"] == true {
		return jeanTaskSlug
	}
	return activeProjectSlug()
}

// jeanTaskPrompt : consigne ajoutée à une tâche Jean. Son texte final devient un
// message dans la conversation Jean.
func jeanTaskPrompt(p string) string {
	return p + "\n\n(Your final answer is sent to the user as a message in your conversation with them: write it directly to them, short and natural, in their language. If there is nothing worth telling them this time, answer exactly " + jeanSilence + ".)"
}

// Tâche de Jean en cours et son dernier outil, pour la montrer aussi à qui ouvre
// le fil Jean pendant qu'elle tourne.
var (
	jeanTaskMu   sync.Mutex
	jeanTaskName string
	jeanTaskTool string
)

// jeanTaskDelta publie l'état de la tâche Jean dans le fil Jean, s'il est affiché.
// L'UI en fait la ligne d'activité animée (« Travaille sur… », « Cherche… »).
func (c *Conversation) jeanTaskDelta(d map[string]any) {
	c.mu.Lock()
	active, epoch := c.ID == jeanConvID, c.epoch
	c.mu.Unlock()
	if active {
		c.appendDelta(epoch, map[string]any{"jean_task": d})
	}
}

func (c *Conversation) jeanTaskStart(name string) {
	jeanTaskMu.Lock()
	jeanTaskName, jeanTaskTool = name, ""
	jeanTaskMu.Unlock()
	c.jeanTaskDelta(map[string]any{"name": name})
}

// jeanTaskAct signale l'outil que la tâche vient d'appeler (une fois par changement).
func (c *Conversation) jeanTaskAct(tool string) {
	jeanTaskMu.Lock()
	same := tool == jeanTaskTool
	jeanTaskTool = tool
	name := jeanTaskName
	jeanTaskMu.Unlock()
	if !same {
		c.jeanTaskDelta(map[string]any{"name": name, "tool": tool})
	}
}

func (c *Conversation) jeanTaskEnd() {
	jeanTaskMu.Lock()
	jeanTaskName, jeanTaskTool = "", ""
	jeanTaskMu.Unlock()
	c.jeanTaskDelta(map[string]any{"done": true})
}

// jeanTaskReplay republie la tâche en cours (s'il y en a une) dans le fil Jean.
func (c *Conversation) jeanTaskReplay() {
	jeanTaskMu.Lock()
	name, tool := jeanTaskName, jeanTaskTool
	jeanTaskMu.Unlock()
	if name != "" {
		c.jeanTaskDelta(map[string]any{"name": name, "tool": tool})
	}
}

// ---- contexte vidé après inactivité ------------------------------------------

// jeanIdleHours : délai d'inactivité (heures) au-delà duquel le prochain message
// repart d'un contexte vide. Clé de config JEAN_IDLE_HOURS, 0 = jamais. 3 h par
// défaut : une conversation reprise le lendemain n'a pas besoin de traîner celle
// de la veille (le journal et jean_search la retrouvent au besoin), et le fil
// unique de Jean ne grossit plus sans fin.
func jeanIdleHours() float64 {
	if v := strings.TrimSpace(ReadConfig()["JEAN_IDLE_HOURS"]); v != "" {
		if h, err := strconv.ParseFloat(v, 64); err == nil && h >= 0 {
			return h
		}
	}
	return 3
}

// jeanIdleExpired : le dernier message de l'UTILISATEUR (pas les messages
// spontanés de Jean) date-t-il de plus que le délai ?
func jeanIdleExpired(log []LogEvent, now time.Time) bool {
	h := jeanIdleHours()
	if h <= 0 {
		return false
	}
	for i := len(log) - 1; i >= 0; i-- {
		if _, ok := log[i].Delta["user"]; ok {
			return now.Sub(time.UnixMilli(log[i].TS)) > time.Duration(h*float64(time.Hour))
		}
	}
	return false
}

// ---- plusieurs appareils -------------------------------------------------------

// errOtherConvBusy : l'envoi viserait une autre conversation que celle qui génère.
var errOtherConvBusy = fmt.Errorf("une réponse est en cours dans une autre conversation (sur un autre appareil ?) : attends qu'elle finisse")

// alignConvForMode garantit qu'un message part dans la BONNE conversation. Le
// serveur n'a qu'une conversation active, partagée par tous les appareils : un
// téléphone resté en mode Jean pendant qu'un autre appareil ouvrait une
// conversation de projet envoyait sa question DANS ce projet (et la réponse
// n'apparaissait jamais chez Jean). Ici :
//   - mode Jean demandé, conversation active ≠ Jean → on ouvre celle de Jean ;
//   - autre mode demandé, conversation active = Jean → nouvelle conversation.
//
// Jamais pendant une génération (on ne coupe pas une réponse en cours ailleurs).
func (c *Conversation) alignConvForMode(want string) error {
	onJean := c.currentID() == jeanConvID
	if (want == "jean") == onJean {
		return nil
	}
	if c.isGenerating() {
		return errOtherConvBusy
	}
	if want == "jean" {
		return c.OpenJean()
	}
	c.NewSession()
	return nil
}
