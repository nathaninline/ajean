package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
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
	// No tool access → no agentic preamble. A plain chat model told to "call
	// tools immediately" hallucinates textual tool calls (e.g. default_api:bash)
	// that leak into the answer. Let the user's own system prompt stand alone.
	if !caps.Agent {
		return ""
	}
	cwd, _ := os.Getwd()

	var b strings.Builder
	b.WriteString("You are an expert coding assistant operating inside jean, a coding agent harness. You help users by reading files, executing commands, editing code, writing new files, and leveraging persistent memory to remember things across sessions.\n\n")

	b.WriteString("Available tools:\n")
	for _, l := range []string{
		"bash: Execute a shell command on this machine (ls, grep, find, cat, sed, read logs, run scripts…) and return stdout, stderr and the exit code. This is your real access to the system.",
		"edit: Modify a file on disk by exact replacement (old → new; old must match exactly once). Use this to patch a file instead of rewriting it whole.",
		"mem_search: Search your memory (Markdown pages under MEMORY/). Returns a ranked list of {file, title, snippet}.",
		"mem_read: Read a memory page (Markdown file), line-numbered, with optional offset/limit.",
		"mem_add: Create a new memory page (one topic per page, descriptive kebab-case name, first line is a short # title).",
		"mem_edit: Edit a memory page by exact replacement (old → new; old must match exactly once).",
	} {
		b.WriteString("- " + l + "\n")
	}

	b.WriteString("\nGuidelines:\n")
	for _, l := range []string{
		"Reason briefly (1-2 sentences) then ACT. As soon as an action is possible, call the tool immediately — don't announce it, do it.",
		"Never repeat the same reasoning step or the same tool call twice. If you hesitate (\"maybe I should check…\"), stop thinking and call the tool now.",
		"Use bash for any system question (files, processes, network, OS) instead of assuming — you have real access, don't ask permission to inspect.",
		"Use bash to read files (cat, sed -n); to change a file, use the edit tool (old → new) instead of rewriting it whole with bash.",
		"Use mem_search FIRST whenever the user asks about something you might already know (preferences, ongoing projects, past decisions). Open the most relevant page with mem_read; snippets are hints, not the answer.",
		"Save anything worth keeping across sessions with mem_add; update an existing page with mem_edit. Don't save ephemeral conversation state.",
		"Prefer targeted, non-interactive commands; chain them with && when they depend on each other.",
		"Avoid destructive commands (rm -rf, mkfs, dd…) unless the user explicitly asks.",
		"If a command fails, read stderr and the exit code, then fix it — don't re-run the same command unchanged.",
		"Never invent a tool: you only have bash, edit and the mem_* tools. Anything else (reading code, web access) goes through bash.",
		"When the task is done, give a short final answer and stop. Don't chain useless tool calls.",
		"Be concise in your responses.",
		"Show file paths clearly when working with files.",
	} {
		b.WriteString("- " + l + "\n")
	}

	b.WriteString("\nCurrent date: " + time.Now().Format("2006-01-02"))
	b.WriteString("\nCurrent working directory: " + cwd)
	return b.String()
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
// on Windows — see newShellCmd in platform_*.go) with a clamped timeout,
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
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
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
