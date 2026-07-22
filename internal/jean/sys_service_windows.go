//go:build windows

package jean

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// On Windows there is no systemd, so "the service" is a background copy of
// `jean serve` that this process launches detached. We track it with a PID file
// and stream its output to a log file under JEAN_HOME. This needs no admin
// rights and no external tools beyond the always-present tasklist/taskkill.

const (
	createNewProcessGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	detachedProcess       = 0x00000008 // DETACHED_PROCESS
)

func pidFilePath() string { return filepath.Join(JeanHome(), serviceName()+".pid") }
func logFilePath() string { return filepath.Join(JeanHome(), serviceName()+".log") }

func serviceAction(action string) error {
	switch action {
	case "start":
		return svcStart()
	case "stop":
		return svcStop(true)
	case "restart":
		_ = svcStop(false)
		time.Sleep(500 * time.Millisecond)
		return svcStart()
	case "status":
		return svcStatus()
	case "enable", "disable":
		fmt.Printf("%s '%s' n'est pas géré sur Windows (pas de service système).\n", yellow("[info]"), action)
		fmt.Printf("       Pour un démarrage au boot, crée une tâche planifiée ou un service via %s.\n", bold("sc.exe"))
		return nil
	default:
		return fmt.Errorf("action inconnue: %s", action)
	}
}

func svcStart() error {
	if pid := readServicePID(); pid > 0 && processAlive(pid) {
		fmt.Printf("%s déjà démarré (PID %d)\n", yellow("[info]"), pid)
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(JeanHome(), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(logFilePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("ouverture du log %s: %w", logFilePath(), err)
	}
	defer logf.Close()

	cmd := exec.Command(self, "serve")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("démarrage de 'jean serve': %w", err)
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(pidFilePath(), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("écriture du PID: %w", err)
	}
	// Don't wait — let it run detached.
	_ = cmd.Process.Release()
	return checkStarted(pid)
}

func checkStarted(pid int) error {
	time.Sleep(2 * time.Second)
	if processAlive(pid) {
		fmt.Printf("%s %s: démarré (PID %d)\n", green("[ok]"), serviceName(), pid)
		fmt.Printf("       logs: %s  (jean logs pour suivre)\n", dim(logFilePath()))
		return nil
	}
	fmt.Printf("%s %s: le processus s'est arrêté — derniers logs :\n", red("[ERREUR]"), serviceName())
	fmt.Println("------------------------------------------------")
	fmt.Print(tailFile(logFilePath(), 20))
	fmt.Println("------------------------------------------------")
	fmt.Printf("→ jean logs   pour plus de détails\n→ jean edit   pour corriger config.env\n")
	_ = os.Remove(pidFilePath())
	return fmt.Errorf("service %s non démarré", serviceName())
}

func svcStop(verbose bool) error {
	pid := readServicePID()
	if pid <= 0 || !processAlive(pid) {
		_ = os.Remove(pidFilePath())
		if verbose {
			fmt.Println(yellow("[info]") + " aucun service en cours d'exécution")
		}
		return nil
	}
	// taskkill /T tue aussi le processus enfant llama-server.
	cmd := hideCmd(exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("arrêt du PID %d: %w\n%s", pid, err, string(out))
	}
	_ = os.Remove(pidFilePath())
	if verbose {
		fmt.Println(green("[ok]") + " arrêté")
	}
	return nil
}

func svcStatus() error {
	pid := readServicePID()
	if pid > 0 && processAlive(pid) {
		fmt.Printf("%s %s: actif (PID %d)\n", green("[ok]"), serviceName(), pid)
	} else {
		fmt.Printf("%s %s: arrêté\n", yellow("[info]"), serviceName())
	}
	fmt.Printf("  logs   : %s\n", logFilePath())
	fmt.Printf("  config : %s\n", confPath())
	return nil
}

func serviceLogs() error {
	path := logFilePath()
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("aucun log à %s (le service a-t-il déjà démarré ?): %w", path, err)
	}
	defer f.Close()
	// Print the tail, then follow appended bytes (poor man's tail -f).
	fmt.Print(tailFile(path, 80))
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			os.Stdout.Write(buf[:n])
		}
		if err == io.EOF {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if err != nil {
			return err
		}
	}
}

// serviceIsActive reports whether the background server is running.
func serviceIsActive() bool {
	pid := readServicePID()
	return pid > 0 && processAlive(pid)
}

func readServicePID() int {
	b, err := os.ReadFile(pidFilePath())
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

// processAlive reports whether a PID is currently running. On utilise l'API
// Win32 (OpenProcess + GetExitCodeProcess) plutôt que `tasklist` : l'ancien
// appel externe faisait CLIGNOTER une fenêtre de console à CHAQUE vérification,
// or l'UI web poll le statut en boucle → rafale de fenêtres noires. L'API ne
// lance aucun process.
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

// tailFile returns the last n lines of the file at path (best-effort).
func tailFile(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n") + "\n"
}
