package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// État de conversation CÔTÉ SERVEUR — une seule conversation partagée par tous
// les appareils. Avant, l'historique vivait dans le localStorage de chaque
// navigateur : refresh = perte des détails (outils/vitesses/raisonnement),
// contexte différent par appareil, et fermer l'onglet coupait la génération
// (liée à r.Context()). Ici l'état est possédé par le serveur, persisté sur
// disque, et la génération tourne dans une goroutine détachée : fermer le
// navigateur ne l'arrête plus, et se reconnecter rejoue tout le fil.
//
// Idée clé : l'UI reconstruit déjà tout l'affichage à partir d'une suite
// d'événements SSE `delta` (content, reasoning_content, tool_used, stats…). On
// JOURNALISE ces événements (Log) horodatés par Seq. Se reconnecter = rejouer
// Log[from:] puis suivre les événements en direct — aucun code de rendu nouveau.

// maxLogEvents plafonne le journal d'AFFICHAGE (pas la vue modèle). Une très
// longue conversation ne fait pas exploser la mémoire/le disque ; la troncature
// ne retire que de vieilles bulles à l'écran, jamais du contexte du modèle
// (celui-ci vit dans Messages, réduit séparément par le compactage).
const maxLogEvents = 5000

// LogEvent = un événement d'affichage rejouable (un delta SSE + son numéro de
// séquence monotone + un horodatage serveur en ms). Le TS permet au client de
// calculer la vitesse (tok/s) à partir du temps RÉEL de génération — correct
// aussi bien en direct qu'au replay (où tout arrive d'un bloc côté client).
type LogEvent struct {
	Seq   int            `json:"seq"`
	TS    int64          `json:"ts"`
	Delta map[string]any `json:"delta"`
}

// Conversation est le fil unique partagé. Protégé par mu ; cond réveille les
// abonnés (aucun canal par abonné : les abonnés lisent Log au-delà de leur
// dernier Seq puis attendent cond — replay et direct sont le même chemin).
type Conversation struct {
	mu   sync.Mutex
	cond *sync.Cond

	Messages []Message  `json:"messages"` // vue « modèle » (nourrit runChat)
	Log      []LogEvent `json:"log"`      // vue « UI » rejouable
	Seq      int        `json:"seq"`
	CtxUsed  int        `json:"ctx_used"` // taille réelle du contexte au dernier tour

	Generating bool               `json:"-"`
	cancel     context.CancelFunc // annule la génération en cours (/stop)
	epoch      int                // incrémenté à chaque reset → invalide les abonnés
}

var conv = func() *Conversation {
	c := &Conversation{}
	c.cond = sync.NewCond(&c.mu)
	return c
}()

// convPath = fichier de persistance sur la box jean (JeanHome = /etc/jean en
// prod). En clair : la box déchiffre déjà pour lancer le modèle, le relais reste
// aveugle — le persister ici ne change rien à la posture E2E.
func convPath() string { return filepath.Join(JeanHome(), "conversation.json") }

// LoadConversation recharge l'état persisté au démarrage du process. Sans fichier
// (première fois) on part d'une conversation vide.
func LoadConversation() {
	b, err := os.ReadFile(convPath())
	if err != nil {
		return
	}
	conv.mu.Lock()
	defer conv.mu.Unlock()
	_ = json.Unmarshal(b, conv)
	// Une génération n'a pas pu survivre à l'arrêt du process : on repart propre.
	conv.Generating = false
	conv.cancel = nil
}

// persist écrit l'état sur disque (appelé en fin de tour et sur reset, pas à
// chaque delta). L'appelant NE doit PAS détenir mu.
func (c *Conversation) persist() {
	c.mu.Lock()
	b, err := json.Marshal(c)
	c.mu.Unlock()
	if err != nil {
		return
	}
	_ = os.MkdirAll(JeanHome(), 0o755)
	tmp := convPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, convPath())
}

// appendDelta journalise un événement d'affichage et réveille les abonnés.
func (c *Conversation) appendDelta(delta map[string]any) {
	c.mu.Lock()
	c.Seq++
	c.Log = append(c.Log, LogEvent{Seq: c.Seq, TS: time.Now().UnixMilli(), Delta: delta})
	if len(c.Log) > maxLogEvents {
		c.Log = c.Log[len(c.Log)-maxLogEvents:]
	}
	c.cond.Broadcast()
	c.mu.Unlock()
}

// convState renvoie un instantané léger (pour /api/chat/state).
func (c *Conversation) state() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{"seq": c.Seq, "generating": c.Generating, "ctx_used": c.CtxUsed}
}

// ErrBusy : une génération est déjà en cours (un seul tour à la fois).
var ErrBusy = fmt.Errorf("génération en cours")

// StartTurn ajoute le message utilisateur et lance la génération EN ARRIÈRE-PLAN
// (context.Background, détaché de toute connexion HTTP). Renvoie ErrBusy si un
// tour est déjà en cours, ou une erreur si le modèle n'est pas prêt.
func (c *Conversation) StartTurn(text string, caps Caps, temperature float64) error {
	if !healthCheck() {
		return fmt.Errorf("⏳ Le modèle est encore en train de charger — réessaie dans quelques secondes.")
	}
	c.mu.Lock()
	if c.Generating {
		c.mu.Unlock()
		return ErrBusy
	}
	c.Generating = true
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.Messages = append(c.Messages, Message{Role: "user", Content: text})
	c.mu.Unlock()

	// Borne de tour + bulle utilisateur (rejouables).
	c.appendDelta(map[string]any{"user": text})
	if temperature == 0 {
		temperature = 0.7
	}
	go c.generate(ctx, caps, temperature)
	return nil
}

// generate exécute un tour complet et journalise chaque événement. Détaché : la
// fermeture du navigateur n'a aucun effet ici, seul /stop (cancel) l'interrompt.
func (c *Conversation) generate(ctx context.Context, caps Caps, temperature float64) {
	defer func() {
		c.mu.Lock()
		c.Generating = false
		c.cancel = nil
		c.mu.Unlock()
		c.appendDelta(map[string]any{"turn_done": true})
		c.persist()
	}()

	// Snapshot de la vue modèle.
	c.mu.Lock()
	msgs := append([]Message(nil), c.Messages...)
	ctxUsed := c.CtxUsed
	c.mu.Unlock()

	// Compaction proactive (façon Hermes) sur la vue MODÈLE uniquement ; le journal
	// d'affichage garde le fil complet. On signale juste un toast au client.
	if compacted, changed := MaybeCompact(ctx, msgs, caps, ctxUsed); changed {
		msgs = compacted
		c.mu.Lock()
		c.Messages = compacted
		c.mu.Unlock()
		c.appendDelta(map[string]any{"compacted": true})
	}

	var content strings.Builder
	extra, _ := runChat(ctx, InjectSkills(msgs, caps), temperature, caps, func(ev StreamEvent) bool {
		switch {
		case ev.Err != nil:
			c.appendDelta(map[string]any{"error": ev.Err.Error()})
		case ev.ToolUsed != nil:
			c.appendDelta(map[string]any{"tool_used": map[string]any{
				"name": ev.ToolUsed.Name, "label": ev.ToolUsed.Label,
				"result": ev.ToolUsed.Result, "done": ev.ToolUsed.Done, "typing": ev.ToolUsed.Typing,
			}})
		case ev.Stats != nil:
			// Taille réelle du contexte (usage.prompt_tokens + généré) pour le compteur
			// et la décision de compactage au tour suivant.
			if ev.Stats.PromptTokensTotal > 0 {
				c.mu.Lock()
				c.CtxUsed = ev.Stats.PromptTokensTotal + ev.Stats.GenTokens
				c.mu.Unlock()
			}
			c.appendDelta(map[string]any{"stats": ev.Stats})
		case ev.DropReasoning:
			c.appendDelta(map[string]any{"drop_reasoning": true})
		case ev.Reasoning != "":
			c.appendDelta(map[string]any{"reasoning_content": ev.Reasoning})
		case ev.Content != "":
			content.WriteString(ev.Content)
			c.appendDelta(map[string]any{"content": ev.Content})
		}
		return true // génération détachée : on ne s'interrompt jamais sur un abonné
	})

	// Persiste la vue modèle : messages d'outils (assistant tool_calls + résultats)
	// PUIS la réponse finale — même ordre que l'ancien client, pour que le modèle
	// garde la trace de ce qu'il a fait.
	c.mu.Lock()
	c.Messages = append(c.Messages, extra...)
	if s := content.String(); strings.TrimSpace(s) != "" {
		c.Messages = append(c.Messages, Message{Role: "assistant", Content: s})
	}
	c.mu.Unlock()
}

// Stop interrompt la génération en cours (le cas échéant).
func (c *Conversation) Stop() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Reset démarre une nouvelle conversation (vide) pour TOUS les appareils. On
// interrompt une éventuelle génération, on vide tout et on bump epoch pour que
// les abonnés reçoivent l'ordre de nettoyer leur affichage.
func (c *Conversation) Reset() {
	c.Stop()
	c.mu.Lock()
	c.Messages = nil
	c.Log = nil
	c.Seq = 0
	c.CtxUsed = 0
	c.epoch++
	c.cond.Broadcast()
	c.mu.Unlock()
	c.persist()
}

// Subscribe diffuse les événements au client via emit, en commençant par le
// replay de Log[from:] puis en suivant le direct. Bloque jusqu'à ce que ctx (la
// connexion HTTP) soit annulé — la génération, elle, continue indépendamment.
// emit renvoie false si l'écriture échoue (client parti) → on sort.
func (c *Conversation) Subscribe(ctx context.Context, from int, emit func(map[string]any) bool) {
	// Réveille les attentes de cond quand la connexion se ferme (sinon cond.Wait
	// resterait bloqué faute de nouvel événement).
	go func() {
		<-ctx.Done()
		c.mu.Lock()
		c.cond.Broadcast()
		c.mu.Unlock()
	}()

	c.mu.Lock()
	last := from
	epoch := c.epoch
	announced := false // a-t-on signalé la fin du replay (caught_up) ?
	for {
		if ctx.Err() != nil {
			c.mu.Unlock()
			return
		}
		// Un reset a eu lieu : on ordonne au client de nettoyer et on repart de 0.
		if c.epoch != epoch {
			epoch = c.epoch
			last = 0
			c.mu.Unlock()
			if !emit(map[string]any{"reset": true}) {
				return
			}
			c.mu.Lock()
			continue
		}
		// Envoie tout ce qui est plus récent que `last`.
		sent := false
		for _, ev := range c.Log {
			if ev.Seq <= last {
				continue
			}
			last = ev.Seq
			delta := ev.Delta
			ts := ev.TS
			c.mu.Unlock()
			out := map[string]any{"seq": ev.Seq, "ts": ts}
			for k, v := range delta {
				out[k] = v
			}
			if !emit(out) {
				return
			}
			c.mu.Lock()
			sent = true
			if ctx.Err() != nil {
				c.mu.Unlock()
				return
			}
		}
		if sent {
			continue // il peut y avoir eu de nouveaux événements pendant l'envoi
		}
		// Replay drainé : on le signale UNE fois au client (il replie alors
		// instantanément les vieilles bulles, sans animation), avant de suivre le
		// direct. Événement synthétique, non journalisé.
		if !announced {
			announced = true
			c.mu.Unlock()
			if !emit(map[string]any{"caught_up": true}) {
				return
			}
			c.mu.Lock()
			continue
		}
		c.cond.Wait() // rien de neuf : dort jusqu'au prochain Broadcast (ou ctx annulé)
	}
}
