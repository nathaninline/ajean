//go:build linux

package ajean

// Processeur et stockage pour la zone Appareil :
// charge CPU entre deux relevés, température, et place libre sur le
// disque des données d'AJEAN.

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var (
	cpuMu        sync.Mutex
	cpuPrevIdle  uint64
	cpuPrevTotal uint64
	cpuLastPct   int
)

func cpuTelemetry() map[string]any {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil
	}
	line, _, _ := strings.Cut(string(b), "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return nil
	}
	var idle, total uint64
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	cpuMu.Lock()
	if cpuPrevTotal > 0 && total > cpuPrevTotal {
		dt, di := total-cpuPrevTotal, idle-cpuPrevIdle
		cpuLastPct = int(100 - di*100/dt)
	}
	cpuPrevIdle, cpuPrevTotal = idle, total
	pct := cpuLastPct
	cpuMu.Unlock()
	return map[string]any{"util": pct, "cores": runtime.NumCPU(), "temp": cpuTemp()}
}

// cpuTemp : la sonde du processeur (k10temp, coretemp, zenpower), 0 si inconnue.
func cpuTemp() int {
	hw, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, d := range hw {
		name, _ := os.ReadFile(filepath.Join(d, "name"))
		switch strings.TrimSpace(string(name)) {
		case "k10temp", "coretemp", "zenpower", "cpu_thermal":
			if b, err := os.ReadFile(filepath.Join(d, "temp1_input")); err == nil {
				v, _ := strconv.Atoi(strings.TrimSpace(string(b)))
				return v / 1000
			}
		}
	}
	return 0
}

// diskTelemetry : disque qui porte les données d'AJEAN (Mo).
func diskTelemetry() map[string]any {
	var st syscall.Statfs_t
	p := AjeanHome()
	if syscall.Statfs(p, &st) != nil {
		return nil
	}
	total := st.Blocks * uint64(st.Bsize) >> 20
	free := st.Bavail * uint64(st.Bsize) >> 20
	if total == 0 {
		return nil
	}
	return map[string]any{"used": total - free, "total": total, "path": p}
}
