package jean

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	toolDefaultTimeout = 30
	toolMaxTimeout     = 300
	toolMaxOutput      = 8000 // characters of stdout/stderr returned to the model
)

// baseSystemPrompt is the always-on system preamble. Structured like pi's
// proven prompt (identity → method → guidelines → concision → date): a concrete,
// procedural prompt gives the model rails so it stops deliberating forever
// ("Wait, let me check… Wait, I'll just run it…") and commits to an action.
// The per-tool "Outil disponible" sections live in machine/skills prompts so
// they only appear when the matching feature is on.
func baseSystemPrompt(caps Caps) string {
	hasMem := caps.Mem != MemOff
	// No tool access at all → no agentic preamble. A plain chat model told to
	// "call tools immediately" hallucinates textual tool calls (e.g.
	// default_api:bash) that leak into the answer. Let the user's own system
	// prompt stand alone. (Internet requiert l'agent, donc pas testé ici.)
	if !caps.Agent && !hasMem {
		return ""
	}
	var b strings.Builder
	// Prompt VOLONTAIREMENT court. Un préambule verbeux (longue liste de
	// « guidelines », surtout des méta-instructions sur la réflexion) fait
	// sur-raisonner les modèles à reasoning (Qwen3) : ils émettent leur <think>
	// puis le token de fin SANS appeler d'outil (~25-45 % de tours « morts »
	// mesurés). Une version courte et directe ramène ça à 0 %. NE PAS regonfler.
	b.WriteString("You are jean, an expert assistant operating directly on this machine with real tools.")
	if caps.Mem == MemAlways {
		b.WriteString(" You evolve with every conversation: you actively maintain a persistent memory so nothing useful is lost between sessions.")
	}
	b.WriteString("\n\nTools:\n")
	if caps.Agent {
		b.WriteString("- bash — run a shell command on this machine (inspect files, processes, logs, run scripts).\n")
		b.WriteString("- edit — patch a file by exact replacement (old → new, old must be unique).\n")
	}
	if hasMem {
		b.WriteString("- mem_search / mem_read / mem_add / mem_edit — your persistent Markdown memory under MEMORY/.\n")
	}
	// Politique d'usage de la mémoire selon le mode.
	switch caps.Mem {
	case MemAlways:
		b.WriteString("\nManaging your memory is part of the job, not optional:\n")
		b.WriteString("- When the user tells you to remember something, or shares a preference, fact, decision, or how-to worth keeping, save it with mem_add (or mem_edit to update an existing page) — do it on your own, without being asked.\n")
		b.WriteString("- Before doing any task or answering, first call mem_search to see whether your memory already holds the answer or how to do it, then mem_read the best page. Do this even when the request has new specifics like a name, a place or a value — your saved method still applies, only the parameter changes.\n")
	case MemOnDemand:
		b.WriteString("\nMemory is ON-DEMAND: you have the mem_* tools but do NOT read or write memory on your own. Call mem_search/mem_read only when the user explicitly asks you to recall or look something up, and mem_add/mem_edit only when the user explicitly asks you to remember something. Otherwise leave memory untouched and answer directly.\n")
	}
	if caps.Agent {
		b.WriteString("\nFor anything about the system or files, use bash instead of guessing. Act immediately — call the right tool, then answer. Never end your turn after only thinking. Be concise.\n")
		if caps.Mem == MemAlways {
			b.WriteString("Before answering any question about yourself or this machine, always call mem_search first — even trivial-seeming ones. Testing with a tool never replaces this: memory may hold context the tool won't reveal. Search memory, then verify, then answer.\n")
		}
	}
	if caps.Internet {
		b.WriteString("\nWeb access (Crawl4AI): web_search (DuckDuckGo), web_open (fetch a URL → metadata + outline), web_read (read a line range of an opened URL), web_grep (regex in an opened URL). Workflow: web_open first, then web_read/web_grep.\n")
		year := time.Now().Format("2006")
		b.WriteString("Your training data is stale. For ANY question about recent/latest/current things (releases, versions, news, prices, scores, fixtures, 'since when') call web_search BEFORE writing any date or version. Your answer must match the dates/facts you actually read.\n")
		b.WriteString("SEARCH QUERY YEAR RULE: today is in " + year + ". If your query includes a year, use ONLY " + year + " — NEVER write a past year like " + prevYear(year) + " that you remember from training; it silently biases results toward stale pages. Default: put no year at all and let the freshest result win. Don't hedge ('probably', 'I think') about a fact a tool can verify — search instead.\n")
	}
	b.WriteString("\nDate: " + time.Now().Format("2006-01-02"))
	return b.String()
}

// prevYear returns the year before the given "2006"-formatted year string, used
// to name explicitly the stale year the model must NOT put in search queries.
func prevYear(year string) string {
	n, err := strconv.Atoi(year)
	if err != nil {
		return year
	}
	return strconv.Itoa(n - 1)
}

// machineSystemPrompt returns a short briefing about the host the model is
// running on, so that when machine access is enabled it knows *which* machine
// run_shell acts upon (and doesn't claim it has no access to "your PC").
// Returns "" when machine access is off.
func machineSystemPrompt(caps Caps) string {
	if !caps.Agent {
		return ""
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	who := ""
	if u, err := user.Current(); err == nil {
		who = u.Username
	}
	cwd, _ := os.Getwd()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Machine: host=%s, %s/%s", host, runtime.GOOS, runtime.GOARCH))
	if who != "" {
		b.WriteString(", user=" + who)
	}
	if cwd != "" {
		b.WriteString(", cwd=" + cwd)
	}
	b.WriteString(".")
	return b.String()
}

// runShell executes a command via the platform shell (bash -c on Unix, cmd /C
// on Windows — see newShellCmd in sys_platform_*.go) with a clamped timeout,
// returning a single string formatted "exit: N\n\nstdout:\n...\n\nstderr:\n..."
// truncated to keep tool output bounded.
func runShell(command string, timeoutSec int) string {
	if timeoutSec <= 0 {
		timeoutSec = toolDefaultTimeout
	}
	if timeoutSec > toolMaxTimeout {
		timeoutSec = toolMaxTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := newShellCmd(ctx, command)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("[timeout après %ds]", timeoutSec)
	}
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return fmt.Sprintf("[erreur: %v]", err)
		}
	}
	out := tailRunes(stdout.String(), toolMaxOutput)
	errOut := tailRunes(stderr.String(), toolMaxOutput)
	parts := []string{fmt.Sprintf("exit: %d", exit)}
	if out != "" {
		parts = append(parts, "stdout:\n"+out)
	}
	if errOut != "" {
		parts = append(parts, "stderr:\n"+errOut)
	}
	return strings.Join(parts, "\n\n")
}

// fileEdit applies a single exact-text replacement to a file on disk: oldText
// must appear EXACTLY once (otherwise it errors), so the model can patch a file
// without rewriting it whole. Returns a short status string for the tool result.
func fileEdit(path, oldText, newText string) string {
	if strings.TrimSpace(path) == "" {
		return "[erreur] chemin vide"
	}
	if oldText == "" {
		return "[erreur] old vide"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "[erreur] " + err.Error()
	}
	content := string(b)
	n := strings.Count(content, oldText)
	if n == 0 {
		return "[erreur] old introuvable dans le fichier"
	}
	if n > 1 {
		return fmt.Sprintf("[erreur] old apparaît %d fois — ajoute du contexte pour le rendre unique", n)
	}
	updated := strings.Replace(content, oldText, newText, 1)
	// Préserve les permissions d'origine (un script 0755 doit rester exécutable).
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode()
	}
	if err := os.WriteFile(path, []byte(updated), mode); err != nil {
		return "[erreur] " + err.Error()
	}
	return fmt.Sprintf("[ok] %s modifié (1 remplacement)", path)
}

// tailRunes returns the last n runes of s (used to cap tool output).
func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
