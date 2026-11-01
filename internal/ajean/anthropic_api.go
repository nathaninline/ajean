package ajean

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// anthropic_api.go : API Anthropic (/v1/messages) devant le preset actif, pour
// brancher Claude Code (ANTHROPIC_BASE_URL) sans retoucher le chat template.
//
// On ne transmet PAS la requête Anthropic au moteur : chaque template Jinja
// casse à sa façon sur ce format (système en plusieurs blocs, tool_result dans
// des messages user, blocs thinking rejoués…). On la traduit en une requête
// /v1/chat/completions « propre » (un seul system en tête, contenus aplatis,
// rôle tool), que n'importe quel template d'origine accepte, puis on retraduit
// la réponse (et le flux SSE) au format Anthropic. Le moteur visé est celui du
// preset actif (resolveChatEndpoint) : llama.cpp local, API externe ou GPU Cloud.

// --- format Anthropic (entrée) ---

type antRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	System        json.RawMessage `json:"system"`
	Messages      []antMessage    `json:"messages"`
	Tools         []antTool       `json:"tools"`
	ToolChoice    *antToolChoice  `json:"tool_choice"`
	Temperature   *float64        `json:"temperature"`
	TopP          *float64        `json:"top_p"`
	TopK          *int            `json:"top_k"`
	StopSequences []string        `json:"stop_sequences"`
	Stream        bool            `json:"stream"`
}

type antMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type antBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	} `json:"source"`
}

type antTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type antToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// --- format OpenAI (vers le moteur) ---

type oaMsg struct {
	Role       string       `json:"role"`
	Content    any          `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// antBlocks lit un contenu Anthropic : chaîne simple ou tableau de blocs.
func antBlocks(raw json.RawMessage) []antBlock {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return []antBlock{{Type: "text", Text: s}}
	}
	var bs []antBlock
	_ = json.Unmarshal(raw, &bs)
	return bs
}

// antText : texte seul d'un contenu (images et autres blocs ignorés).
func antText(raw json.RawMessage) string {
	var parts []string
	for _, b := range antBlocks(raw) {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// userContent : chaîne si le message n'a que du texte (ce que tous les
// templates savent rendre), tableau de parts OpenAI s'il y a des images.
func userContent(blocks []antBlock) any {
	var texts []string
	var parts []map[string]any
	hasImage := false
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				texts = append(texts, b.Text)
				parts = append(parts, map[string]any{"type": "text", "text": b.Text})
			}
		case "image":
			if b.Source == nil {
				continue
			}
			u := b.Source.URL
			if b.Source.Type == "base64" {
				u = "data:" + b.Source.MediaType + ";base64," + b.Source.Data
			}
			if u != "" {
				hasImage = true
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": u}})
			}
		}
	}
	if !hasImage {
		if len(texts) == 0 {
			return nil
		}
		return strings.Join(texts, "\n\n")
	}
	return parts
}

// antToOpenAI traduit une requête Anthropic en requête chat/completions.
func antToOpenAI(in antRequest, model string) map[string]any {
	var msgs []oaMsg
	if sys := antText(in.System); sys != "" {
		msgs = append(msgs, oaMsg{Role: "system", Content: sys})
	}
	for _, m := range in.Messages {
		blocks := antBlocks(m.Content)
		if m.Role == "assistant" {
			var texts []string
			var calls []oaToolCall
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if b.Text != "" {
						texts = append(texts, b.Text)
					}
				case "tool_use":
					var c oaToolCall
					c.ID, c.Type = b.ID, "function"
					c.Function.Name = b.Name
					c.Function.Arguments = "{}"
					if a := bytes.TrimSpace(b.Input); len(a) > 0 && string(a) != "null" {
						c.Function.Arguments = string(a)
					}
					calls = append(calls, c)
				}
				// thinking / redacted_thinking : pas rejoués (les templates les
				// gèrent mal et le moteur n'en a pas besoin pour continuer).
			}
			om := oaMsg{Role: "assistant", Content: strings.Join(texts, "\n\n"), ToolCalls: calls}
			if len(texts) == 0 && len(calls) > 0 {
				om.Content = nil
			}
			msgs = appendMerged(msgs, om)
			continue
		}
		// user : les tool_result deviennent des messages tool, placés juste après
		// l'assistant qui a appelé ; le reste du texte suit en message user.
		var rest []antBlock
		for _, b := range blocks {
			if b.Type != "tool_result" {
				rest = append(rest, b)
				continue
			}
			txt := antText(b.Content)
			if b.IsError {
				txt = "Erreur : " + txt
			}
			msgs = append(msgs, oaMsg{Role: "tool", ToolCallID: b.ToolUseID, Content: txt})
		}
		if c := userContent(rest); c != nil {
			msgs = appendMerged(msgs, oaMsg{Role: "user", Content: c})
		}
	}
	out := map[string]any{"model": model, "messages": msgs, "stream": in.Stream}
	if in.MaxTokens > 0 {
		out["max_tokens"] = in.MaxTokens
	}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if in.TopK != nil {
		out["top_k"] = *in.TopK
	}
	if len(in.StopSequences) > 0 {
		out["stop"] = in.StopSequences
	}
	if in.Stream {
		out["stream_options"] = map[string]any{"include_usage": true}
	}
	var tools []map[string]any
	for _, t := range in.Tools {
		if len(t.InputSchema) == 0 { // outils serveur Anthropic (web_search…) : pas exécutables ici
			continue
		}
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
			"name": t.Name, "description": t.Description, "parameters": t.InputSchema,
		}})
	}
	if len(tools) > 0 {
		out["tools"] = tools
		if tc := in.ToolChoice; tc != nil {
			switch tc.Type {
			case "any":
				out["tool_choice"] = "required"
			case "none":
				out["tool_choice"] = "none"
			case "tool":
				out["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": tc.Name}}
			}
		}
	}
	return out
}

// appendMerged fusionne deux messages user (texte) consécutifs : beaucoup de
// templates exigent l'alternance user/assistant.
func appendMerged(msgs []oaMsg, m oaMsg) []oaMsg {
	if n := len(msgs); n > 0 && m.Role == "user" && msgs[n-1].Role == "user" {
		a, okA := msgs[n-1].Content.(string)
		b, okB := m.Content.(string)
		if okA && okB {
			msgs[n-1].Content = a + "\n\n" + b
			return msgs
		}
	}
	return append(msgs, m)
}

func antStopReason(finish string) string {
	switch finish {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	}
	return "end_turn"
}

func antID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func antError(w http.ResponseWriter, code int, kind, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]any{"type": kind, "message": msg}})
}

// antKeyOK : la clé du client (x-api-key ou Bearer, Claude Code envoie l'une ou
// l'autre selon ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN) contre la clé API de
// la machine. Sans clé configurée, seule la boucle locale est admise, et sur un
// nom local : une requête du tunnel ou de <machine>.oai.ajean.link peut arriver
// d'une adresse locale, son Host la trahit.
func antKeyOK(r *http.Request) bool {
	want := readAPIKey()
	if want == "" {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		name := r.Host
		if h, _, err := net.SplitHostPort(name); err == nil {
			name = h
		}
		localName := name == "localhost" || name == "127.0.0.1" || name == "::1"
		return ip != nil && ip.IsLoopback() && localName
	}
	// Les deux en-têtes peuvent arriver ensemble (Claude Code connecté à un
	// compte ajoute le sien) : l'un des deux suffit.
	for _, got := range []string{r.Header.Get("x-api-key"), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")} {
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(want)) == 1 {
			return true
		}
	}
	return false
}

// isAnthropicPath : routes servies par anthropicHandler.
func isAnthropicPath(p string) bool {
	return p == "/v1/messages" || p == "/v1/messages/count_tokens"
}

// anthropicHandler sert /v1/messages et /v1/messages/count_tokens.
func anthropicHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			antError(w, http.StatusMethodNotAllowed, "invalid_request_error", "POST attendu")
			return
		}
		if !antKeyOK(r) {
			antError(w, http.StatusUnauthorized, "authentication_error", "clé API invalide")
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			antError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		var in antRequest
		if err := json.Unmarshal(raw, &in); err != nil {
			antError(w, http.StatusBadRequest, "invalid_request_error", "JSON invalide : "+err.Error())
			return
		}
		if r.URL.Path == "/v1/messages/count_tokens" {
			// Estimation (~4 caractères par token) : Claude Code s'en sert pour sa
			// jauge, pas pour facturer.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]int{"input_tokens": len(raw) / 4})
			return
		}
		ep := resolveChatEndpoint()
		if ep.URL == "" {
			antError(w, http.StatusServiceUnavailable, "api_error", "aucun moteur actif")
			return
		}
		body, _ := json.Marshal(antToOpenAI(in, ep.Model))
		// Client tiers : son passage sur le moteur est compté (llm_rewarm.go).
		ctx := withThirdParty(r.Context())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(body))
		if err != nil {
			antError(w, http.StatusInternalServerError, "api_error", err.Error())
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if ep.Key != "" {
			req.Header.Set("Authorization", "Bearer "+ep.Key)
		}
		resp, err := doLLM(ctx, req, body, ep)
		if err != nil {
			antError(w, http.StatusBadGateway, "api_error", "moteur injoignable : "+err.Error())
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			kind := "api_error"
			if resp.StatusCode == http.StatusBadRequest {
				kind = "invalid_request_error"
			}
			antError(w, resp.StatusCode, kind, fmt.Sprintf("moteur : %s", strings.TrimSpace(string(b))))
			return
		}
		model := in.Model
		if model == "" {
			model = ep.Model
		}
		if in.Stream {
			antStream(w, resp.Body, model)
			return
		}
		antWhole(w, resp.Body, model)
	})
}

type oaChoiceMsg struct {
	Content          string       `json:"content"`
	ReasoningContent string       `json:"reasoning_content"`
	ToolCalls        []oaToolCall `json:"tool_calls"`
}

type oaUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// antWhole : réponse non streamée.
func antWhole(w http.ResponseWriter, body io.Reader, model string) {
	var out struct {
		Choices []struct {
			Message      oaChoiceMsg `json:"message"`
			FinishReason string      `json:"finish_reason"`
		} `json:"choices"`
		Usage oaUsage `json:"usage"`
	}
	if err := json.NewDecoder(body).Decode(&out); err != nil || len(out.Choices) == 0 {
		antError(w, http.StatusBadGateway, "api_error", "réponse du moteur illisible")
		return
	}
	c := out.Choices[0]
	content := []map[string]any{}
	if c.Message.ReasoningContent != "" {
		content = append(content, map[string]any{"type": "thinking", "thinking": c.Message.ReasoningContent, "signature": ""})
	}
	if c.Message.Content != "" {
		content = append(content, map[string]any{"type": "text", "text": c.Message.Content})
	}
	for _, tc := range c.Message.ToolCalls {
		id := tc.ID
		if id == "" {
			id = antID("toolu_")
		}
		input := json.RawMessage(tc.Function.Arguments)
		if !json.Valid(input) {
			input = json.RawMessage("{}")
		}
		content = append(content, map[string]any{"type": "tool_use", "id": id, "name": tc.Function.Name, "input": input})
	}
	stop := antStopReason(c.FinishReason)
	if len(c.Message.ToolCalls) > 0 {
		stop = "tool_use"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": antID("msg_"), "type": "message", "role": "assistant", "model": model,
		"content": content, "stop_reason": stop, "stop_sequence": nil,
		"usage": map[string]int{"input_tokens": out.Usage.PromptTokens, "output_tokens": out.Usage.CompletionTokens},
	})
}

// antStream retraduit le flux SSE OpenAI en événements Anthropic
// (message_start, content_block_start/delta/stop, message_delta, message_stop).
func antStream(w http.ResponseWriter, body io.Reader, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	fl, _ := w.(http.Flusher)
	emit := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if fl != nil {
			fl.Flush()
		}
	}
	emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": antID("msg_"), "type": "message", "role": "assistant", "model": model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 0, "output_tokens": 0},
	}})

	idx := -1        // index du bloc Anthropic ouvert
	cur := ""        // "thinking", "text", "tool"
	curTool := -1    // index OpenAI de l'appel d'outil ouvert
	sawTool := false // au moins un tool_use émis
	closeBlock := func() {
		if cur != "" {
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
			cur = ""
		}
	}
	open := func(kind string, block map[string]any) {
		closeBlock()
		idx++
		cur = kind
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": block})
	}
	finish, usage := "", oaUsage{}

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ch struct {
			Choices []struct {
				Delta        oaChoiceMsg `json:"delta"`
				FinishReason *string     `json:"finish_reason"`
			} `json:"choices"`
			Usage *oaUsage `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &ch) != nil {
			continue
		}
		if ch.Usage != nil {
			usage = *ch.Usage
		}
		for _, c := range ch.Choices {
			d := c.Delta
			if d.ReasoningContent != "" {
				if cur != "thinking" {
					open("thinking", map[string]any{"type": "thinking", "thinking": ""})
				}
				emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx,
					"delta": map[string]any{"type": "thinking_delta", "thinking": d.ReasoningContent}})
			}
			if d.Content != "" {
				if cur != "text" {
					open("text", map[string]any{"type": "text", "text": ""})
				}
				emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx,
					"delta": map[string]any{"type": "text_delta", "text": d.Content}})
			}
			for i, tc := range d.ToolCalls {
				ti := i
				if tc.Index != nil {
					ti = *tc.Index
				}
				if cur != "tool" || ti != curTool {
					id := tc.ID
					if id == "" {
						id = antID("toolu_")
					}
					open("tool", map[string]any{"type": "tool_use", "id": id, "name": tc.Function.Name, "input": map[string]any{}})
					curTool, sawTool = ti, true
				}
				if tc.Function.Arguments != "" {
					emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx,
						"delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Function.Arguments}})
				}
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				finish = *c.FinishReason
			}
		}
	}
	closeBlock()
	stop := antStopReason(finish)
	if sawTool {
		stop = "tool_use"
	}
	emit("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": map[string]int{"input_tokens": usage.PromptTokens, "output_tokens": usage.CompletionTokens}})
	emit("message_stop", map[string]any{"type": "message_stop"})
}
