//go:build unix

package ajean

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// defaultAjeanHome est la racine des données quand ni $AJEAN_HOME ni
// /etc/default/ajean n'en imposent une.
//
// Linux : /etc/ajean (machine de prod, service systemd lancé par root).
// macOS : /etc/ajean n'est PAS écrivable par une app lancée depuis le Finder
// (« mkdir /etc/ajean: permission denied »), et un Mac est une machine de bureau,
// pas un serveur — on retombe donc sur le dossier utilisateur standard, sauf si
// un `sudo ajean install` a déjà créé /etc/ajean à notre nom.
func defaultAjeanHome() string { return unixHome("ajean") }

func unixHome(name string) string {
	if runtime.GOOS != "darwin" || os.Geteuid() == 0 {
		return "/etc/" + name
	}
	if isWritableDir("/etc/" + name) {
		return "/etc/" + name // installé par `sudo ajean install` puis chown à l'utilisateur
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Library", "Application Support", name)
	}
	return "/etc/" + name
}

// isWritableDir teste qu'un dossier existe ET qu'on peut y écrire (le seul test
// fiable : les permissions POSIX seules ignorent ACL, SIP, volumes read-only).
func isWritableDir(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	probe := filepath.Join(dir, ".ajean-write-test")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	f.Close()
	_ = os.Remove(probe)
	return true
}

// hideCmd : no-op sur Unix (pas de fenêtre de console à masquer).
func hideCmd(cmd *exec.Cmd) *exec.Cmd { return cmd }

// openBrowser ouvre l'URL dans le navigateur par défaut (macOS: open, Linux:
// xdg-open). Best-effort.
func openBrowser(url string) error {
	bin := "xdg-open"
	if runtime.GOOS == "darwin" {
		bin = "open"
	}
	return exec.Command(bin, url).Start()
}

// totalRAMGB renvoie la RAM physique totale en Go (Linux: /proc/meminfo,
// macOS: sysctl hw.memsize).
func totalRAMGB() float64 {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err != nil {
			return 0
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); err == nil {
			return v / (1024 * 1024 * 1024)
		}
		return 0
	}
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				if kb, err := strconv.ParseFloat(f[1], 64); err == nil {
					return kb / (1024 * 1024) // kB → Go
				}
			}
		}
	}
	return 0
}

// setLibraryPath ensures llama-server can load shared libs bundled next to the
// binary by prepending dir to LD_LIBRARY_PATH. It also appends the CUDA runtime
// lib directories: a CUDA-enabled build links against libcudart/libcublas, which
// live under /usr/local/cuda*/lib64 and are often absent from the global ld
// cache — without them llama-server fails to load the GPU backend (or runs
// degraded), costing a large chunk of throughput.
// Sur macOS, dyld IGNORE LD_LIBRARY_PATH : la variable équivalente est
// DYLD_LIBRARY_PATH. Les binaires macOS des releases llama.cpp sont livrés avec
// leurs libllama/libggml*.dylib À CÔTÉ de l'exécutable — sans cette variable,
// llama-server meurt instantanément sur « Library not loaded », donc sans le
// moindre message dans l'UI.
func setLibraryPath(dir string) {
	envVar, joined := libraryPathValue(dir)
	_ = os.Setenv(envVar, joined)
	if runtime.GOOS == "darwin" {
		// Filet de sécurité : DYLD_FALLBACK_LIBRARY_PATH est consulté en dernier
		// recours et survit à certains contextes où DYLD_LIBRARY_PATH est purgé.
		_ = os.Setenv("DYLD_FALLBACK_LIBRARY_PATH", joined+":/usr/local/lib:/usr/lib")
	}
}

// libraryPathValue calcule (nom de variable, valeur) sans toucher à
// l'environnement du process : utilisé pour lancer un llama-server ÉPHÉMÈRE
// (ex. `--list-devices` depuis l'UI) sans polluer — ni faire grossir à chaque
// appel — le LD_LIBRARY_PATH du serveur web lui-même.
func libraryPathValue(dir string) (string, string) {
	parts := []string{dir}
	parts = append(parts, cudaLibDirs()...)
	envVar := "LD_LIBRARY_PATH"
	if runtime.GOOS == "darwin" {
		envVar = "DYLD_LIBRARY_PATH"
	}
	if existing := os.Getenv(envVar); existing != "" {
		parts = append(parts, existing)
	}
	return envVar, strings.Join(parts, ":")
}

// libraryPathEnv renvoie un environnement complet (os.Environ + la variable de
// recherche de bibliothèques) pour une commande ponctuelle.
func libraryPathEnv(dir string) []string {
	k, v := libraryPathValue(dir)
	return append(os.Environ(), k+"="+v)
}

// cudaLibDirs returns the CUDA runtime lib directories present on the machine,
// preferring the highest-versioned install. Empty when no CUDA toolkit is found.
func cudaLibDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d != "" && !seen[d] && isDir(d) {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	// Default symlink first (usually points at the active toolkit).
	add("/usr/local/cuda/lib64")
	add("/usr/local/cuda/targets/x86_64-linux/lib")
	// Installs versionnés, du plus récent au plus ancien (tri sémantique : le plus
	// récent doit primer, et cuda-12.10 > cuda-12.4, ce qu'un tri de chaînes rate).
	versioned, _ := filepath.Glob("/usr/local/cuda-*/lib64")
	sortByVersionDesc(versioned)
	for _, d := range versioned {
		add(d)
	}
	// Toolkit hors /usr/local (Arch : /opt/cuda, ou CUDA_PATH / CUDA_HOME).
	if nvcc := unixCudaToolkitNvcc(); nvcc != "" {
		root := filepath.Dir(filepath.Dir(nvcc))
		add(filepath.Join(root, "lib64"))
		add(filepath.Join(root, "targets", "x86_64-linux", "lib"))
	}
	return dirs
}

// newShellCmd builds the command used by the run_shell tool. The second return
// value is a cleanup func the caller must defer (a no-op here; on Windows it
// removes the temporary .bat file — see the Windows twin).
func newShellCmd(ctx context.Context, command string) (*exec.Cmd, func()) {
	return exec.CommandContext(ctx, "/bin/bash", "-c", command), func() {}
}

// ramUsageMB renvoie (utilisée, totale) en Mo pour /api/ram. Linux : /proc/meminfo.
// macOS : sysctl pour le total, vm_stat pour ce qui est réellement libre (pages
// free + inactive + speculative ; le reste — wired, active, compressé — est
// considéré occupé, comme le fait le Moniteur d'activité).
func ramUsageMB() (used, total int) {
	if runtime.GOOS == "darwin" {
		total = int(totalRAMGB() * 1024)
		if total == 0 {
			return 0, 0
		}
		out, err := exec.Command("vm_stat").Output()
		if err != nil {
			return 0, total
		}
		pageSize := 4096
		freePages := 0
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Mach Virtual Memory Statistics") {
				// « … (page size of 16384 bytes) » — l'Apple Silicon n'est pas en 4 Ko.
				if i := strings.Index(line, "page size of "); i >= 0 {
					if v, err := strconv.Atoi(strings.Fields(line[i+len("page size of "):])[0]); err == nil {
						pageSize = v
					}
				}
				continue
			}
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "Pages free", "Pages inactive", "Pages speculative":
				if n, err := strconv.Atoi(strings.Trim(strings.TrimSpace(v), ".")); err == nil {
					freePages += n
				}
			}
		}
		freeMB := freePages * pageSize / (1024 * 1024)
		if freeMB > total {
			freeMB = total
		}
		return total - freeMB, total
	}
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	var totalKB, availKB int
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.Atoi(f[1]) // kB
		switch f[0] {
		case "MemTotal:":
			totalKB = v
		case "MemAvailable:":
			availKB = v
		}
	}
	if totalKB == 0 {
		return 0, 0
	}
	return (totalKB - availKB) / 1024, totalKB / 1024
}

// --- Supervision de processus détachés (worker de lien, service en mode
// utilisateur). Pendant Unix de sys_platform_windows.go.

// spawnDetached prépare une commande qui survivra à la mort de AJEAN : Setsid la
// place dans une nouvelle session, donc elle n'est pas tuée avec notre groupe.
func spawnDetached(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// pidAlive : le signal 0 ne tue rien, il teste juste l'existence du process.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// killTree arrête le process ET ses enfants. Setsid ayant fait de lui un chef de
// groupe, un PID négatif vise le groupe entier ; SIGKILL en dernier recours.
func killTree(pid int) {
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	for i := 0; i < 30 && pidAlive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if pidAlive(pid) {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}
