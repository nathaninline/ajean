package ajean

// llm_prewarm.go — pré-chauffage du PRÉFIXE de prompt auprès du moteur.
//
// Le premier message d'une conversation paie le préfill de tout ce qui précède
// le texte de l'utilisateur : préambule système, briefing machine, prompt du
// preset et surtout les schémas d'outils — plusieurs milliers de jetons. Sur un
// modèle local, c'est l'essentiel du délai avant le premier jeton, et ce préfixe
// est IDENTIQUE d'une conversation à l'autre d'un même mode (le contexte projet,
// lui, part dans le premier message utilisateur, voir normalizeSystemMessages).
//
// Dès que le moteur répond, on envoie donc une complétion minuscule
// (max_tokens=1) avec EXACTEMENT ce préfixe. llama-server garde le cache KV du
// créneau (--cache-ram / --cache-idle-slots sont actifs par défaut) : le premier
// vrai message ne traite plus que son propre texte, et le premier jeton arrive
// tout de suite. Rien n'est affiché : la requête est interne, sa réponse jetée.
//
// Deux règles tiennent ce pré-chauffage honnête :
//
//  1. Il ne part QUE si la conversation active est vide. Avec un seul créneau de
//     cache (-np 1, le cas courant), chauffer pendant qu'une conversation est en
//     cours écraserait son cache KV : le message suivant repaierait tout
//     l'historique. Une conversation commencée (ou en génération) n'est jamais
//     touchée, quoi qu'il arrive au prompt, aux outils ou à l'interface.
//  2. Le préfixe est celui du MODE qui recevra le premier message (Jean, Projet,
//     Rapide, Modèle de base), construit par les mêmes capacités que le vrai envoi
//     (capsFromBody), pas par les réglages globaux de la machine : sinon le
//     préfixe chauffé ne correspond pas au vrai premier tour et le cache ne se
//     raccorde pas. Voir prewarmTarget pour la façon dont le mode est connu.
//
// Le pré-chauffage est BÉNIN : jamais pendant une génération (le compteur
// llmInFlight le dit), annulé dès qu'un tour utilisateur démarre (le tour
// chauffera le cache lui-même), deux tentatives au plus, signature mémorisée
// pour ne pas rejouer le même préfixe à chaque sondage de /api/status.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// Texte du faux message utilisateur : il ne sert qu'à rendre le prompt
	// valide pour tous les gabarits de chat. Seul le préfixe qui le précède
	// nous intéresse.
	llmPrewarmUserText = "ok"
	// Borne : sur un gros modèle CPU le préfill peut être long, mais au-delà le
	// pré-chauffage retarderait le premier vrai message pour rien.
	llmPrewarmTimeout = 30 * time.Second
	// Deux essais suffisent (moteur ancien qui refuserait le corps, coupure…).
	// Ensuite on abandonne jusqu'au prochain redémarrage du moteur ou changement
	// de préfixe.
	llmPrewarmMaxTries = 2
	// Une fois le préfixe chauffé, on revérifie quand même de temps en temps
	// qu'il n'a pas changé (prompt du preset édité, outils activés…) — sans
	// reconstruire le préfixe à chaque sondage de /api/status (toutes les 5 s).
	llmPrewarmRecheck = 15 * time.Second
	// Clé (bucket state) du dernier mode de chat réellement utilisé à l'envoi.
	// Sert à deviner le mode d'une conversation encore vide (voir prewarmTarget).
	lastChatModeKey = "last_chat_mode"
)

// llmInFlight compte les complétions réellement en cours vers le moteur (chat,
// tâches, Jean, apps…). Sert au pré-chauffage : inutile — et nuisible — de
// s'insérer devant une génération déjà lancée.
var llmInFlight atomic.Int32

// llmTurnsActive couvre les TROUS entre deux requêtes d'un même tour (exécution
// d'un outil, lecture de fichier…) : llmInFlight retombe à zéro alors que le tour
// n'est pas fini et qu'un pré-chauffage s'intercalerait devant l'étape suivante.
// Compteur (pas un booléen) : un Reset peut lancer le tour suivant avant que le
// tour abandonné ne se termine ; sa décrémentation ne doit pas libérer le tour
// vivant.
var llmTurnsActive atomic.Int32

// llmPrewarmState : un seul pré-chauffage à la fois, et un souvenir de ce qui a
// déjà été chauffé pour ne pas le refaire à chaque sondage.
//
// ⚠️ Ordre des verrous : conv.mu PUIS llmWarm.mu (StartTurn appelle
// cancelLLMPrewarmForTurn en tenant conv.mu). Ne jamais lire la conversation
// (prewarmTarget) en tenant llmWarm.mu.
var llmWarm struct {
	mu        sync.Mutex
	healthy   bool // dernier état de santé observé (détecte un redémarrage moteur)
	sig       string
	warmed    bool
	inflight  bool
	attempts  int
	nextTry   time.Time
	lastCheck time.Time
	cancel    context.CancelFunc
}

// prewarmModeValid : modes de chat que le serveur connaît (voir requestedMode).
func prewarmModeValid(mode string) bool {
	switch mode {
	case "project", "fast", "base", "jean":
		return true
	}
	return false
}

// rememberChatMode retient le mode du dernier message envoyé. Le navigateur ne
// dit son mode au serveur qu'avec le premier message d'une conversation
// (Conversation.lockMode) : tant qu'elle est vide, le dernier mode utilisé est la
// meilleure information disponible — on ne change guère de mode d'un message à
// l'autre. N'écrit que si la valeur change.
func rememberChatMode(mode string) {
	if prewarmModeValid(mode) && getStr(bkState, lastChatModeKey) != mode {
		_ = putStr(bkState, lastChatModeKey, mode)
	}
}

// prewarmCaps : capacités du mode, construites comme pour un vrai envoi
// (handleChatSend → applyChatMode + capsFromBody). Ce sont elles — et non
// globalCaps() — qui décident du préambule, du briefing machine, du prompt du
// preset et des outils du premier tour : Rapide n'a ni mémoire ni prompt de
// preset, Jean a ses outils et son préambule, Modèle de base n'a rien.
func prewarmCaps(mode string) Caps {
	var b chatReq
	applyChatMode(&b, mode)
	return capsFromBody(b)
}

// prewarmTarget dit si le pré-chauffage a le droit de partir, et pour quel mode.
//
// ok=false (rien à faire) quand :
//   - la conversation active n'est pas vide ou génère : son cache KV est à elle,
//     et le créneau est unique avec -np 1 ;
//   - le mode du prochain message est inconnu : on ne devine pas, un préfixe
//     chauffé pour rien coûte un préfill et ne raccorde pas le vrai tour ;
//   - le mode ne correspond pas à la conversation ouverte : Jean est une
//     conversation unique et persistante (jeanConvID), les autres modes n'y
//     écrivent jamais. Dans ce cas, le premier message ouvrira une AUTRE
//     conversation (alignConvForMode) dont on ne sait pas si elle est vide.
//
// Le mode est celui de la conversation s'il est déjà fixé (Jean), sinon celui du
// dernier message envoyé (rememberChatMode).
func prewarmTarget() (mode string, ok bool) {
	conv.mu.Lock()
	id, cmode := conv.ID, conv.Mode
	idle := len(conv.Messages) == 0 && !conv.Generating
	conv.mu.Unlock()
	if !idle {
		return "", false
	}
	mode = cmode
	if mode == "" {
		mode = getStr(bkState, lastChatModeKey)
	}
	if !prewarmModeValid(mode) {
		return "", false
	}
	if (mode == "jean") != (id == jeanConvID) {
		return "", false
	}
	return mode, true
}

// prewarmPrefix construit le préfixe commun à toute conversation du mode décrit
// par caps : le bloc système tel qu'InjectSkills + normalizeSystemMessages le
// produisent, et les outils tels que runChat les annonce. Le contexte projet est
// volontairement absent (il vit dans le premier message utilisateur).
//
// Renvoie ok=false quand il n'y a rien à chauffer (modèle brut, sans système ni
// outil) : envoyer une requête ne gagnerait rien.
func prewarmPrefix(caps Caps) (system string, tools []Tool, ok bool) {
	var parts []string
	if bp := baseSystemPrompt(caps); bp != "" {
		parts = append(parts, bp)
	}
	if mp := machineSystemPrompt(caps); mp != "" {
		parts = append(parts, mp)
	}
	// Même ordre que generate : le prompt du preset est le premier message
	// système, InjectSkills le fusionne APRÈS le préambule.
	if sp := readSysPrompt(); sp != "" && caps.Agent && !caps.Terminal {
		parts = append(parts, sp)
	}
	system = strings.Join(parts, "\n\n")
	// Même liste que runChat : withoutRecallTools retire recall/recall_search
	// tant que le fil n'a pas été compacté (c'est le cas d'une conversation
	// neuve, celle qu'on pré-chauffe).
	tools = withRecallTools(EnabledTools(caps), nil)
	// Mode Jean : runChat ajoute à chaque outil le champ « description »
	// (chat_tool_desc.go). Sans lui, les schémas chauffés diffèrent de ceux du
	// vrai tour et le cache ne se raccorde pas au-delà du bloc système.
	if caps.Jean {
		tools, _ = withToolDesc(tools)
	}
	return system, tools, system != "" || len(tools) > 0
}

// prewarmSignature identifie le préfixe chauffé. Un changement de preset, de
// modèle, d'outil ou de prompt système doit relancer le pré-chauffage.
func prewarmSignature(ep chatEndpoint, system string, tools []Tool) string {
	h := sha256.New()
	h.Write([]byte(ep.URL))
	h.Write([]byte{0})
	h.Write([]byte(ep.Model))
	h.Write([]byte{0})
	h.Write([]byte(system))
	for _, t := range tools {
		b, _ := json.Marshal(t) // ordre des champs fixe, clés de map triées
		h.Write([]byte{0})
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// prewarmPayload est le corps envoyé au moteur. Volontairement minimal : on ne
// veut qu'un préfill, pas une génération.
func prewarmPayload(ep chatEndpoint, system string, tools []Tool) map[string]any {
	msgs := make([]Message, 0, 2)
	if system != "" {
		msgs = append(msgs, Message{Role: "system", Content: system})
	}
	msgs = append(msgs, Message{Role: "user", Content: llmPrewarmUserText})
	p := map[string]any{
		"model":        ep.Model,
		"messages":     msgs,
		"max_tokens":   1,
		"temperature":  0,
		"stream":       false,
		"cache_prompt": true,
	}
	if len(tools) > 0 {
		p["tools"] = tools
		p["parallel_tool_calls"] = false
	}
	return p
}

// LLMPrewarm doit être appelé à chaque fois que l'état du moteur est connu au
// service d'interface (démarrage de l'UI/link, sondage /api/status). Il est non
// bloquant : le pré-chauffage part dans sa propre goroutine.
//
// healthy=false signale un moteur qui ne répond pas encore : au prochain
// passage à true, le cache KV est forcément froid, on ré-arme.
func LLMPrewarm(healthy bool) {
	// La conversation est lue AVANT de prendre llmWarm.mu (ordre des verrous, voir
	// llmWarm). Elle décide de tout : sans conversation active vide, on ne touche
	// pas au créneau de cache — mais l'état de santé, lui, est suivi quand même
	// (un redémarrage du moteur doit ré-armer, même pendant une conversation).
	mode, canWarm := prewarmTarget()

	// Phase 1 : état rapide, sans travail lourd sous le verrou.
	llmWarm.mu.Lock()
	if !healthy {
		llmWarm.healthy = false
		llmWarm.mu.Unlock()
		return
	}
	if !llmWarm.healthy {
		llmWarm.healthy = true
		llmWarm.sig, llmWarm.warmed, llmWarm.attempts = "", false, 0
		llmWarm.lastCheck = time.Time{}
	}
	if !canWarm {
		llmWarm.mu.Unlock()
		return
	}
	// Une génération réelle occupe le moteur : on ne se met pas devant elle.
	// (Si le pré-chauffage est déjà parti, le tour le coupera — voir StartTurn.)
	if llmInFlight.Load() > 0 || llmTurnsActive.Load() > 0 || llmWarm.inflight {
		llmWarm.mu.Unlock()
		return
	}
	if llmWarm.warmed && time.Since(llmWarm.lastCheck) < llmPrewarmRecheck {
		llmWarm.mu.Unlock()
		return
	}
	// Après un échec, on laisse passer le recul avant de reconstruire le préfixe
	// (connexions MCP, sondage Crawl4AI) : rien ne doit tourner en boucle.
	if !llmWarm.warmed && time.Now().Before(llmWarm.nextTry) {
		llmWarm.mu.Unlock()
		return
	}
	llmWarm.lastCheck = time.Now()
	llmWarm.mu.Unlock()

	// Phase 2 : construction du préfixe HORS verrou. Elle peut être lente (les
	// serveurs MCP se connectent, Crawl4AI est sondé) et ne doit jamais retarder
	// cancelLLMPrewarmForTurn, appelé au démarrage d'un tour utilisateur.
	cfg := ReadConfig()
	if isExternalConfig(cfg) || isCloudConfig(cfg) {
		return
	}
	ep := resolveChatEndpoint()
	if ep.URL == "" {
		return
	}
	system, tools, ok := prewarmPrefix(prewarmCaps(mode))
	if !ok {
		return
	}
	sig := prewarmSignature(ep, system, tools)

	// Phase 3 : décision et lancement, sous verrou, en revérifiant l'état (un
	// tour a pu démarrer pendant la phase 2).
	llmWarm.mu.Lock()
	if llmWarm.sig != sig {
		llmWarm.sig, llmWarm.warmed, llmWarm.attempts = sig, false, 0
	}
	if llmWarm.warmed || llmWarm.inflight || llmInFlight.Load() > 0 || llmTurnsActive.Load() > 0 ||
		llmWarm.attempts >= llmPrewarmMaxTries || time.Now().Before(llmWarm.nextTry) {
		llmWarm.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), llmPrewarmTimeout)
	llmWarm.cancel = cancel
	llmWarm.inflight = true
	llmWarm.mu.Unlock()

	payload := prewarmPayload(ep, system, tools)
	go func(sig string) {
		// Dernier garde-fou, APRÈS l'enregistrement de cancel ci-dessus : la
		// conversation a pu recevoir un message (ou un autre mode a pu s'ouvrir)
		// depuis la lecture du début. StartTurn pose Generating sous conv.mu AVANT
		// d'appeler cancelLLMPrewarmForTurn, donc deux cas seulement : soit le
		// tour a démarré avant, on le voit ici et on n'envoie rien ; soit il
		// démarre après, et son cancelLLMPrewarmForTurn coupe cette requête.
		skipped := false
		var err error
		if m, ok := prewarmTarget(); !ok || m != mode {
			skipped = true
		} else {
			err = sendLLMPrewarm(ctx, ep, payload)
		}
		cancel()
		llmWarm.mu.Lock()
		llmWarm.inflight = false
		llmWarm.cancel = nil
		// Un autre préfixe a pu prendre la main pendant l'envoi (changement de
		// preset, de prompt système…) : son état ne doit pas être écrasé par le
		// résultat de l'ancien. Une requête non envoyée (conversation devenue
		// active) ne compte ni comme essai ni comme succès : on réessaiera quand
		// une conversation vide sera de nouveau ouverte.
		if llmWarm.sig == sig && !skipped {
			if err != nil {
				llmWarm.attempts++
				// Recul : un moteur qui refuse ce corps n'a pas besoin d'être
				// resondé toutes les deux secondes.
				llmWarm.nextTry = time.Now().Add(time.Duration(llmWarm.attempts) * 15 * time.Second)
			} else {
				llmWarm.warmed = true
				llmWarm.attempts = 0
			}
		}
		llmWarm.mu.Unlock()
	}(sig)
}

// cancelLLMPrewarmForTurn coupe un pré-chauffage en cours quand un tour
// utilisateur démarre : sa demande passe devant tout travail interne. On note le
// cache comme chauffé — le tour va de toute façon traiter (et garder) le préfixe.
func cancelLLMPrewarmForTurn() {
	llmWarm.mu.Lock()
	cancel := llmWarm.cancel
	if cancel != nil {
		llmWarm.cancel = nil
		llmWarm.warmed = true
	}
	llmWarm.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// sendLLMPrewarm envoie la requête de pré-chauffage et draine la réponse (pour
// laisser la connexion réutilisable par le vrai message).
func sendLLMPrewarm(ctx context.Context, ep chatEndpoint, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if ep.External {
		if ep.Key != "" {
			req.Header.Set("Authorization", "Bearer "+ep.Key)
		}
	} else {
		authHeader(req)
	}
	// Client par défaut : même pool de connexions que le chat, donc le premier
	// message réutilise la connexion déjà ouverte.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pré-chauffage : HTTP %d", resp.StatusCode)
	}
	return nil
}
