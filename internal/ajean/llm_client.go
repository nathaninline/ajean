package ajean

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
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
			Description: "Search your memory for pages matching a query.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Keywords"},
					"limit": map[string]any{"type": "integer", "description": "Default 8, max 30"},
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
			Description: "Read the content of a memory page.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file":   map[string]any{"type": "string", "description": "Page name"},
					"offset": map[string]any{"type": "integer", "description": "Start line (default 1)"},
					"limit":  map[string]any{"type": "integer", "description": "Lines (max 500)"},
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
			Description: "Create a new memory page.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file":    map[string]any{"type": "string", "description": "Kebab-case page name"},
					"content": map[string]any{"type": "string", "description": "Markdown, first line = title #"},
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
			Description: "Replace an exact, unique snippet of text in a memory page.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file": map[string]any{"type": "string", "description": "Page name"},
					"old":  map[string]any{"type": "string", "description": "Exact text to replace (unique)"},
					"new":  map[string]any{"type": "string", "description": "Replacement"},
				},
				"required": []string{"file", "old", "new"},
			},
		},
	}
}

func seeImageTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "see_image",
			Description: "Load an image file from disk so you can actually see it (png, jpg, gif, webp, bmp).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file": map[string]any{"type": "string", "description": "Path to the image (relative to your working folder unless absolute)"},
				},
				"required": []string{"file"},
			},
		},
	}
}

func memDeleteTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "mem_delete",
			Description: "Delete a memory page.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file": map[string]any{"type": "string", "description": "Page name"},
				},
				"required": []string{"file"},
			},
		},
	}
}

// trackerTool : UN seul outil pour les données datées qui s'accumulent (compteurs,
// relevés, journaux). Piloté par `action`. La navigation est hiérarchique et vit
// dans les RÉPONSES (vue d'ensemble → année → mois → événements), jamais tout d'un
// coup — voir tracker.go.
func trackerTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "tracker",
			Description: "Dated data that piles up (counters, readings, follow-up logs). No args: overview. name: open a tracker. name+when (\"2026\", \"2026-07\"): zoom in. action add appends a dated point (when omitted=now, creates tracker if new); edit/delete act on one point by id. Navigates by level, never dumps all.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "enum": []string{"view", "add", "edit", "delete"}, "description": "Default view."},
					"name":   map[string]any{"type": "string", "description": "Tracker name; omit for overview."},
					"when":   map[string]any{"type": "string", "description": "view: slice to zoom (2026, 2026-07). add/edit: point date, omit=now."},
					"text":   map[string]any{"type": "string", "description": "Point value/note (add/edit)."},
					"id":     map[string]any{"type": "string", "description": "Point id (edit/delete), shown in brackets."},
				},
			},
		},
	}
}

func editTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "edit",
			Description: "Replace an exact, unique snippet of text in a file.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file": map[string]any{"type": "string", "description": "Path. Relative names resolve inside your working folder; pass an absolute path only for a file the user explicitly located there."},
					"old":  map[string]any{"type": "string", "description": "Exact text to replace (unique)"},
					"new":  map[string]any{"type": "string", "description": "Replacement"},
				},
				"required": []string{"file", "old", "new"},
			},
		},
	}
}

func writeTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "write",
			Description: "Create or overwrite a file with exact content — always use this to write files, never shell redirection.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file":    map[string]any{"type": "string", "description": "Path. A relative name lands in your working folder — the default for anything you create. Absolute path only if the user named a location; never into system dirs (/usr, /bin, /etc, /usr/local/bin)."},
					"content": map[string]any{"type": "string", "description": "Full content"},
				},
				"required": []string{"file", "content"},
			},
		},
	}
}

func bashTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "bash",
			Description: "Run a " + agentTargetShellName() + " command and return its stdout, stderr, and exit code.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "The command"},
					"timeout": map[string]any{"type": "integer", "description": fmt.Sprintf("Timeout s (default %d, max %d)", toolDefaultTimeout, toolMaxTimeout)},
				},
				"required": []string{"command"},
			},
		},
	}
}

// machinesListTool advertises the read-only view of the paired postes (other
// machines) and the current execution target. No arguments.
func machinesListTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "machines_list",
			Description: "List the paired postes (other machines you can operate on) with their connection state, and show which one is your current execution target.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

// machinesUseTool switches which machine the agent operates on: a poste slug, or
// "local" to come back to this server. Same target the user picks by hand in the
// Postes distants panel.
func machinesUseTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "machines_use",
			Description: "Switch the machine you operate on: your bash/write/edit tools then run on it. Pass a poste slug (from machines_list), or \"local\" to operate on this server.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"machine": map[string]any{"type": "string", "description": "Poste slug to switch to, or \"local\" for this server."},
				},
				"required": []string{"machine"},
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
	if mm := machineMgmtSystemPrompt(caps); mm != "" {
		parts = append(parts, mm)
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

// normalizeSystemMessages garantit un UNIQUE message system, en tête de séquence.
// Certains gabarits de chat (Qwen3.x notamment) rejettent tout message system
// ailleurs qu'en position 0, ou en plusieurs exemplaires, avec « System message
// must be at the beginning » (500 côté llama-server, issue #26). Or la boucle
// d'agent peut en insérer un plus loin : la relance « n'appelle plus d'outil »
// (voir plus bas) l'ajoute en fin de séquence, et le résumé de compaction
// (chat_compact.go) en insère un aussi. On fusionne donc tous les system en un
// seul, placé au début, juste avant l'envoi.
//
// La séquence renvoyée est une COPIE : l'historique d'origine garde sa forme pour
// l'affichage, la persistance et la compaction. Un modèle qui accepte le system
// n'importe où n'est pas gêné par un system en tête, donc la normalisation est
// sûre pour tous.
func normalizeSystemMessages(msgs []Message) []Message {
	var sys []string
	sawSystem := false
	rest := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "system" {
			if s, ok := m.Content.(string); ok {
				sawSystem = true
				if strings.TrimSpace(s) != "" {
					sys = append(sys, s)
				}
				continue
			}
			// Contenu system non textuel (cas théorique) : on le préserve tel quel
			// plutôt que de le perdre.
		}
		rest = append(rest, m)
	}
	// Aucun message system du tout : rien à réordonner, on rend l'entrée telle quelle.
	if !sawSystem {
		return msgs
	}
	// Des system existaient mais tous vides : on les a retirés (un system vide en
	// position non-nulle casserait tout autant), sans en réinsérer.
	if len(sys) == 0 {
		return rest
	}
	out := make([]Message, 0, len(rest)+1)
	out = append(out, Message{Role: "system", Content: strings.Join(sys, "\n\n")})
	return append(out, rest...)
}

// EnabledTools returns the tools to advertise on the next inference call.
func EnabledTools(caps Caps) []Tool {
	tools := []Tool{}
	if caps.Agent {
		tools = append(tools, bashTool(), writeTool(), editTool())
		// Tâches planifiées : l'IA peut se donner elle-même des rappels/veilles
		// récurrents et les gérer. Réservé au mode agent (une tâche sans outils
		// pour agir n'aurait rien à livrer).
		tools = append(tools, taskListTool(), taskCreateTool(), taskUpdateTool(), taskDeleteTool())
		// Mémoire longue de la conversation : rappeler un bloc archivé au compactage
		// (recall) ou le retrouver par mots-clés (recall_search). Réservé au mode
		// agent — le compactage n'injecte les ids rappelables que là (voir
		// compactSummaryUserMsg). Voir chat_recall.go.
		tools = append(tools, recallTool(), recallSearchTool())
		// Voir une image du disque : seulement quand la vision est réellement active
		// (projecteur MMPROJ). Sinon l'outil ne pourrait que renvoyer une erreur, et
		// l'annoncer ferait croire au modèle qu'il a des yeux qu'il n'a pas.
		if visionEnabled() {
			tools = append(tools, seeImageTool())
		}
		// Gestion des machines (postes) : donnée seulement quand la capacité est
		// activée. machines_list voit les postes, machines_use bascule la cible.
		// L'ajout d'un poste, Jean le fait avec bash (ssh + ajean remote install).
		if machinesEnabled() {
			tools = append(tools, machinesListTool(), machinesUseTool())
		}
	}
	// Mémoire = axe indépendant du mode agent : les outils mem_* sont fournis dès
	// que le mode mémoire n'est pas « off » (que l'agent soit actif ou non).
	if caps.Mem != MemOff {
		tools = append(tools, memSearchTool(), memReadTool(), memAddTool(), memEditTool(), memDeleteTool())
		// Trackers (3ᵉ type de mémoire : données datées qui s'accumulent). Même axe que
		// la mémoire (persistant, par projet), donc fourni dès que la mémoire est ON.
		tools = append(tools, trackerTool())
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
	// Postes distants : PAS de nouveaux outils. L'IA garde bash/write/edit ; c'est
	// leur CIBLE D'EXÉCUTION qui change quand un poste est sélectionné (voir le
	// routage dans la boucle d'outils et agentTargetSlug). Redonner des outils que
	// le modèle a déjà (node__…__shell alors qu'il a bash) doublonnait le catalogue.
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
	// Body : contenu en cours d'écriture par un outil d'écriture (write/edit,
	// mem_add/mem_edit), diffusé ligne à ligne pendant que le modèle le tape,
	// pour que la bulle se remplisse en direct au lieu de rester figée puis de
	// s'ouvrir d'un coup. Transitoire : seul Diff (état final) est rejoué.
	Body string
	// Diff : lignes ajoutées/retirées quand l'outil a MODIFIÉ quelque chose
	// (edit, mem_add, mem_edit). L'UI les affiche en vert (+) et rouge (-).
	Diff []DiffLine
	// ArgToks : nombre CUMULÉ de tokens que le modèle a produits pour écrire les
	// ARGUMENTS de cet appel (le code d'un write/edit, la commande d'un bash…). Ces
	// tokens sont générés par le modèle au même titre que le raisonnement ou la
	// réponse, mais n'étaient comptés nulle part dans le compteur du bas. On envoie
	// le CUMUL (pas un incrément) pour que le replay — qui ne garde que l'événement
	// final de chaque outil — retrouve le total exact. Le client fait la différence
	// par bulle (voir handleDelta). 0 = rien à compter.
	ArgToks int
}

// shownDisplayMax borne ce qu'un résultat d'outil occupe dans le FLUX vers l'UI.
// Aligné sur le plus haut plafond côté modèle (mcpMaxOutput = 12000 ; shell et
// web = 8000) : le modèle et l'UI voient donc la même chose, et l'étiquette
// « ~N tok » de la bulle dit la VRAIE taille du résultat.
//
// Avant, cette borne était à 4000 : toute page web un peu longue s'affichait
// « ~1004 tok » — la valeur du plafond, pas celle de la page. Le compteur
// mentait, et il mentait toujours avec le même chiffre.
const shownDisplayMax = 12000

// shownResult prépare un résultat d'outil pour l'affichage.
func shownResult(s string) string {
	if r := []rune(s); len(r) > shownDisplayMax {
		return string(r[:shownDisplayMax]) + "\n…[tronqué]"
	}
	return s
}

// dedupableTool indique si un appel RIGOUREUSEMENT identique (même outil, mêmes
// arguments) doit être court-circuité au lieu d'être rejoué. Vrai pour les outils
// où un ré-appel identique n'a pas de sens ou produit une fausse erreur (rejouer
// une écriture déjà appliquée → « old introuvable »).
//
// FAUX pour bash : relancer la MÊME commande est un usage courant et légitime —
// l'IA crée un fichier, le lance, le modifie, puis le RELANCE avec la même ligne ;
// le résultat change parce que le fichier a changé. La dédup la bloquait par un
// « [déjà fait] » exaspérant. bash a des effets de bord : on le laisse toujours
// s'exécuter.
//
// FAUX aussi pour machines_use / machines_list : changer de machine est un
// changement d'ÉTAT, pas une lecture idempotente. Rebasculer sur une machine déjà
// visitée (« local » → poste → « local ») réémet le MÊME appel : la dédup le
// bloquait, et l'IA se retrouvait coincée sur une cible, incapable d'y revenir.
func dedupableTool(name string) bool {
	return name != "bash" && name != "machines_use" && name != "machines_list"
}

// repeatedCallResult construit ce qu'on renvoie quand le modèle redemande un
// appel RIGOUREUSEMENT identique (même outil, mêmes arguments) dans le même tour.
// L'appel n'est jamais rejoué — on répond depuis le résultat mémorisé.
//
// Le plafond d'itérations et l'anti-boucle ont été retirés en v0.6.3 parce qu'ils
// coupaient des recherches légitimes ; il ne restait donc plus RIEN pour arrêter
// un modèle qui redemande dix fois la même page. Et le mécanisme se retournait
// contre lui-même : la note « déjà exécuté » était collée APRÈS le contenu, donc
// noyée en fin d'un résultat de plusieurs milliers de caractères — le modèle
// voyait le contenu, pas l'avertissement, et recommençait.
//
// D'où l'escalade : la note passe EN TÊTE, et à partir de la 2ᵉ redemande on ne
// renvoie plus la charge utile du tout. Le tour n'est pas coupé (le modèle garde
// la main), mais redemander la même chose ne rapporte plus rien — ni contenu, ni
// contexte consommé.
func repeatedCallResult(prev string, repeats int) string {
	if repeats >= 2 {
		return "[déjà fait] Cet appel exact a déjà été exécuté " + strconv.Itoa(repeats) +
			" fois dans ce tour ; son résultat est plus haut dans la conversation. " +
			"Ne le redemande plus : réponds avec ce que tu as, ou change d'approche " +
			"(autre URL, autres arguments, web_grep pour cibler)."
	}
	return "[déjà fait] Appel identique déjà exécuté dans ce tour — non rejoué. " +
		"Voici à nouveau son résultat ; ne le redemande pas une troisième fois.\n\n" + prev
}

// writeBodyKey returns the argument holding the text an écriture tool is about
// to commit — the part worth showing live in the bubble — or "" for tools that
// have no such body (bash, lectures, recherches).
func writeBodyKey(tool string) string {
	switch tool {
	case "write", "mem_add":
		return "content"
	case "edit", "mem_edit":
		return "new"
	}
	return ""
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
// On classe d'abord sur les erreurs TYPÉES (errors.Is / net.Error) : la
// reconnaissance par sous-chaîne dépend de la langue et du format des messages
// de l'OS, et « connexion refusée » d'un Windows français ne ressemble à aucun
// des motifs anglais. Les sous-chaînes restent en second rideau, pour les
// erreurs enveloppées par une bibliothèque qui perd le type d'origine.
func friendlyLLMError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err // /stop : pas d'alarme
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return errEngineDown()
	case errors.Is(err, context.DeadlineExceeded), isNetTimeout(err):
		return fmt.Errorf("⚠️ Le moteur (llama-server) met trop de temps à répondre (port %d) — il est peut-être surchargé ou en plein chargement. Réessaie dans un instant.", LLMPort())
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET):
		return errEngineReset()
	}
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "context canceled"):
		return err
	case strings.Contains(low, "connection refused"), strings.Contains(low, "actively refused"), strings.Contains(low, "connectex"), strings.Contains(low, "no connection could be made"):
		return errEngineDown()
	case strings.Contains(low, "timeout"), strings.Contains(low, "deadline exceeded"):
		return fmt.Errorf("⚠️ Le moteur (llama-server) met trop de temps à répondre (port %d) — il est peut-être surchargé ou en plein chargement. Réessaie dans un instant.", LLMPort())
	case strings.Contains(low, "eof"), strings.Contains(low, "connection reset"):
		return errEngineReset()
	}
	return err
}

func errEngineDown() error {
	return fmt.Errorf("⚠️ Le moteur (llama-server) ne répond pas sur le port %d. Il est probablement en train de démarrer ou de charger le modèle — réessaie dans quelques secondes.", LLMPort())
}

func errEngineReset() error {
	return fmt.Errorf("⚠️ Connexion au moteur (llama-server, port %d) interrompue — il a peut-être redémarré. Réessaie.", LLMPort())
}

// streamCutError explique un flux de complétion coupé en cours de route. Cas à
// part de friendlyLLMError : ici la requête avait ABOUTI (200 reçu, tokens déjà
// reçus), c'est la lecture qui a lâché. Le dire autrement qu'un « le moteur ne
// répond pas » évite d'envoyer l'utilisateur vérifier un moteur qui va bien.
func streamCutError(err error) error {
	if errors.Is(err, bufio.ErrTooLong) {
		return fmt.Errorf("⚠️ Réponse du moteur illisible : une ligne du flux dépasse la taille maximale (%d Mio). C'est presque toujours un appel d'outil démesuré (écriture d'un très gros fichier). Le tour est abandonné pour ne pas exécuter un appel tronqué.", 8)
	}
	return fmt.Errorf("⚠️ Le flux de réponse du moteur (llama-server, port %d) a été coupé en cours de route : %v. La réponse est incomplète et le tour est abandonné — réessaie.", LLMPort(), err)
}

// isNetTimeout : une erreur réseau qui se déclare elle-même comme un délai
// dépassé (net.Error.Timeout), quel que soit son libellé.
func isNetTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// repetitionGuard détecte un modèle qui rabâche la même phrase dans son
// raisonnement au lieu d'avancer — signe empirique de blocage, pas une simple
// longue réflexion. Constaté sur ce projet en analysant deux blocages réels
// (déchiffrage d'un atlas de police à partir d'un dump de pixels, tâche que le
// modèle ne pouvait pas vraiment résoudre en texte seul) : dans les deux cas,
// UNE phrase quasi identique revenait 82 puis 132 fois d'affilée, jamais
// reformulée différemment — un match texte exact après normalisation suffit,
// pas besoin de similarité floue. Portée : une seule complétion HTTP (voir son
// utilisation dans runChat, réinitialisé à chaque itération) — les deux cas
// réels tenaient chacun dans un seul appel de génération, jamais étalés sur
// plusieurs tours d'outil.
type repetitionGuard struct {
	buf  strings.Builder
	seen map[string]int
}

// repetitionGuardThreshold : nombre de répétitions EXACTES avant de couper.
// Sur les deux cas réels le motif était déjà installé dès la 2e/3e occurrence
// — attendre plus ne fait que gâcher des tokens (ces cas sont allés jusqu'à 82
// et 132 répétitions avant de s'arrêter tout seuls).
const repetitionGuardThreshold = 3

// repetitionGuardMinLen : ignore les phrases courtes (« Let me check. », « OK,
// next. ») qui reviennent légitimement sans être un signe de blocage — seules
// les phrases assez longues pour porter une vraie idée comptent.
const repetitionGuardMinLen = 40

// feed avale un morceau de texte (delta de streaming) et détecte, phrase par
// phrase (coupée sur . ! ? ou saut de ligne), une répétition. looping=true dès
// que la MÊME phrase normalisée atteint repetitionGuardThreshold occurrences ;
// sentence porte alors cette phrase (pour l'expliquer au modèle ensuite).
func (g *repetitionGuard) feed(chunk string) (looping bool, sentence string) {
	if g.seen == nil {
		g.seen = map[string]int{}
	}
	g.buf.WriteString(chunk)
	for {
		s := g.buf.String()
		idx := strings.IndexAny(s, ".!?\n")
		if idx < 0 {
			break
		}
		raw := s[:idx+1]
		g.buf.Reset()
		g.buf.WriteString(s[idx+1:])
		norm := strings.Join(strings.Fields(strings.ToLower(raw)), " ")
		if len(norm) < repetitionGuardMinLen {
			continue
		}
		g.seen[norm]++
		if g.seen[norm] >= repetitionGuardThreshold {
			return true, strings.TrimSpace(raw)
		}
	}
	return false, ""
}

// truncateRunes coupe s à n runes (jamais en plein milieu d'un caractère
// multi-octets), avec une ellipse si tronqué.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
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
	// Nombre de fois où le modèle a REDEMANDÉ un appel déjà exécuté. Sert à durcir
	// la réponse progressivement (voir repeatedCallResult) : rendre le contenu une
	// fois, puis refuser en le renvoyant vers ce qu'il a déjà.
	repeatCount := map[string]int{}
	// Garde-fou « le modèle est coincé » : deux signes distincts partagent le
	// même compteur/plafond, ce sont deux symptômes du même problème. (1) pensé
	// sans agir : certains modèles à reasoning planifient un appel d'outil dans
	// leur <think> puis émettent le token de fin SANS l'émettre (ni réponse, ni
	// tool_call) — parfois après avoir reformulé le même plan plusieurs fois sans
	// jamais l'exécuter (observé : ~15 000 tokens de raisonnement en boucle sur
	// une tâche complexe, zéro contenu visible). (2) répétition mécanique
	// détectée EN COURS de flux par repetitionGuard (voir son commentaire et son
	// utilisation plus bas) : deux blocages réels analysés montraient une phrase
	// quasi identique revenant 82 puis 132 fois — (1) seul ne les aurait jamais
	// interrompus puisque le modèle finit par émettre un finish_reason normal
	// après avoir tout dit, juste beaucoup trop tard. Dans les deux cas on
	// relance le tour avec un nudge explicite au lieu d'afficher tout de suite
	// « pas de réponse » — maxNudges fois, pas plus : un modèle vraiment bloqué
	// doit finir par rendre la main plutôt que de faire attendre indéfiniment
	// (voir le message affiché après le dernier essai).
	const maxNudges = 2
	nudgeCount := 0
	// Pas de plafond d'itérations ni d'anti-boucle : ils coupaient des tours
	// parfaitement légitimes (une recherche enchaîne facilement des dizaines
	// d'appels, parfois identiques). Le seul frein est le bouton stop, qui annule
	// le contexte — c'est un choix assumé.
	for iter := 0; ; iter++ {
		payload := map[string]any{
			"model": "ajean",
			// Normalisé juste avant l'envoi : un seul system, en tête. Les gabarits
			// stricts (Qwen3.x) refusent un system ailleurs qu'en position 0 (issue #26).
			"messages":    normalizeSystemMessages(messages),
			"stream":      true,
			"temperature": temperature,
			// include_usage → chunk final avec `usage.prompt_tokens` = taille TOTALE
			// du prompt (préfixe caché compris), contrairement à timings.prompt_n qui
			// ne compte que les tokens nouvellement traités. Sert au comptage exact du
			// contexte (sinon le system prompt déjà en cache n'est pas recompté).
			"stream_options": map[string]any{"include_usage": true},
		}
		// Paramètres d'échantillonnage du preset (top_p, top_k, min_p, présence…,
		// et TEMP qui écrase la température par défaut si le preset la fixe). Appliqués
		// à chaque itération : une bascule de preset en cours de tour est ainsi prise
		// en compte, comme le reste de la config relue par ReadConfig().
		applySampling(payload)
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
					// ⚠️ Journaliser AVANT d'installer le résultat : l'ancien ordre
					// passait `messages` déjà remplacé comme état « avant », donc la
					// ligne comparait le résultat à lui-même et n'apprenait rien.
					logCompact("réactif", 0, messages, c, changed)
					messages = c
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
		lastPreview := ""   // last command preview emitted (to stream the typing)
		lastBodyLines := -1 // lignes déjà diffusées du corps en cours d'écriture
		// argToks : tokens produits pour écrire les ARGUMENTS d'outil dans cette
		// complétion (≈ 1 par chunk de flux portant du texte d'arguments). Envoyé en
		// CUMUL sur les événements tool_used pour alimenter le compteur du bas.
		// argToksFlushed : n'attacher le cumul qu'UNE fois au done (cas de plusieurs
		// appels dans une même complétion), pour ne pas le compter en double.
		argToks := 0
		argToksFlushed := false
		// Renvoie le cumul d'argToks au PREMIER appel (le done du premier outil de la
		// complétion), puis 0 : le client compte par bulle, une double attache le
		// gonflerait. Le typing porte déjà le cumul en direct ; ce flush garantit le
		// total exact au replay, où seul le done survit à la coalescence.
		flushArgToks := func() int {
			if argToksFlushed {
				return 0
			}
			argToksFlushed = true
			return argToks
		}
		// Per-completion reasoning-split state (see reasoningOn comment above).
		sawReasoningField := false
		thinkOpen := reasoningOn
		var thinkTail strings.Builder
		// Anti-boucle : réinitialisé à CHAQUE complétion (voir repetitionGuard) —
		// une répétition détectée ici coupe la lecture du flux plus bas.
		var loopGuard repetitionGuard
		loopDetected := false
		loopSentence := ""
		// Scanner à gros tampon : un chunk peut porter un gros JSON d'arguments
		// (écriture de fichier). 8 Mio et non 1 : au-delà du tampon, le scanner
		// s'arrête sur « token too long » AU MILIEU du flux, et jusqu'à la 0.8.4
		// personne ne le voyait (voir sc.Err() plus bas).
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
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
					// Un chunk de flux portant du texte d'arguments ≈ 1 token généré.
					if tc.Function.Arguments != "" {
						argToks++
					}
				}
				// Stream the command being typed: extract the partial value and
				// emit it whenever it grows, so the UI shows it appear live.
				if cur := toolCalls[0]; cur != nil {
					key := "command"
					switch cur.Function.Name {
					case "mem_search", "web_search":
						key = "query"
					case "mem_read", "mem_add", "mem_edit", "mem_delete", "edit", "write", "see_image":
						key = "file"
					case "web_open", "web_read", "web_grep":
						key = "url"
					case "task_create", "task_update", "tracker":
						key = "name"
					case "task_delete", "recall":
						key = "id"
					case "recall_search":
						key = "query"
					case "machines_use":
						key = "machine"
					case "task_list", "machines_list":
						key = ""
					}
					p := previewArg(cur.Function.Arguments, key)
					// Corps en cours de frappe pour les outils d'écriture : on le diffuse
					// à la LIGNE, pas au token. Un événement par token republierait tout le
					// contenu à chaque fois (coût quadratique, et c'est ce flot qui saturait
					// le rendu mobile) ; à la ligne, le nombre d'événements est celui du
					// fichier et l'animation reste fluide.
					body := ""
					if bk := writeBodyKey(cur.Function.Name); bk != "" {
						body = previewArg(cur.Function.Arguments, bk)
					}
					grew := body != "" && strings.Count(body, "\n") > lastBodyLines
					if (p != "" && p != lastPreview) || grew {
						if p != "" {
							lastPreview = p
						}
						if grew {
							lastBodyLines = strings.Count(body, "\n")
						}
						if !cb(StreamEvent{ToolUsed: &ToolUsedEvent{Name: cur.Function.Name, Label: lastPreview, Body: body, Typing: true, ArgToks: argToks}}) {
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
				if looping, sentence := loopGuard.feed(ch.Delta.ReasoningContent); looping {
					loopDetected = true
					loopSentence = sentence
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
					// (llama.cpp with --reasoning-format, Nathan's fork) strips the
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
		// ⚠️ Le flux a-t-il fini, ou CASSÉ ? sc.Scan() renvoie false dans les deux
		// cas, et l'erreur n'était jamais consultée : une lecture coupée en plein
		// milieu (connexion réinitialisée, ligne plus longue que le tampon) était
		// donc indiscernable d'une fin normale. Vécu par l'utilisateur : l'agent
		// enchaîne quelques commandes puis « s'arrête et rend la main sans avoir
		// terminé, ni même commenté », pendant que le journal de llama-server
		// affiche un « stop processing » parfaitement normal (issue #19). Pire,
		// des appels d'outils accumulés à moitié auraient été EXÉCUTÉS avec des
		// arguments tronqués. On refuse donc le tour, en le disant.
		scanErr := sc.Err()
		resp.Body.Close()
		if aborted {
			return extra, nil
		}
		if scanErr != nil && ctx.Err() == nil {
			err := streamCutError(scanErr)
			cb(StreamEvent{Err: err})
			return extra, err
		}

		// Le modèle rabâchait la même phrase (voir repetitionGuard) : on a coupé la
		// lecture du flux tôt (dès la 3e répétition, pas après des dizaines) plutôt
		// que d'attendre une fin de tour normale qui n'allait pas venir. Les appels
		// d'outils accumulés jusque-là (s'il y en a) sont abandonnés avec le reste :
		// une répétition en cours de <think> n'a rien produit d'exploitable.
		if loopDetected {
			if nudgeCount < maxNudges {
				nudgeCount++
				cb(StreamEvent{DropReasoning: true})
				messages = append(messages, Message{
					Role: "user",
					Content: "You've repeated the same point several times in a row without making progress (\"" + truncateRunes(loopSentence, 180) + "\") — that's a sign this approach is stuck. Drop it: try something concretely different (a different method, tool, or angle), or if you already know enough, give your answer now. Don't repeat that reasoning again.",
				})
				continue
			}
			cb(StreamEvent{Content: "_(le modèle tournait en boucle sur la même idée — arrêté après " + strconv.Itoa(repetitionGuardThreshold) + " répétitions)_"})
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
				// Arrêt demandé : on n'enchaîne pas les outils restants. Sans ce
				// garde, un stop pendant une série d'appels laissait défiler toute
				// la série avant de reprendre la main.
				if ctx.Err() != nil {
					return extra, nil
				}
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				// Derive the human label (command / skill name) up front so we can
				// announce the call BEFORE running it — otherwise the UI shows
				// nothing while a slow shell command runs and looks frozen.
				label := ""
				switch tc.Function.Name {
				case "mem_search", "web_search", "recall_search":
					label, _ = args["query"].(string)
				case "mem_read", "mem_add", "mem_edit", "mem_delete", "edit", "write", "see_image":
					label, _ = args["file"].(string)
				case "recall":
					label, _ = args["id"].(string)
				case "bash":
					label, _ = args["command"].(string)
				case "web_open", "web_read":
					label, _ = args["url"].(string)
				case "web_grep":
					u, _ := args["url"].(string)
					p, _ := args["pattern"].(string)
					label = p + " @ " + u
				case "task_create", "task_update":
					label, _ = args["name"].(string)
				case "task_delete":
					label, _ = args["id"].(string)
				case "machines_use":
					label, _ = args["machine"].(string)
				case "tracker":
					act, _ := args["action"].(string)
					nm, _ := args["name"].(string)
					wh, _ := args["when"].(string)
					label = strings.TrimSpace(strings.TrimSpace(act+" "+nm) + " " + wh)
					if label == "" {
						label = "vue d'ensemble"
					}
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
				var visionImg map[string]any // partie image_url (see_image), réinjectée après le résultat
				// Appel rigoureusement identique déjà exécuté dans ce tour : on ne le
				// rejoue pas. Les petits modèles réémettent volontiers deux fois la
				// même écriture ; la rejouer produisait une fausse erreur (« old
				// introuvable », puisque le remplacement est déjà fait).
				callKey := tc.Function.Name + "\x00" + tc.Function.Arguments
				if prev, seen := doneCalls[callKey]; seen && dedupableTool(tc.Function.Name) {
					repeatCount[callKey]++
					result = repeatedCallResult(prev, repeatCount[callKey])
					cb(StreamEvent{ToolUsed: &ToolUsedEvent{Name: tc.Function.Name, Label: label, Result: shownResult(result), Done: true, ArgToks: flushArgToks()}})
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
				case "tracker":
					act, _ := args["action"].(string)
					nm, _ := args["name"].(string)
					wh, _ := args["when"].(string)
					txt, _ := args["text"].(string)
					evID, _ := args["id"].(string)
					switch strings.ToLower(strings.TrimSpace(act)) {
					case "add":
						if id, e := trackerAdd(nm, wh, txt); e != nil {
							result = "[erreur] " + e.Error()
						} else {
							result = fmt.Sprintf("[ok] point ajouté au tracker « %s » (id %s)", nm, id)
						}
					case "edit":
						if e := trackerEditEvent(slugify(nm), evID, wh, txt); e != nil {
							result = "[erreur] " + e.Error()
						} else {
							result = fmt.Sprintf("[ok] point %s modifié", evID)
						}
					case "delete":
						if strings.TrimSpace(evID) == "" {
							if e := trackerDelete(slugify(nm)); e != nil {
								result = "[erreur] " + e.Error()
							} else {
								result = fmt.Sprintf("[ok] tracker « %s » supprimé", nm)
							}
						} else if e := trackerDeleteEvent(slugify(nm), evID); e != nil {
							result = "[erreur] " + e.Error()
						} else {
							result = fmt.Sprintf("[ok] point %s supprimé", evID)
						}
					default: // view
						result = trackerView(nm, wh)
					}
				case "write":
					content, _ := args["content"].(string)
					if tgt := agentTargetSlug(); tgt != "" {
						// Cible = un poste distant : on écrit LÀ-BAS. Pas de diff (on
						// n'a pas l'ancien contenu du fichier distant).
						result = nodeCall(tgt, nodeCapWrite, map[string]any{"path": label, "content": content})
					} else {
						result = fileWrite(label, content)
						if !strings.HasPrefix(result, "[erreur]") {
							diff = addedDiff(content)
						}
					}
				case "edit":
					oldText, _ := args["old"].(string)
					newText, _ := args["new"].(string)
					if tgt := agentTargetSlug(); tgt != "" {
						result = nodeEditRemote(tgt, label, oldText, newText)
					} else {
						result = fileEdit(label, oldText, newText)
						// Diff seulement si l'édition a réussi (sinon le fichier n'a pas bougé).
						if !strings.HasPrefix(result, "[erreur]") {
							diff = lineDiff(oldText, newText)
						}
					}
				case "bash":
					to := 0
					switch v := args["timeout"].(type) {
					case float64:
						to = int(v)
					case int:
						to = v
					}
					if tgt := agentTargetSlug(); tgt != "" {
						// Cible = un poste distant : la commande s'exécute LÀ-BAS via le
						// canal du poste (fail-closed : nodeCall renvoie une erreur si le
						// poste est déconnecté, on n'exécute JAMAIS sur le serveur à sa place).
						result = nodeCall(tgt, nodeCapShell, map[string]any{"command": label, "timeout": to})
					} else {
						result = runShell(ctx, label, to)
					}
				case "mem_delete":
					if werr := MemDelete(label); werr != nil {
						result = "[erreur] " + werr.Error()
					} else {
						result = fmt.Sprintf("[ok] page '%s' supprimée", label)
					}
				case "recall":
					result = toolRecall(args)
				case "recall_search":
					result = toolRecallSearch(args)
				case "task_list":
					result = toolTaskList(args)
				case "see_image":
					result, visionImg = toolSeeImage(label)
				case "task_create":
					result = toolTaskCreate(args)
				case "task_update":
					result = toolTaskUpdate(args)
				case "task_delete":
					result = toolTaskDelete(args)
				case "machines_list":
					result = toolMachinesList()
				case "machines_use":
					result = toolMachinesUse(args)
				case "web_search":
					result = capWebOutput(toolWebSearch(args))
				case "web_open":
					result = capWebOutput(toolWebOpen(args))
				case "web_read":
					result = capWebOutput(toolWebRead(args))
				case "web_grep":
					result = capWebOutput(toolWebGrep(args))
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
				cb(StreamEvent{ToolUsed: &ToolUsedEvent{Name: tc.Function.Name, Label: label, Result: shownResult(result), Done: true, Diff: diff, ArgToks: flushArgToks()}})
				toolMsg := Message{Role: "tool", ToolCallID: tc.ID, Content: result}
				messages = append(messages, toolMsg)
				extra = append(extra, toolMsg)
				// see_image a réussi : on donne l'image à VOIR au modèle. Le message
				// tool ne porte que du texte ; l'image passe donc dans un message
				// utilisateur multimodal juste après (même format que les pièces
				// jointes, compris par --mmproj), et le tour reprend en la voyant.
				if visionImg != nil {
					imgMsg := Message{Role: "user", Content: []map[string]any{
						{"type": "text", "text": "Image demandée (" + label + ") :"},
						visionImg,
					}}
					messages = append(messages, imgMsg)
					extra = append(extra, imgMsg)
				}
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
			if len(tools) > 0 && !disableTools && nudgeCount < maxNudges {
				nudgeCount++
				// Le raisonnement de ce tour avorté ne mène à rien : on demande à
				// l'UI de l'effacer avant de relancer, pour ne pas afficher deux
				// blocs de réflexion successifs.
				cb(StreamEvent{DropReasoning: true})
				nudge := "You reasoned but did not call a tool or answer. Act NOW: call the appropriate tool directly (e.g. mem_search/mem_read/bash), or give your final answer if you already have the info. Don't explain, act."
				if nudgeCount > 1 {
					// Le premier nudge n'a pas suffi : le modèle re-décrit le même plan
					// sans l'exécuter. Second nudge plus impératif, on lui interdit
					// explicitement de re-raisonner.
					nudge = "You are stuck re-describing the same plan without executing it. Stop reasoning. In your NEXT message, either call ONE tool right now, or write your final answer in plain text using only what you already know — no more planning, no more thinking, act or answer this instant."
				}
				messages = append(messages, Message{Role: "user", Content: nudge})
				continue
			}
			cb(StreamEvent{Content: "_(le modèle n'a pas produit de réponse — finish: " + finishReason + ")_"})
		}
		return extra, nil
	}
}

// healthClient : /health doit répondre tout de suite ou pas du tout. Sans
// timeout (http.Get et son client par défaut n'en ont aucun), un moteur qui
// accepte la connexion sans jamais répondre — cas classique d'un très gros
// modèle en cours de chargement, ou d'un process figé — bloquait healthCheck
// indéfiniment. Et comme StartTurn commence par là, /api/chat/send restait
// pendu : l'utilisateur voyait un bouton d'envoi qui ne rendait jamais la main.
var healthClient = &http.Client{Timeout: 3 * time.Second}

// healthCheck pings llama.cpp's /health endpoint.
func healthCheck() bool {
	resp, err := healthClient.Get(fmt.Sprintf("http://localhost:%d/health", LLMPort()))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == 200
}
