package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// cmdServe replaces the historic start.sh: read config.env, build the
// llama-server invocation, and exec it (replacing this process so systemd
// supervises llama-server directly).
func cmdServe(args []string) error {
	cfg := ReadConfig()
	bin := cfg["BIN"]
	if bin == "" {
		return fmt.Errorf("BIN non défini dans %s", confPath())
	}
	model := cfg["MODEL"]
	if model == "" {
		return fmt.Errorf("MODEL non défini dans %s", confPath())
	}

	// Resolve LD_LIBRARY_PATH the same way start.sh did so that custom llama.cpp
	// builds with bundled shared libs continue to load their .so neighbours.
	ld := filepath.Dir(bin)
	if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
		ld = ld + ":" + existing
	}
	_ = os.Setenv("LD_LIBRARY_PATH", ld)

	get := func(key, fallback string) string {
		if v, ok := cfg[key]; ok && v != "" {
			return v
		}
		return fallback
	}
	kv := get("KV_TYPE", "")
	ktv := get("KV_TYPE_K", kv)
	vtv := get("KV_TYPE_V", kv)

	llmArgs := []string{bin,
		"-m", model,
		"-ngl", get("NGL", "999"),
		"-c", get("CTX", "32768"),
		"-t", get("THREADS", "0"),
		"-tb", get("THREADS_BATCH", "0"),
		"-b", get("BATCH", "2048"),
		"-ub", get("UBATCH", "512"),
		"--host", get("HOST", "0.0.0.0"),
		"--port", get("PORT", "8080"),
	}
	if ktv != "" {
		llmArgs = append(llmArgs, "-ctk", ktv)
	}
	if vtv != "" {
		llmArgs = append(llmArgs, "-ctv", vtv)
	}
	if r := cfg["REASONING"]; r != "" {
		llmArgs = append(llmArgs, "--reasoning", r, "--reasoning-budget", "0")
	}
	// API_KEY protège le serveur quand il est exposé sur internet : llama-server
	// exige alors l'en-tête "Authorization: Bearer <clé>". La clé est lue depuis
	// $JEAN_HOME/.api_key en priorité (elle survit ainsi aux changements de preset
	// qui réécrivent config.env), avec config.env comme repli rétro-compatible.
	if k := readAPIKey(); k != "" {
		llmArgs = append(llmArgs, "--api-key", k)
	} else if k := cfg["API_KEY"]; k != "" {
		llmArgs = append(llmArgs, "--api-key", k)
	}
	// EXTRA_ARGS is appended verbatim, split on whitespace like the shell would.
	for _, a := range trimSplit(cfg["EXTRA_ARGS"], " ") {
		llmArgs = append(llmArgs, a)
	}

	// Working dir = JEAN_HOME so relative paths in EXTRA_ARGS (e.g. --mmproj
	// mmproj-F16.gguf) still resolve.
	_ = os.Chdir(JeanHome())

	fmt.Fprintf(os.Stderr, "[jean serve] %s  model=%s  port=%s\n",
		bin, filepath.Base(model), get("PORT", "8080"))

	// exec replaces this process — same as start.sh's `exec`.
	return syscall.Exec(bin, llmArgs, os.Environ())
}
