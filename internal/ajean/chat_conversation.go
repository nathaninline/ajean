package ajean

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

// maxLogEvents plafonne le journal d'AFFICHAGE (pas la vue modèle). Depuis la
// coalescence en fin de tour (compactLogLocked), un tour terminé ne pèse plus
// qu'une poignée d'événements au lieu d'un par token → 20000 couvre des
// centaines de tours. La marge sert surtout à absorber UN tour en cours (streamé
// token par token) avant sa coalescence : un très gros tour (long raisonnement +
// réponse) ne doit pas se faire tronquer le début avant d'être compacté.
const maxLogEvents = 20000

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

	// ID stable de la SESSION en cours. La conversation active est une session
	// comme les autres (reflétée dans le bucket des sessions via upsertSession) :
	// ouvrir/renommer/favoriser passe par cet id, qui survit à open→new.
	ID string `json:"id"`

	Messages []Message  `json:"messages"` // vue « modèle » (nourrit runChat)
	Log      []LogEvent `json:"log"`      // vue « UI » rejouable
	Seq      int        `json:"seq"`
	CtxUsed  int        `json:"ctx_used"` // taille réelle du contexte au dernier tour

	// Nom personnalisé et favori HÉRITÉS d'une conversation restaurée depuis
	// l'historique : ils voyagent avec la conversation active pour qu'un
	// « recharger » puis « clear chat » les conserve au ré-archivage (au lieu de
	// repartir sur un titre auto sans favori). Vides pour une conversation neuve.
	ActiveTitle string `json:"active_title,omitempty"`
	ActiveFav   bool   `json:"active_fav,omitempty"`

	Generating bool      `json:"-"`
	genStart   time.Time // début du tour en cours → permet à un client qui se reconnecte de reprendre le chrono à la BONNE valeur (pas à zéro)
	// pendingReplay distingue les deux causes d'un bump d'epoch : une RESTAURATION
	// depuis l'historique (true → un fil complet suit, à rejouer REPLIÉ comme au
	// chargement de page) vs un « clear chat » (false → conversation vide). Lu par
	// les abonnés au moment où ils constatent le changement d'epoch.
	pendingReplay bool
	cancel        context.CancelFunc // annule la génération en cours (/stop)
	epoch         int                // incrémenté à chaque reset → invalide les abonnés

	// Quand le verrou de génération est pris par une TÂCHE de fond (RunAutonomous)
	// et non par un tour utilisateur, on retient son id/nom : l'UI affiche alors
	// « tâche en cours » au lieu de faire croire à l'utilisateur qu'il génère, et
	// le bouton stop s'étiquette en conséquence. Vides = c'est un tour normal.
	runningTaskID   string
	runningTaskName string
}

var conv = func() *Conversation {
	c := &Conversation{ID: newSessionID()}
	c.cond = sync.NewCond(&c.mu)
	return c
}()

// La conversation est persistée en base, sur la machine qui fait tourner le
// modèle. En clair : cette machine déchiffre déjà pour lancer le modèle, le
// relais reste aveugle — la persister ici ne change rien à la posture E2E.

// LoadConversation recharge l'état persisté au démarrage du process. Sans état
// enregistré (première fois) on part d'une conversation vide.
func LoadConversation() {
	b := getBytes(bkChat, "conversation")
	if len(b) == 0 {
		return
	}
	conv.mu.Lock()
	defer conv.mu.Unlock()
	_ = json.Unmarshal(b, conv)
	// État d'avant l'id de session (migration) : on lui en attribue un pour qu'il
	// devienne une session gérable comme les autres.
	if conv.ID == "" {
		conv.ID = newSessionID()
	}
	// Une génération n'a pas pu survivre à l'arrêt du process : on repart propre.
	conv.Generating = false
	conv.cancel = nil
}

// newSessionID génère un identifiant de session unique (nanosecondes), même
// schéma que les archives.
func newSessionID() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

// persist enregistre l'état (appelé en fin de tour et sur reset, pas à chaque
// delta). L'appelant NE doit PAS détenir mu.
func (c *Conversation) persist() {
	c.mu.Lock()
	b, err := json.Marshal(c)
	c.mu.Unlock()
	if err != nil {
		return
	}
	_ = putBytes(bkChat, "conversation", b)
	// La conversation active EST une session : on la reflète dans la liste des
	// sessions pour qu'elle y apparaisse (marquée « en cours ») et reste à jour.
	c.upsertSession()
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

// evToks lit le nombre de tokens porté par un événement texte : 1 pour un delta
// brut (streaming), ou la valeur `toks` accumulée pour un événement déjà coalescé.
func evToks(d map[string]any) int {
	switch v := d["toks"].(type) {
	case int:
		return v
	case float64: // relu depuis le JSON persisté
		return int(v)
	}
	return 1
}

// evTS0 lit l'horodatage de DÉBUT d'un événement texte (présent seulement sur les
// événements coalescés) ; sinon on retombe sur `fallback` (le TS de l'événement).
func evTS0(d map[string]any, fallback int64) int64 {
	switch v := d["ts0"].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return fallback
}

// compactLogLocked coalesce EN PLACE les suites d'événements content /
// reasoning_content du journal d'affichage en un seul événement chacun (même
// logique que coalesceReplay). Sans ça, le journal grossit token par token et
// atteint maxLogEvents en quelques réponses → on perd le DÉBUT de la conversation
// à l'affichage. On l'appelle en fin de tour (les événements sont alors figés).
// On préserve `toks` (somme) et `ts0` (premier) pour que le compteur de vitesse
// (tok/s) reste correct au replay. Verrou détenu par l'appelant.
func (c *Conversation) compactLogLocked() {
	if len(c.Log) < 2 {
		return
	}
	textKey := func(d map[string]any) string {
		if _, ok := d["content"].(string); ok {
			return "content"
		}
		if _, ok := d["reasoning_content"].(string); ok {
			return "reasoning_content"
		}
		return ""
	}
	out := make([]LogEvent, 0, len(c.Log))
	var buf strings.Builder
	bufKey := ""
	var cur LogEvent
	var toks int
	var ts0 int64
	var seq0 int
	flush := func() {
		if bufKey == "" {
			return
		}
		// seq0 = seq du PREMIER delta fusionné (cur.Seq, lui, vaut celui du dernier).
		cur.Delta = map[string]any{bufKey: buf.String(), "toks": toks, "ts0": ts0, "seq0": seq0}
		out = append(out, cur)
		buf.Reset()
		bufKey, toks, ts0, seq0 = "", 0, 0, 0
	}
	for _, ev := range c.Log {
		key := textKey(ev.Delta)
		if key == "" {
			flush()
			out = append(out, ev)
			continue
		}
		if bufKey != "" && bufKey != key {
			flush()
		}
		if bufKey == "" {
			bufKey = key
			cur = LogEvent{Seq: ev.Seq, TS: ev.TS}
			ts0 = evTS0(ev.Delta, ev.TS)
			seq0 = evSeq0(ev.Delta, ev.Seq)
		}
		buf.WriteString(ev.Delta[key].(string))
		cur.Seq, cur.TS = ev.Seq, ev.TS
		toks += evToks(ev.Delta)
	}
	flush()
	c.Log = out
}

// compactAndPublish exécute UNE compaction et en publie tout le cycle de vie :
// bannière de progression, journal, installation du résultat, jauge de contexte.
// Renvoie l'historique (compacté ou inchangé) et s'il a changé.
//
// Les trois compactions (début de tour, fin de tour, bouton manuel) déroulaient
// la même douzaine de lignes recopiées, y compris le calcul du surcoût fixe :
// une correction dans l'une ne suivait pas dans les deux autres.
//
// Le surcoût, justement : le contexte RÉEL mesuré moins l'estimation des
// messages donne ce que l'estimation ne voit pas (prompt système injecté,
// schémas d'outils, gabarit de chat). On le rajoute à l'estimation d'après
// compaction, sinon la jauge s'effondre puis resaute au tour suivant.
func (c *Conversation) compactAndPublish(ctx context.Context, epoch int, phase string, msgs []Message, ctxUsed int, caps Caps) ([]Message, bool) {
	c.appendDelta(epoch, map[string]any{"compacting": true})
	compacted, changed := compactMessages(ctx, msgs, caps)
	c.appendDelta(epoch, map[string]any{"compacting": false})
	logCompact(phase, ctxUsed, msgs, compacted, changed)
	if !changed {
		// Seuil franchi mais compaction sans effet (torse vide, ou réduction sous
		// le minimum exigé) : on le DIT. Retirer la bannière sans un mot donnait,
		// vu de l'UI, « le compactage automatique ne fait rien ».
		c.appendDelta(epoch, map[string]any{"compact_noop": true})
		return msgs, false
	}
	overhead := ctxUsed - estimateTokens(msgs)
	if overhead < 0 {
		overhead = 0
	}
	est := estimateTokens(compacted) + overhead
	c.mu.Lock()
	if c.epoch == epoch {
		c.Messages = compacted
		c.CtxUsed = est // le vrai compte reviendra avec les stats du prochain tour
	}
	c.mu.Unlock()
	c.appendDelta(epoch, map[string]any{"compacted": true})
	c.appendDelta(epoch, map[string]any{"ctx_used": est}) // fait chuter la jauge tout de suite
	return compacted, true
}

// convState renvoie un instantané léger (pour /api/chat/state).
func (c *Conversation) state() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	// `turns` = nombre d'échanges, borne du curseur de portée de l'export (voir
	// countTurns). Compté ici plutôt que par un appel dédié : c'est un balayage
	// du journal déjà en main, et l'état est de toute façon relu à l'ouverture.
	turns := 0
	for _, ev := range c.Log {
		if _, ok := ev.Delta["user"]; ok {
			turns++
		}
	}
	// Durée écoulée du tour EN COURS (ms) : un client qui recharge la page en pleine
	// génération reprend ainsi le chrono à la vraie valeur, au lieu de le faire
	// repartir de zéro (l'événement `user` qui l'aurait démarré a été consommé au
	// replay). 0 hors génération.
	var genElapsed int64
	if c.Generating && !c.genStart.IsZero() {
		genElapsed = time.Since(c.genStart).Milliseconds()
	}
	return map[string]any{
		"seq": c.Seq, "generating": c.Generating, "ctx_used": c.CtxUsed, "turns": turns,
		"gen_elapsed_ms": genElapsed,
		// Non vide seulement quand une tâche de fond occupe le verrou de génération.
		"running_task":    c.runningTaskName,
		"running_task_id": c.runningTaskID,
	}
}

// ErrBusy : une génération est déjà en cours (un seul tour à la fois).
var ErrBusy = fmt.Errorf("génération en cours")

// errModelLoading est renvoyée telle quelle à l'utilisateur, dans le chat : ce
// n'est pas un défaut mais une attente, et le message doit le dire.
var errModelLoading = fmt.Errorf("⏳ Le modèle est encore en train de charger — réessaie dans quelques secondes.")

// StartTurn ajoute le message utilisateur et lance la génération EN ARRIÈRE-PLAN
// (context.Background, détaché de toute connexion HTTP). Renvoie ErrBusy si un
// tour est déjà en cours, ou une erreur si le modèle n'est pas prêt.
// files = pièces jointes déjà déposées (web_upload.go). Elles sont annoncées au
// MODÈLE en tête du message, mais rendues comme pastilles dans la bulle : le fil
// doit montrer ce que l'utilisateur a écrit, pas la consigne qu'on ajoute pour lui.
func (c *Conversation) StartTurn(text string, files []attachInfo, caps Caps, temperature float64) error {
	if !healthCheck() {
		return errModelLoading
	}
	c.mu.Lock()
	if c.Generating {
		c.mu.Unlock()
		return ErrBusy
	}
	c.Generating = true
	c.genStart = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	// Envoi sans un mot, juste un fichier : la bulle reste vide (les pastilles
	// disent tout), mais le modèle a besoin d'une demande — sans elle il reçoit
	// une liste de fichiers et rien à en faire.
	prompt := text
	if strings.TrimSpace(prompt) == "" {
		prompt = "Prends-en connaissance."
	}
	// Content = simple texte d'ordinaire ; format multimodal (texte + images) quand
	// la vision est active et qu'une pièce jointe est une image (userMessageContent).
	c.Messages = append(c.Messages, Message{Role: "user", Content: userMessageContent(files, prompt)})
	epoch := c.epoch
	c.mu.Unlock()

	// Borne de tour + bulle utilisateur (rejouables). Persistée tout de suite :
	// si le process meurt en pleine génération (crash, restart après MAJ), le
	// message de l'utilisateur survit au lieu de disparaître avec le tour.
	delta := map[string]any{"user": text}
	if len(files) > 0 {
		delta["files"] = files
	}
	c.appendDelta(epoch, delta)
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
	// Horloge du tour : durée réelle (préchauffe + réflexion + outils + réponse),
	// journalisée dans turn_done pour que l'UI affiche la MÊME durée en direct et
	// après un rechargement (le chrono client, lui, n'existe qu'en direct).
	turnStart := time.Now()
	defer func() {
		c.mu.Lock()
		stale := c.epoch != epoch
		// ⚠️ Un tour périmé ne touche PAS à l'état courant. Depuis que Reset
		// débloque lui-même la conversation, un tour abandonné peut se terminer
		// APRÈS le démarrage du suivant : remettre Generating à false ici
		// déclarerait « libre » une génération toute neuve, et l'UI afficherait
		// un fil qui se remplit avec un bouton « envoyer » actif.
		if !stale {
			c.Generating = false
			c.cancel = nil
		}
		c.mu.Unlock()
		if stale {
			return // Reset pendant le tour : Reset a déjà persisté l'état vide
		}
		c.appendDelta(epoch, map[string]any{"turn_done": true, "elapsed_ms": time.Since(turnStart).Milliseconds()})
		c.mu.Lock()
		c.compactLogLocked() // le tour est fini : coalesce ses tokens pour garder le journal petit
		c.mu.Unlock()
		c.persist()
		// Notification Web Push : ce chemin (generate) ne sert QUE les tours
		// utilisateur — les tâches de fond passent par RunAutonomous, sans turn_done
		// dans la conversation partagée — donc pas de spam. Détaché : l'envoi HTTP
		// vers le service de push ne doit pas retenir la fin du tour. Corps générique
		// (pas d'extrait de réponse) : la notif transite par Apple/Google.
		if hasPushSubs() {
			go sendPushToAll("AJEAN", "Réponse prête · "+fmtDurFR(time.Since(turnStart)))
		}
	}()

	// Snapshot de la vue modèle.
	c.mu.Lock()
	msgs := append([]Message(nil), c.Messages...)
	ctxUsed := c.CtxUsed
	c.mu.Unlock()

	// Compaction proactive (façon Hermes) sur la vue MODÈLE uniquement ; le journal
	// d'affichage garde le fil complet. Le résumé est un appel modèle non streamé :
	// il bloque plusieurs secondes AVANT que la vraie réponse commence, d'où la
	// bannière de progression émise par compactAndPublish.
	if compactWouldTrigger(msgs, ctxUsed) {
		if out, changed := c.compactAndPublish(ctx, epoch, "début-tour", msgs, ctxUsed, caps); changed {
			msgs = out
		}
	}

	// Prompt système personnalisé (UI → /api/sysprompt, fichier côté serveur).
	// Injecté seulement dans la vue envoyée au modèle, jamais persisté dans
	// c.Messages : modifiable à chaud, effet dès le tour suivant.
	final := msgs
	if sp := readSysPrompt(); sp != "" {
		final = append([]Message{{Role: "system", Content: sp}}, msgs...)
	}

	// newBase : vue modèle publiée par une compaction survenue PENDANT le tour.
	// Non-nil = elle remplace l'historique (elle contient déjà le tour en cours).
	var newBase []Message
	var content strings.Builder
	extra, _ := runChat(ctx, InjectSkills(final, caps), temperature, caps, func(ev StreamEvent) bool {
		switch {
		case ev.Err != nil:
			// Arrêt volontaire (bouton stop → cancel du contexte) : ce n'est pas une
			// erreur, juste une interruption. Afficher « Post http://…: context
			// canceled » en rouge est laid et alarmant pour rien — on pose à la place
			// une note discrète. Un vrai échec (ctx non annulé) garde son message.
			if ctx.Err() != nil {
				c.appendDelta(epoch, map[string]any{"content": "\n\n_Génération interrompue._"})
			} else {
				c.appendDelta(epoch, map[string]any{"error": ev.Err.Error()})
			}
		case ev.ToolUsed != nil:
			tu := map[string]any{
				"name": ev.ToolUsed.Name, "label": ev.ToolUsed.Label,
				"result": ev.ToolUsed.Result, "done": ev.ToolUsed.Done, "typing": ev.ToolUsed.Typing,
			}
			// Corps en cours de frappe : transitoire (l'état final est le diff), on
			// ne l'ajoute que quand il est là pour ne pas gonfler chaque événement.
			if ev.ToolUsed.Body != "" {
				tu["body"] = ev.ToolUsed.Body
			}
			// Lignes +/- d'une écriture (edit / mémoire) : persistées avec le reste
			// pour que le diff soit encore là après un rafraîchissement.
			if len(ev.ToolUsed.Diff) > 0 {
				tu["diff"] = ev.ToolUsed.Diff
			}
			c.appendDelta(epoch, map[string]any{"tool_used": tu})
		case ev.NewHistory != nil:
			// Compaction faite en cours de tour : elle remplace la base au lieu de
			// s'ajouter à l'ancienne (voir StreamEvent.NewHistory). On retire le
			// préfixe système injecté à la volée (prompt perso + skills, fusionnés en
			// UN message system en tête) : il n'appartient pas à l'historique persisté
			// et doit rester modifiable à chaud.
			base := ev.NewHistory
			for len(base) > 0 && base[0].Role == "system" {
				base = base[1:]
			}
			newBase = append([]Message(nil), base...)
		case ev.Compacting != nil:
			// Compaction déclenchée pendant la boucle d'outils : même bannière que la
			// compaction de début de tour.
			c.appendDelta(epoch, map[string]any{"compacting": *ev.Compacting})
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
		if newBase != nil {
			c.Messages = newBase
		}
		c.Messages = append(c.Messages, extra...)
		if s := content.String(); strings.TrimSpace(s) != "" {
			c.Messages = append(c.Messages, Message{Role: "assistant", Content: s})
		}
	}
	msgs = append([]Message(nil), c.Messages...)
	ctxUsed = c.CtxUsed
	stale := c.epoch != epoch
	c.mu.Unlock()

	// Compaction de FIN DE TOUR. C'était LE trou : le seuil n'était testé qu'au
	// DÉBUT d'un tour, avec le contexte du tour précédent. Le tour qui fait
	// franchir le seuil se termine donc à 81% et… rien. La jauge reste haute, seul
	// le bouton manuel s'affiche, et l'utilisateur voit un compactage automatique
	// qui « ne marche pas » — alors qu'il attendait simplement le message suivant.
	// On compacte donc DÈS que le tour qui a franchi le seuil est fini : la jauge
	// retombe tout de suite et le tour suivant démarre avec de la marge.
	// Sauf si un Reset est passé (rien à compacter) ou si le tour a été annulé
	// (bouton stop) : on n'enchaîne pas plusieurs secondes de résumé sur un stop.
	if stale || ctx.Err() != nil || !compactWouldTrigger(msgs, ctxUsed) {
		return
	}
	// context.Background() et non ctx : le tour est terminé, son contexte peut
	// être annulé alors que cette compaction-là doit aller au bout.
	c.compactAndPublish(context.Background(), epoch, "fin-tour", msgs, ctxUsed, caps)
}

// CompactNow force une compaction du contexte MAINTENANT, sans attendre le seuil
// (bouton « compacter » de l'UI). Détaché comme la génération : émet la bannière
// de progression, résume les anciens tours, remplace le torse et persiste. Les
// événements passent par le flux d'abonnement, donc tous les appareils voient la
// progression. Renvoie ErrBusy si un tour est déjà en cours.
func (c *Conversation) CompactNow() error {
	if !healthCheck() {
		return errModelLoading
	}
	c.mu.Lock()
	if c.Generating {
		c.mu.Unlock()
		return ErrBusy
	}
	c.Generating = true
	c.genStart = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	msgs := append([]Message(nil), c.Messages...)
	lastReal := c.CtxUsed // dernier contexte réel mesuré, pour estimer le surcoût fixe
	epoch := c.epoch
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			// Même précaution que dans generate : une compaction abandonnée par un
			// Reset ne doit pas déclarer « libre » le tour qui a démarré depuis.
			if c.epoch == epoch {
				c.Generating = false
				c.cancel = nil
			}
			c.mu.Unlock()
		}()
		if _, changed := c.compactAndPublish(ctx, epoch, "manuel", msgs, lastReal, Caps{}); changed {
			c.persist()
		}
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
//
// Reset DÉBLOQUE toujours, et c'est sa deuxième raison d'être. Il se contentait
// avant de vider le fil : si un tour restait coincé (moteur redémarré sous ses
// pieds, commande shell accrochée à ses tubes), Generating restait vrai pour
// toujours et tout message suivant se voyait refusé « génération en cours ».
// Vider le fil ne changeait rien, rafraîchir non plus : il fallait redémarrer le
// service. « Nouvelle conversation » est le geste qu'on tente naturellement dans
// ce cas ; il doit donc rendre la main, quoi qu'il arrive au tour abandonné, que
// le bump d'epoch réduit de toute façon au silence.
func (c *Conversation) Reset() {
	c.Stop()
	c.mu.Lock()
	c.Messages = nil
	c.Log = nil
	c.Seq = 0
	c.CtxUsed = 0
	c.ID = newSessionID() // session vierge = nouvel id stable
	c.ActiveTitle = ""    // conversation neuve : ni nom hérité, ni favori
	c.ActiveFav = false
	c.epoch++
	c.pendingReplay = false // conversation vide : rien à rejouer
	c.Generating = false
	c.cancel = nil
	c.cond.Broadcast()
	c.mu.Unlock()
	c.persist()
}

// evSeq0 lit le seq de DÉBUT d'un bloc texte : sur un événement coalescé (dont le
// Seq vaut celui du DERNIER delta fusionné), c'est le seq du PREMIER delta du bloc ;
// sur un delta brut, c'est son propre Seq.
//
// ⚠️ Sans ça : un client qui a déjà affiché une PARTIE d'un bloc (streaming en
// cours) et qui reçoit ensuite ce bloc coalescé — parce que la compaction de fin de
// tour a remplacé la suite de deltas par un seul événement de Seq plus grand que
// son dernier seq vu — le concatène à ce qu'il affichait déjà : la réponse
// apparaissait DEUX FOIS (la 1re copie tronquée à l'endroit exact où le client en
// était). Cf. `replace` ci-dessous.
func evSeq0(d map[string]any, fallback int) int {
	switch v := d["seq0"].(type) {
	case int:
		return v
	case float64: // relu depuis le JSON persisté
		return int(v)
	}
	return fallback
}

// decorateEvent aplatit un événement du journal pour l'émission SSE et marque
// `replace` quand le client a DÉJÀ vu le début du bloc (from tombe à l'intérieur) :
// le texte envoyé est alors le bloc ENTIER, donc le client doit remplacer sa bulle
// au lieu d'y concaténer.
func decorateEvent(ev LogEvent, from int) map[string]any {
	out := map[string]any{"seq": ev.Seq, "ts": ev.TS}
	for k, v := range ev.Delta {
		out[k] = v
	}
	if isTextDelta(ev.Delta) && evSeq0(ev.Delta, ev.Seq) <= from {
		out["replace"] = true
	}
	return out
}

// isTextDelta : événement porteur de texte (content / reasoning_content).
func isTextDelta(d map[string]any) bool {
	if _, ok := d["content"].(string); ok {
		return true
	}
	_, ok := d["reasoning_content"].(string)
	return ok
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
	var bufSeq, bufToks, bufSeq0 int
	var bufTs0, bufTs int64
	flush := func() {
		if bufKey == "" {
			return
		}
		m := map[string]any{bufKey: buf.String(), "seq": bufSeq, "ts": bufTs, "ts0": bufTs0, "toks": bufToks, "seq0": bufSeq0}
		// Le client a déjà affiché le début de ce bloc (from tombe dedans, ce qui
		// arrive quand la compaction de fin de tour l'a fusionné) : on renvoie le
		// bloc entier et on lui demande de REMPLACER sa bulle, pas d'y ajouter.
		if bufSeq0 <= from {
			m["replace"] = true
		}
		out = append(out, m)
		buf.Reset()
		bufKey, bufSeq, bufToks, bufTs0, bufTs, bufSeq0 = "", 0, 0, 0, 0, 0
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
				bufKey, bufTs0, bufSeq0 = key, evTS0(ev.Delta, ev.TS), evSeq0(ev.Delta, ev.Seq)
			}
			buf.WriteString(ev.Delta[key].(string))
			bufSeq, bufTs = ev.Seq, ev.TS
			bufToks += evToks(ev.Delta) // 1 pour un delta brut, N pour un événement déjà coalescé
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
	// awaitingCaughtUp : après un reset de RESTAURATION (un fil complet suit), on
	// doit clore le rejeu par un caught_up — c'est lui qui fait sortir le client du
	// mode « replay » (bulles repliées, pas d'animation). Un reset de « clear chat »
	// (conversation vide) n'en a pas besoin.
	awaitingCaughtUp := false
	c.mu.Lock()
	for {
		if ctx.Err() != nil {
			c.mu.Unlock()
			return
		}
		if c.epoch != epoch { // reset → on ordonne au client de nettoyer et on repart
			epoch = c.epoch
			last = 0
			replay := c.pendingReplay
			c.mu.Unlock()
			if !emit(map[string]any{"reset": true, "replay": replay}) {
				return
			}
			awaitingCaughtUp = replay
			c.mu.Lock()
			continue
		}
		// Copie des événements en attente SOUS verrou, émission HORS verrou : on
		// n'itère jamais sur c.Log pendant que la génération peut y écrire ou que
		// la troncature (maxLogEvents) peut le déplacer.
		//
		// c.Log est trié par Seq croissant : on trouve le premier événement neuf
		// par dichotomie plutôt qu'en relisant tout. Le balayage complet coûtait
		// la longueur du journal (jusqu'à 20 000) À CHAQUE TOKEN et pour CHAQUE
		// appareil connecté, verrou tenu — donc au détriment de la génération
		// elle-même. C'est quadratique sur un long fil.
		i := sort.Search(len(c.Log), func(i int) bool { return c.Log[i].Seq > last })
		var pending []LogEvent
		if i < len(c.Log) {
			pending = append(pending, c.Log[i:]...)
		}
		if len(pending) == 0 {
			// Fil restauré entièrement rejoué : on clôt par caught_up (le client
			// quitte alors le mode replay). Une seule fois par restauration.
			if awaitingCaughtUp {
				awaitingCaughtUp = false
				c.mu.Unlock()
				if !emit(map[string]any{"caught_up": true}) {
					return
				}
				c.mu.Lock()
				continue
			}
			c.cond.Wait()
			continue
		}
		lastEmitted := last
		last = pending[len(pending)-1].Seq
		c.mu.Unlock()
		for _, ev := range pending {
			// `lastEmitted` (avant mise à jour) sert de repère : si la compaction de
			// fin de tour vient de fusionner un bloc que le client suivait en direct,
			// l'événement fusionné arrive avec un Seq supérieur au sien → `replace`.
			if !emit(decorateEvent(ev, lastEmitted)) {
				return
			}
		}
		c.mu.Lock()
	}
}
