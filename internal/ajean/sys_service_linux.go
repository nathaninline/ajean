//go:build linux

package ajean

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// serviceAction wraps `systemctl <action> <svc>` with passwordless sudo where
// it makes sense, and prints a follow-up status check after start/restart.
func serviceAction(action string) error {
	// API Externe : aucun moteur local (arrêt). GPU Cloud : relais local vers le GPU.
	action = cloudServiceAction(action)
	svc := serviceName()
	needsRoot := action == "start" || action == "stop" || action == "restart" || action == "enable" || action == "disable"
	args := []string{}
	bin := "systemctl"
	if needsRoot && os.Geteuid() != 0 {
		bin = "sudo"
		args = append(args, "-n", "systemctl")
	}
	args = append(args, action, svc)
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	switch action {
	case "start", "restart":
		return checkStarted(svc)
	case "stop":
		fmt.Println(green("[ok]") + " arrêté")
	case "enable":
		fmt.Println(green("[ok]") + " démarrage auto activé")
	case "disable":
		fmt.Println(green("[ok]") + " démarrage auto désactivé")
	}
	return nil
}

// unitState renvoie ActiveState et SubState de l'unité (« activating » /
// « auto-restart » par exemple).
func unitState(svc string) (active, sub string) {
	out, _ := exec.Command("systemctl", "show", svc, "-p", "ActiveState", "-p", "SubState").Output()
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "ActiveState":
			active = v
		case "SubState":
			sub = v
		}
	}
	return
}

// checkStarted attend que l'unité se stabilise, puis dit la vérité.
//
// ⚠️ « activating » n'est PAS un succès : avec Restart=on-failure, un moteur qui
// meurt au démarrage repasse en boucle par activating (SubState auto-restart).
// L'ancien test l'acceptait, et `ajean start` répondait « [ok] activating » sur
// un service qui ne démarrerait jamais — d'où des rapports « ajean start dit ok
// mais ajean test ne répond pas ». On distingue donc le SubState, et on laisse
// au moteur le temps de charger un gros modèle (il reste en activating, mais
// PAS en auto-restart).
func checkStarted(svc string) error {
	var state, sub string
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Second)
		state, sub = unitState(svc)
		if state == "active" {
			fmt.Printf("%s %s: actif\n", green("[ok]"), svc)
			return nil
		}
		if state == "failed" || sub == "auto-restart" {
			break // inutile d'attendre : il boucle sur un échec
		}
	}
	if state == "activating" && sub != "auto-restart" {
		// Chargement en cours (un gros .gguf prend des minutes) : légitime.
		fmt.Printf("%s %s: démarrage en cours (chargement du modèle) — %s pour suivre\n",
			green("[ok]"), svc, bold("ajean logs"))
		return nil
	}
	what := state
	if sub == "auto-restart" {
		what = "redémarre en boucle (le moteur meurt au lancement)"
	}
	fmt.Printf("%s %s: %s — derniers logs :\n", red("[ERREUR]"), svc, what)
	fmt.Println("------------------------------------------------")
	logs, _ := exec.Command("journalctl", "-u", svc, "-n", "20", "--no-pager").Output()
	fmt.Print(string(logs))
	fmt.Println("------------------------------------------------")
	fmt.Printf("→ ajean logs   pour plus de détails\n→ ajean edit   pour corriger la configuration (BIN, MODEL…)\n")
	return fmt.Errorf("service %s non démarré", svc)
}

func serviceLogs() error {
	cmd := exec.Command("journalctl", "-u", serviceName(), "-n", "80", "-f")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// serviceIsActive reports whether the systemd unit is currently running.
func serviceIsActive() bool {
	out, _ := exec.Command("systemctl", "is-active", serviceName()).Output()
	return strings.TrimSpace(string(out)) == "active"
}

// serviceLogTail renvoie les n dernières lignes du journal du service (pour
// l'UI web). Linux : journalctl.
func serviceLogTail(n int) string {
	out, err := exec.Command("journalctl", "-u", serviceName(), "-n", strconv.Itoa(n), "--no-pager").CombinedOutput()
	if err != nil && len(out) == 0 {
		return "journalctl indisponible : " + err.Error()
	}
	return string(out)
}

// unitAction lance `systemctl <action> <unit>` sur une AUTRE unité que le
// moteur (service externe d'un preset, voir backend_extservice.go), via sudo -n
// quand on n'est pas root : sudoers doit l'autoriser explicitement.
func unitAction(unit, action string) error {
	args := []string{action, unit}
	bin := "systemctl"
	if os.Geteuid() != 0 {
		bin = "sudo"
		args = append([]string{"-n", "systemctl"}, args...)
	}
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// waitGPUsReleased attend (au plus `max`) que les process `pids` (ceux de
// l'unité qu'on vient d'arrêter) n'occupent plus les GPU NVIDIA. `systemctl
// stop` rend la main dès que le process principal est mort, mais le pilote peut
// libérer la VRAM une seconde plus tard : un moteur lancé dans cet intervalle
// mourait sur « cudaMalloc failed: out of memory ». On ne regarde QUE ces
// process : un autre programme GPU permanent (génération d'images…) faisait
// sinon attendre le délai entier à chaque bascule. Sans `pids` connus, on
// attend qu'aucun process n'occupe les GPU. Sans nvidia-smi, on ne fait rien.
func waitGPUsReleased(pids []int, max time.Duration) {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return
	}
	watch := map[string]bool{}
	for _, p := range pids {
		watch[strconv.Itoa(p)] = true
	}
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		out, err := exec.Command("nvidia-smi", "--query-compute-apps=pid", "--format=csv,noheader").Output()
		if err != nil {
			return
		}
		busy := false
		for _, l := range strings.Split(string(out), "\n") {
			l = strings.TrimSpace(l)
			if l != "" && (len(watch) == 0 || watch[l]) {
				busy = true
				break
			}
		}
		if !busy {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// unitPIDs : les process de l'unité systemd (son cgroup), relevés AVANT son
// arrêt pour que waitGPUsReleased sache lesquels attendre. nil si inconnu.
func unitPIDs(unit string) []int {
	out, err := exec.Command("systemctl", "show", unit, "-p", "ControlGroup", "--value").Output()
	cg := strings.TrimSpace(string(out))
	if err != nil || cg == "" {
		return nil
	}
	for _, root := range []string{"/sys/fs/cgroup", "/sys/fs/cgroup/unified", "/sys/fs/cgroup/systemd"} {
		b, err := os.ReadFile(root + cg + "/cgroup.procs")
		if err != nil {
			continue
		}
		var pids []int
		for _, f := range strings.Fields(string(b)) {
			if n, err := strconv.Atoi(f); err == nil {
				pids = append(pids, n)
			}
		}
		return pids
	}
	return nil
}

// unitActiveState : ActiveState d'une autre unité (service externe de preset).
func unitActiveState(unit string) string {
	a, _ := unitState(unit)
	return a
}
