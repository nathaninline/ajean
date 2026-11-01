package ajean

// llm_rewarm.go — garder la conversation CHAUDE quand on la laisse de côté.
//
// Le moteur n'a qu'un créneau de cache (-np 1, le cas courant). Tant que seule
// la conversation s'en sert, chaque message ne calcule que son propre texte :
// tout l'historique est déjà dans le cache KV. Mais d'autres travaux passent
// par ce même créneau pendant qu'on est parti : description des images de Jean
// (après 10 min de calme), tâches planifiées, sommeil, compaction, et surtout
// les clients tiers (IDE branché sur l'endpoint OpenAI). Chacun remplace le
// cache par SON prompt. Au retour, le message suivant repaie le préfill de
// TOUT le fil — plusieurs minutes sur un long historique, pire sur un modèle
// hybride (Qwen3.5+) qui ne sait reprendre qu'à ses points de sauvegarde.
// D'où le « il met 3 minutes à repartir alors qu'il était chaud ».
//
// Ici, on remet la conversation en place AVANT le retour de l'utilisateur :
// dès que le moteur est resté calme un moment après un travail étranger, une
// complétion invisible (max_tokens=1) rejoue exactement le préfixe du prochain
// tour — même vue modèle (turnModelView), mêmes messages système
// (InjectSkills), même normalisation (wireMessages), mêmes outils. Le vrai
// message suivant retrouve tout le fil en cache.
//
// Mêmes garde-fous que le pré-chauffage (llm_prewarm.go), dont il partage le
// réglage : jamais pendant un tour, une tâche ou une révision ; annulé dès
// qu'une vraie requête arrive (doLLM, StartTurn) ; deux essais au plus par
// état de conversation ; ni API externe ni GPU cloud (facturés).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	rewarmTick = 15 * time.Second
	// Calme exigé avant de re-chauffer après un travail de fond d'AJEAN : il
	// s'enchaîne souvent avec un autre (révision puis description d'images…).
	rewarmQuietInternal = 30 * time.Second
	// Après un client tiers (IDE), plus long : il travaille par rafales, et
	// re-chauffer le chat entre deux rafales lui ferait repayer SON contexte.
	rewarmQuietExternal = 3 * time.Minute
	// Un long fil sur un gros modèle peut demander plusieurs minutes de préfill.
	rewarmTimeout  = 15 * time.Minute
	rewarmMaxTries = 2
)

// Qui a utilisé le moteur : la conversation (son cache reste en place), un
// travail de fond d'AJEAN, ou un client tiers.
type engineUser int

const (
	engineUserBackground engineUser = iota
	engineUserConv
	engineUserThirdParty
)

type engineUserKey struct{}

// withConvTurn marque les requêtes d'un tour de LA conversation : elles
// laissent le cache KV à la conversation.
func withConvTurn(ctx context.Context) context.Context {
	return context.WithValue(ctx, engineUserKey{}, engineUserConv)
}

// withThirdParty marque les requêtes d'un client tiers (API OpenAI/Anthropic).
func withThirdParty(ctx context.Context) context.Context {
	return context.WithValue(ctx, engineUserKey{}, engineUserThirdParty)
}

func engineUserOf(ctx context.Context) engineUser {
	if ctx == nil {
		return engineUserBackground
	}
	u, _ := ctx.Value(engineUserKey{}).(engineUser)
	return u
}

// engineSlot : à qui appartient le créneau de cache, et l'état du re-chauffage.
var engineSlot struct {
	mu        sync.Mutex
	displaced bool      // le cache ne tient plus la conversation
	external  bool      // … et c'est un client tiers qui l'a pris
	lastUse   time.Time // fin de la dernière requête observée
	slotTask  int       // id_task du créneau (/slots) au dernier relevé, -1 = à relever
	warmSig   string    // empreinte de la conversation que le cache tient
	warmStamp string    // repère bon marché de cette même conversation (convStamp)
	inflight  bool
	cancel    context.CancelFunc
	failSig   string
	attempts  int
	nextTry   time.Time
}

func init() { engineSlot.slotTask = -1 }

// noteEngineUse : une requête vers le moteur vient de se terminer. Appelé par
// doLLM (engineRequestDone) avec l'auteur lu dans le contexte de la requête.
func noteEngineUse(u engineUser) {
	engineSlot.mu.Lock()
	defer engineSlot.mu.Unlock()
	engineSlot.lastUse = time.Now()
	engineSlot.slotTask = -1 // notre propre requête a changé l'id du créneau : à relever
	switch u {
	case engineUserConv:
		engineSlot.displaced, engineSlot.external = false, false
	case engineUserThirdParty:
		engineSlot.displaced, engineSlot.external = true, true
	default:
		engineSlot.displaced = true
	}
}

// cancelConvRewarm coupe un re-chauffage en cours : une vraie requête arrive.
func cancelConvRewarm() {
	engineSlot.mu.Lock()
	cancel := engineSlot.cancel
	engineSlot.cancel = nil
	engineSlot.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// rewarmPrefix : ce que le prochain tour de la conversation enverra avant le
// message de l'utilisateur. ok=false quand la conversation ne se prête pas au
// re-chauffage (vide, en cours, compaction imminente, contexte Jean expiré).
type rewarmPrefix struct {
	msgs  []Message // vue modèle + InjectSkills, avant wireMessages
	tools []Tool
	sig   string
	stamp string
}

// convStamp : repère bon marché de l'état de la conversation (sans construire
// le préfixe, qui interroge les serveurs MCP). Doit être lu sous conv.mu.
func convStampLocked() string {
	return fmt.Sprintf("%s/%d/%d/%d", conv.ID, conv.epoch, conv.Seq, len(conv.Messages))
}

func buildRewarmPrefix(ep chatEndpoint) (rewarmPrefix, bool) {
	conv.mu.Lock()
	if conv.Generating || len(conv.Messages) == 0 {
		conv.mu.Unlock()
		return rewarmPrefix{}, false
	}
	msgs := append([]Message(nil), conv.Messages...)
	ctxUsed, id, mode := conv.CtxUsed, conv.ID, conv.Mode
	caps, known := conv.lastCaps, conv.hasLastCaps
	jeanExpired := id == jeanConvID && jeanIdleExpired(conv.Log, time.Now())
	stamp := convStampLocked()
	conv.mu.Unlock()
	if !known {
		if !prewarmModeValid(mode) {
			return rewarmPrefix{}, false
		}
		caps = prewarmCaps(mode)
	}
	// Prochain tour = contexte vidé (Jean après une longue pause) ou compaction
	// d'abord : le fil actuel ne sera pas renvoyé tel quel, inutile de le chauffer.
	if (caps.Jean && jeanExpired) || compactWouldTrigger(msgs, ctxUsed) {
		return rewarmPrefix{}, false
	}
	full := InjectSkills(turnModelView(msgs, caps), caps)
	tools := withRecallTools(EnabledTools(caps), full)
	if caps.Jean {
		tools, _ = withToolDesc(tools)
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", ep.URL, ep.Model)
	_ = json.NewEncoder(h).Encode(full)
	_ = json.NewEncoder(h).Encode(tools)
	return rewarmPrefix{msgs: full, tools: tools, sig: hex.EncodeToString(h.Sum(nil)[:12]), stamp: stamp}, true
}

// rewarmPayload : le préfixe suivi d'un message utilisateur bidon, comme le
// pré-chauffage. Échantillonnage du preset compris : chat_template_kwargs
// (reasoning_effort) change le rendu du gabarit, donc le préfixe.
func rewarmPayload(ep chatEndpoint, p rewarmPrefix) map[string]any {
	msgs := append(append([]Message(nil), p.msgs...), Message{Role: "user", Content: llmPrewarmUserText})
	payload := map[string]any{
		"model":        ep.Model,
		"messages":     wireMessages(msgs),
		"stream":       false,
		"cache_prompt": true,
	}
	applySampling(payload)
	payload["max_tokens"] = 1
	payload["temperature"] = 0
	if len(p.tools) > 0 {
		payload["tools"] = p.tools
		payload["parallel_tool_calls"] = false
	}
	return payload
}

// convRewarmSettled : un tour vient de finir, le cache tient la conversation
// telle qu'elle est. On retient son empreinte (sans toucher à displaced : une
// compaction de fin de tour a pu passer après et prendre le créneau).
func convRewarmSettled() {
	cfg := ReadConfig()
	if isExternalConfig(cfg) || isCloudConfig(cfg) {
		return
	}
	p, ok := buildRewarmPrefix(resolveChatEndpoint())
	if !ok {
		return
	}
	engineSlot.mu.Lock()
	engineSlot.warmSig, engineSlot.warmStamp = p.sig, p.stamp
	engineSlot.mu.Unlock()
}

// engineSlotTask lit l'id de la dernière tâche du créneau unique de llama-server
// (/slots). Sert à voir passer les clients qui parlent DIRECTEMENT au moteur
// (IDE sur :8080) sans passer par AJEAN. ok=false : endpoint absent (--no-slots,
// autre moteur) ou plusieurs créneaux (rien à conclure).
func engineSlotTask() (int, bool) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://localhost:%d/slots", LLMPort()), nil)
	if err != nil {
		return 0, false
	}
	authHeader(req)
	resp, err := healthClient.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return 0, false
	}
	var slots []struct {
		IDTask *int `json:"id_task"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&slots) != nil || len(slots) != 1 || slots[0].IDTask == nil {
		return 0, false
	}
	return *slots[0].IDTask, true
}

// engineIdle : rien d'AJEAN n'utilise le moteur en ce moment.
func engineIdle() bool {
	if llmInFlight.Load() > 0 || llmTurnsActive.Load() > 0 {
		return false
	}
	llmWarm.mu.Lock()
	warming := llmWarm.inflight
	llmWarm.mu.Unlock()
	return !warming && !jeanModelBusy()
}

// convRewarmCheck : un passage de la boucle. Décide et lance au plus un
// re-chauffage, dans sa propre goroutine.
func convRewarmCheck() {
	if !prewarmEnabled() {
		return
	}
	cfg := ReadConfig()
	if isExternalConfig(cfg) || isCloudConfig(cfg) || !engineIdle() {
		return
	}
	// Clients qui parlent directement au moteur : l'id de tâche du créneau a
	// bougé sans nous.
	if task, ok := engineSlotTask(); ok && engineIdle() {
		engineSlot.mu.Lock()
		if !engineSlot.inflight {
			if engineSlot.slotTask >= 0 && task != engineSlot.slotTask {
				engineSlot.displaced, engineSlot.external = true, true
				engineSlot.lastUse = time.Now()
			}
			engineSlot.slotTask = task
		}
		engineSlot.mu.Unlock()
	}

	engineSlot.mu.Lock()
	quiet := rewarmQuietInternal
	if engineSlot.external {
		quiet = rewarmQuietExternal
	}
	wait := engineSlot.inflight || time.Since(engineSlot.lastUse) < quiet || time.Now().Before(engineSlot.nextTry)
	displaced, warmStamp := engineSlot.displaced, engineSlot.warmStamp
	engineSlot.mu.Unlock()
	if wait {
		return
	}
	// Cache intact et conversation inchangée : rien à reconstruire.
	conv.mu.Lock()
	same := convStampLocked() == warmStamp
	conv.mu.Unlock()
	if !displaced && same {
		return
	}
	if !healthCheck() {
		return
	}

	ep := resolveChatEndpoint()
	if ep.URL == "" || ep.External || ep.Cloud {
		return
	}
	p, ok := buildRewarmPrefix(ep)
	if !ok {
		return
	}

	engineSlot.mu.Lock()
	if engineSlot.inflight || (!engineSlot.displaced && engineSlot.warmSig == p.sig) ||
		(engineSlot.failSig == p.sig && engineSlot.attempts >= rewarmMaxTries) {
		engineSlot.mu.Unlock()
		return
	}
	if engineSlot.failSig != p.sig {
		engineSlot.failSig, engineSlot.attempts = p.sig, 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), rewarmTimeout)
	engineSlot.cancel = cancel
	engineSlot.inflight = true
	engineSlot.mu.Unlock()

	go func() {
		// Dernier contrôle après l'enregistrement de cancel : un tour qui démarre
		// maintenant passera par cancelLLMPrewarmForTurn → cancelConvRewarm.
		var err error
		skipped := !engineIdle() || conv.isGenerating()
		if !skipped {
			err = sendLLMPrewarm(ctx, ep, rewarmPayload(ep, p))
		}
		cancelled := ctx.Err() == context.Canceled
		cancel()
		engineSlot.mu.Lock()
		defer engineSlot.mu.Unlock()
		engineSlot.inflight = false
		engineSlot.cancel = nil
		engineSlot.slotTask = -1
		switch {
		case skipped || cancelled:
			// Une vraie requête a pris la main : ni essai ni succès.
		case err != nil:
			engineSlot.attempts++
			engineSlot.nextTry = time.Now().Add(time.Duration(engineSlot.attempts) * time.Minute)
		default:
			engineSlot.warmSig, engineSlot.warmStamp = p.sig, p.stamp
			engineSlot.displaced, engineSlot.external = false, false
			engineSlot.attempts = 0
		}
	}()
}

// startConvRewarm lance la boucle (une fois, dans le process qui possède la
// conversation et parle au moteur : le service d'interface).
var convRewarmOnce sync.Once

func startConvRewarm() {
	convRewarmOnce.Do(func() {
		go func() {
			for {
				time.Sleep(rewarmTick)
				convRewarmCheck()
			}
		}()
	})
}
