//go:build unix

package ajean

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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

// defaultEditor is used by `ajean edit` when $EDITOR is unset.
func defaultEditor() string { return "nano" }

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
	// Versioned installs, newest last so it takes precedence in PATH order.
	versioned, _ := filepath.Glob("/usr/local/cuda-*/lib64")
	sort.Strings(versioned)
	for i := len(versioned) - 1; i >= 0; i-- {
		add(versioned[i])
	}
	return dirs
}

// autoInstallTool installs a missing build tool with the system package manager
// (apt/dnf/pacman on Linux, brew on macOS). Best-effort: returns an error when no
// known manager is available or the install fails. Uses sudo on Linux when not
// already root (brew refuses to run as root).
func autoInstallTool(name string) error {
	managers := []struct {
		bin     string
		install []string // args before the package name
	}{
		{"apt-get", []string{"install", "-y"}},
		{"dnf", []string{"install", "-y"}},
		{"pacman", []string{"-S", "--noconfirm"}},
		{"brew", []string{"install"}},
	}
	for _, m := range managers {
		if _, err := exec.LookPath(m.bin); err != nil {
			continue
		}
		argv := append(append([]string{m.bin}, m.install...), name)
		if m.bin != "brew" && os.Geteuid() != 0 {
			if _, err := exec.LookPath("sudo"); err != nil {
				return fmt.Errorf("%s requiert root (ni root ni sudo disponibles)", m.bin)
			}
			argv = append([]string{"sudo"}, argv...)
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
		return cmd.Run()
	}
	return fmt.Errorf("aucun gestionnaire de paquets connu — installe %s manuellement", name)
}

// msvcGenerator is Windows-only; on Unix the default CMake generator (Unix
// Makefiles) is correct, so detectBuildPlan never calls this for real. Present
// only so the shared code compiles.
func msvcGenerator() string { return "" }

// ensureCompiler makes sure a C/C++ toolchain (cc + c++ + make) is present,
// installing it via the system package manager when missing. build-essential on
// Debian/Ubuntu pulls the lot; elsewhere we fall back to individual packages.
func ensureCompiler() error {
	haveCC := hasTool("cc") || hasTool("gcc") || hasTool("clang")
	haveCXX := hasTool("c++") || hasTool("g++") || hasTool("clang++")
	if haveCC && haveCXX && hasTool("make") {
		return nil
	}
	candidates := []string{"build-essential", "gcc", "g++", "make"}
	if _, err := exec.LookPath("apt-get"); err != nil {
		// Non-Debian: build-essential n'existe pas, on vise les paquets directs.
		candidates = []string{"gcc", "gcc-c++", "make"}
	}
	fmt.Println(yellow("[info]") + " compilateur C/C++ absent — installation via le gestionnaire de paquets…")
	for _, pkg := range candidates {
		_ = autoInstallTool(pkg) // best-effort, paquets variables selon la distro
	}
	if (hasTool("cc") || hasTool("gcc")) && (hasTool("c++") || hasTool("g++")) && hasTool("make") {
		fmt.Println(green("✓") + " compilateur C/C++ prêt.")
		return nil
	}
	return fmt.Errorf("compilateur C/C++ introuvable — installe gcc/g++/make (ou build-essential) manuellement")
}

// cudaPathEnv is Windows-specific (the MSBuild CUDA integration needs CUDA_PATH);
// on Unix the Makefiles/Ninja generators find nvcc via PATH/CUDACXX, so there's
// nothing extra to inject.
func cudaPathEnv(toolkitDir string) []string { return nil }

// ensureCudaVSIntegration is Windows-specific (MSBuild CUDA integration check);
// no-op on Unix.
func ensureCudaVSIntegration(toolkitDir string) error { return nil }

// ensureAccelerator is a no-op on Unix: CUDA/ROCm toolkits are installed through
// the distro (their layout is already probed by findNvcc / detectBuildPlan), and
// auto-installing multi-GB GPU toolkits across distros is too varied to do safely.
func ensureAccelerator() {}

// refreshToolPath is a no-op on Unix: package managers install into directories
// already on PATH (/usr/bin, /usr/local/bin), unlike Windows.
func refreshToolPath() {}

// execServer replaces the current process with llama-server (so systemd
// supervises llama-server directly, as the old start.sh did with `exec`).
// args[0] must be the binary path.
func execServer(bin string, args []string) error {
	return syscall.Exec(bin, args, os.Environ())
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
