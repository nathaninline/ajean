package agent

// Le contexte du modèle n'est pas infini. Trois garde-fous, du plus fin au plus
// large, pour qu'une conversation puisse durer sans jamais buter dessus :
//
//  1. un résultat d'outil énorme (une longue page, un gros fichier) est écourté
//     avant d'entrer dans le fil ;
//  2. pendant un tour qui s'allonge, les vieux résultats d'outils sont vidés :
//     le modèle a déjà lu ce qu'ils disaient ;
//  3. entre deux tours, les plus vieux échanges sortent du contexte et sont
//     remplacés par un RÉSUMÉ, tenu à jour au fil de la conversation. Le journal,
//     lui, garde tout : memory_search retrouve le détail.
//
// Et si l'API refuse quand même une demande pour sa longueur, le fil est allégé
// et la demande repart une fois.

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ajean/internal/config"
	"ajean/internal/home"
	"ajean/internal/llm"
)

const (
	maxToolResult     = 24_000 // caractères d'un résultat d'outil gardés dans le fil
	keepRecentResults = 6      // résultats d'outils jamais vidés : le modèle travaille dessus
	summaryInputMax   = 60_000 // caractères d'échanges donnés à résumer en une fois
	summaryTimeout    = 2 * time.Minute

	emptiedResult = "[résultat retiré du contexte pour faire de la place : relance l'outil si tu en as encore besoin]"
	summaryNote   = "(Automatic, not written by the user.) The earlier part of this conversation left your context. Here is its summary; the details remain in your journal (memory_search).\n\n"
	summaryAck    = "Understood, I keep it in mind."
)

// tooLongRe : ce que disent les API quand une demande dépasse le contexte du modèle.
var tooLongRe = regexp.MustCompile(`(?i)context.{0,20}(length|window|size)|maximum context|too (long|large|many tokens)|token limit|exceeds? .{0,30}tokens`)

// tooLong : l'API a-t-elle refusé la demande pour sa longueur ?
func tooLong(err error) bool {
	return llm.Rejected(err) && tooLongRe.MatchString(err.Error())
}

func size(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, tc := range m.ToolCalls {
			n += len(tc.Function.Arguments)
		}
	}
	return n
}

// clipResult écourte un résultat d'outil trop long en gardant son début et sa
// fin (une erreur est souvent à la fin), et le dit au modèle.
func clipResult(s string) string {
	r := []rune(s)
	if len(r) <= maxToolResult {
		return s
	}
	head, tail := maxToolResult*2/3, maxToolResult/3
	return string(r[:head]) + "\n[… résultat trop long : " + strconv.Itoa(len(r)-maxToolResult) + " caractères retirés ici …]\n" + string(r[len(r)-tail:])
}

// lighten ramène msgs sous target en vidant les vieux résultats d'outils, du
// plus ancien au plus récent. Les derniers ne sont jamais touchés.
func lighten(msgs []llm.Message, target int) {
	var results []int
	for i, m := range msgs {
		if m.Role == "tool" {
			results = append(results, i)
		}
	}
	over := size(msgs) - target
	for k := 0; k < len(results)-keepRecentResults && over > 0; k++ {
		m := &msgs[results[k]]
		if len(m.Content) > len(emptiedResult) {
			over -= len(m.Content) - len(emptiedResult)
			m.Content = emptiedResult
		}
	}
}

// cutIndex : où couper msgs pour que la suite tienne dans target. Toujours sur
// un message de l'utilisateur, pour ne jamais séparer un appel d'outil de son
// résultat ; à défaut de tenir, on ne garde que le dernier tour.
func cutIndex(msgs []llm.Message, target int) int {
	cut := 0
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role != "user" {
			continue
		}
		cut = i
		if size(msgs[i:]) <= target {
			break
		}
	}
	return cut
}

// trimLocked : au-delà du budget, les plus vieux messages sortent du contexte
// et sont renvoyés pour être résumés. On redescend à la moitié du budget, pour
// ne pas recommencer à chaque tour.
func (c *conversation) trimLocked() (dropped []llm.Message) {
	budget := config.Get().MaxContextChars
	if size(c.msgs) <= budget {
		return nil
	}
	cut := cutIndex(c.msgs, budget/2)
	dropped = c.msgs[:cut:cut]
	c.msgs = append([]llm.Message(nil), c.msgs[cut:]...)
	return dropped
}

// contextLocked : ce que le modèle reçoit de la conversation, c'est-à-dire le
// résumé de ce qui en est sorti (s'il y en a un), puis les messages récents.
// lead est le nombre de messages que le résumé occupe en tête.
func (c *conversation) contextLocked() (msgs []llm.Message, lead int) {
	if c.summary != "" {
		msgs = []llm.Message{{Role: "user", Content: summaryNote + c.summary}, {Role: "assistant", Content: summaryAck}}
	}
	return append(msgs, c.msgs...), len(msgs)
}

func summaryFile(id string) string { return home.State("contexts", id+".md") }

// loadSummary relit le résumé enregistré d'une conversation (vide s'il n'y en a pas).
func loadSummary(id string) string {
	raw, _ := os.ReadFile(summaryFile(id))
	return strings.TrimSpace(string(raw))
}

// summarize fond dans le résumé de la conversation les messages qui viennent de
// sortir du contexte. Un échec n'est pas grave : le journal garde tout, seul le
// résumé reste en retard.
func (c *conversation) summarize(dropped []llm.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), summaryTimeout)
	defer cancel()
	c.mu.Lock()
	previous := c.summary
	c.mu.Unlock()

	ep := config.Get().Chat()
	ep.Effort = "" // un résumé ne demande pas de réflexion particulière
	m, err := llm.Chat(ctx, ep, []llm.Message{{Role: "user", Content: summaryPrompt(previous, dropped)}}, nil, nil)
	text := strings.TrimSpace(m.Content)
	if err == nil && text == "" {
		err = errors.New("le modèle n'a rien répondu")
	}
	if err != nil {
		log.Printf("conversation %s : résumé non mis à jour : %v", c.id, err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deleted {
		return
	}
	c.summary = text
	if err := home.WriteFile(summaryFile(c.id), []byte(text+"\n")); err != nil {
		log.Printf("conversation %s : résumé non enregistré : %v", c.id, err)
	}
}

// summaryPrompt : la demande de résumé. Les messages sortis sont donnés en
// clair ; un passage trop long garde son début et sa fin.
func summaryPrompt(previous string, dropped []llm.Message) string {
	var b strings.Builder
	for _, m := range dropped {
		switch {
		case m.Role == "tool":
			b.WriteString("[tool result] " + clip(m.Content, 600) + "\n")
		case len(m.ToolCalls) > 0:
			for _, tc := range m.ToolCalls {
				b.WriteString("[assistant called " + tc.Function.Name + " " + clip(tc.Function.Arguments, 300) + "]\n")
			}
			if strings.TrimSpace(m.Content) != "" {
				b.WriteString("assistant: " + m.Content + "\n")
			}
		default:
			b.WriteString(m.Role + ": " + m.Content + "\n")
		}
	}
	part := b.String()
	if r := []rune(part); len(r) > summaryInputMax {
		half := summaryInputMax / 2
		part = string(r[:half]) + "\n[…]\n" + string(r[len(r)-half:])
	}
	var p strings.Builder
	p.WriteString("You are keeping the running summary of a long conversation between a user and their assistant. " +
		"The part below is leaving the assistant's context: the summary is all that will remain of it in front of the assistant.\n" +
		"Write the updated summary. Keep what the assistant needs to continue as if it had read everything: what the user wanted and why, " +
		"what was done and decided, facts, names, numbers, dates, file names, URLs, the user's preferences and corrections, and what is still open or promised. " +
		"Drop small talk and the detail of tool calls. Plain text, in the user's language, at most 400 words. Reply with the summary only.\n")
	if previous != "" {
		p.WriteString("\n=== SUMMARY SO FAR ===\n" + previous + "\n")
	}
	p.WriteString("\n=== PART LEAVING THE CONTEXT ===\n" + part)
	return p.String()
}

// forgetSummary efface le résumé enregistré d'une conversation supprimée.
func forgetSummary(id string) {
	if err := os.Remove(summaryFile(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("résumé de %s non effacé : %v", filepath.Base(summaryFile(id)), err)
	}
}

// summarizeLater met à jour le résumé en arrière-plan : le tour est fini pour
// l'utilisateur, il n'a pas à attendre un appel de plus au modèle.
func (c *conversation) summarizeLater(dropped []llm.Message) {
	if len(dropped) > 0 {
		go c.summarize(dropped)
	}
}

// minBudget : plancher du budget de contexte, pour qu'un tour tienne toujours.
const minBudget = 20_000

// shrinkBudget ramène durablement le budget de contexte sous n caractères :
// l'API vient de refuser un fil de cette taille, le modèle a donc un contexte
// plus petit que prévu. Le fil sera raccourci (et résumé) à la fin de ce tour ;
// renvoie ce qu'il faut dire à l'utilisateur.
func shrinkBudget(n int) string {
	budget := max(minBudget, n*2/3)
	if err := config.Update(func(c *config.Config) error { c.MaxContextChars = budget; return nil }); err != nil {
		log.Printf("budget de contexte non réduit : %v", err)
	}
	return "la conversation est trop longue pour ce modèle : j'ai raccourci ce que je lui envoie, renvoie ton message"
}
