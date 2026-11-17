package machine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Service : un service systemd de l'utilisateur, tel que le montre l'écran Machine.
type Service struct {
	Unit        string `json:"unit"` // ex. llm-server.service
	Description string `json:"description"`
	Active      string `json:"active"` // active, inactive, failed, activating…
	Sub         string `json:"sub"`    // running, dead, exited…
	Since       int64  `json:"since"`  // démarré (ou arrêté) depuis, epoch ms (0 : inconnu)
	Mem         uint64 `json:"mem"`    // RAM du service (octets, cgroup)
	VRAM        uint64 `json:"vram"`   // VRAM de ses processus (octets)
	Self        bool   `json:"self"`   // c'est AJEAN lui-même : jamais d'arrêt depuis l'interface
}

var unitName = regexp.MustCompile(`^[A-Za-z0-9@._:-]{1,120}$`)

// Unit normalise un nom de service (« llm-server » → « llm-server.service ») et
// refuse ce qui n'en est pas un.
func Unit(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, ".") {
		return "", errors.New("nom de service vide")
	}
	if !strings.Contains(name, ".") {
		name += ".service"
	}
	if !unitName.MatchString(name) || !strings.HasSuffix(name, ".service") || strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("nom de service invalide : %q", name)
	}
	return name, nil
}

func systemctl(ctx context.Context, args ...string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("services : Linux uniquement (systemd)")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if s == "" {
			s = err.Error()
		}
		return s, errors.New(s)
	}
	return s, nil
}

// Status : l'état des services donnés, dans l'ordre.
func Status(units []string) []Service {
	out := []Service{}
	if len(units) == 0 || runtime.GOOS != "linux" {
		return out
	}
	vram := gpuByPID()
	for _, u := range units {
		s := Service{Unit: u}
		raw, _ := systemctl(context.Background(), "show", u, "-p", "Description,ActiveState,SubState,MainPID,ControlGroup,StateChangeTimestampMonotonic")
		p := map[string]string{}
		for _, l := range strings.Split(raw, "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				p[k] = v
			}
		}
		s.Description, s.Active, s.Sub = p["Description"], p["ActiveState"], p["SubState"]
		if s.Active == "" {
			s.Active = "unknown"
		}
		s.Self = p["MainPID"] == strconv.Itoa(os.Getpid())
		if mono, err := strconv.ParseInt(p["StateChangeTimestampMonotonic"], 10, 64); err == nil && mono > 0 {
			s.Since = sinceBoot(mono)
		}
		if cg := p["ControlGroup"]; cg != "" {
			dir := filepath.Join("/sys/fs/cgroup", cg)
			s.Mem = cgroupMem(dir)
			if b, err := os.ReadFile(filepath.Join(dir, "cgroup.procs")); err == nil {
				for _, f := range strings.Fields(string(b)) {
					pid, _ := strconv.Atoi(f)
					s.VRAM += vram[pid]
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// sinceBoot : une heure monotone systemd (µs depuis le démarrage) en epoch ms.
func sinceBoot(mono int64) int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	up, _ := strconv.ParseFloat(f[0], 64)
	boot := time.Now().Add(-time.Duration(up * float64(time.Second)))
	return boot.Add(time.Duration(mono) * time.Microsecond).UnixMilli()
}

// cgroupMem : la mémoire d'un service sans le cache de fichiers récupérable
// (comme docker stats) ; sinon un modèle lu depuis le disque compterait en plus.
func cgroupMem(dir string) uint64 {
	b, err := os.ReadFile(filepath.Join(dir, "memory.current"))
	if err != nil {
		return 0
	}
	cur, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if st, err := os.ReadFile(filepath.Join(dir, "memory.stat")); err == nil {
		for _, l := range strings.Split(string(st), "\n") {
			if v, ok := strings.CutPrefix(l, "inactive_file "); ok {
				if n, _ := strconv.ParseUint(strings.TrimSpace(v), 10, 64); n < cur {
					cur -= n
				}
			}
		}
	}
	return cur
}

// Available : les services écrits par l'utilisateur (~/.config/systemd/user),
// pour en choisir un à suivre. Ceux du bureau et du système n'y sont pas.
func Available() []string {
	out := []string{}
	dir, err := os.UserConfigDir()
	if err != nil {
		return out
	}
	entries, err := os.ReadDir(filepath.Join(dir, "systemd", "user"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".service") && !strings.Contains(n, "@.") && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// Act démarre, arrête ou redémarre un service. AJEAN lui-même ne s'arrête pas
// depuis son interface (il ne pourrait plus se rallumer).
func Act(unit, action string) error {
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("action inconnue : %s", action)
	}
	if action != "start" {
		for _, s := range Status([]string{unit}) {
			if s.Self && action == "stop" {
				return errors.New("AJEAN ne peut pas s'arrêter lui-même depuis son interface")
			}
		}
	}
	if action == "restart" {
		// Se redémarrer : la réponse doit partir avant que le processus ne soit remplacé.
		for _, s := range Status([]string{unit}) {
			if s.Self {
				_, err := systemctl(context.Background(), "--no-block", "restart", unit)
				return err
			}
		}
	}
	_, err := systemctl(context.Background(), action, unit)
	return err
}

// Logs : les dernières lignes du journal d'un service.
func Logs(unit string, n int) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("services : Linux uniquement (systemd)")
	}
	n = min(max(n, 10), 500)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", "--user", "-u", unit, "-n", strconv.Itoa(n), "--no-pager", "-o", "short-iso").CombinedOutput()
	if err != nil && len(out) == 0 {
		return "", err
	}
	return string(out), nil
}
