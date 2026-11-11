//go:build windows

package ajean

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Ancien moteur (0.18 et avant) : sous Windows, c'était une copie de
// `ajean serve` lancée détachée et suivie par un fichier PID. AJEAN ne la lance
// plus (les modèles sont des services, voir services_windows.go) ; il ne reste
// ici que son arrêt, pour qu'une machine mise à jour ne garde pas un vieux
// moteur en mémoire (« Quitter » de la zone de notification, désinstallation).

const (
	createNewProcessGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	detachedProcess       = 0x00000008 // DETACHED_PROCESS
	createNoWindow        = 0x08000000 // CREATE_NO_WINDOW (aucune console pour l'enfant)
)

func pidFilePath() string { return filepath.Join(AjeanHome(), serviceName()+".pid") }

// svcStop arrête l'ancien moteur s'il tourne encore (sans effet sinon).
func svcStop(verbose bool) error {
	pid := readServicePID()
	if pid <= 0 || !processAlive(pid) {
		_ = os.Remove(pidFilePath())
		if verbose {
			fmt.Println(yellow("[info]") + " aucun service en cours d'exécution")
		}
		return nil
	}
	// taskkill /T tue aussi le processus enfant (le serveur du modèle).
	cmd := hideCmd(exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("arrêt du PID %d: %w\n%s", pid, err, string(out))
	}
	// taskkill /F rend la main AVANT que Windows ait fini de détruire les processus :
	// la mémoire du modèle (verrouillée par le pilote) et le port restent pris un
	// moment (issue #114). On attend la vraie fin.
	waitServiceGone(pid, 30*time.Second)
	_ = os.Remove(pidFilePath())
	if verbose {
		fmt.Println(green("[ok]") + " arrêté")
	}
	return nil
}

// waitServiceGone attend que le processus de l'ancien moteur soit terminé et que
// son port ne réponde plus (son enfant le tient jusqu'au bout). Borné par max.
func waitServiceGone(pid int, max time.Duration) {
	port := strings.TrimSpace(ReadConfig()["PORT"])
	if port == "" {
		port = "8080"
	}
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if !processAlive(pid) && !portListening(port) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// portListening : vrai si quelque chose accepte des connexions sur ce port local.
func portListening(port string) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func readServicePID() int {
	b, err := os.ReadFile(pidFilePath())
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

// processAlive : le PID désigne un processus en cours. API Win32 (OpenProcess +
// GetExitCodeProcess) plutôt que `tasklist` : un appel externe fait clignoter
// une fenêtre de console à chaque vérification.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const (
		queryLimitedInfo = 0x1000
		stillActive      = 259
	)
	k := syscall.NewLazyDLL("kernel32.dll")
	h, _, _ := k.NewProc("OpenProcess").Call(queryLimitedInfo, 0, uintptr(pid))
	if h == 0 {
		return false
	}
	defer k.NewProc("CloseHandle").Call(h)
	var code uint32
	r, _, _ := k.NewProc("GetExitCodeProcess").Call(h, uintptr(unsafe.Pointer(&code)))
	if r == 0 {
		return false
	}
	return code == stillActive
}
