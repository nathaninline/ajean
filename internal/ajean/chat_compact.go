package ajean

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	// dans le torse DÉGRAISSÉ (repli si le résumé échoue).
	compactToolPruneLen = 200
	// Longueur à laquelle on RACCOURCIT (sans l'effacer) un résultat d'outil avant
	// de le donner au résumeur : assez pour que les faits d'une page web y soient,
	// assez court pour que dix pages tiennent dans la transcription.
	compactToolSummaryLen = 1200
)

// compactPrunedMarker remplace un vieux résultat d'outil dans le torse. Il dit
// EXPLICITEMENT de ne pas relancer l'outil : le texte précédent (« Old tool
// result cleared ») se lisait comme une invitation à re-télécharger la page, et
// le modèle repartait en boucle — page relue, contexte plein, nouveau compactage,
// résultat re-effacé, et ainsi de suite.
const compactPrunedMarker = "[Old tool result removed to save context. The important content is in the summary above — do NOT call this tool again to fetch it back.]"

// compactSummaryPrefix ouvre le message `user` synthétique qui porte le résumé.
// Sert aussi à le reconnaître pour ne pas le confondre avec une vraie demande.
const compactSummaryPrefix = "[CONTEXT COMPACTED]"

// compactEnabled indique si le compactage automatique du contexte est actif.
// Défaut : true. Seule une valeur off/false/0/no/non explicite (config.env
// COMPACT) le désactive.
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
// ou nil quand l'assistant n'a que des tool_calls). Un message multimodal
// (userMessageContent : parties texte + image quand la vision est active) porte
// un tableau de parties ; on en recolle les segments `text` pour que l'estimation
// de contexte et le transcript de compaction ne partent pas d'un texte vide.
func msgText(m Message) string {
	switch v := m.Content.(type) {
	case string:
		return v
	case []map[string]any:
		var b strings.Builder
		for _, part := range v {
			if part["type"] == "text" {
				if s, ok := part["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String()
	case []any: // même contenu relu depuis le JSON persisté (map générique)
		var b strings.Builder
		for _, p := range v {
			if part, ok := p.(map[string]any); ok && part["type"] == "text" {
				if s, ok := part["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String()
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

// logCompact trace UNE ligne par décision de compaction sur la sortie d'erreur
// (donc dans `journalctl -u ajean-ui`). Sans ça, une compaction qui ne se
// déclenche pas — ou qui se déclenche et n'enlève rien — est invisible : côté
// UI on ne voit qu'une jauge qui reste haute, sans savoir si le seuil n'a pas
// été atteint ou si la réduction a été refusée.
func logCompact(phase string, used int, before, after []Message, changed bool) {
	fmt.Fprintf(os.Stderr, "[compact] %s ctx=%d seuil=%d/%d est_avant=%d est_apres=%d msgs=%d→%d changé=%v\n",
		phase, used, int(float64(ctxWindow())*compactTriggerFrac), ctxWindow(),
		estimateTokens(before), estimateTokens(after), len(before), len(after), changed)
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
//     jusqu'à remplir le budget, puis on recule jusqu'à une frontière SÛRE :
//     un message `user`, ou un `assistant` (qui, s'il porte des tool_calls, part
//     dans la queue AVEC ses résultats). On ne sépare ainsi jamais un
//     assistant+tool_calls de ses `tool`, et on ne laisse jamais un `tool`
//     orphelin en tête de queue.
//
// Reculer jusqu'à un `user` UNIQUEMENT était trop strict et rendait toute
// 2ᵉ compaction inopérante dans un même tour : pendant une longue boucle
// d'outils il n'y a AUCUN message `user`, donc la queue avalait toute la
// séquence d'outils et le torse était vide (« le compactage ne fait rien »
// alors que ce sont précisément les pages web lues qui remplissent la
// fenêtre). S'arrêter sur un `assistant` coupe proprement entre deux groupes
// d'appels d'outils.
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
	for tailStart > head && msgs[tailStart].Role != "user" && msgs[tailStart].Role != "assistant" {
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
	//    ⚠️ Ce torse dégraissé ne sert QUE de repli si le résumé échoue — surtout
	//    PAS d'entrée au résumeur, cf. juste en dessous.
	pruned := make([]Message, len(torso))
	for i, m := range torso {
		pruned[i] = m
		if m.Role == "tool" {
			if t := msgText(m); len(t) > compactToolPruneLen {
				pruned[i].Content = compactPrunedMarker
			}
		}
	}

	// 4. Résumé du torse par le modèle local (un seul appel).
	//
	//    Le résumé se fait sur le torse ORIGINAL, pas sur le dégraissé. C'était LE
	//    bug de fond : on effaçait tous les résultats d'outils PUIS on demandait un
	//    résumé de ce qui restait. Le résumeur ne voyait donc que « Tool result:
	//    [Old tool result cleared] » à la place de chaque page web lue — le résumé
	//    ne pouvait contenir AUCUNE des informations trouvées, seulement la trace
	//    que des outils avaient tourné. À chaque compactage, l'IA repartait donc
	//    d'une recherche vide et recommençait à zéro : elle ne s'arrêtait jamais.
	//
	//    Les résultats d'outils sont seulement RACCOURCIS (leur début, qui porte
	//    l'essentiel : titre, en-tête, premières lignes) pour que la transcription
	//    reste bornée. Les faits survivent, le volume reste maîtrisé.
	forSummary := make([]Message, len(torso))
	for i, m := range torso {
		forSummary[i] = m
		if m.Role == "tool" {
			if r := []rune(msgText(m)); len(r) > compactToolSummaryLen {
				forSummary[i].Content = string(r[:compactToolSummaryLen]) + "\n[…suite coupée]"
			}
		}
	}
	summary, err := summarizeTranscript(ctx, renderTranscript(forSummary))
	var mid []Message
	if err != nil || strings.TrimSpace(summary) == "" {
		mid = pruned
	} else {
		// Le résumé est injecté comme un tour utilisateur→assistant (jamais un
		// message `system` au milieu : certains gabarits, ex. Qwen, exigent que le
		// system soit uniquement en tête — cf. mémoire qwen36-chat-template-fix).
		mid = []Message{
			{Role: "user", Content: compactSummaryPrefix + " The earlier turns of this conversation were summarized to save context. Here is the summary:\n\n" + summary},
			{Role: "assistant", Content: "Understood. I'll resume from exactly where I left off, using the findings above, without redoing work that is already done."},
		}
	}

	// La demande EN COURS ne doit JAMAIS être diluée dans le résumé. Pendant une
	// longue boucle d'outils (recherche web : dix pages lues d'affilée), la queue
	// n'est faite que d'appels d'outils : le message `user` qui a lancé la
	// recherche tombe dans le torse, alors que le TOUT PREMIER message de la
	// conversation, lui, reste épinglé en tête. Après compaction le modèle voyait
	// donc, comme seule demande explicite, la question du DÉBUT de la conversation
	// — et il y répondait en abandonnant la recherche en cours.
	// On réinjecte donc textuellement la dernière vraie demande du torse, juste
	// avant la queue (les résultats d'outils qu'elle a produits la suivent, comme
	// dans l'historique d'origine). Le torse reste entièrement compactable.
	var pending []Message
	for i := len(torso) - 1; i >= 0; i-- {
		if torso[i].Role != "user" {
			continue
		}
		if strings.HasPrefix(msgText(torso[i]), compactSummaryPrefix) {
			continue // résumé d'une compaction précédente, pas une demande
		}
		pending = []Message{torso[i]}
		break
	}

	out := make([]Message, 0, head+len(mid)+len(pending)+len(msgs)-tailStart)
	out = append(out, msgs[:head]...)
	out = append(out, mid...)
	out = append(out, pending...)
	out = append(out, msgs[tailStart:]...)

	// Garantie de réduction : on n'accepte la compaction que si elle enlève au
	// moins ~20% du contexte estimé. Sinon (torse déjà maigre, résumé peu rentable)
	// on la refuse — sans ça, ajean « compactait » à presque chaque message sans
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

The assistant is MID-TASK: it will read your summary and must resume exactly where it left off, WITHOUT redoing work it has already done. Its own internal reasoning is NOT part of the transcript and is lost — your summary is the only memory it keeps.

Summarize densely and faithfully, keeping ONLY the essentials:
- The user's CURRENT request, goal(s) and constraints
- FINDINGS: the concrete information already gathered — facts, figures, dates, names, URLs, file paths, values, config. This is the most important part: whatever is not here is lost and will have to be looked up again.
- Sources already consulted (URLs opened, files read, commands run) — so they are not consulted a second time
- Decisions made and established facts
- STATE OF PROGRESS: what is already answered, what is still missing, and the next concrete step
Strict rules: no preamble or conclusion, no verbatim or long quotes, no throwaway detail. Use short bullet points. Aim for 300 words MAX — this is a compression summary, not a report.
Write the summary in the SAME language as the conversation.`

	payload := map[string]any{
		"model": "ajean",
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
	// pour garantir une vraie compression. 2200 caractères (≈ 550 tokens) et pas
	// 1500 : le résumé doit désormais porter les FAITS déjà trouvés, pas seulement
	// l'intention, sinon l'IA repart en recherche après chaque compactage. Coupé
	// sur une frontière de rune (é, … ne doivent pas devenir des �).
	if r := []rune(c); len(r) > 2200 {
		c = strings.TrimSpace(string(r[:2200])) + " […]"
	}
	return c, nil
}
