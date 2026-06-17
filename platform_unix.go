//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// defaultJeanHome is the data root when neither $JEAN_HOME nor /etc/default/jean
// override it.
func defaultJeanHome() string { return "/etc/jean" }

// defaultEditor is used by `jean edit` when $EDITOR is unset.
func defaultEditor() string { return "nano" }

// setLibraryPath ensures llama-server can load shared libs bundled next to the
// binary by prepending dir to LD_LIBRARY_PATH. It also appends the CUDA runtime
// lib directories: a CUDA-enabled build links against libcudart/libcublas, which
// live under /usr/local/cuda*/lib64 and are often absent from the global ld
// cache — without them llama-server fails to load the GPU backend (or runs
// degraded), costing a large chunk of throughput.
func setLibraryPath(dir string) {
	parts := []string{dir}
	parts = append(parts, cudaLibDirs()...)
	if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
		parts = append(parts, existing)
	}
	_ = os.Setenv("LD_LIBRARY_PATH", strings.Join(parts, ":"))
}

// cudaLibDirs returns the CUDA runtime lib directories present on the machine,
// preferring the highest-versioned install. Empty when no CUDA toolkit is found.
func cudaLibDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d != "" && !seen[d] && isDir(d) {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	// Default symlink first (usually points at the active toolkit).
	add("/usr/local/cuda/lib64")
	add("/usr/local/cuda/targets/x86_64-linux/lib")
	// Versioned installs, newest last so it takes precedence in PATH order.
	versioned, _ := filepath.Glob("/usr/local/cuda-*/lib64")
	sort.Strings(versioned)
	for i := len(versioned) - 1; i >= 0; i-- {
		add(versioned[i])
	}
	return dirs
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
