package ajean

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// sys_service.go : affichage VRAM (ajean vram). De l'ancien service moteur
// (ajean-engine, 0.18 et avant) il ne reste que l'arrêt et le retrait, voir
// sys_service_<os>.go et sys_install_<os>.go ; les modèles sont des services
// (services*.go).

func showVram() error {
	out, err := hideCmd(exec.Command("nvidia-smi",
		"--query-gpu=name,memory.used,memory.total,utilization.gpu,temperature.gpu",
		"--format=csv,noheader,nounits")).Output()
	if err != nil {
		return fmt.Errorf("nvidia-smi indisponible: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) != 5 {
			continue
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		name := parts[0]
		used, _ := strconv.Atoi(parts[1])
		total, _ := strconv.Atoi(parts[2])
		util, _ := strconv.Atoi(parts[3])
		temp, _ := strconv.Atoi(parts[4])
		pct := 0
		if total > 0 {
			pct = used * 100 / total
		}
		full := pct / 5
		bar := strings.Repeat("█", full) + strings.Repeat("░", 20-full)
		fmt.Printf("\n  %s\n", cyan(name))
		fmt.Printf("  VRAM  %s  %3d%%   %.1f / %.1f GiB\n", green(bar), pct, float64(used)/1024, float64(total)/1024)
		fmt.Printf("  GPU   %3d%%      Temp  %d°C\n\n", util, temp)
	}
	return nil
}
