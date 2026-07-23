package jean

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
// epoch est celui capturé au début du tour : si un Reset est passé entre-temps,
// l'événement appartient à l'ancienne conversation et est jeté (sinon il
// polluerait le journal tout neuf avec des Seq repartis de zéro).
func (c *Conversation) appendDelta(epoch int, delta map[string]any) {
	c.mu.Lock()
	if c.epoch != epoch {
		c.mu.Unlock()
		return
	}
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
	epoch := c.epoch
	c.mu.Unlock()

	// Borne de tour + bulle utilisateur (rejouables). Persistée tout de suite :
	// si le process meurt en pleine génération (crash, restart après MAJ), le
	// message de l'utilisateur survit au lieu de disparaître avec le tour.
	c.appendDelta(epoch, map[string]any{"user": text})
	c.persist()
	if temperature == 0 {
		temperature = 0.7
	}
	go c.generate(ctx, caps, temperature, epoch)
	return nil
}

// generate exécute un tour complet et journalise chaque événement. Détaché : la
// fermeture du navigateur n'a aucun effet ici, seul /stop (cancel) l'interrompt.
// epoch est capturé au StartTurn : si un Reset survient pendant la génération,
// tout ce que ce tour produirait ensuite (deltas, messages, persistance) est
// abandonné au lieu de ressusciter des morceaux de l'ancienne conversation.
func (c *Conversation) generate(ctx context.Context, caps Caps, temperature float64, epoch int) {
	defer func() {
		c.mu.Lock()
		c.Generating = false
		c.cancel = nil
		stale := c.epoch != epoch
		c.mu.Unlock()
		if stale {
			return // Reset pendant le tour : Reset a déjà persisté l'état vide
		}
		c.appendDelta(epoch, map[string]any{"turn_done": true})
		c.persist()
	}()

	// Snapshot de la vue modèle.
	c.mu.Lock()
	msgs := append([]Message(nil), c.Messages...)
	ctxUsed := c.CtxUsed
	c.mu.Unlock()

	// Compaction proactive (façon Hermes) sur la vue MODÈLE uniquement ; le journal
	// d'affichage garde le fil complet. On signale juste un toast au client.
	// Si le contexte dépasse le seuil, le résumé (un appel modèle non streamé) va
	// bloquer plusieurs secondes AVANT que la vraie réponse commence. On affiche
	// une bannière « compactage en cours » pendant ce temps (sinon l'UI se fige
	// sans aucune info), puis on la retire — que le compactage ait changé quelque
	// chose ou non.
	willCompact := compactWouldTrigger(msgs, ctxUsed)
	if willCompact {
		c.appendDelta(epoch, map[string]any{"compacting": true})
	}
	if compacted, changed := MaybeCompact(ctx, msgs, caps, ctxUsed); changed {
		// Surcoût fixe non compactable (prompt système injecté + schémas d'outils +
		// gabarit) = contexte réel mesuré − estimation des messages. On le rajoute à
		// l'estimation post-compaction pour que la jauge affiche une valeur réaliste
		// (sinon elle chute trop bas puis resaute au tour suivant).
		overhead := ctxUsed - estimateTokens(msgs)
		if overhead < 0 {
			overhead = 0
		}
		est := estimateTokens(compacted) + overhead
		msgs = compacted
		c.mu.Lock()
		if c.epoch == epoch {
			c.Messages = compacted
			c.CtxUsed = est // le vrai compte reviendra avec les stats du tour
		}
		c.mu.Unlock()
		c.appendDelta(epoch, map[string]any{"compacting": false})
		c.appendDelta(epoch, map[string]any{"compacted": true})
		c.appendDelta(epoch, map[string]any{"ctx_used": est}) // fait chuter la jauge tout de suite
	} else if willCompact {
		c.appendDelta(epoch, map[string]any{"compacting": false})
	}

	// Prompt système personnalisé (UI → /api/sysprompt, fichier côté serveur).
	// Injecté seulement dans la vue envoyée au modèle, jamais persisté dans
	// c.Messages : modifiable à chaud, effet dès le tour suivant.
	final := msgs
	if sp := readSysPrompt(); sp != "" {
		final = append([]Message{{Role: "system", Content: sp}}, msgs...)
	}

	var content strings.Builder
	extra, _ := runChat(ctx, InjectSkills(final, caps), temperature, caps, func(ev StreamEvent) bool {
		switch {
		case ev.Err != nil:
			c.appendDelta(epoch, map[string]any{"error": ev.Err.Error()})
		case ev.ToolUsed != nil:
			c.appendDelta(epoch, map[string]any{"tool_used": map[string]any{
				"name": ev.ToolUsed.Name, "label": ev.ToolUsed.Label,
				"result": ev.ToolUsed.Result, "done": ev.ToolUsed.Done, "typing": ev.ToolUsed.Typing,
			}})
		case ev.Stats != nil:
			// Taille réelle du contexte (usage.prompt_tokens + généré) pour le compteur
			// et la décision de compactage au tour suivant.
			if ev.Stats.PromptTokensTotal > 0 {
				c.mu.Lock()
				if c.epoch == epoch {
					c.CtxUsed = ev.Stats.PromptTokensTotal + ev.Stats.GenTokens
				}
				c.mu.Unlock()
			}
			c.appendDelta(epoch, map[string]any{"stats": ev.Stats})
		case ev.DropReasoning:
			c.appendDelta(epoch, map[string]any{"drop_reasoning": true})
		case ev.Reasoning != "":
			c.appendDelta(epoch, map[string]any{"reasoning_content": ev.Reasoning})
		case ev.Content != "":
			content.WriteString(ev.Content)
			c.appendDelta(epoch, map[string]any{"content": ev.Content})
		}
		return true // génération détachée : on ne s'interrompt jamais sur un abonné
	})

	// Persiste la vue modèle : messages d'outils (assistant tool_calls + résultats)
	// PUIS la réponse finale — même ordre que l'ancien client, pour que le modèle
	// garde la trace de ce qu'il a fait. Sauf si un Reset est passé entre-temps :
	// la nouvelle conversation vide ne doit pas hériter de la fin de l'ancienne.
	c.mu.Lock()
	if c.epoch == epoch {
		c.Messages = append(c.Messages, extra...)
		if s := content.String(); strings.TrimSpace(s) != "" {
			c.Messages = append(c.Messages, Message{Role: "assistant", Content: s})
		}
	}
	c.mu.Unlock()
}

// CompactNow force une compaction du contexte MAINTENANT, sans attendre le seuil
// (bouton « compacter » de l'UI). Détaché comme la génération : émet la bannière
// de progression, résume les anciens tours, remplace le torse et persiste. Les
// événements passent par le flux d'abonnement, donc tous les appareils voient la
// progression. Renvoie ErrBusy si un tour est déjà en cours.
func (c *Conversation) CompactNow() error {
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
	msgs := append([]Message(nil), c.Messages...)
	lastReal := c.CtxUsed // dernier contexte réel mesuré, pour estimer le surcoût fixe
	epoch := c.epoch
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			c.Generating = false
			c.cancel = nil
			c.mu.Unlock()
		}()
		c.appendDelta(epoch, map[string]any{"compacting": true})
		compacted, changed := compactMessages(ctx, msgs, Caps{})
		c.appendDelta(epoch, map[string]any{"compacting": false})
		if !changed {
			c.appendDelta(epoch, map[string]any{"compact_noop": true})
			return
		}
		overhead := lastReal - estimateTokens(msgs)
		if overhead < 0 {
			overhead = 0
		}
		est := estimateTokens(compacted) + overhead // + surcoût fixe (système+outils) pour une jauge réaliste
		c.mu.Lock()
		if c.epoch == epoch {
			c.Messages = compacted
			c.CtxUsed = est // feedback immédiat ; le vrai compte revient au prochain tour
		}
		c.mu.Unlock()
		c.appendDelta(epoch, map[string]any{"compacted": true})
		c.appendDelta(epoch, map[string]any{"ctx_used": est})
		c.persist()
	}()
	return nil
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

// coalesceReplay fusionne les deltas texte consécutifs (content / reasoning_content)
// d'un même bloc en UN seul événement, pour que le replay au chargement soit léger
// (quelques événements par tour au lieu de milliers de tokens). On conserve le
// nombre de tokens fusionnés (toks) et les bornes d'horodatage (ts0→ts) pour que le
// client reconstitue le compteur ET la vitesse. Les événements non-texte (user,
// tool_used, stats, turn_done…) passent tels quels.
func coalesceReplay(events []LogEvent, from int) []map[string]any {
	var out []map[string]any
	var buf strings.Builder
	bufKey := ""
	var bufSeq, bufToks int
	var bufTs0, bufTs int64
	flush := func() {
		if bufKey == "" {
			return
		}
		out = append(out, map[string]any{bufKey: buf.String(), "seq": bufSeq, "ts": bufTs, "ts0": bufTs0, "toks": bufToks})
		buf.Reset()
		bufKey, bufSeq, bufToks, bufTs0, bufTs = "", 0, 0, 0, 0
	}
	// Coalescence des outils : un appel d'outil génère plein d'événements tool_used
	// intermédiaires (streaming des arguments) jusqu'à un dernier avec done=true. Au
	// replay, seul l'état FINAL de chaque bulle compte (les intermédiaires ne servent
	// qu'à l'affichage progressif en direct). Sans ça, un long fil rejoue des milliers
	// de tool_used → 2-3 s de rendu inutile sur mobile. On ne garde donc que les
	// done=true, en mémorisant le dernier non-done pour le cas d'un outil interrompu.
	var pendingTool map[string]any
	flushTool := func() {
		if pendingTool != nil {
			out = append(out, pendingTool)
			pendingTool = nil
		}
	}
	for _, ev := range events {
		if ev.Seq <= from {
			continue
		}
		_, isTool := ev.Delta["tool_used"].(map[string]any)
		// Tout événement NON-outil clôt une éventuelle bulle d'outil en attente, pour
		// préserver l'ordre (l'outil non terminé s'affiche avant ce qui le suit).
		if !isTool {
			flushTool()
		}
		// Delta texte ? (une seule clé content ou reasoning_content, valeur string)
		key := ""
		if s, ok := ev.Delta["content"].(string); ok {
			key, _ = "content", s
		} else if s, ok := ev.Delta["reasoning_content"].(string); ok {
			key, _ = "reasoning_content", s
		}
		if key != "" {
			if bufKey != "" && bufKey != key {
				flush()
			}
			if bufKey == "" {
				bufKey, bufTs0 = key, ev.TS
			}
			buf.WriteString(ev.Delta[key].(string))
			bufSeq, bufTs = ev.Seq, ev.TS
			bufToks++
			continue
		}
		flush()
		m := map[string]any{"seq": ev.Seq, "ts": ev.TS}
		for k, v := range ev.Delta {
			m[k] = v
		}
		if isTool {
			tu, _ := ev.Delta["tool_used"].(map[string]any)
			done, _ := tu["done"].(bool)
			if done {
				pendingTool = nil // les intermédiaires de cet outil sont superflus
				out = append(out, m)
			} else {
				pendingTool = m // on retient le dernier état non terminé, sans l'émettre
			}
			continue
		}
		out = append(out, m)
	}
	flush()
	flushTool()
	return out
}

// Subscribe diffuse les événements au client via emit : d'abord un REPLAY coalescé
// de Log[from:] (léger), puis un caught_up, puis le DIRECT événement par événement.
// Bloque jusqu'à ce que ctx (la connexion HTTP) soit annulé — la génération, elle,
// continue indépendamment. emit renvoie false si l'écriture échoue (client parti).
func (c *Conversation) Subscribe(ctx context.Context, from int, emit func(map[string]any) bool) {
	// Réveille les attentes de cond quand la connexion se ferme.
	go func() {
		<-ctx.Done()
		c.mu.Lock()
		c.cond.Broadcast()
		c.mu.Unlock()
	}()

	// 0. Amorçage anti-buffering. Le chat E2E d'app.ajean.link traverse Cloudflare
	// (ajean.link, proxy orange) : tant qu'un proxy intermédiaire n'a pas reçu assez
	// d'octets, il bufferise la réponse et ne la relaie qu'en retard (symptôme :
	// derniers messages qui arrivent 20-30 s après le reste, de façon intermittente
	// sur Safari). Envoyer d'emblée un gros événement de padding force le proxy à
	// basculer en mode streaming tout de suite. Le client ignore la clé `pad`.
	if !emit(map[string]any{"pad": strings.Repeat("·", 2048)}) {
		return
	}

	// 1. Replay coalescé (snapshot hors verrou pour ne pas bloquer la génération).
	c.mu.Lock()
	snapshot := append([]LogEvent(nil), c.Log...)
	epoch := c.epoch
	c.mu.Unlock()
	last := from
	for _, ev := range coalesceReplay(snapshot, from) {
		if ctx.Err() != nil {
			return
		}
		if !emit(ev) {
			return
		}
		if s, ok := ev["seq"].(int); ok {
			last = s
		}
	}
	if !emit(map[string]any{"caught_up": true}) {
		return
	}
	// Le padding doit venir APRÈS caught_up, pas dessus. Un proxy (Cloudflare) garde
	// toujours le DERNIER bout de flux en tampon jusqu'au prochain flush (~2-3 s). En
	// envoyant un gros pad juste après, ce sont ses octets — et non le dernier vrai
	// message — qui deviennent la « queue » qui attend : le dernier message et
	// caught_up, eux, sont poussés dehors immédiatement. Le client ignore `pad`.
	// 16 Ko : largement au-dessus du tampon de coalescence d'un proxy courant.
	if !emit(map[string]any{"pad": strings.Repeat("·", 16384)}) {
		return
	}

	// 2. Direct : événements granulaires au-delà de `last`.
	c.mu.Lock()
	for {
		if ctx.Err() != nil {
			c.mu.Unlock()
			return
		}
		if c.epoch != epoch { // reset → on ordonne au client de nettoyer et on repart
			epoch = c.epoch
			last = 0
			c.mu.Unlock()
			if !emit(map[string]any{"reset": true}) {
				return
			}
			c.mu.Lock()
			continue
		}
		// Copie des événements en attente SOUS verrou, émission HORS verrou : on
		// n'itère jamais sur c.Log pendant que la génération peut y écrire ou que
		// la troncature (maxLogEvents) peut le déplacer.
		var pending []LogEvent
		for _, ev := range c.Log {
			if ev.Seq > last {
				pending = append(pending, ev)
			}
		}
		if len(pending) == 0 {
			c.cond.Wait()
			continue
		}
		last = pending[len(pending)-1].Seq
		c.mu.Unlock()
		for _, ev := range pending {
			out := map[string]any{"seq": ev.Seq, "ts": ev.TS}
			for k, v := range ev.Delta {
				out[k] = v
			}
			if !emit(out) {
				return
			}
		}
		c.mu.Lock()
	}
}
