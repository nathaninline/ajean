//go:build windows

package ajean

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
// `ajean serve` that this process launches detached. We track it with a PID file
// and stream its output to a log file under AJEAN_HOME. This needs no admin
// rights and no external tools beyond the always-present tasklist/taskkill.

const (
	createNewProcessGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	detachedProcess       = 0x00000008 // DETACHED_PROCESS
	createNoWindow        = 0x08000000 // CREATE_NO_WINDOW (aucune console pour l'enfant)
)

func pidFilePath() string { return filepath.Join(AjeanHome(), serviceName()+".pid") }

// engineLogInUse = chemin RÉELLEMENT écrit pour le journal du moteur. Vide tant
// qu'aucun démarrage n'a résolu le chemin ; renseigné par openEngineLog, qui bascule
// sur un repli si le journal principal n'est pas accessible en écriture (issue #88 :
// un `.log` créé par un `ajean start` élevé appartient à l'admin, et l'ajean-ui non
// élevé ne peut plus y écrire → l'ouverture échouait et bloquait tout le démarrage).
var engineLogInUse string

func logFilePath() string {
	if engineLogInUse != "" {
		return engineLogInUse
	}
	return filepath.Join(AjeanHome(), serviceName()+".log")
}

// openEngineLog ouvre le journal du moteur en écriture, avec repli : d'abord dans
// AJEAN_HOME, sinon dans le dossier temporaire de l'utilisateur (toujours accessible).
// Renvoie nil si AUCUN chemin n'est ouvrable — dans ce cas le moteur démarre quand
// même, simplement sans journal redirigé : ne jamais faire échouer le démarrage juste
// parce qu'on n'a pas pu ouvrir un fichier de log.
func openEngineLog() *os.File {
	primary := filepath.Join(AjeanHome(), serviceName()+".log")
	if err := os.MkdirAll(AjeanHome(), 0o755); err == nil {
		if f, err := os.OpenFile(primary, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			engineLogInUse = primary
			return f
		}
	}
	fb := filepath.Join(os.TempDir(), serviceName()+".log")
	if f, err := os.OpenFile(fb, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		engineLogInUse = fb
		return f
	}
	engineLogInUse = ""
	return nil
}

func serviceAction(action string) error {
	// API Externe : aucun moteur local (arrêt). GPU Cloud : relais local vers le GPU.
	action = cloudServiceAction(action)
	switch action {
	case "start":
		return svcStart()
	case "stop":
		return svcStop(true)
	case "restart":
		// Un stop refusé (moteur lancé en admin, restart sans élévation) laissait
		// l'ancien moteur tourner ; on le signale au lieu de faire comme si (#80).
		if err := svcStop(false); err != nil {
			return err
		}
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
	if err := os.MkdirAll(AjeanHome(), 0o755); err != nil {
		return err
	}
	// Journal résilient : un échec d'ouverture ne DOIT PAS empêcher le moteur de
	// démarrer (issue #88). openEngineLog bascule sur un repli, ou renvoie nil (aucun
	// journal) — dans ce cas la sortie de l'enfant est simplement ignorée.
	logf := openEngineLog()
	if logf != nil {
		defer logf.Close()
	}

	cmd := exec.Command(self, "serve")
	if logf != nil {
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	cmd.Dir = AjeanHome() // les chemins relatifs de config.env se résolvent depuis AJEAN_HOME
	// createNoWindow + HideWindow : le service enfant (`ajean serve`) ne doit JAMAIS
	// faire clignoter de console noire quand AJEAN est lancé en mode app (double-clic).
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | detachedProcess | createNoWindow,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("démarrage de 'ajean serve': %w", err)
	}
	pid := cmd.Process.Pid
	// Écriture du PID en best-effort : si le dossier n'est pas accessible en écriture,
	// on n'abandonne PAS un moteur déjà lancé (issue #88) — on avertit seulement, quitte
	// à ce que le suivi (stop/status) soit dégradé jusqu'au prochain démarrage propre.
	if err := os.WriteFile(pidFilePath(), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		fmt.Printf("%s PID non enregistré (%v) — le moteur tourne (PID %d) mais stop/status peuvent être imprécis\n", yellow("[avert]"), err, pid)
	}
	// Don't wait — let it run detached.
	_ = cmd.Process.Release()
	return checkStarted(pid)
}

func checkStarted(pid int) error {
	time.Sleep(2 * time.Second)
	if processAlive(pid) {
		fmt.Printf("%s %s: démarré (PID %d)\n", green("[ok]"), serviceName(), pid)
		fmt.Printf("       logs: %s  (ajean logs pour suivre)\n", dim(logFilePath()))
		return nil
	}
	fmt.Printf("%s %s: le processus s'est arrêté — derniers logs :\n", red("[ERREUR]"), serviceName())
	fmt.Println("------------------------------------------------")
	fmt.Print(tailFile(logFilePath(), 20))
	fmt.Println("------------------------------------------------")
	fmt.Printf("→ ajean logs   pour plus de détails\n→ ajean edit   pour corriger config.env\n")
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

// serviceLogTail renvoie les n dernières lignes du journal du service (pour
// l'UI web).
func serviceLogTail(n int) string { return tailFile(logFilePath(), n) }
