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
