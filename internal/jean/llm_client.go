package jean

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"
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

// Tool definitions: OpenAI-shaped function schemas advertised to the model when
// the agent mode is on. The memory tools (mem_*) let the model keep persistent
// Markdown notes across sessions; bash is its real access to the machine.

func memSearchTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "mem_search",
			Description: "Search your memory (Markdown pages under MEMORY/). Returns a ranked list of {file, title, snippet}. Use it FIRST when the user mentions something you might already know (preferences, ongoing projects, past decisions). Follow up with mem_read on the most relevant page.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Keywords or a short phrase"},
					"limit": map[string]any{"type": "integer", "description": "Max results (default 8, max 30)"},
				},
				"required": []string{"query"},
			},
		},
	}
}

func memReadTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "mem_read",
			Description: "Read a memory page (Markdown file). 1-indexed output, lines prefixed with their number. offset/limit for long pages.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file":   map[string]any{"type": "string", "description": "Page name (e.g. docker-notes.md)"},
					"offset": map[string]any{"type": "integer", "description": "Start line (1-indexed, default 1)"},
					"limit":  map[string]any{"type": "integer", "description": "Number of lines (default 500, max 500)"},
				},
				"required": []string{"file"},
			},
		},
	}
}

func memAddTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "mem_add",
			Description: "Create a new memory page (Markdown). One topic per page, descriptive kebab-case name. First line = short title (#). Refuses to overwrite an existing page (use mem_edit).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file":    map[string]any{"type": "string", "description": "Page name (e.g. docker-notes.md)"},
					"content": map[string]any{"type": "string", "description": "Markdown content (first line = title #)"},
				},
				"required": []string{"file", "content"},
			},
		},
	}
}

func memEditTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "mem_edit",
			Description: "Edit a memory page by exact replacement: old → new. old must appear EXACTLY once in the page (add context to make it unique). To append, put the current end of the page in old and the extended version in new.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file": map[string]any{"type": "string", "description": "Page name"},
					"old":  map[string]any{"type": "string", "description": "Exact text to replace (unique in the page)"},
					"new":  map[string]any{"type": "string", "description": "Replacement text"},
				},
				"required": []string{"file", "old", "new"},
			},
		},
	}
}

func editTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "edit",
			Description: "Modify a file on disk by exact replacement: old → new. old must appear EXACTLY once in the file (add surrounding context to make it unique). Prefer this over rewriting the whole file — it avoids retyping everything.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file": map[string]any{"type": "string", "description": "Path of the file to modify"},
					"old":  map[string]any{"type": "string", "description": "Exact text to replace (unique in the file)"},
					"new":  map[string]any{"type": "string", "description": "Replacement text"},
				},
				"required": []string{"file", "old", "new"},
			},
		},
	}
}

func bashTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "bash",
			Description: "Execute a shell command (bash) on this machine and return stdout, stderr and the exit code. For inspecting the system, running scripts, reading/editing files, reading logs. Avoid destructive commands unless explicitly asked.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "The bash command"},
					"timeout": map[string]any{"type": "integer", "description": fmt.Sprintf("Timeout s (default %d, max %d)", toolDefaultTimeout, toolMaxTimeout)},
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
	// Internet = accès web actif (serveur Crawl4AI configuré + joignable) : ajoute
	// les outils web_search/web_open/web_read/web_grep. Requiert aussi Agent.
	Internet bool
	// Mem = mode d'accès à la mémoire persistante (off / ondemand / always),
	// indépendant du mode agent. Voir MemMode.
	Mem MemMode
}

// globalCaps reads the machine-wide config — the default when a request doesn't
// specify its own capabilities.
func globalCaps() Caps {
	// Internet inclut la joignabilité du serveur Crawl4AI : « actif ET fonctionnel ».
	// Ainsi le prompt système (chat_tools.go) et les outils fournis (EnabledTools) sont
	// gouvernés par la MÊME condition — sinon le prompt promet web_search alors que
	// l'outil n'existe pas, et le modèle le tape en bash (command not found).
	// Mémoire et accès internet sont des sous-réglages du mode agent (cf. UI web) :
	// sans agent, on ne fournit NI les outils mem_*, NI les outils web. Ça garde le
	// prompt système et les outils cohérents avec l'interface (blocs grisés quand
	// l'agent est off) — l'IA en chat pur répond sans mémoire ni web.
	agent := agentEnabled()
	if !agent {
		return Caps{Agent: false, Internet: false, Mem: MemOff}
	}
	return Caps{Agent: true, Internet: internetEnabled() && crawlReachable(), Mem: memMode()}
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
		tools = append(tools, bashTool(), editTool())
	}
	// Mémoire = axe indépendant du mode agent : les outils mem_* sont fournis dès
	// que le mode mémoire n'est pas « off » (que l'agent soit actif ou non).
	if caps.Mem != MemOff {
		tools = append(tools, memSearchTool(), memReadTool(), memAddTool(), memEditTool())
	}
	// Outils web : seulement si le mode agent ET l'accès internet sont actifs.
	// caps.Internet intègre déjà la joignabilité (globalCaps / override web_server.go),
	// donc prompt et outils restent cohérents — pas de web_search halluciné.
	if caps.Agent && caps.Internet {
		tools = append(tools, webSearchTool(), webOpenTool(), webReadTool(), webGrepTool())
	}
	// Outils MCP : serveurs tiers configurés par le propriétaire de la machine.
	// Comme bash, ils exécutent du code arbitraire côté hôte → réservés au mode
	// agent. La découverte est paresseuse et cachée (voir mcp_client.go).
	if caps.Agent {
		tools = append(tools, mcpTools()...)
	}
	return tools
}

// StreamEvent is what a ChatCallback receives for each piece of streamed output.
// Exactly one of {Content, Reasoning, ToolUsed, Stats, Err, DropReasoning} is set
// per call.
type StreamEvent struct {
	Content   string
	Reasoning string
	ToolUsed  *ToolUsedEvent
	Stats     *StatsEvent
	Err       error
	// DropReasoning demande à l'UI de retirer la dernière bulle de raisonnement :
	// le modèle a « pensé sans agir » et on relance le tour, ce raisonnement-là
	// est mort-né et ne doit pas rester à l'écran (sinon double raisonnement).
	DropReasoning bool
	// Compacting signale une compaction déclenchée EN COURS DE TOUR (boucle
	// d'outils) : true à l'entrée du résumé, false à la sortie. Même bannière que
	// la compaction proactive de début de tour, qui elle est émise directement par
	// Conversation.generate. nil = l'événement ne parle pas de compaction.
	Compacting *bool
	// NewHistory publie la vue modèle APRÈS une compaction faite en cours de tour.
	// Sans elle la compaction est perdue : l'appelant reconstruit l'historique en
	// ajoutant les messages du tour à sa copie d'AVANT compaction, donc le fil
	// complet revient et le tour suivant re-déborde aussitôt (43% → 92% en un
	// message). Cette liste contient DÉJÀ tout le tour en cours : l'appelant doit
	// REMPLACER son historique par elle (préfixe système injecté retiré), pas l'y
	// ajouter.
	NewHistory []Message
}
type ToolUsedEvent struct {
	Name   string
	Label  string // user-visible summary (skill name or the command)
	Result string // tool output (stdout/stderr/exit for run_shell, skill body for read_skill)
	Done   bool   // false = call announced (command only); true = result is ready
	Typing bool   // true = command still being written (partial), no spinner yet
	// Diff : lignes ajoutées/retirées quand l'outil a MODIFIÉ quelque chose
	// (edit, mem_add, mem_edit). L'UI les affiche en vert (+) et rouge (-).
	Diff []DiffLine
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
		PromptN         int     `json:"prompt_n"`
		PromptMs        float64 `json:"prompt_ms"`
		PromptPerSecond float64 `json:"prompt_per_second"`
		PredictedN      int     `json:"predicted_n"`
		PredictedMs     float64 `json:"predicted_ms"`
		PredictedPerSec float64 `json:"predicted_per_second"`
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
// friendlyLLMError transforme une erreur de transport vers llama-server en un
// message clair. Le cas le plus fréquent — « connection refused » (Linux) /
// « actively refused » (Windows) — arrive quand le moteur redémarre ou charge
// encore le modèle ; l'erreur brute (« dial tcp 127.0.0.1:8080… ») ne dit rien à
// l'utilisateur. On garde le silence sur une annulation volontaire (/stop).
func friendlyLLMError(err error) error {
	if err == nil {
		return nil
	}
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "context canceled"):
		return err // /stop : pas d'alarme
	case strings.Contains(low, "connection refused"), strings.Contains(low, "actively refused"), strings.Contains(low, "connectex"), strings.Contains(low, "no connection could be made"):
		return fmt.Errorf("⚠️ Le moteur (llama-server) ne répond pas sur le port %d. Il est probablement en train de démarrer ou de charger le modèle — réessaie dans quelques secondes.", LLMPort())
	case strings.Contains(low, "timeout"), strings.Contains(low, "deadline exceeded"):
		return fmt.Errorf("⚠️ Le moteur (llama-server) met trop de temps à répondre (port %d) — il est peut-être surchargé ou en plein chargement. Réessaie dans un instant.", LLMPort())
	case strings.Contains(low, "eof"), strings.Contains(low, "connection reset"):
		return fmt.Errorf("⚠️ Connexion au moteur (llama-server, port %d) interrompue — il a peut-être redémarré. Réessaie.", LLMPort())
	}
	return err
}

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
	// Filet réactif (façon Hermes) : si llama-server refuse le prompt (souvent un
	// dépassement de la fenêtre de contexte après de gros résultats d'outils), on
	// compacte l'historique en vol et on rejoue le tour — une seule fois.
	compactedRetry := false
	// Appels d'outil déjà exécutés (clé = nom + arguments bruts) : sert à ne pas
	// rejouer deux fois exactement la même écriture dans un même échange.
	doneCalls := map[string]string{}
	// Garde-fou « pensé sans agir » : certains modèles à reasoning planifient un
	// appel d'outil dans leur <think> puis émettent le token de fin SANS l'émettre
	// (ni réponse, ni tool_call). On relance alors UNE fois le tour avec un nudge
	// explicite au lieu d'afficher « pas de réponse ».
	nudged := false
	// Pas de plafond d'itérations ni d'anti-boucle : ils coupaient des tours
	// parfaitement légitimes (une recherche enchaîne facilement des dizaines
	// d'appels, parfois identiques). Le seul frein est le bouton stop, qui annule
	// le contexte — c'est un choix assumé.
	for iter := 0; ; iter++ {
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
			err = friendlyLLMError(err)
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
			// Le prompt a peut-être dépassé la fenêtre de contexte : on tente une
			// compaction en vol et on rejoue le tour (une seule fois) avant tout le
			// reste. C'est le filet de secours à la Hermes.
			if compactEnabled() && !compactedRetry {
				if c, changed := compactMessages(ctx, messages, caps); changed {
					compactedRetry = true
					messages = c
					logCompact("réactif", 0, messages, c, changed)
					// Même publication qu'en cours de tour : sans elle, la compaction de
					// secours ne survit pas à la fin du tour et le prompt re-déborde au
					// message suivant.
					extra = nil
					cb(StreamEvent{NewHistory: append([]Message(nil), messages...)})
					continue
				}
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
			// Timings ET usage (include_usage) arrivent sur le CHUNK FINAL qui, sur ce
			// build llama.cpp (MTP/spéculatif), a `choices:[]` — on les traite AVANT le
			// garde de choices, sinon `gen_tokens`/`gen_per_second` (decode) sont jetés
			// et l'UI retombe à « 0 tok/s » à la fin de la génération.
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
			if len(ch.Delta.ToolCalls) > 0 {
				// Un appel d'outil clôt le texte : on vide MAINTENANT le reliquat
				// retenu par la garde « </think> » (voir plus bas). Sinon il n'était
				// émis qu'en fin de flux, donc APRÈS l'événement d'outil, et l'UI
				// (qui coupe la bulle en cours à chaque tool_used) affichait la fin
				// de la phrase — souvent coupée en plein mot — dans une bulle
				// séparée sous l'outil.
				if thinkOpen && thinkTail.Len() > 0 {
					tail := thinkTail.String()
					thinkTail.Reset()
					if !cb(StreamEvent{Content: tail}) {
						aborted = true
						break
					}
				}
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
					switch cur.Function.Name {
					case "mem_search", "web_search":
						key = "query"
					case "mem_read", "mem_add", "mem_edit", "edit":
						key = "file"
					case "web_open", "web_read", "web_grep":
						key = "url"
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
					// The prompt opened a <think> block. Stream `content` LIVE as
					// the answer, holding back only a short tail that could be the
					// start of a literal "</think>". A reasoning-aware backend
					// (llama.cpp with --reasoning-format, Alice's fork) strips the
					// think tags server-side, so </think> never appears in content
					// and the whole answer streams straight through — including
					// when the model answers WITHOUT thinking (no reasoning_content,
					// no </think>), which is exactly what used to get dumped into
					// the reasoning bubble. A vanilla build that leaves the thinking
					// inline still gets carved at the </think> below.
					thinkTail.WriteString(ch.Delta.Content)
					s := thinkTail.String()
					if i := strings.Index(s, thinkClose); i >= 0 {
						// Vanilla inline think: reasoning before </think>, answer
						// after. Route the reasoning to its bubble, drop the tag.
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
						// No </think> yet: stream as content, holding back a tail
						// that could be a partial "</think>". Back the cut up to a
						// UTF-8 rune boundary so a multi-byte char (é, …) is never
						// split — otherwise the two halves decode as � (mojibake).
						cut := len(s) - (len(thinkClose) - 1)
						for cut > 0 && !utf8.RuneStart(s[cut]) {
							cut--
						}
						if cut > 0 {
							emit := s[:cut]
							thinkTail.Reset()
							thinkTail.WriteString(s[cut:])
							if !cb(StreamEvent{Content: emit}) {
								aborted = true
								break
							}
						}
					}
				}
			}
		}
		// Flush the held-back tail (never part of a </think>): it's answer text.
		if !aborted && thinkOpen && thinkTail.Len() > 0 {
			cb(StreamEvent{Content: strings.TrimLeft(thinkTail.String(), "\r\n")})
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
				// Les arguments DOIVENT être du JSON valide : ils sont rangés dans
				// l'historique vu par le modèle, et le template de chat de llama.cpp les
				// re-parse à CHAQUE requête suivante. Un modèle très quantifié peut sortir
				// des arguments vides OU tronqués/non-JSON (ex: `{"command":"python3 …`) ;
				// stockés tels quels, ils font échouer le parsing du template → 500 en
				// boucle jusqu'au reset. On neutralise tout ce qui n'est pas du JSON valide
				// en objet vide (l'appel a de toute façon déjà été exécuté). json.Valid("")
				// étant faux, ça couvre aussi le cas vide d'origine.
				if !json.Valid([]byte(tc.Function.Arguments)) {
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
			// 2. Execute each tool locally and append a "tool" reply.
			for _, tc := range tcs {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				// Derive the human label (command / skill name) up front so we can
				// announce the call BEFORE running it — otherwise the UI shows
				// nothing while a slow shell command runs and looks frozen.
				label := ""
				switch tc.Function.Name {
				case "mem_search", "web_search":
					label, _ = args["query"].(string)
				case "mem_read", "mem_add", "mem_edit", "edit":
					label, _ = args["file"].(string)
				case "bash":
					label, _ = args["command"].(string)
				case "web_open", "web_read":
					label, _ = args["url"].(string)
				case "web_grep":
					u, _ := args["url"].(string)
					p, _ := args["pattern"].(string)
					label = p + " @ " + u
				default:
					// Outils MCP : libellé = un aperçu compact des arguments.
					if isMCPTool(tc.Function.Name) {
						label = mcpArgLabel(args)
					}
				}
				cb(StreamEvent{ToolUsed: &ToolUsedEvent{Name: tc.Function.Name, Label: label}})

				result := ""
				// diff : rempli par les outils d'écriture (edit / mémoire) pour que
				// l'UI montre les lignes ajoutées et retirées.
				var diff []DiffLine
				// Appel rigoureusement identique déjà exécuté dans ce tour : on ne le
				// rejoue pas. Les petits modèles réémettent volontiers deux fois la
				// même écriture ; la rejouer produisait une fausse erreur (« old
				// introuvable », puisque le remplacement est déjà fait).
				callKey := tc.Function.Name + "\x00" + tc.Function.Arguments
				if prev, seen := doneCalls[callKey]; seen {
					result = prev + "\n[note] appel identique déjà exécuté dans ce tour — non rejoué"
					cb(StreamEvent{ToolUsed: &ToolUsedEvent{Name: tc.Function.Name, Label: label, Result: result, Done: true}})
					toolMsg := Message{Role: "tool", ToolCallID: tc.ID, Content: result}
					messages = append(messages, toolMsg)
					extra = append(extra, toolMsg)
					continue
				}
				switch tc.Function.Name {
				case "mem_search":
					lim := 0
					if v, ok := args["limit"].(float64); ok {
						lim = int(v)
					}
					hits := MemSearch(label, lim)
					if len(hits) == 0 {
						result = "[aucun résultat]"
					} else {
						var b strings.Builder
						for _, h := range hits {
							fmt.Fprintf(&b, "- %s — %s\n  %s\n", h.File, h.Title, h.Snippet)
						}
						result = strings.TrimRight(b.String(), "\n")
					}
				case "mem_read":
					off, lim := 0, 0
					if v, ok := args["offset"].(float64); ok {
						off = int(v)
					}
					if v, ok := args["limit"].(float64); ok {
						lim = int(v)
					}
					if c, rerr := MemRead(label, off, lim); rerr != nil {
						result = "[erreur] " + rerr.Error()
					} else {
						result = c
					}
				case "mem_add":
					content, _ := args["content"].(string)
					if werr := MemAdd(label, content); werr != nil {
						result = "[erreur] " + werr.Error()
					} else {
						result = fmt.Sprintf("[ok] page '%s' créée", label)
						diff = addedDiff(content)
					}
				case "mem_edit":
					oldText, _ := args["old"].(string)
					newText, _ := args["new"].(string)
					if werr := MemEdit(label, oldText, newText); errors.Is(werr, errAlreadyApplied) {
						result = fmt.Sprintf("[ok] page '%s' %s", label, werr.Error())
					} else if werr != nil {
						result = "[erreur] " + werr.Error()
					} else {
						result = fmt.Sprintf("[ok] page '%s' modifiée", label)
						diff = lineDiff(oldText, newText)
					}
				case "edit":
					oldText, _ := args["old"].(string)
					newText, _ := args["new"].(string)
					result = fileEdit(label, oldText, newText)
					// Diff seulement si l'édition a réussi (sinon le fichier n'a pas bougé).
					if !strings.HasPrefix(result, "[erreur]") {
						diff = lineDiff(oldText, newText)
					}
				case "bash":
					to := 0
					switch v := args["timeout"].(type) {
					case float64:
						to = int(v)
					case int:
						to = v
					}
					result = runShell(label, to)
				case "web_search":
					result = toolWebSearch(args)
				case "web_open":
					result = toolWebOpen(args)
				case "web_read":
					result = toolWebRead(args)
				case "web_grep":
					result = toolWebGrep(args)
				default:
					if isMCPTool(tc.Function.Name) {
						result = mcpCall(tc.Function.Name, args)
					} else {
						result = "[erreur] outil inconnu: " + tc.Function.Name
					}
				}
				if !strings.HasPrefix(result, "[erreur]") {
					doneCalls[callKey] = result
				}
				shown := result
				if r := []rune(shown); len(r) > 4000 {
					shown = string(r[:4000]) + "\n…[tronqué]"
				}
				cb(StreamEvent{ToolUsed: &ToolUsedEvent{Name: tc.Function.Name, Label: label, Result: shown, Done: true, Diff: diff}})
				toolMsg := Message{Role: "tool", ToolCallID: tc.ID, Content: result}
				messages = append(messages, toolMsg)
				extra = append(extra, toolMsg)
			}
			// Compaction EN COURS DE TOUR. Le seuil n'était testé qu'AU DÉBUT du tour :
			// une boucle d'outils peut à elle seule remplir la fenêtre (résultats
			// enchaînés), on partait à 60% et on finissait en dépassement — rattrapé au
			// mieux par le filet réactif sur 500, une seule fois. On re-teste donc ici,
			// avec le contexte RÉEL du dernier appel (usage.prompt_tokens + généré).
			if used := stats.PromptTokensTotal + stats.GenTokens; compactWouldTrigger(messages, used) {
				yes, no := true, false
				cb(StreamEvent{Compacting: &yes})
				c, changed := compactMessages(ctx, messages, caps)
				cb(StreamEvent{Compacting: &no})
				logCompact("en-tour", used, messages, c, changed)
				if changed {
					messages = c
					// La nouvelle base contient déjà tout ce tour : on la publie et on
					// repart d'un `extra` vide, sinon l'appelant la ré-empilerait avec
					// les messages du tour et dupliquerait tout.
					extra = nil
					cb(StreamEvent{NewHistory: append([]Message(nil), messages...)})
				}
			}
			continue
		}
		// Normal end of turn. If the model produced no visible answer at all
		// (empty content, e.g. it stopped right after a tool result), say so
		// instead of leaving the user staring at a silent, finished chat.
		if strings.TrimSpace(assistantContent.String()) == "" {
			// Filet de sécurité (le vrai fix est le prompt court, voir baseSystemPrompt) :
			// si un modèle « pense sans agir » malgré tout, on le relance UNE fois avec
			// une consigne impérative au lieu d'afficher « pas de réponse ».
			if len(tools) > 0 && !disableTools && !nudged {
				nudged = true
				// Le raisonnement de ce tour avorté ne mène à rien : on demande à
				// l'UI de l'effacer avant de relancer, pour ne pas afficher deux
				// blocs de réflexion successifs.
				cb(StreamEvent{DropReasoning: true})
				messages = append(messages, Message{
					Role:    "user",
					Content: "You reasoned but did not call a tool or answer. Act NOW: call the appropriate tool directly (e.g. mem_search/mem_read/bash), or give your final answer if you already have the info. Don't explain, act.",
				})
				continue
			}
			cb(StreamEvent{Content: "_(le modèle n'a pas produit de réponse — finish: " + finishReason + ")_"})
		}
		return extra, nil
	}
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
