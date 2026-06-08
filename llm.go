package main

import (
	"bufio"
	"bytes"
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
func readSkillTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "read_skill",
			Description: "Lit le contenu détaillé d'un skill listé dans le system prompt.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "Nom exact du skill"},
				},
				"required": []string{"name"},
			},
		},
	}
}

func runShellTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "run_shell",
			Description: "Exécute une commande shell sur le serveur (bash). Retourne stdout, stderr et le code de sortie. Utilise-le pour inspecter le système, lancer des scripts, consulter des logs. Évite les commandes destructrices sauf si l'utilisateur le demande explicitement.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "La commande bash à exécuter"},
					"timeout": map[string]any{"type": "integer", "description": fmt.Sprintf("Timeout en secondes (défaut %d, max %d)", toolDefaultTimeout, toolMaxTimeout)},
				},
				"required": []string{"command"},
			},
		},
	}
}

// InjectSkills prepends the lightweight skills directory message to msgs when
// skills are enabled. Merges with an existing system message if present.
func InjectSkills(msgs []Message) []Message {
	sp := skillsSystemPrompt()
	if sp == "" {
		return msgs
	}
	if len(msgs) > 0 && msgs[0].Role == "system" {
		existing, _ := msgs[0].Content.(string)
		merged := append([]Message{{Role: "system", Content: sp + "\n\n" + existing}}, msgs[1:]...)
		return merged
	}
	return append([]Message{{Role: "system", Content: sp}}, msgs...)
}

// EnabledTools returns the tools to advertise on the next inference call.
func EnabledTools() []Tool {
	tools := []Tool{}
	if skillsEnabled() && len(ListSkills()) > 0 {
		tools = append(tools, readSkillTool())
	}
	if toolsEnabled() {
		tools = append(tools, runShellTool())
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
	Name  string
	Label string // user-visible summary (skill name or first ~80 chars of command)
}

// StatsEvent carries llama.cpp's per-completion timing (final chunk).
type StatsEvent struct {
	PromptTokens    int     `json:"prompt_tokens"`
	PromptPerSecond float64 `json:"prompt_per_second"`
	PromptMs        float64 `json:"prompt_ms"`
	GenTokens       int     `json:"gen_tokens"`
	GenPerSecond    float64 `json:"gen_per_second"`
	GenMs           float64 `json:"gen_ms"`
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
}

// runChat drives the full inference loop including tool calling.
// On finish_reason="tool_calls" we execute locally, append a "tool" message
// and call /v1/chat/completions again — up to 8 iterations as a safety cap.
func runChat(messages []Message, temperature float64, cb ChatCallback) error {
	tools := EnabledTools()
	for iter := 0; iter < 8; iter++ {
		payload := map[string]any{
			"model":       "jean",
			"messages":    messages,
			"stream":      true,
			"temperature": temperature,
		}
		if len(tools) > 0 {
			payload["tools"] = tools
		}
		body, _ := json.Marshal(payload)
		url := fmt.Sprintf("http://localhost:%d/v1/chat/completions", LLMPort())
		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		authHeader(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cb(StreamEvent{Err: err})
			return err
		}
		toolCalls := map[int]*ToolCall{}
		assistantContent := strings.Builder{}
		finishReason := ""
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
			if err := json.Unmarshal([]byte(data), &chunk); err != nil || len(chunk.Choices) == 0 {
				continue
			}
			ch := chunk.Choices[0]
			if ch.FinishReason != "" {
				finishReason = ch.FinishReason
			}
			if chunk.Timings != nil {
				cb(StreamEvent{Stats: &StatsEvent{
					PromptTokens:    chunk.Timings.PromptN,
					PromptPerSecond: chunk.Timings.PromptPerSecond,
					PromptMs:        chunk.Timings.PromptMs,
					GenTokens:       chunk.Timings.PredictedN,
					GenPerSecond:    chunk.Timings.PredictedPerSec,
					GenMs:           chunk.Timings.PredictedMs,
				}})
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
				continue
			}
			if ch.Delta.ReasoningContent != "" {
				if !cb(StreamEvent{Reasoning: ch.Delta.ReasoningContent}) {
					aborted = true
					break
				}
			}
			if ch.Delta.Content != "" {
				assistantContent.WriteString(ch.Delta.Content)
				if !cb(StreamEvent{Content: ch.Delta.Content}) {
					aborted = true
					break
				}
			}
		}
		resp.Body.Close()
		if aborted {
			return nil
		}

		if finishReason == "tool_calls" && len(toolCalls) > 0 {
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
			// 2. Execute each tool locally and append a "tool" reply.
			for _, tc := range tcs {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				result := ""
				label := ""
				switch tc.Function.Name {
				case "read_skill":
					name, _ := args["name"].(string)
					label = name
					if c := SkillContent(name); c != "" {
						result = c
					} else {
						result = fmt.Sprintf("[erreur] skill '%s' introuvable", name)
					}
				case "run_shell":
					cmd, _ := args["command"].(string)
					to := 0
					switch v := args["timeout"].(type) {
					case float64:
						to = int(v)
					case int:
						to = v
					}
					label = cmd
					if len(label) > 80 {
						label = label[:80] + "…"
					}
					result = runShell(cmd, to)
				default:
					result = "[erreur] outil inconnu: " + tc.Function.Name
				}
				cb(StreamEvent{ToolUsed: &ToolUsedEvent{Name: tc.Function.Name, Label: label}})
				messages = append(messages, Message{Role: "tool", ToolCallID: tc.ID, Content: result})
			}
			continue
		}
		return nil
	}
	cb(StreamEvent{Content: "\n\n[stop: trop d'appels d'outils]"})
	return nil
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
