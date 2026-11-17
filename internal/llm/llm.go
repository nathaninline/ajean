// Package llm parle à une API compatible OpenAI : /chat/completions en flux avec
// appels d'outils, /models et /embeddings. Marche avec OpenAI, Anthropic (point
// d'accès compatible), OpenRouter, Mistral, Groq, DeepSeek, Gemini, Ollama,
// LM Studio, llama.cpp…
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Endpoint : où envoyer les requêtes.
type Endpoint struct {
	URL   string // base de l'API, ex. https://api.openai.com/v1
	Key   string
	Model string
	// Effort : l'effort de raisonnement demandé au modèle (low, high…). Vide : son
	// réglage par défaut. Toutes les API et tous les modèles ne le connaissent pas.
	Effort string
}

// Ready : l'essentiel est-il renseigné ? (la clé est facultative : un serveur local n'en a pas)
func (e Endpoint) Ready() bool { return e.URL != "" && e.Model != "" }

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// Reasoning : la réflexion d'un modèle à raisonnement. Certaines API exigent
	// qu'elle leur soit renvoyée tant que le tour (appels d'outils) n'est pas fini.
	Reasoning string `json:"reasoning_content,omitempty"`
	// Images : images jointes à un message de l'utilisateur (URL « data: »).
	Images []string `json:"-"`
}

// MarshalJSON : un message avec images part sous la forme « contenu en
// plusieurs parties » de l'API ; les autres gardent un contenu texte simple,
// que tous les fournisseurs acceptent.
func (m Message) MarshalJSON() ([]byte, error) {
	type plain Message
	if len(m.Images) == 0 {
		return json.Marshal(plain(m))
	}
	parts := []map[string]any{{"type": "text", "text": m.Content}}
	for _, img := range m.Images {
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": img}})
	}
	return json.Marshal(map[string]any{"role": m.Role, "content": parts})
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool : la déclaration d'un outil, telle que l'API l'attend.
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type Function struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ErrNotConfigured : aucune API n'est encore renseignée.
var ErrNotConfigured = errors.New("aucune API configurée : ouvre les réglages et renseigne l'URL, la clé et le modèle")

// Pas de délai global : une réponse en flux peut durer ; chaque appel est borné
// par son contexte.
var client = &http.Client{}

// base : l'URL saisie, sans barre finale ni « /chat/completions ».
func base(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	return strings.TrimSuffix(u, "/chat/completions")
}

func request(ctx context.Context, method, url, key string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return nil, &APIError{Status: resp.StatusCode, Message: apiError(b)}
	}
	return resp, nil
}

// APIError : l'API a répondu par un refus.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("l'API a répondu %d : %s", e.Status, e.Message)
}

// Rejected : l'API a-t-elle refusé la requête elle-même (son contenu), et non
// échoué pour une raison passagère (quota, surcharge, réseau) ?
func Rejected(err error) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	switch e.Status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

// apiError sort le champ error.message d'une réponse d'erreur au format OpenAI ;
// à défaut, le début du corps.
func apiError(b []byte) string {
	var e struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil {
		switch v := e.Error.(type) {
		case string:
			return v
		case map[string]any:
			if m, ok := v["message"].(string); ok && m != "" {
				return m
			}
		}
	}
	s := strings.TrimSpace(string(b))
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return s
}

// silenceTimeout : silence maximal de l'API pendant une réponse. Large, parce
// qu'un modèle à raisonnement peut réfléchir longtemps avant son premier mot.
const silenceTimeout = 3 * time.Minute

var errSilent = errors.New("l'API ne répond plus (aucune donnée reçue depuis 3 minutes)")

// silent : le contexte a-t-il été annulé par la minuterie de silence (et non
// par l'appelant) ? Une minuterie qu'on ne peut plus arrêter a déjà sonné.
func silent(ctx context.Context, silence *time.Timer) bool {
	return ctx.Err() != nil && !silence.Stop()
}

// chunk : un morceau du flux de /chat/completions.
type chunk struct {
	Error   any    `json:"error"`
	Usage   *Usage `json:"usage"`
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			Reasoning  string `json:"reasoning_content"`
			Reasoning2 string `json:"reasoning"` // autre nom employé par certains serveurs
			ToolCalls  []struct {
				Index    *int         `json:"index"`
				ID       string       `json:"id"`
				Function FunctionCall `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

// Phases d'une réponse, signalées à qui l'a demandé (WithPhase) : le modèle lit
// d'abord tout le contexte (prefill), puis réfléchit, prépare un appel d'outil
// ou écrit. Sans ça, l'interface affichait « réfléchit » pendant la lecture.
const (
	PhaseReading  = "reading"
	PhaseThinking = "thinking"
	PhaseTool     = "tool"
)

type phaseKey struct{}

// WithPhase renvoie un contexte dont les appels à Chat signalent leurs phases à f.
func WithPhase(ctx context.Context, f func(string)) context.Context {
	return context.WithValue(ctx, phaseKey{}, f)
}

// Chat envoie la conversation et lit la réponse en flux. onDelta (facultatif)
// reçoit le texte au fil de l'eau ; le message complet, texte et appels
// d'outils, est renvoyé.
func Chat(ctx context.Context, ep Endpoint, msgs []Message, tools []Tool, onDelta func(string)) (Message, error) {
	out := Message{Role: "assistant"}
	if !ep.Ready() {
		return out, ErrNotConfigured
	}
	body := map[string]any{"model": ep.Model, "messages": msgs, "stream": true}
	if ep.Effort != "" {
		body["reasoning_effort"] = ep.Effort
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	// Un serveur qui accepte la connexion puis se tait bloquerait le tour sans
	// fin : la requête est annulée si rien n'arrive pendant silenceTimeout.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	silence := time.AfterFunc(silenceTimeout, cancel)
	defer silence.Stop()
	onPhase, _ := ctx.Value(phaseKey{}).(func(string))
	phase := ""
	setPhase := func(p string) {
		if onPhase != nil && p != phase {
			phase = p
			onPhase(p)
		}
	}
	setPhase(PhaseReading)
	onUsage, _ := ctx.Value(usageKey{}).(func(Usage))
	if _, refused := noUsage.Load(base(ep.URL)); onUsage != nil && !refused {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	resp, err := request(ctx, http.MethodPost, base(ep.URL)+"/chat/completions", ep.Key, body)
	if err != nil && body["stream_options"] != nil && Rejected(err) {
		// Une API qui ne connaît pas stream_options : sans, et plus jamais avec.
		noUsage.Store(base(ep.URL), true)
		delete(body, "stream_options")
		resp, err = request(ctx, http.MethodPost, base(ep.URL)+"/chat/completions", ep.Key, body)
	}
	if err != nil {
		if silent(ctx, silence) {
			err = errSilent
		}
		if ep.Effort != "" && Rejected(err) {
			// Le refus vient peut-être de ce paramètre : le dire, sinon l'erreur est incompréhensible.
			err = fmt.Errorf("%w (si ce modèle ne gère pas l'effort de raisonnement « %s », remets-le sur Auto)", err, ep.Effort)
		}
		return out, err
	}
	defer resp.Body.Close()

	calls := map[int]*ToolCall{}
	last := -1
	var text, reasoning strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		silence.Reset(silenceTimeout)
		data, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var c chunk
		if json.Unmarshal([]byte(data), &c) != nil {
			continue
		}
		if c.Error != nil {
			return out, fmt.Errorf("erreur de l'API : %s", apiError([]byte(data)))
		}
		if c.Usage != nil && c.Usage.Prompt > 0 && onUsage != nil {
			onUsage(*c.Usage)
		}
		for _, ch := range c.Choices {
			if r := ch.Delta.Reasoning + ch.Delta.Reasoning2; r != "" {
				reasoning.WriteString(r)
				setPhase(PhaseThinking)
			}
			if len(ch.Delta.ToolCalls) > 0 {
				setPhase(PhaseTool)
			}
			if ch.Delta.Content != "" {
				phase = "" // l'écriture se voit d'elle-même ; une phase suivante sera de nouveau signalée
				text.WriteString(ch.Delta.Content)
				if onDelta != nil {
					onDelta(ch.Delta.Content)
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				// Certaines API n'envoient pas d'index : un nouvel id ouvre un nouvel appel.
				idx := last
				switch {
				case tc.Index != nil:
					idx = *tc.Index
				case tc.ID != "" || last < 0:
					idx = last + 1
				}
				cur := calls[idx]
				if cur == nil {
					cur = &ToolCall{Type: "function"}
					calls[idx] = cur
				}
				last = max(last, idx)
				if tc.ID != "" {
					cur.ID = tc.ID
				}
				cur.Function.Name += tc.Function.Name
				cur.Function.Arguments += tc.Function.Arguments
			}
		}
	}
	if err := ctx.Err(); err != nil {
		if silent(ctx, silence) {
			err = errSilent
		}
		return out, err
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("réponse interrompue : %w", err)
	}
	out.Content, out.Reasoning = text.String(), reasoning.String()
	idxs := make([]int, 0, len(calls))
	for i := range calls {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for n, i := range idxs {
		tc := *calls[i]
		if tc.Function.Name == "" {
			continue
		}
		if tc.ID == "" {
			tc.ID = fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), n)
		}
		if strings.TrimSpace(tc.Function.Arguments) == "" {
			tc.Function.Arguments = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, tc)
	}
	return out, nil
}

// Test : un appel minimal pour valider URL, modèle et clé. Renvoie la réponse du modèle.
func Test(ctx context.Context, ep Endpoint) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	m, err := Chat(ctx, ep, []Message{{Role: "user", Content: "Réponds seulement : ok"}}, nil, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(m.Content), nil
}

// Models : les modèles que l'API annonce (GET /models).
func Models(ctx context.Context, ep Endpoint) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := request(ctx, http.MethodGet, base(ep.URL)+"/models", ep.Key, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&r); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(r.Data))
	for _, m := range r.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Embed : un vecteur par texte (POST /embeddings, avec ep.Model comme modèle
// d'embeddings). Sert à la recherche par le sens.
func Embed(ctx context.Context, ep Endpoint, texts []string) ([][]float32, error) {
	if !ep.Ready() {
		return nil, ErrNotConfigured
	}
	resp, err := request(ctx, http.MethodPost, base(ep.URL)+"/embeddings", ep.Key, map[string]any{"model": ep.Model, "input": texts})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&r); err != nil {
		return nil, err
	}
	if len(r.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings : %d vecteurs reçus pour %d textes", len(r.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, d := range r.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, errors.New("embeddings : index hors bornes")
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}

// Transcribe : le texte d'un enregistrement audio (POST /audio/transcriptions,
// avec ep.Model comme modèle de transcription). Sert à la dictée.
func Transcribe(ctx context.Context, ep Endpoint, filename string, audio []byte) (string, error) {
	if !ep.Ready() {
		return "", ErrNotConfigured
	}
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	part, err := w.CreateFormFile("file", filename)
	if err == nil {
		_, err = part.Write(audio)
	}
	if err == nil {
		err = w.WriteField("model", ep.Model)
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base(ep.URL)+"/audio/transcriptions", &form)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if ep.Key != "" {
		req.Header.Set("Authorization", "Bearer "+ep.Key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 != 2 {
		return "", &APIError{Status: resp.StatusCode, Message: apiError(raw)}
	}
	var r struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("réponse de transcription illisible : %w", err)
	}
	return strings.TrimSpace(r.Text), nil
}
