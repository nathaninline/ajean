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
	// 0.20 (et non 0.25) : on garde un peu moins de tours récents verbatim pour
	// compacter davantage à chaque passe. But affiché par les utilisateurs :
	// conversations quasi infinies « sans s'en rendre compte », donc réductions
	// franches et rares plutôt que fréquentes et molles.
	compactTailFrac = 0.20
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

// compactSummaryBudget renvoie le budget en tokens du résumé, adapté à la
// fenêtre. Assez pour PORTER LE FIL (la demande, les faits déjà trouvés, les
// décisions, l'état d'avancement) sans jamais le perdre — c'était la plainte de
// fond : à 700 tokens fixes, tout le passé d'une longue conversation était
// écrasé en un demi-paragraphe et le modèle « perdait le fil ». Mais le résumé
// reste toujours minuscule devant le torse compacté (souvent des dizaines de
// milliers de tokens), donc la réduction demeure massive. Bornes : 700 (petites
// fenêtres) à 1600 (grandes), ~4% de la fenêtre entre les deux.
func compactSummaryBudget() int {
	n := ctxWindow() * 4 / 100
	if n < 700 {
		n = 700
	}
	if n > 1600 {
		n = 1600
	}
	return n
}

// imageLostMarker remplace la partie `image_url` d'un message multimodal dans
// msgText : ni recallEligible ni le résumeur ne voient jamais l'image elle-même
// (msgText n'en extrait que le texte), donc SANS ce marqueur un message
// see_image ne pèse que sa légende (« Image demandée (fichier) : »), largement
// sous recallArchiveMinLen — il n'est jamais archivé, et le résumeur ne sait
// même pas qu'une image a été montrée. Au compactage suivant, l'image
// disparaît donc du contexte SANS AUCUNE trace : ni bloc recall, ni mention
// dans le résumé — observé en usage réel comme cause probable de boucles de
// raisonnement (le modèle a bien regardé une image de police/pixels, mais ne
// peut plus s'y référer après compaction et redérive à l'aveugle en texte).
// Ce marqueur ne rend pas l'image rappelable (recall reste du texte pur), mais
// il rend sa PERTE visible : le résumeur peut la mentionner dans sa prose, et
// le modèle sait qu'il doit rappeler see_image plutôt que de deviner.
const imageLostMarker = " [image — not kept past this point in the conversation once summarized; call see_image on the same file again if you still need to see it]"

// msgText extrait le texte d'un message (Content est `any`, en pratique string
// ou nil quand l'assistant n'a que des tool_calls). Un message multimodal
// (userMessageContent : parties texte + image quand la vision est active) porte
// un tableau de parties ; on en recolle les segments `text` pour que l'estimation
// de contexte et le transcript de compaction ne partent pas d'un texte vide, et
// on marque la présence d'une partie `image_url` (voir imageLostMarker) — sans
// quoi elle est invisible à toute la chaîne de compaction.
func msgText(m Message) string {
	switch v := m.Content.(type) {
	case string:
		return v
	case []map[string]any:
		var b strings.Builder
		for _, part := range v {
			switch part["type"] {
			case "text":
				if s, ok := part["text"].(string); ok {
					b.WriteString(s)
				}
			case "image_url":
				b.WriteString(imageLostMarker)
			}
		}
		return b.String()
	case []any: // même contenu relu depuis le JSON persisté (map générique)
		var b strings.Builder
		for _, p := range v {
			part, ok := p.(map[string]any)
			if !ok {
				continue
			}
			switch part["type"] {
			case "text":
				if s, ok := part["text"].(string); ok {
					b.WriteString(s)
				}
			case "image_url":
				b.WriteString(imageLostMarker)
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
//   - head : nb de messages protégés en tête = messages système UNIQUEMENT.
//     Le 1er message utilisateur N'EST PLUS épinglé : il était gardé verbatim pour
//     « ancrer l'objectif », mais quand la conversation avait changé de sujet, le
//     modèle voyait cette vieille demande (à laquelle il avait déjà répondu) et y
//     répondait à nouveau après compaction. L'objectif courant est désormais porté
//     explicitement par le résumé roulant, et le 1er message tombe dans le torse :
//     il est donc résumé ET archivé (rappelable par recall), sans rester planté en
//     tête. La demande RÉELLEMENT en cours, elle, est réinjectée juste avant la
//     queue (voir « pending » dans compactMessages).
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

	// 3. Archivage des gros blocs (mémoire longue). AVANT de compacter, chaque bloc
	//    du torse assez long est enregistré VERBATIM sous un id (r7…) : le résumé et
	//    le repli le remplacent par une référence à cet id, que le modèle pourra
	//    rappeler avec recall(id). C'est ce qui rend le compactage sûr même s'il est
	//    agressif — rien n'est perdu, seulement déplacé hors du contexte.
	//    On n'archive QUE si le modèle a réellement ses outils (mode agent) : sans
	//    eux il ne pourrait pas rappeler, autant garder un résumé propre sans ids.
	//    Un échec d'écriture (base absente, tests) laisse archived[i] nil → repli
	//    transparent sur le comportement classique (troncature/marqueur sans id).
	archived := make([]*recallEntry, len(torso))
	var index []recallEntry
	if caps.Agent && compactEnabled() {
		names := torsoToolNames(torso)
		for i, m := range torso {
			if !recallEligible(m) {
				continue
			}
			content := msgText(m)
			label := recallLabel(m.Role, names[m.ToolCallID], content)
			id, aerr := archiveRecallBlock(label, m.Role, content)
			if aerr != nil {
				continue
			}
			archived[i] = &recallEntry{id: id, label: label}
			index = append(index, recallEntry{id: id, label: label})
		}
	}

	// 4. Dégraissage sans IA : les vieux résultats d'outils longs deviennent un
	//    marqueur. On travaille sur une copie pour ne pas muter l'historique amont.
	//    ⚠️ Ce torse dégraissé ne sert QUE de repli si le résumé échoue — surtout
	//    PAS d'entrée au résumeur, cf. juste en dessous. Un bloc archivé pointe vers
	//    son id (récupérable par recall) au lieu d'être effacé aveuglément.
	pruned := make([]Message, len(torso))
	for i, m := range torso {
		pruned[i] = m
		if e := archived[i]; e != nil {
			pruned[i].Content = recallMarker(e.id, e.label)
			continue
		}
		if m.Role == "tool" {
			if t := msgText(m); len(t) > compactToolPruneLen {
				pruned[i].Content = compactPrunedMarker
			}
		}
	}

	// 5. Résumé du torse par le modèle local (un seul appel).
	//
	//    Le résumé se fait sur le torse ORIGINAL, pas sur le dégraissé. C'était LE
	//    bug de fond : on effaçait tous les résultats d'outils PUIS on demandait un
	//    résumé de ce qui restait. Le résumeur ne voyait donc que « Tool result:
	//    [Old tool result cleared] » à la place de chaque page web lue — le résumé
	//    ne pouvait contenir AUCUNE des informations trouvées, seulement la trace
	//    que des outils avaient tourné. À chaque compactage, l'IA repartait donc
	//    d'une recherche vide et recommençait à zéro : elle ne s'arrêtait jamais.
	//
	//    Un bloc archivé est réduit à sa TÊTE (assez pour le résumer fidèlement).
	//    On ne montre PAS l'id recall au résumeur : sa seule tâche est d'écrire de
	//    la prose. Lui exposer « recall:rN » l'incitait à répondre juste par l'id
	//    (résumé dégénéré observé en test) ; l'index des ids est de toute façon
	//    ajouté séparément et de façon déterministe par compactSummaryUserMsg.
	//    Les résultats d'outils non archivés sont seulement RACCOURCIS.
	forSummary := make([]Message, len(torso))
	for i, m := range torso {
		forSummary[i] = m
		if archived[i] != nil {
			r := []rune(msgText(m))
			headTxt := string(r)
			if len(r) > recallSummaryHeadLen {
				headTxt = string(r[:recallSummaryHeadLen]) + "…"
			}
			forSummary[i].Content = headTxt + "\n[…reste de ce bloc omis ici]"
			continue
		}
		if m.Role == "tool" {
			if r := []rune(msgText(m)); len(r) > compactToolSummaryLen {
				forSummary[i].Content = string(r[:compactToolSummaryLen]) + "\n[…suite coupée]"
			}
		}
	}
	summary, err := summarizeTranscript(ctx, renderTranscript(forSummary))
	var mid []Message
	if err != nil || summaryLooksEmpty(summary) {
		// Résumé raté (erreur, vide, ou juste une référence recall) → on garde le
		// torse dégraissé, qui porte au moins les têtes de blocs + les marqueurs
		// recall : bien plus informatif qu'un résumé dégénéré.
		mid = pruned
	} else {
		// Le résumé est injecté comme un tour utilisateur→assistant (jamais un
		// message `system` au milieu : certains gabarits, ex. Qwen, exigent que le
		// system soit uniquement en tête — cf. mémoire qwen36-chat-template-fix).
		// Le message porte aussi la CONSCIENCE de la compaction et l'index des ids
		// rappelables (voir compactSummaryUserMsg).
		mid = []Message{
			{Role: "user", Content: compactSummaryUserMsg(summary, index)},
			{Role: "assistant", Content: "Understood. I'll resume from exactly where I left off, using the findings above, and call recall(id) if I need the full content of an archived block, without redoing work that is already done."},
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

// summaryLooksEmpty détecte un résumé raté : trop court, ou constitué seulement
// de références « recall:rN » (un petit modèle, mal aiguillé, répond parfois par
// un id au lieu d'écrire une vraie prose — vu en test). Dans ce cas on préfère le
// repli dégraissé. Seuil à 40 caractères de VRAI texte (hors jetons recall).
func summaryLooksEmpty(s string) bool {
	s = strings.TrimSpace(s)
	if len([]rune(s)) < 40 {
		return true
	}
	var real strings.Builder
	for _, f := range strings.Fields(s) {
		low := strings.ToLower(strings.Trim(f, ".,;:()[]-•*"))
		if strings.HasPrefix(low, "recall:r") || strings.HasPrefix(low, "recall(") {
			continue
		}
		real.WriteString(f)
		real.WriteString(" ")
	}
	return len([]rune(strings.TrimSpace(real.String()))) < 40
}

// recallEntry associe un id d'archive à son libellé court, pour l'index injecté
// dans le message de compaction.
type recallEntry struct{ id, label string }

// recallEligible dit si un message du torse mérite d'être archivé sous un id :
// un bloc assez gros (le résumé ne le porterait pas fidèlement) d'un rôle réel,
// et qui n'est pas déjà le résumé d'une compaction précédente (on ne ré-archive
// pas un résumé).
func recallEligible(m Message) bool {
	switch m.Role {
	case "tool", "user", "assistant":
	default:
		return false
	}
	t := msgText(m)
	if len([]rune(t)) <= recallArchiveMinLen {
		return false
	}
	return !strings.HasPrefix(t, compactSummaryPrefix)
}

// torsoToolNames associe chaque ToolCallID du torse au nom de l'outil qui l'a
// produit (l'assistant porte les tool_calls, le résultat ne porte que l'id),
// pour donner un libellé lisible aux blocs archivés (« web_read: … »).
func torsoToolNames(torso []Message) map[string]string {
	names := map[string]string{}
	for _, m := range torso {
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				names[tc.ID] = tc.Function.Name
			}
		}
	}
	return names
}

// recallMarker remplace un bloc archivé dans le torse dégraissé (repli). Il cite
// l'id récupérable et interdit explicitement de relancer l'outil pour le refaire.
func recallMarker(id, label string) string {
	return fmt.Sprintf("[Block archived to save context — full content retrievable with recall(\"%s\") (%s). Do NOT re-run a tool to fetch it back.]", id, label)
}

// compactSummaryUserMsg construit le message `user` synthétique qui porte le
// résumé. Il rend le modèle CONSCIENT que la conversation a été compactée (il le
// sait aussi bien que nous), et lui donne l'index des blocs rappelables. L'index
// ne liste QUE les ids créés à cette passe : sa taille reste bornée, une
// conversation infinie ne gonfle jamais cette section (les vieux blocs restent
// joignables par recall_search).
func compactSummaryUserMsg(summary string, index []recallEntry) string {
	var b strings.Builder
	b.WriteString(compactSummaryPrefix)
	if len(index) > 0 {
		b.WriteString(" The earlier turns of this conversation were summarized to save context, but nothing is lost: any block referenced below (or in the summary) as recall:rN can be brought back verbatim with the recall(id) tool, and recall_search(\"keywords\") finds older archived blocks not listed here. Here is the summary:\n\n")
	} else {
		b.WriteString(" The earlier turns of this conversation were summarized to save context. Here is the summary:\n\n")
	}
	b.WriteString(summary)
	if len(index) > 0 {
		b.WriteString("\n\nArchived blocks you can bring back with recall(id):\n")
		for _, e := range index {
			fmt.Fprintf(&b, "- %s — %s\n", e.id, e.label)
		}
		b.WriteString("Only recall a block when you actually need its full content.")
	}
	return b.String()
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
Strict rules: no preamble or conclusion, no verbatim or long quotes, no throwaway detail. Use short bullet points. Be as concise as you can WHILE keeping every fact, decision and still-open task: a detail you drop here is lost for good, so when in doubt keep it. This is a dense compression summary, not a report. Always write ACTUAL prose sentences/bullets — never answer with just an id or a reference.
Write the summary in the SAME language as the conversation.`

	// Budget adapté à la fenêtre (cf. compactSummaryBudget) : porte le fil sans
	// jamais laisser le résumé enfler au point d'annuler la réduction.
	budget := compactSummaryBudget()

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
		"max_tokens": budget,
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
	// pour garantir une vraie compression. La coupe suit le budget (≈ 4 car./token)
	// au lieu d'un 2200 fixe : le résumé doit porter les FAITS déjà trouvés, pas
	// seulement l'intention, sinon l'IA repart en recherche après chaque compactage.
	// Coupé sur une frontière de rune (é, … ne doivent pas devenir des �).
	maxChars := budget * 4
	if r := []rune(c); len(r) > maxChars {
		c = strings.TrimSpace(string(r[:maxChars])) + " […]"
	}
	return c, nil
}
