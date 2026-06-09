//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// defaultJeanHome is the data root when $JEAN_HOME is unset. We use
// %ProgramData%\jean (machine-wide, the closest analogue to /etc/jean), falling
// back to %LOCALAPPDATA%\jean for unprivileged setups.
func defaultJeanHome() string {
	if pd := os.Getenv("ProgramData"); pd != "" {
		return filepath.Join(pd, "jean")
	}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		return filepath.Join(la, "jean")
	}
	return filepath.Join(os.TempDir(), "jean")
}

// defaultEditor is used by `jean edit` when $EDITOR is unset.
func defaultEditor() string { return "notepad" }

// setLibraryPath ensures llama-server can load DLLs bundled next to the binary.
// Windows resolves dependent DLLs via PATH (and the binary's own directory), so
// we prepend dir to PATH.
func setLibraryPath(dir string) {
	p := dir
	if existing := os.Getenv("PATH"); existing != "" {
		p = dir + string(os.PathListSeparator) + existing
	}
	_ = os.Setenv("PATH", p)
}

// execServer runs llama-server as a child process and waits for it. Windows has
// no exec() that replaces the current image, so `jean serve` stays alive as the
// parent (this is the detached process the service supervisor tracks).
// args[0] is the binary path; the rest are its arguments.
func execServer(bin string, args []string) error {
	cmd := exec.Command(bin, args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

// newShellCmd builds the command used by the run_shell tool.
func newShellCmd(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "cmd", "/C", command)
}
