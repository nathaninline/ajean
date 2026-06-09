//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// defaultJeanHome is the data root when neither $JEAN_HOME nor /etc/default/jean
// override it.
func defaultJeanHome() string { return "/etc/jean" }

// defaultEditor is used by `jean edit` when $EDITOR is unset.
func defaultEditor() string { return "nano" }

// setLibraryPath ensures llama-server can load shared libs bundled next to the
// binary by prepending dir to LD_LIBRARY_PATH.
func setLibraryPath(dir string) {
	ld := dir
	if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
		ld = ld + ":" + existing
	}
	_ = os.Setenv("LD_LIBRARY_PATH", ld)
}

// execServer replaces the current process with llama-server (so systemd
// supervises llama-server directly, as the old start.sh did with `exec`).
// args[0] must be the binary path.
func execServer(bin string, args []string) error {
	return syscall.Exec(bin, args, os.Environ())
}

// newShellCmd builds the command used by the run_shell tool.
func newShellCmd(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/bash", "-c", command)
}
