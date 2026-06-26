package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Message is one entry in the chat history sent to llama.cpp.
// `Content` may be nil when an assistant message only contains tool_calls.
type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}
type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// readSkillTool / runShellTool: OpenAI-shaped function definitions advertised
// to the model when the corresponding feature flag is on.
// skillTool : un seul outil pour toute la gestion des skills (lecture +
// auto-gestion création/maj/suppression), ce qui évite d'envoyer trois schémas.
func skillTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "skill",
			Description: "Gère tes skills (guides Markdown réutilisables). action: read=lire, write=créer/remplacer, append=ajouter à la fin sans réécrire, delete=supprimer. content requis pour write/append (1re ligne = titre court #).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":  map[string]any{"type": "string", "enum": []string{"read", "write", "append", "delete"}},
					"name":    map[string]any{"type": "string", "description": "Nom du skill (alphanum, ._-)"},
					"content": map[string]any{"type": "string", "description": "Contenu Markdown (write/append)"},
				},
				"required": []string{"action", "name"},
			},
		},
	}
}

func runShellTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "run_shell",
			Description: "Exécute une commande shell (bash) sur cette machine et retourne stdout, stderr et le code de sortie. Pour inspecter le système, lancer des scripts, lire des logs. Évite les commandes destructrices sauf demande explicite.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "La commande bash"},
					"timeout": map[string]any{"type": "integer", "description": fmt.Sprintf("Timeout s (défaut %d, max %d)", toolDefaultTimeout, toolMaxTimeout)},
				},
				"required": []string{"command"},
			},
		},
	}
}

// Caps are the per-request capabilities (tool access) for a chat turn. They let
// a caller (e.g. an ajean.link agent with its own tools/skills toggles) scope
// what the model can do for this conversation, instead of always inheriting the
// machine's global config. Use globalCaps() to fall back to the global config.
type Caps struct {
	// Agent = mode agent actif : un seul interrupteur qui débloque TOUS les
	// outils de l'IA (shell + skills). Un skill est un outil comme un autre.
	Agent bool
}

// globalCaps reads the machine-wide config — the default when a request doesn't
// specify its own capabilities.
func globalCaps() Caps {
	return Caps{Agent: agentEnabled()}
}

// InjectSkills prepends context system messages to msgs: the decisive-agent
// preamble + machine briefing (when tools are enabled) and the lightweight
// skills directory (when skills are enabled). Merges with an existing system
// message if present.
func InjectSkills(msgs []Message, caps Caps) []Message {
	var parts []string
	// Decisive-agent preamble (anti-loop) — only when the model actually has
	// tools, otherwise it nudges a plain chat model to "call tools" it doesn't
	// have, which leaks malformed tool-call text into the answer.
	if bp := baseSystemPrompt(caps); bp != "" {
		parts = append(parts, bp)
	}
	if mp := machineSystemPrompt(caps); mp != "" {
		parts = append(parts, mp)
	}
	if sp := skillsSystemPrompt(caps); sp != "" {
		parts = append(parts, sp)
	}
	if len(parts) == 0 {
		return msgs
	}
	prefix := strings.Join(parts, "\n\n")
	if len(msgs) > 0 && msgs[0].Role == "system" {
		existing, _ := msgs[0].Content.(string)
		merged := append([]Message{{Role: "system", Content: prefix + "\n\n" + existing}}, msgs[1:]...)
		return merged
	}
	return append([]Message{{Role: "system", Content: prefix}}, msgs...)
}

// EnabledTools returns the tools to advertise on the next inference call.
func EnabledTools(caps Caps) []Tool {
	tools := []Tool{}
	if caps.Agent {
		tools = append(tools, skillTool(), runShellTool())
	}
	return tools
}

// StreamEvent is what a ChatCallback receives for each piece of streamed output.
// Exactly one of {Content, Reasoning, ToolUsed, Stats, Err} is set per call.
type StreamEvent struct {
	Content   string
	Reasoning string
	ToolUsed  *ToolUsedEvent
	Stats     *StatsEvent
	Err       error
}
type ToolUsedEvent struct {
	Name   string
	Label  string // user-visible summary (skill name or the command)
	Result string // tool output (stdout/stderr/exit for run_shell, skill body for read_skill)
	Done   bool   // false = call announced (command only); true = result is ready
	Typing bool   // true = command still being written (partial), no spinner yet
}

// previewArg pulls the (possibly incomplete) string value of key out of a
// streaming tool-call arguments JSON, so the UI can show the command being
// typed live. Best-effort: it tolerates a truncated tail and basic escapes.
func previewArg(args, key string) string {
	i := strings.Index(args, "\""+key+"\"")
	if i < 0 {
		return ""
	}
	rest := args[i+len(key)+2:]
	if j := strings.Index(rest, ":"); j >= 0 {
		rest = rest[j+1:]
	} else {
		return ""
	}
	q := strings.Index(rest, "\"")
	if q < 0 {
		return ""
	}
	rest = rest[q+1:]
	var b strings.Builder
	for x := 0; x < len(rest); x++ {
		c := rest[x]
		if c == '\\' && x+1 < len(rest) {
			switch rest[x+1] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte(rest[x+1])
			}
			x++
			continue
		}
		if c == '"' {
			break
		}
		b.WriteByte(c)
	}
	return b.String()
}

// StatsEvent carries llama.cpp's per-completion timing (final chunk).
type StatsEvent struct {
	PromptTokens    int     `json:"prompt_tokens,omitempty"`
	PromptPerSecond float64 `json:"prompt_per_second,omitempty"`
	PromptMs        float64 `json:"prompt_ms,omitempty"`
	GenTokens       int     `json:"gen_tokens,omitempty"`
	GenPerSecond    float64 `json:"gen_per_second,omitempty"`
	GenMs           float64 `json:"gen_ms,omitempty"`
	// Taille TOTALE du prompt traité ce tour (préfixe caché compris), issue de
	// `usage.prompt_tokens`. 0 si le backend ne renvoie pas d'usage.
	PromptTokensTotal int `json:"prompt_tokens_total,omitempty"`
}

// ChatCallback receives stream events. Return false to abort the stream.
type ChatCallback func(StreamEvent) bool

// completionResp / streamChunk model the subset of llama.cpp's
// OpenAI-compatible /v1/chat/completions response that we care about.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			ToolCalls        []ToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	// llama.cpp's "timings" appears on the final chunk and on intermediate
	// /completion endpoint responses. Snake-case mapping per llama.cpp source.
	Timings *struct {
		PromptN          int     `json:"prompt_n"`
		PromptMs         float64 `json:"prompt_ms"`
		PromptPerSecond  float64 `json:"prompt_per_second"`
		PredictedN       int     `json:"predicted_n"`
		PredictedMs      float64 `json:"predicted_ms"`
		PredictedPerSec  float64 `json:"predicted_per_second"`
	} `json:"timings"`
	// Chunk final (include_usage) : taille totale du prompt, hors choices.
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// runChat drives the full inference loop including tool calling.
// On finish_reason="tool_calls" we execute locally, append a "tool" message
// and call /v1/chat/completions again — up to 8 iterations as a safety cap.
const thinkClose = "</think>"

// runChat returns `extra`: the tool-turn messages (assistant-with-tool_calls and
// their tool results) it appended during the agentic loop. The caller persists
// these into the durable history BEFORE the final assistant text, so the model
// keeps the trace of what it already read/ran across user turns — otherwise it
// re-invokes the same skill/command every turn (it has no memory of having done
// it) and can confabulate paths/results it can no longer see.
func runChat(ctx context.Context, messages []Message, temperature float64, caps Caps, cb ChatCallback) ([]Message, error) {
	var extra []Message
	tools := EnabledTools(caps)
	// Some backends (vanilla llama.cpp builds) don't populate `reasoning_content`
	// in streaming mode: the model's <think> block (opened by the chat template)
	// arrives inline in `content`, terminated by a literal </think>. When
	// reasoning is enabled we split that out ourselves so the UI's reasoning
	// bubble works regardless of backend. The ik_llama.cpp fork already sends
	// reasoning_content, in which case we leave content untouched.
	reasoningOn := reasoningActive(ReadConfig()["REASONING"])
	// When llama.cpp fails to parse a model-generated tool call (HTTP 500), we
	// retry the same turn once with tools removed so the model answers in plain
	// text from the tool results already gathered, instead of dying mid-chat.
	disableTools := false
	// Anti-loop net: if the model re-emits the exact same tool call (name+args)
	// several times, it's stuck — break instead of spinning to the iteration cap.
	lastSig := ""
	repeatSig := 0
	for iter := 0; iter < 8; iter++ {
		payload := map[string]any{
			"model":       "jean",
			"messages":    messages,
			"stream":      true,
			"temperature": temperature,
			// include_usage → chunk final avec `usage.prompt_tokens` = taille TOTALE
			// du prompt (préfixe caché compris), contrairement à timings.prompt_n qui
			// ne compte que les tokens nouvellement traités. Sert au comptage exact du
			// contexte (sinon le system prompt déjà en cache n'est pas recompté).
			"stream_options": map[string]any{"include_usage": true},
		}
		if len(tools) > 0 && !disableTools {
			payload["tools"] = tools
			// The model sometimes emits parallel tool calls, which this llama.cpp
			// build serialises as two concatenated JSON objects in one arguments
			// string ("{...}{...}") and then fails to parse (HTTP 500). Forcing a
			// single tool call per turn avoids that.
			payload["parallel_tool_calls"] = false
		}
		body, _ := json.Marshal(payload)
		url := fmt.Sprintf("http://localhost:%d/v1/chat/completions", LLMPort())
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return extra, err
		}
		req.Header.Set("Content-Type", "application/json")
		authHeader(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cb(StreamEvent{Err: err})
			return extra, err
		}
		// A non-200 here (e.g. context window exceeded after several large tool
		// outputs) is NOT valid SSE: without this check we'd scan an empty/HTML
		// body, find no data lines, and return silently — the chat just stops
		// with no answer. Surface the body so the cause is visible instead.
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
			resp.Body.Close()
			msg := strings.TrimSpace(string(b))
			if msg == "" {
				msg = resp.Status
			}
			// Most common 500 here: llama.cpp couldn't parse a malformed tool call
			// the model emitted. Retry the turn once without tools so it answers
			// in plain text rather than leaving the chat dead.
			if !disableTools && len(tools) > 0 {
				disableTools = true
				// Nudge the model to answer in plain text from what it already
				// gathered, so it doesn't immediately re-emit a tool call that
				// llama.cpp would again fail to parse.
				messages = append(messages, Message{Role: "system", Content: "N'appelle plus d'outil. Réponds maintenant directement en français à partir des informations déjà obtenues."})
				continue
			}
			err := fmt.Errorf("llama-server a renvoyé %d : %s", resp.StatusCode, msg)
			cb(StreamEvent{Err: err})
			return extra, err
		}
		toolCalls := map[int]*ToolCall{}
		assistantContent := strings.Builder{}
		finishReason := ""
		// Accumulateur de stats : timings (prefill/decode) puis usage (total prompt)
		// arrivent sur des chunks séparés ; on émet une copie complète à chaque MAJ
		// pour que les consommateurs (terminal, web) aient toujours tout.
		var stats StatsEvent
		lastPreview := "" // last command preview emitted (to stream the typing)
		// Per-completion reasoning-split state (see reasoningOn comment above).
		sawReasoningField := false
		thinkOpen := reasoningOn
		var thinkTail strings.Builder
		// scanner with a big buffer — some chunks include large arguments JSON
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		aborted := false
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(line[5:])
			if data == "" || data == "[DONE]" {
				continue
			}
			var chunk streamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			// Le chunk d'usage (include_usage) arrive seul, sans choices : on l'émet
			// avant le guard pour ne pas le perdre.
			if chunk.Usage != nil && chunk.Usage.PromptTokens > 0 {
				stats.PromptTokensTotal = chunk.Usage.PromptTokens
				s := stats
				cb(StreamEvent{Stats: &s})
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			ch := chunk.Choices[0]
			if ch.FinishReason != "" {
				finishReason = ch.FinishReason
			}
			if chunk.Timings != nil {
				stats.PromptTokens = chunk.Timings.PromptN
				stats.PromptPerSecond = chunk.Timings.PromptPerSecond
				stats.PromptMs = chunk.Timings.PromptMs
				stats.GenTokens = chunk.Timings.PredictedN
				stats.GenPerSecond = chunk.Timings.PredictedPerSec
				stats.GenMs = chunk.Timings.PredictedMs
				s := stats
				cb(StreamEvent{Stats: &s})
			}
			if len(ch.Delta.ToolCalls) > 0 {
				for i, tc := range ch.Delta.ToolCalls {
					// llama.cpp's stream may omit index; fall back to slot i.
					idx := i
					cur, ok := toolCalls[idx]
					if !ok {
						cur = &ToolCall{Type: "function"}
						toolCalls[idx] = cur
					}
					if tc.ID != "" {
						cur.ID = tc.ID
					}
					if tc.Function.Name != "" {
						cur.Function.Name = tc.Function.Name
					}
					cur.Function.Arguments += tc.Function.Arguments
				}
				// Stream the command being typed: extract the partial value and
				// emit it whenever it grows, so the UI shows it appear live.
				if cur := toolCalls[0]; cur != nil {
					key := "command"
					if cur.Function.Name == "skill" {
						key = "name"
					}
					if p := previewArg(cur.Function.Arguments, key); p != "" && p != lastPreview {
						lastPreview = p
						if !cb(StreamEvent{ToolUsed: &ToolUsedEvent{Name: cur.Function.Name, Label: p, Typing: true}}) {
							aborted = true
							break
						}
					}
				}
				continue
			}
			if ch.Delta.ReasoningContent != "" {
				// Backend already separates reasoning — trust it, disable our split.
				sawReasoningField = true
				thinkOpen = false
				if !cb(StreamEvent{Reasoning: ch.Delta.ReasoningContent}) {
					aborted = true
					break
				}
			}
			if ch.Delta.Content != "" {
				assistantContent.WriteString(ch.Delta.Content)
				if !thinkOpen || sawReasoningField {
					if !cb(StreamEvent{Content: ch.Delta.Content}) {
						aborted = true
						break
					}
				} else {
					// Inside the prompt-opened <think> block: stream to the
					// reasoning bubble until the closing </think>, then switch
					// the remainder to normal content.
					thinkTail.WriteString(ch.Delta.Content)
					s := thinkTail.String()
					if i := strings.Index(s, thinkClose); i >= 0 {
						reason := s[:i]
						after := strings.TrimLeft(s[i+len(thinkClose):], "\r\n")
						thinkOpen = false
						thinkTail.Reset()
						if reason != "" && !cb(StreamEvent{Reasoning: reason}) {
							aborted = true
							break
						}
						if after != "" && !cb(StreamEvent{Content: after}) {
							aborted = true
							break
						}
					} else {
						// Hold back a tail that could be a partial "</think>".
						keep := len(thinkClose) - 1
						if len(s) > keep {
							emit := s[:len(s)-keep]
							thinkTail.Reset()
							thinkTail.WriteString(s[len(s)-keep:])
							if !cb(StreamEvent{Reasoning: emit}) {
								aborted = true
								break
							}
						}
					}
				}
			}
		}
		// Flush any buffered reasoning if the stream ended mid-think.
		if !aborted && thinkOpen && thinkTail.Len() > 0 {
			cb(StreamEvent{Reasoning: thinkTail.String()})
		}
		resp.Body.Close()
		if aborted {
			return extra, nil
		}

		// Treat any accumulated tool calls as a tool turn even if the backend set
		// finish_reason to "stop" instead of "tool_calls" (some llama.cpp builds
		// do this) — otherwise we'd skip execution AND skip answering.
		if len(toolCalls) > 0 {
			// 1. Append assistant message with tool_calls so the model sees its own decision next turn.
			idxs := make([]int, 0, len(toolCalls))
			for k := range toolCalls {
				idxs = append(idxs, k)
			}
			sort.Ints(idxs)
			tcs := make([]ToolCall, 0, len(idxs))
			for i, k := range idxs {
				tc := *toolCalls[k]
				if tc.ID == "" {
					tc.ID = fmt.Sprintf("call_%d_%d", iter, i)
				}
				if tc.Function.Arguments == "" {
					tc.Function.Arguments = "{}"
				}
				tcs = append(tcs, tc)
			}
			assistant := Message{Role: "assistant", ToolCalls: tcs}
			if s := assistantContent.String(); s != "" {
				assistant.Content = s
			}
			messages = append(messages, assistant)
			extra = append(extra, assistant)
			// Loop guard: same exact call(s) as last turn? Count it; on the 3rd
			// identical turn, stop so we don't churn the same command forever.
			sig := strings.Builder{}
			for _, tc := range tcs {
				sig.WriteString(tc.Function.Name)
				sig.WriteByte('\x00')
				sig.WriteString(tc.Function.Arguments)
				sig.WriteByte('\n')
			}
			if s := sig.String(); s == lastSig {
				repeatSig++
				if repeatSig >= 2 {
					cb(StreamEvent{Content: "\n\n[stop: appel d'outil répété en boucle — " + tcs[0].Function.Name + "]"})
					return extra, nil
				}
			} else {
				lastSig = s
				repeatSig = 0
			}
			// 2. Execute each tool locally and append a "tool" reply.
			for _, tc := range tcs {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				// Derive the human label (command / skill name) up front so we can
				// announce the call BEFORE running it — otherwise the UI shows
				// nothing while a slow shell command runs and looks frozen.
				label := ""
				switch tc.Function.Name {
				case "skill":
					label, _ = args["name"].(string)
				case "run_shell":
					label, _ = args["command"].(string)
				}
				cb(StreamEvent{ToolUsed: &ToolUsedEvent{Name: tc.Function.Name, Label: label}})

				result := ""
				switch tc.Function.Name {
				case "skill":
					action, _ := args["action"].(string)
					content, _ := args["content"].(string)
					switch action {
					case "read":
						if c := SkillContent(label); c != "" {
							result = c
						} else {
							result = fmt.Sprintf("[erreur] skill '%s' introuvable", label)
						}
					case "write":
						if werr := SaveSkill(label, "", content); werr != nil {
							result = "[erreur] " + werr.Error()
						} else {
							result = fmt.Sprintf("[ok] skill '%s' enregistré", label)
						}
					case "append":
						if werr := AppendSkill(label, content); werr != nil {
							result = "[erreur] " + werr.Error()
						} else {
							result = fmt.Sprintf("[ok] skill '%s' enrichi (append)", label)
						}
					case "delete":
						if derr := DeleteSkill(label); derr != nil {
							result = "[erreur] " + derr.Error()
						} else {
							result = fmt.Sprintf("[ok] skill '%s' supprimé", label)
						}
					default:
						result = "[erreur] action skill inconnue: " + action
					}
				case "run_shell":
					to := 0
					switch v := args["timeout"].(type) {
					case float64:
						to = int(v)
					case int:
						to = v
					}
					result = runShell(label, to)
				default:
					result = "[erreur] outil inconnu: " + tc.Function.Name
				}
				shown := result
				if r := []rune(shown); len(r) > 4000 {
					shown = string(r[:4000]) + "\n…[tronqué]"
				}
				cb(StreamEvent{ToolUsed: &ToolUsedEvent{Name: tc.Function.Name, Label: label, Result: shown, Done: true}})
				toolMsg := Message{Role: "tool", ToolCallID: tc.ID, Content: result}
				messages = append(messages, toolMsg)
				extra = append(extra, toolMsg)
			}
			continue
		}
		// Normal end of turn. If the model produced no visible answer at all
		// (empty content, e.g. it stopped right after a tool result), say so
		// instead of leaving the user staring at a silent, finished chat.
		if strings.TrimSpace(assistantContent.String()) == "" {
			cb(StreamEvent{Content: "_(le modèle n'a pas produit de réponse — finish: " + finishReason + ")_"})
		}
		return extra, nil
	}
	cb(StreamEvent{Content: "\n\n[stop: trop d'appels d'outils]"})
	return extra, nil
}

// healthCheck pings llama.cpp's /health endpoint.
func healthCheck() bool {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/health", LLMPort()))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == 200
}
