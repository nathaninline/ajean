package ajean

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// chat_passation.go — La PASSATION : un point de sauvegarde volontaire de la
// conversation, écrit comme page mémoire datée. L'utilisateur la déclenche
// quand il le veut (bouton « passation » dans le menu +) ; elle ne supprime
// rien, n'injecte rien, ne vide pas la session. C'est un document stable que
// l'IA peut relire dans une session future pour reprendre là où on s'était
// arrêté, sans porter le « poison » d'un contexte compacté.
//
// Structure imposée au modèle :
//   Objectif
//   Problématique
//   Fichiers importants
//   Ce qui a raté
//   Prochaine étape
//
// Le nom de la page est passation-YYYY-MM-DD-HHMM.md (jamais d'écrasement).
// Si le nom est déjà pris (deux passations à la même minute), on ajoute -2, -3…

const passationPrompt = `Tu es un rédacteur de passation. Tu lis la transcription d'une conversation entre un humain et un IA (avec ses outils). Tu en extrais un document structuré, destiné à être relu dans une session neuve pour reprendre exactement là où on s'est arrêté.

Structure EXACTE (pas d'autre section, pas de préambule) :
## Objectif
Ce que la conversation cherchait à accomplir. En 1-2 phrases.

## Problématique
Les contraintes, les difficultés, les points d'attention qui restent. En quelques puces.

## Fichiers importants
Chemin (ou nom) + un mot sur le rôle de chaque fichier pertinent. Liste courte.

## Ce qui a raté
Ce qui a échoué, été abandonné, ou reste inconnu. Si rien : « — ».

## Prochaine étape
L'action concrète immédiate à faire pour continuer. 1-3 puces.

Règles :
- Pas de verbatim, pas de longue citation.
- Pas de méta-commentaire (« Cette passation… »).
- Dense, factuel, actionnable.
- En même langue que la conversation.
- Pas de limite de longueur : sois COMPLET plutôt que court. Chaque section doit porter tout ce qui est utile à la reprise ; développe autant que nécessaire.`

// passationName génère le nom de page mémoire : passation-YYYY-MM-DD-HHMM.md
// Si le nom existe déjà (passation précédente à la même minute), on ajoute -2, -3…
// jusqu'à trouver un nom libre. Jamais d'écrasement.
func passationName() string {
	stamp := time.Now().Format("2006-01-02-1504")
	base := "passation-" + stamp + ".md"
	if !memNameTaken(base) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("passation-%s-%d.md", stamp, i)
		if !memNameTaken(candidate) {
			return candidate
		}
	}
}

// memNameTaken dit si une page mémoire porte déjà ce nom.
func memNameTaken(name string) bool {
	p, err := safeMemPath(name)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// passationTranscript rend la transcription de la conversation pour le résumeur.
// On plafonne à ~0.7× la fenêtre (en caractères) en gardant la FIN (la plus
// récente), comme renderTranscript.
func passationTranscript(msgs []Message) string {
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
			// Raccourcir les résultats d'outils longs (comme le compacteur).
			if t := msgText(m); len(t) > 800 {
				fmt.Fprintf(&b, "Tool result: %s\n[…suite coupée]\n", t[:800])
			} else {
				fmt.Fprintf(&b, "Tool result: %s\n", t)
			}
		case "system":
			// On inclut le system pour que le modèle sache quel projet on est sur.
			fmt.Fprintf(&b, "System: %s\n", msgText(m))
		}
	}
	s := b.String()
	maxChars := int(float64(ctxWindow()) * 2.8)
	if maxChars > 0 && len(s) > maxChars {
		s = "[…début tronqué…]\n" + s[len(s)-maxChars:]
	}
	return s
}

// runPassation lance l'appel au modèle et écrit la page mémoire. Renvoie le nom
// de la page créée. Les événements de progression sont publiés dans le flux.
func (c *Conversation) runPassation(ctx context.Context, epoch int) (string, error) {
	// Vérifier qu'il y a au moins un message utilisateur.
	hasUser := false
	for _, m := range c.Messages {
		if m.Role == "user" && strings.TrimSpace(msgText(m)) != "" {
			hasUser = true
			break
		}
	}
	if !hasUser {
		return "", fmt.Errorf("conversation vide — rien à passer")
	}

	c.appendDelta(epoch, map[string]any{"passation": "en_cours"})

	transcript := passationTranscript(c.Messages)

	// Appel au modèle (non streamé, comme summarizeTranscript).
	payload := map[string]any{
		"model": "ajean",
		"messages": []Message{
			{Role: "system", Content: passationPrompt},
			{Role: "user",     Content: transcript},
		},
		"stream":      false,
		"temperature": 0.3,
		"max_tokens":  2048,
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://localhost:%d/v1/chat/completions", LLMPort())
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		c.appendDelta(epoch, map[string]any{"passation": "erreur", "detail": err.Error()})
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	authHeader(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.appendDelta(epoch, map[string]any{"passation": "erreur", "detail": err.Error()})
		return "", friendlyLLMError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		errMsg := fmt.Sprintf("modèle %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		c.appendDelta(epoch, map[string]any{"passation": "erreur", "detail": errMsg})
		return "", fmt.Errorf("%s", errMsg)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		c.appendDelta(epoch, map[string]any{"passation": "erreur", "detail": "réponse illisible"})
		return "", fmt.Errorf("réponse du modèle illisible: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		c.appendDelta(epoch, map[string]any{"passation": "erreur", "detail": "réponse vide du modèle"})
		return "", fmt.Errorf("le modèle n'a produit aucun contenu")
	}

	content := strings.TrimSpace(out.Choices[0].Message.Content)

	// Écrire la page mémoire (MemAdd gère le chiffrement + l'index MEMORY.md).
	name := passationName()
	if err := MemAdd(name, content); err != nil {
		c.appendDelta(epoch, map[string]any{"passation": "erreur", "detail": err.Error()})
		return "", fmt.Errorf("écriture mémoire: %w", err)
	}

	c.appendDelta(epoch, map[string]any{"passation": "done", "file": name})
	return name, nil
}

// PassationNow est appelé par le handler HTTP. Lance la passation en
// arrière-plan (comme CompactNow) et renvoie immédiatement.
func (c *Conversation) PassationNow() error {
	if !healthCheck() {
		return errModelLoading
	}
	c.mu.Lock()
	if c.Generating {
		c.mu.Unlock()
		return ErrBusy
	}
	c.Generating = true
	epoch := c.epoch
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			if c.epoch == epoch {
				c.Generating = false
				c.cancel = nil
			}
			c.mu.Unlock()
		}()
		c.runPassation(ctx, epoch)
	}()
	return nil
}
