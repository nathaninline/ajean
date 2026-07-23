package jean

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Compactage du contexte, façon Hermes Agent : au lieu de vider la conversation
// quand la fenêtre de contexte se remplit, on la scinde en trois zones —
//
//	Head  (tête)  : messages système + tout premier message utilisateur. Protégé.
//	Tail  (queue) : les tours récents (dans un budget de tokens). Protégé.
//	Torso (torse) : tout le milieu. C'est LA SEULE zone compactée.
//
// Le torse est d'abord dégraissé sans IA (les vieux résultats d'outils longs
// sont remplacés par un marqueur), puis résumé par le modèle local en UN seul
// appel, et le tout est remplacé par un court résumé. Résultat : des
// conversations quasi illimitées sans jamais « clear », comme Hermes.
//
// La logique vit côté serveur (dans le flux de chat) donc elle profite à TOUS
// les clients — UI web, terminal, accès distant ajean.link — sans duplication.

const (
	// Seuil de déclenchement proactif : on compacte quand l'historique estimé
	// dépasse cette fraction de la fenêtre de contexte.
	compactTriggerFrac = 0.75
	// Budget de la queue : fraction de la fenêtre gardée intacte (tours récents).
	// Plus la queue est petite, plus on compacte de torse d'un coup → le contexte
	// retombe bas et met longtemps à re-déclencher (au lieu de compacter souvent).
	compactTailFrac = 0.25
	// Un résultat d'outil du torse plus long que ça est remplacé par un marqueur
	// avant le résumé (dégraissage sans IA, gratuit).
	compactToolPruneLen = 200
)

const compactPrunedMarker = "[Old tool result cleared to save context]"

// compactEnabled indique si le compactage automatique du contexte est actif.
// Défaut : true. Seule une valeur off/false/0/no/non explicite (config.env
// COMPACT) le désactive — cohérent avec toolLimitEnabled().
func compactEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(ReadConfig()["COMPACT"])) {
	case "off", "false", "0", "no", "non":
		return false
	}
	return true
}

// ctxWindow renvoie la fenêtre de contexte configurée (config.env CTX), 32768
// par défaut — la même valeur que celle passée à llama-server au lancement.
func ctxWindow() int {
	if v := ReadConfig()["CTX"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 32768
}

// msgText extrait le texte d'un message (Content est `any`, en pratique string
// ou nil quand l'assistant n'a que des tool_calls).
func msgText(m Message) string {
	if s, ok := m.Content.(string); ok {
		return s
	}
	return ""
}

// msgTokens estime grossièrement le coût en tokens d'un message (~4 caractères
// par token, plus un forfait par message pour le rôle et les délimiteurs). C'est
// volontairement approximatif : le comptage EXACT vient de llama.cpp
// (PromptTokensTotal) ; ici on veut juste décider quand compacter.
func msgTokens(m Message) int {
	n := 4
	n += len(msgText(m)) / 4
	for _, tc := range m.ToolCalls {
		n += (len(tc.Function.Name) + len(tc.Function.Arguments)) / 4
	}
	return n
}

// estimateTokens estime la taille de l'historique en tokens.
func estimateTokens(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += msgTokens(m)
	}
	return total
}

// MaybeCompact compacte l'historique si (et seulement si) il dépasse le seuil
// proactif. Renvoie l'historique (compacté ou inchangé) et un booléen indiquant
// s'il a changé. À appeler sur l'historique BRUT (avant InjectSkills) pour que
// le résultat puisse être renvoyé au client sans le préfixe système injecté.
//
// knownTokens = taille RÉELLE du contexte au tour précédent (usage.prompt_tokens
// + tokens générés), telle que rapportée par llama.cpp et affichée par l'UI. On
// la préfère à estimateTokens() car cette dernière n'est qu'une heuristique et,
// surtout, ne « voit » pas le prompt système injecté (machine briefing) ni le
// gabarit de chat — donc elle sous-estime largement le vrai contexte. 0 = inconnu
// (clients sans compteur, ex. terminal) → repli sur l'estimation.
// compactWouldTrigger indique si un tour VA déclencher une compaction proactive
// (compactage activé ET contexte au-dessus du seuil). Exposé pour que le flux de
// chat puisse afficher une bannière « compactage en cours » AVANT de lancer le
// résumé (qui bloque plusieurs secondes), au lieu d'une UI figée sans info.
func compactWouldTrigger(msgs []Message, knownTokens int) bool {
	if !compactEnabled() {
		return false
	}
	used := knownTokens
	if used <= 0 {
		used = estimateTokens(msgs)
	}
	return used >= int(float64(ctxWindow())*compactTriggerFrac)
}

func MaybeCompact(ctx context.Context, msgs []Message, caps Caps, knownTokens int) ([]Message, bool) {
	if !compactWouldTrigger(msgs, knownTokens) {
		return msgs, false
	}
	return compactMessages(ctx, msgs, caps)
}

// compactMessages exécute la compaction sans tenir compte du seuil (utilisé en
// secours réactif quand llama-server refuse un prompt trop long). Renvoie
// l'historique compacté et true s'il a effectivement changé.
// compactBounds calcule les frontières head/tail pour un historique donné et un
// budget de queue (en tokens). Fonction pure (pas d'IO) → testable :
//   - head : nb de messages protégés en tête = messages système + 1er message
//     utilisateur (il ancre l'objectif). Un message user est une frontière sûre.
//   - tailStart : index de début de la queue protégée. On remonte depuis la fin
//     jusqu'à remplir le budget, puis on recule jusqu'au message `user` précédent
//     pour que la queue démarre sur un début de tour — on ne sépare JAMAIS un
//     assistant+tool_calls de ses résultats `tool`.
//
// Le torse à compacter est [head, tailStart). Il est vide (tailStart <= head)
// quand il n'y a rien à résumer.
func compactBounds(msgs []Message, tailBudget int) (head, tailStart int) {
	for head < len(msgs) && msgs[head].Role == "system" {
		head++
	}
	if head < len(msgs) && msgs[head].Role == "user" {
		head++
	}
	tailStart = len(msgs)
	acc := 0
	for i := len(msgs) - 1; i >= head; i-- {
		acc += msgTokens(msgs[i])
		tailStart = i
		if acc >= tailBudget {
			break
		}
	}
	for tailStart > head && msgs[tailStart].Role != "user" {
		tailStart--
	}
	return head, tailStart
}

func compactMessages(ctx context.Context, msgs []Message, caps Caps) ([]Message, bool) {
	// Budget de queue = fraction de la CONVERSATION (pas de la fenêtre). Le lier à
	// la fenêtre était le bug : une conversation de 25k tokens dans une fenêtre de
	// 64k gardait 16k (0.25×64k) en queue → torse minuscule → réduction < 20% →
	// refusée. Lié à la conversation, on garde toujours ~25% des tours récents et
	// on compacte les ~75% du début, quelle que soit la taille de la fenêtre.
	tailBudget := int(float64(estimateTokens(msgs)) * compactTailFrac)
	head, tailStart := compactBounds(msgs, tailBudget)

	// Rien à compacter : le torse [head, tailStart) est vide.
	if tailStart <= head {
		return msgs, false
	}

	torso := msgs[head:tailStart]

	// 3. Dégraissage sans IA : les vieux résultats d'outils longs deviennent un
	//    marqueur. On travaille sur une copie pour ne pas muter l'historique amont.
	pruned := make([]Message, len(torso))
	for i, m := range torso {
		pruned[i] = m
		if m.Role == "tool" {
			if t := msgText(m); len(t) > compactToolPruneLen {
				pruned[i].Content = compactPrunedMarker
			}
		}
	}

	// 4. Résumé du torse par le modèle local (un seul appel). En cas d'échec on
	//    garde le torse dégraissé plutôt que de perdre du contenu.
	summary, err := summarizeTranscript(ctx, renderTranscript(pruned))
	var mid []Message
	if err != nil || strings.TrimSpace(summary) == "" {
		mid = pruned
	} else {
		// Le résumé est injecté comme un tour utilisateur→assistant (jamais un
		// message `system` au milieu : certains gabarits, ex. Qwen, exigent que le
		// system soit uniquement en tête — cf. mémoire qwen36-chat-template-fix).
		mid = []Message{
			{Role: "user", Content: "[CONTEXT COMPACTED] The earlier turns of this conversation were summarized to save context. Here is the summary:\n\n" + summary},
			{Role: "assistant", Content: "Understood, I'll continue from this context."},
		}
	}

	out := make([]Message, 0, head+len(mid)+len(msgs)-tailStart)
	out = append(out, msgs[:head]...)
	out = append(out, mid...)
	out = append(out, msgs[tailStart:]...)

	// Garantie de réduction : on n'accepte la compaction que si elle enlève au
	// moins ~20% du contexte estimé. Sinon (torse déjà maigre, résumé peu rentable)
	// on la refuse — sans ça, jean « compactait » à presque chaque message sans
	// vraiment réduire, puis re-déclenchait aussitôt.
	before, after := estimateTokens(msgs), estimateTokens(out)
	if after > before*4/5 {
		return msgs, false
	}
	return out, true
}

// renderTranscript sérialise le torse en texte lisible pour le résumeur.
func renderTranscript(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "user":
			fmt.Fprintf(&b, "User: %s\n", msgText(m))
		case "assistant":
			if t := msgText(m); t != "" {
				fmt.Fprintf(&b, "Assistant: %s\n", t)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "Assistant → tool %s(%s)\n", tc.Function.Name, tc.Function.Arguments)
			}
		case "tool":
			fmt.Fprintf(&b, "Tool result: %s\n", msgText(m))
		case "system":
			fmt.Fprintf(&b, "System: %s\n", msgText(m))
		}
	}
	s := b.String()
	// Garde-fou pour les petites fenêtres : le résumeur ne doit pas lui-même
	// déborder. On plafonne la transcription (~0,7×contexte en tokens ≈ 2,8
	// caractères/token) en gardant la FIN (la plus récente) et en marquant la
	// troncature de tête.
	maxChars := int(float64(ctxWindow()) * 2.8)
	if maxChars > 0 && len(s) > maxChars {
		s = "[…start truncated…]\n" + s[len(s)-maxChars:]
	}
	return s
}

// summarizeResp modélise le sous-ensemble utile d'une réponse non-streamée de
// /v1/chat/completions.
type summarizeResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// summarizeTranscript demande au modèle local un résumé dense et fidèle du torse.
// Un seul appel NON streamé, sans outils — comme Hermes, on réutilise le modèle
// principal déjà chargé (aucune dépendance, cohérent avec la fenêtre de contexte).
func summarizeTranscript(ctx context.Context, transcript string) (string, error) {
	sys := `You are a context compactor. You are given the transcript of the older turns of a conversation between a user and an AI assistant (with its tools). The PURPOSE of your summary is to let the conversation continue in a fresh, smaller context WITHOUT losing any information that is useful or important to understand what came before and keep working — preserve everything that matters, drop only what is redundant.

Summarize densely and faithfully, keeping ONLY the essentials:
- The user's goal(s) and constraints
- Decisions made and established facts
- Actions/tools run and their important results (file paths, values, config)
- Tasks done, in progress, blocked; next steps
Strict rules: no preamble or conclusion, no verbatim or long quotes, no throwaway detail. Use short bullet points. Aim for 250 words MAX — this is a compression summary, not a report.
Write the summary in the SAME language as the conversation.`

	payload := map[string]any{
		"model": "jean",
		"messages": []Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: transcript},
		},
		"stream":      false,
		"temperature": 0.2,
		// Borne dure : sans ça, un modèle bavard (surtout à reasoning) produit un
		// résumé énorme et lent, donc peu de réduction → re-compaction à chaque tour.
		"max_tokens": 700,
		// Pas de réflexion pour un résumé : plus rapide, plus dense, et évite qu'un
		// modèle hybride gaspille tout le budget en <think> (résumé vide). llama.cpp
		// passe ces kwargs au gabarit Jinja (--jinja).
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://localhost:%d/v1/chat/completions", LLMPort())
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	authHeader(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", friendlyLLMError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return "", fmt.Errorf("résumé: llama-server %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out summarizeResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("résumé: réponse vide")
	}
	c := out.Choices[0].Message.Content
	// Certains modèles à raisonnement préfixent un bloc <think>…</think> : on ne
	// garde que la réponse finale.
	if i := strings.LastIndex(c, thinkClose); i >= 0 {
		c = c[i+len(thinkClose):]
	}
	c = strings.TrimSpace(c)
	// Garde-fou dur : même si le modèle ignore la consigne de longueur, on tronque
	// (~1500 caractères ≈ 375 tokens) pour garantir une vraie compression.
	if len(c) > 1500 {
		c = strings.TrimSpace(c[:1500]) + " […]"
	}
	return c, nil
}
