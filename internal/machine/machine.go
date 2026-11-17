// Package machine mesure la machine sur laquelle tourne AJEAN (processeur,
// mémoire, cartes graphiques) et pilote les services systemd de l'utilisateur
// choisis dans l'écran Machine. Le pilotage ne passe que par l'interface :
// l'assistant n'a aucun outil pour démarrer ou arrêter un service.
package machine

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Stats : une mesure de la machine.
type Stats struct {
	Supported bool    `json:"supported"` // false hors Linux : rien n'est mesuré
	CPU       float64 `json:"cpu"`       // % d'occupation depuis la mesure précédente
	Cores     int     `json:"cores"`
	Load      float64 `json:"load"` // charge moyenne sur 1 min
	MemTotal  uint64  `json:"mem_total"`
	MemUsed   uint64  `json:"mem_used"`
	SwapTotal uint64  `json:"swap_total"`
	SwapUsed  uint64  `json:"swap_used"`
	GPUs      []GPU   `json:"gpus"`
	Uptime    int64   `json:"uptime"` // secondes
}

// GPU : une carte NVIDIA, lue par nvidia-smi.
type GPU struct {
	Index    int     `json:"index"`
	Name     string  `json:"name"`
	MemUsed  uint64  `json:"mem_used"` // octets
	MemTotal uint64  `json:"mem_total"`
	Util     float64 `json:"util"` // %
	Temp     float64 `json:"temp"` // °C
	Power    float64 `json:"power"`
}

var (
	cpuMu             sync.Mutex
	cpuIdle, cpuTotal uint64
)

// Read mesure la machine maintenant.
func Read() Stats {
	if runtime.GOOS != "linux" {
		return Stats{GPUs: []GPU{}}
	}
	s := Stats{Supported: true, Cores: runtime.NumCPU(), CPU: cpuPercent()}
	mem := meminfo()
	s.MemTotal = mem["MemTotal"]
	s.MemUsed = mem["MemTotal"] - mem["MemAvailable"]
	s.SwapTotal = mem["SwapTotal"]
	s.SwapUsed = mem["SwapTotal"] - mem["SwapFree"]
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			s.Load, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			up, _ := strconv.ParseFloat(f[0], 64)
			s.Uptime = int64(up)
		}
	}
	s.GPUs = gpus()
	return s
}

// cpuPercent : l'occupation du processeur depuis l'appel précédent (/proc/stat).
func cpuPercent() float64 {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	line, _, _ := strings.Cut(string(b), "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0
	}
	var total, idle uint64
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle et iowait
			idle += n
		}
	}
	cpuMu.Lock()
	defer cpuMu.Unlock()
	dt, di := total-cpuTotal, idle-cpuIdle
	first := cpuTotal == 0
	cpuTotal, cpuIdle = total, idle
	if first || dt == 0 {
		return 0
	}
	return 100 * float64(dt-di) / float64(dt)
}

// meminfo : /proc/meminfo en octets.
func meminfo() map[string]uint64 {
	m := map[string]uint64{}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(v)
		if len(fields) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(fields[0], 10, 64)
		m[k] = n * 1024
	}
	return m
}

// nvidiaSMI lance nvidia-smi (absent : nil). Borné : une carte bloquée ne doit
// pas figer l'écran.
func nvidiaSMI(args ...string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi", args...).Output()
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(string(bytes.TrimSpace(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func num(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func gpus() []GPU {
	out := []GPU{}
	for _, l := range nvidiaSMI("--query-gpu=index,name,memory.used,memory.total,utilization.gpu,temperature.gpu,power.draw", "--format=csv,noheader,nounits") {
		f := strings.Split(l, ",")
		if len(f) < 7 {
			continue
		}
		out = append(out, GPU{
			Index: int(num(f[0])), Name: strings.TrimSpace(f[1]),
			MemUsed: uint64(num(f[2])) << 20, MemTotal: uint64(num(f[3])) << 20,
			Util: num(f[4]), Temp: num(f[5]), Power: num(f[6]),
		})
	}
	return out
}

// gpuByPID : la VRAM occupée par chaque processus (octets).
func gpuByPID() map[int]uint64 {
	m := map[int]uint64{}
	for _, l := range nvidiaSMI("--query-compute-apps=pid,used_memory", "--format=csv,noheader,nounits") {
		pid, mem, ok := strings.Cut(l, ",")
		if !ok {
			continue
		}
		m[int(num(pid))] += uint64(num(mem)) << 20
	}
	return m
}
