package jean

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// sys_service.go holds the platform-neutral pieces of service management. The
// actual start/stop/restart/status/logs implementation is platform-specific:
//   - sys_service_linux.go   → systemd (systemctl/journalctl)
//   - sys_service_darwin.go  → launchd (launchctl)
//   - sys_service_windows.go → PID-file background process supervisor
//
// editConfig and showVram live here because they work the same everywhere.

func editConfig() error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = defaultEditor()
	}
	cmd := exec.Command(editor, confPath())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	fmt.Println(dim("[info] jean restart pour appliquer"))
	return nil
}

// showVram parses `nvidia-smi --query-gpu=...` and renders a colored bar.
// nvidia-smi is available on both Linux and Windows when an NVIDIA driver is
// installed, so this is platform-neutral.
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
