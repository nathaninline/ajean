//go:build unix

package jean

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
)

// defaultJeanHome is the data root when neither $JEAN_HOME nor /etc/default/jean
// override it.
func defaultJeanHome() string { return "/etc/jean" }

// defaultEditor is used by `jean edit` when $EDITOR is unset.
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

// launchedByDoubleClick : sur Unix, jean tourne en service/CLI, on ne déclenche
// jamais le mode app sur un simple no-arg (éviterait de surprendre un serveur).
// L'expérience app double-clic est propre à Windows/macOS (coque native, phase 3).
func launchedByDoubleClick() bool { return false }

// setLibraryPath ensures llama-server can load shared libs bundled next to the
// binary by prepending dir to LD_LIBRARY_PATH. It also appends the CUDA runtime
// lib directories: a CUDA-enabled build links against libcudart/libcublas, which
// live under /usr/local/cuda*/lib64 and are often absent from the global ld
// cache — without them llama-server fails to load the GPU backend (or runs
// degraded), costing a large chunk of throughput.
func setLibraryPath(dir string) {
	parts := []string{dir}
	parts = append(parts, cudaLibDirs()...)
	if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
		parts = append(parts, existing)
	}
	_ = os.Setenv("LD_LIBRARY_PATH", strings.Join(parts, ":"))
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

// newShellCmd builds the command used by the run_shell tool.
func newShellCmd(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/bash", "-c", command)
}
