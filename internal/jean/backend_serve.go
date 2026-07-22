package jean

import (
	"fmt"
	"os"
	"path/filepath"
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

	// Make sure llama-server can find its bundled shared libraries (the .so/.dll
	// neighbours of the binary). This is platform-specific: LD_LIBRARY_PATH on
	// Linux, PATH on Windows — handled inside execServer.
	setLibraryPath(filepath.Dir(bin))

	// Sélection GPU (jean gpu) : on filtre les devices visibles par llama-server.
	// CUDA_DEVICE_ORDER=PCI_BUS_ID garantit que les index correspondent à ceux
	// affichés par nvidia-smi (sinon CUDA réordonne par "device le plus rapide").
	if v := cfg["CUDA_VISIBLE_DEVICES"]; v != "" {
		_ = os.Setenv("CUDA_VISIBLE_DEVICES", v)
		_ = os.Setenv("CUDA_DEVICE_ORDER", "PCI_BUS_ID")
	}

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
		// budget illimité par défaut (-1) : on laisse le modèle réfléchir jusqu'au
		// bout au lieu de le couper à 2048, ce qui tronquait la vraie réponse (la
		// réflexion atteignait le plafond et il ne restait plus de marge pour le
		// contenu). L'anti-boucle côté llm_client.go reste le garde-fou. NE PAS forcer 0 :
		// sur llama.cpp vanilla, 0 = "immediate end" → coupe tout le raisonnement
		// (le fork ik_llama.cpp l'ignore). Configurable via REASONING_BUDGET.
		llmArgs = append(llmArgs, "--reasoning", r, "--reasoning-budget", get("REASONING_BUDGET", "-1"))
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

	// Hand off to the llama-server process. On Unix this replaces the current
	// process (exec); on Windows it runs as a child and waits. See sys_platform_*.go.
	return execServer(bin, llmArgs)
}
