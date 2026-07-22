package jean

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// backend_gpu.go — sélection du/des GPU utilisés par llama-server.
//
//	jean gpu            liste les GPU et montre la sélection courante
//	jean gpu 1          n'utilise que le GPU d'index 1
//	jean gpu 0 1        utilise les GPU 0 et 1
//	jean gpu all        réinitialise (tous les GPU visibles)
//
// La sélection est stockée dans config.env sous CUDA_VISIBLE_DEVICES ; backend_serve.go
// l'exporte (avec CUDA_DEVICE_ORDER=PCI_BUS_ID pour que les index correspondent
// à ceux affichés par nvidia-smi).

type gpuInfo struct {
	Index    int
	Name     string
	MemTotal string // en MiB
	MemUsed  string
	Cap      string // compute capability
}

func cmdGPU(args []string) error {
	if len(args) == 0 || args[0] == "list" || args[0] == "ls" {
		return gpuList()
	}
	switch args[0] {
	case "all", "reset", "none", "auto":
		return gpuSet("")
	}
	// Sinon : une liste d'index (« 1 », « 0 1 », « 0,1 »).
	gpus, err := detectGPUs()
	if err != nil {
		return err
	}
	raw := strings.Join(args, ",")
	var idx []string
	for _, tok := range strings.Split(raw, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		n, err := strconv.Atoi(tok)
		if err != nil {
			return fmt.Errorf("index GPU invalide: %q (attendu un nombre)", tok)
		}
		if n < 0 || n >= len(gpus) {
			return fmt.Errorf("index GPU %d hors limites (0..%d) — voir « jean gpu »", n, len(gpus)-1)
		}
		idx = append(idx, strconv.Itoa(n))
	}
	if len(idx) == 0 {
		return fmt.Errorf("aucun index fourni")
	}
	return gpuSet(strings.Join(idx, ","))
}

// gpuList affiche les GPU détectés et marque ceux actuellement sélectionnés.
func gpuList() error {
	gpus, err := detectGPUs()
	if err != nil {
		return err
	}
	sel := ReadConfig()["CUDA_VISIBLE_DEVICES"]
	selected := map[int]bool{}
	if sel != "" {
		for _, t := range strings.Split(sel, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				selected[n] = true
			}
		}
	}

	fmt.Println()
	for _, g := range gpus {
		mark := "  "
		line := fmt.Sprintf("[%d] %s  —  %s/%s MiB  (cc %s)", g.Index, g.Name, g.MemUsed, g.MemTotal, g.Cap)
		active := sel == "" || selected[g.Index]
		if sel != "" && selected[g.Index] {
			mark = green("● ")
			line = green(line)
		} else if sel == "" {
			mark = dim("○ ")
		} else {
			mark = dim("○ ")
			line = dim(line)
		}
		_ = active
		fmt.Printf("  %s%s\n", mark, line)
	}
	fmt.Println()
	if sel == "" {
		fmt.Printf("  Sélection : %s (tous les GPU)\n", bold("auto"))
	} else {
		fmt.Printf("  Sélection : %s (CUDA_VISIBLE_DEVICES=%s)\n", bold(sel), sel)
	}
	fmt.Printf("  %s jean gpu <index…>  pour choisir,  jean gpu all  pour réinitialiser\n", dim("→"))
	return nil
}

// gpuSet writes (or clears) CUDA_VISIBLE_DEVICES in config.env then offers a
// restart so the change takes effect.
func gpuSet(value string) error {
	if err := SetConfigKey("CUDA_VISIBLE_DEVICES", value); err != nil {
		return err
	}
	if value == "" {
		fmt.Printf("%s sélection GPU réinitialisée — tous les GPU seront visibles\n", green("[ok]"))
	} else {
		fmt.Printf("%s GPU sélectionné(s) : %s\n", green("[ok]"), bold(value))
	}
	fmt.Print(dim("[info] redémarrer le service pour appliquer ? [Y/n] "))
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() && strings.HasPrefix(strings.ToLower(strings.TrimSpace(sc.Text())), "n") {
		fmt.Println(dim("[info] pense à lancer 'jean restart'"))
		return nil
	}
	return serviceAction("restart")
}

// detectGPUs queries nvidia-smi for the list of NVIDIA GPUs.
func detectGPUs() ([]gpuInfo, error) {
	if !hasTool("nvidia-smi") {
		return nil, fmt.Errorf("nvidia-smi introuvable — sélection GPU disponible uniquement sur NVIDIA")
	}
	out, err := hideCmd(exec.Command("nvidia-smi",
		"--query-gpu=index,name,memory.total,memory.used,compute_cap",
		"--format=csv,noheader,nounits")).Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi a échoué: %w", err)
	}
	var gpus []gpuInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) < 5 {
			continue
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		idx, _ := strconv.Atoi(parts[0])
		gpus = append(gpus, gpuInfo{
			Index: idx, Name: parts[1], MemTotal: parts[2], MemUsed: parts[3], Cap: parts[4],
		})
	}
	if len(gpus) == 0 {
		return nil, fmt.Errorf("aucun GPU NVIDIA détecté")
	}
	return gpus, nil
}
