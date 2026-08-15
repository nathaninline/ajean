//go:build windows

package ajean

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// init makes the Windows console behave like a modern terminal: UTF-8 so the
// Unicode glyphs ajean prints (✓ ▶ …) and child-process output don't turn into
// mojibake (the default OEM codepage, e.g. cp850, renders "✓" as "├ö"), and VT
// processing so the ANSI colour/cursor escapes (including the build progress
// line) are interpreted instead of printed literally. Best-effort: a redirected
// or legacy console just keeps its defaults.
func init() {
	const (
		cpUTF8                          = 65001
		enableVirtualTerminalProcessing = 0x0004
		stdOutputHandle                 = ^uintptr(10) // -11 as DWORD
	)
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	_, _, _ = kernel32.NewProc("SetConsoleOutputCP").Call(uintptr(cpUTF8))
	_, _, _ = kernel32.NewProc("SetConsoleCP").Call(uintptr(cpUTF8))

	getStdHandle := kernel32.NewProc("GetStdHandle")
	getConsoleMode := kernel32.NewProc("GetConsoleMode")
	setConsoleMode := kernel32.NewProc("SetConsoleMode")
	h, _, _ := getStdHandle.Call(stdOutputHandle)
	var mode uint32
	if r, _, _ := getConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode))); r != 0 {
		_, _, _ = setConsoleMode.Call(h, uintptr(mode|enableVirtualTerminalProcessing))
	}
}

// homeRoot is the parent directory of the data root: %ProgramData% (machine-wide,
// the closest analogue to /etc), falling back to %LOCALAPPDATA% for unprivileged
// setups. Ancien et nouveau dossier partagent ce parent, ce qui garantit que la
// migration ajean → ajean est un rename intra-volume (voir sys_migrate.go).
func homeRoot() string {
	if pd := os.Getenv("ProgramData"); pd != "" {
		return pd
	}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		return la
	}
	return os.TempDir()
}

// defaultAjeanHome est la racine des données quand $AJEAN_HOME est absent.
func defaultAjeanHome() string { return filepath.Join(homeRoot(), "ajean") }

// defaultEditor is used by `ajean edit` when $EDITOR is unset.
func defaultEditor() string { return "notepad" }

// openBrowser ouvre l'URL dans le navigateur par défaut. rundll32 évite les
// pièges de quoting de `cmd /c start`.
func openBrowser(url string) error {
	return hideCmd(exec.Command("rundll32", "url.dll,FileProtocolHandler", url)).Start()
}

// hideCmd empêche une commande externe d'ouvrir une fenêtre de console
// (CREATE_NO_WINDOW). Indispensable quand AJEAN tourne sans console (mode app) :
// sinon chaque `nvidia-smi`/`git`/… ferait clignoter une fenêtre noire. Fusionne
// avec les flags déjà présents pour ne pas écraser un éventuel détachement.
func hideCmd(cmd *exec.Cmd) *exec.Cmd {
	const createNoWindow = 0x08000000
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
	return cmd
}

// totalRAMGB renvoie la RAM physique totale en Go (GlobalMemoryStatusEx).
func totalRAMGB() float64 {
	var m struct {
		dwLength                uint32
		dwMemoryLoad            uint32
		ullTotalPhys            uint64
		ullAvailPhys            uint64
		ullTotalPageFile        uint64
		ullAvailPageFile        uint64
		ullTotalVirtual         uint64
		ullAvailVirtual         uint64
		ullAvailExtendedVirtual uint64
	}
	m.dwLength = uint32(unsafe.Sizeof(m))
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	r, _, _ := kernel32.NewProc("GlobalMemoryStatusEx").Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0
	}
	return float64(m.ullTotalPhys) / (1024 * 1024 * 1024)
}

// setLibraryPath ensures llama-server can load its dependent DLLs. Windows
// resolves them via PATH (and the binary's own directory), so we prepend dir.
// For a CUDA build we must also add the CUDA Toolkit's bin: ggml-cuda.dll links
// against cublas64_*/cublasLt64_*.dll which live there, not next to the binary —
// without it the server dies with 0xC0000135 (DLL not found) unless the launching
// shell happened to have CUDA on PATH.
func setLibraryPath(dir string) {
	parts := []string{dir}
	if nvcc := findNvcc(); nvcc != "" {
		binDir := filepath.Dir(nvcc) // …\CUDA\vX.Y\bin
		parts = append(parts, binDir)
		// CUDA 13+ a déplacé les DLL runtime (cublas64_*, cublasLt64_*, cudart64_*)
		// dans bin\x64\ ; sur CUDA 12 elles sont directement dans bin\. On ajoute
		// les deux pour couvrir les deux layouts.
		if x64 := filepath.Join(binDir, "x64"); isDir(x64) {
			parts = append(parts, x64)
		}
	}
	if existing := os.Getenv("PATH"); existing != "" {
		parts = append(parts, existing)
	}
	_ = os.Setenv("PATH", strings.Join(parts, string(os.PathListSeparator)))
}

// libraryPathEnv : même chose pour une commande ponctuelle (ex. --list-devices
// depuis l'UI), sans modifier le PATH du process web.
func libraryPathEnv(dir string) []string {
	parts := []string{dir}
	if nvcc := findNvcc(); nvcc != "" {
		binDir := filepath.Dir(nvcc)
		parts = append(parts, binDir)
		if x64 := filepath.Join(binDir, "x64"); isDir(x64) {
			parts = append(parts, x64)
		}
	}
	if existing := os.Getenv("PATH"); existing != "" {
		parts = append(parts, existing)
	}
	return append(os.Environ(), "PATH="+strings.Join(parts, string(os.PathListSeparator)))
}

// execServer runs llama-server as a child process and waits for it. Windows has
// no exec() that replaces the current image, so `ajean serve` stays alive as the
// parent (this is the detached process the service supervisor tracks).
// args[0] is the binary path; the rest are its arguments.
func execServer(bin string, args []string) error {
	cmd := hideCmd(exec.Command(bin, args[1:]...)) // pas de console pour llama-server (mode app)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

// wingetIDs maps a tool's command name to its winget package ID.
var wingetIDs = map[string]string{
	"git":   "Git.Git",
	"cmake": "Kitware.CMake",
	"ninja": "Ninja-build.Ninja",
}

// autoInstallTool installs a missing build tool via winget (bundled with Windows
// 10/11 and Server 2025). Returns an error if winget is absent or the install
// fails; the caller re-checks availability afterwards.
func autoInstallTool(name string) error {
	if _, err := exec.LookPath("winget"); err != nil {
		return fmt.Errorf("winget introuvable — installe %s manuellement", name)
	}
	id, ok := wingetIDs[name]
	if !ok {
		id = name
	}
	cmd := hideCmd(exec.Command("winget", "install", "--id", id, "-e",
		"--accept-source-agreements", "--accept-package-agreements",
		"--disable-interactivity", "--silent"))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// refreshToolPath reloads the process PATH from the Windows registry (Machine +
// User), so tools just installed by winget become resolvable without restarting
// the shell.
func refreshToolPath() {
	ps := `$m=[Environment]::GetEnvironmentVariable('Path','Machine')
$u=[Environment]::GetEnvironmentVariable('Path','User')
Write-Output ((@($m,$u) | Where-Object { $_ }) -join ';')`
	out, err := hideCmd(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)).Output()
	if err != nil {
		return
	}
	if merged := strings.TrimSpace(string(out)); merged != "" {
		_ = os.Setenv("PATH", merged)
	}
}

// vswherePath returns the location of vswhere.exe, the official tool for
// locating Visual Studio / Build Tools installs. It ships in a fixed spot.
func vswherePath() string {
	base := os.Getenv("ProgramFiles(x86)")
	if base == "" {
		base = os.Getenv("ProgramFiles")
	}
	return filepath.Join(base, "Microsoft Visual Studio", "Installer", "vswhere.exe")
}

// msvcInstallVersion returns the major version of the newest MSVC install that
// has the C++ toolchain (e.g. "17"), or "" if none is found.
func msvcInstallVersion() string {
	vs := vswherePath()
	if _, err := os.Stat(vs); err != nil {
		return ""
	}
	out, err := hideCmd(exec.Command(vs, "-latest", "-products", "*",
		"-requires", "Microsoft.VisualStudio.Component.VC.Tools.x86.x64",
		"-property", "installationVersion")).Output()
	if err != nil {
		return ""
	}
	ver := strings.TrimSpace(string(out))
	if ver == "" {
		return ""
	}
	if i := strings.IndexByte(ver, '.'); i > 0 {
		return ver[:i]
	}
	return ver
}

// msvcGenerator returns the CMake generator name for the installed MSVC, falling
// back to VS 2022 (the version `ensureCompiler` installs).
func msvcGenerator() string {
	switch msvcInstallVersion() {
	case "16":
		return "Visual Studio 16 2019"
	case "15":
		return "Visual Studio 15 2017"
	default:
		return "Visual Studio 17 2022"
	}
}

// ensureCompiler makes sure an MSVC C++ toolchain is available, installing the
// Visual Studio 2022 Build Tools (VCTools workload) via winget if not. This is a
// large download but keeps `ajean llamacpp install` fully unattended on a bare
// Windows box.
func ensureCompiler() error {
	if msvcInstallVersion() != "" {
		return nil
	}
	if _, err := exec.LookPath("winget"); err != nil {
		return fmt.Errorf("compilateur C++ absent et winget introuvable — installe « Visual Studio Build Tools » (charge de travail C++) manuellement")
	}
	fmt.Printf("%s compilateur C++ absent — installation des Build Tools MSVC (gros téléchargement, une seule fois)…\n", yellow("[info]"))
	cmd := hideCmd(exec.Command("winget", "install", "--id", "Microsoft.VisualStudio.2022.BuildTools", "-e",
		"--accept-source-agreements", "--accept-package-agreements",
		"--disable-interactivity",
		"--override", "--quiet --wait --norestart --add Microsoft.VisualStudio.Workload.VCTools --includeRecommended"))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("installation des Build Tools MSVC échouée: %w", err)
	}
	if msvcInstallVersion() == "" {
		return fmt.Errorf("Build Tools installés mais toolchain C++ introuvable — relance la commande ou vérifie l'installation Visual Studio")
	}
	fmt.Printf("%s compilateur C++ prêt.\n", green("✓"))
	return nil
}

// ensureAccelerator installs the CUDA Toolkit when an NVIDIA GPU is present but
// nvcc isn't, so the build can target the GPU instead of falling back to CPU.
// Best-effort: any failure just leaves the machine on the CPU path (the caller
// ignores the return value). The CUDA download is large; we only trigger it when
// a GPU is actually detected.
func ensureAccelerator() {
	if !hasNvidiaGPU() {
		return // pas de GPU NVIDIA visible → rien à installer, build CPU
	}
	if findNvcc() != "" {
		return // toolkit déjà présent
	}
	if _, err := exec.LookPath("winget"); err != nil {
		fmt.Printf("%s GPU NVIDIA détecté mais CUDA Toolkit absent et winget introuvable — build CPU (installe le CUDA Toolkit pour l'accélération GPU)\n", yellow("[info]"))
		return
	}
	fmt.Printf("%s GPU NVIDIA détecté — installation du CUDA Toolkit pour l'accélération GPU (gros téléchargement, une seule fois)…\n", yellow("[info]"))
	cmd := hideCmd(exec.Command("winget", "install", "--id", "Nvidia.CUDA", "-e",
		"--accept-source-agreements", "--accept-package-agreements",
		"--disable-interactivity"))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("%s installation du CUDA Toolkit échouée (%v) — on continue en CPU\n", yellow("[warn]"), err)
		return
	}
	refreshToolPath()
	if findNvcc() != "" {
		fmt.Printf("%s CUDA Toolkit prêt — build GPU activé.\n", green("✓"))
	} else {
		fmt.Printf("%s CUDA Toolkit installé mais nvcc introuvable dans cette session — relance la commande pour activer le GPU\n", yellow("[info]"))
	}
}

// ensureCudaVSIntegration vérifie que l'intégration MSBuild de CUDA (fichiers
// « CUDA x.y.props/targets » dans BuildCustomizations de Visual Studio) est en
// place — sans elle, le générateur Visual Studio échoue sur le cryptique
// « No CUDA toolset found » (CMakeDetermineCompilerId). Cas typiques : CUDA
// installé dans un chemin custom (ex. F:\Cuda) sans cocher « Visual Studio
// Integration », ou VS (ré)installé APRÈS CUDA — l'installeur NVIDIA n'intègre
// que les VS présents au moment où il tourne. On tente d'abord de copier les
// fichiers depuis le toolkit (extras\visual_studio_integration\MSBuildExtensions) ;
// si ça échoue (droits), on explique quoi faire au lieu de laisser l'erreur
// CMake brute. Best-effort : sans vswhere/VS détectable on laisse cmake juger.
func ensureCudaVSIntegration(toolkitDir string) error {
	vs := vswherePath()
	if _, err := os.Stat(vs); err != nil {
		return nil
	}
	out, err := hideCmd(exec.Command(vs, "-latest", "-products", "*",
		"-requires", "Microsoft.VisualStudio.Component.VC.Tools.x86.x64",
		"-property", "installationPath")).Output()
	if err != nil {
		return nil
	}
	installPath := strings.TrimSpace(string(out))
	if installPath == "" {
		return nil
	}
	dsts, _ := filepath.Glob(filepath.Join(installPath, "MSBuild", "Microsoft", "VC", "*", "BuildCustomizations"))
	for _, d := range dsts {
		if m, _ := filepath.Glob(filepath.Join(d, "CUDA *.props")); len(m) > 0 {
			return nil // intégration déjà en place
		}
	}
	// Absente : tentative de réparation depuis le toolkit lui-même.
	src := filepath.Join(toolkitDir, "extras", "visual_studio_integration", "MSBuildExtensions")
	files, _ := filepath.Glob(filepath.Join(src, "*"))
	copied := 0
	for _, dst := range dsts {
		ok := len(files) > 0
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				ok = false
				break
			}
			if err := os.WriteFile(filepath.Join(dst, filepath.Base(f)), b, 0o644); err != nil {
				ok = false
				break
			}
		}
		if ok {
			copied++
		}
	}
	if copied > 0 {
		fmt.Printf("%s intégration Visual Studio de CUDA absente — réparée (fichiers copiés depuis %s)\n", yellow("[fix]"), src)
		return nil
	}
	return fmt.Errorf(`l'intégration Visual Studio de CUDA est absente : aucun fichier « CUDA x.y.props » sous
  %s\MSBuild\Microsoft\VC\<version>\BuildCustomizations
Sans elle, CMake échoue sur « No CUDA toolset found ». Pour corriger, au choix :
  1. relance l'installeur du CUDA Toolkit (installation personnalisée) et coche « CUDA → Visual Studio Integration » — Visual Studio doit déjà être installé à ce moment-là ;
  2. ou copie (en admin) les fichiers de
       %s
     vers le dossier BuildCustomizations ci-dessus, puis relance ajean llamacpp install`, installPath, src)
}

// cudaPathEnv returns the CUDA toolkit env vars the MSBuild CUDA integration
// needs (CUDA_PATH and the version-specific CUDA_PATH_Vx_y), derived from the
// toolkit root, e.g. "...\CUDA\v13.3" → CUDA_PATH_V13_3. Returns NUL-free
// "KEY=VAL" strings.
func cudaPathEnv(toolkitDir string) []string {
	out := []string{"CUDA_PATH=" + toolkitDir}
	// Le dossier se nomme "v13.3" → variable CUDA_PATH_V13_3.
	ver := strings.TrimPrefix(filepath.Base(toolkitDir), "v")
	if ver != "" {
		out = append(out, "CUDA_PATH_V"+strings.ReplaceAll(ver, ".", "_")+"="+toolkitDir)
	}
	return out
}

// newShellCmd builds the command used by the run_shell tool. On Windows the
// command is written to a temporary .bat and run as `cmd /c call "file"` instead
// of `cmd /C "<command>"`.
//
// Pourquoi : passer une commande générée par le modèle en UN seul argument /C
// tombe dans l'enfer du quoting de cmd.exe — guillemets imbriqués, &|<>^, %VAR%,
// accents… se cassent tous, et le modèle enchaîne alors des tentatives inutiles
// (bash, puis PowerShell, puis un fichier Python) avant de s'en sortir. Un .bat
// est lu par l'analyseur batch ligne par ligne, bien plus tolérant ; `chcp 65001`
// remet la sortie en UTF-8 pour les accents. On propage le code de sortie via
// %errorlevel% et on NE supprime PAS le fichier depuis le .bat (un script batch ne
// peut pas se supprimer proprement en cours d'exécution) : le nettoyage se fait en
// Go, dans le cleanup rendu à l'appelant, après la fin du process.
//
// Merci au signalement de la communauté (le workaround « écrire un .bat » venait
// d'un utilisateur Windows).
func newShellCmd(ctx context.Context, command string) (*exec.Cmd, func()) {
	f, err := os.CreateTemp("", "ajean-cmd-*.bat")
	if err != nil {
		// Repli : si le fichier temporaire échoue, on garde l'ancien chemin plutôt
		// que de ne rien lancer. Le quoting peut souffrir, mais la commande part.
		return hideCmd(exec.CommandContext(ctx, "cmd", "/C", command)), func() {}
	}
	batPath := f.Name()
	// Pas de BOM UTF-8 : le placer avant `@echo off` fait échouer cmd.exe
	// (« '∩╗┐@echo' n'est pas reconnu »). chcp suffit pour les accents.
	// \r\n obligatoire : cmd.exe est pointilleux sur les fins de ligne d'un .bat.
	// Fins de ligne normalisées en \r\n : une commande multi-ligne devient
	// plusieurs lignes de batch exécutées l'une après l'autre (cmd.exe exige des
	// \r\n, et un \n seul est mal interprété). C'est un gain net sur `cmd /C`, qui
	// ne sait pas exécuter de commande multi-ligne du tout.
	script := strings.ReplaceAll(command, "\r\n", "\n")
	script = strings.ReplaceAll(script, "\n", "\r\n")
	_, _ = f.WriteString("@echo off\r\n")
	_, _ = f.WriteString("chcp 65001 >nul\r\n")
	_, _ = f.WriteString(script + "\r\n")
	_, _ = f.WriteString("exit /b %errorlevel%\r\n")
	f.Close()
	cleanup := func() { _ = os.Remove(batPath) }
	// `call "chemin"` : les guillemets protègent un TempDir contenant des espaces.
	return hideCmd(exec.CommandContext(ctx, "cmd", "/c", "call", batPath)), cleanup
}

// ramUsageMB renvoie (utilisée, totale) en Mo pour /api/ram (GlobalMemoryStatusEx).
func ramUsageMB() (used, total int) {
	var m struct {
		dwLength                uint32
		dwMemoryLoad            uint32
		ullTotalPhys            uint64
		ullAvailPhys            uint64
		ullTotalPageFile        uint64
		ullAvailPageFile        uint64
		ullTotalVirtual         uint64
		ullAvailVirtual         uint64
		ullAvailExtendedVirtual uint64
	}
	m.dwLength = uint32(unsafe.Sizeof(m))
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	if r, _, _ := kernel32.NewProc("GlobalMemoryStatusEx").Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return 0, 0
	}
	const mb = 1024 * 1024
	return int((m.ullTotalPhys - m.ullAvailPhys) / mb), int(m.ullTotalPhys / mb)
}

// --- Supervision de processus détachés (worker de lien, service). Pendant
// Windows de sys_platform_unix.go.

// spawnDetached prépare une commande détachée et SANS console : en mode app
// (double-clic) le moindre enfant console ferait clignoter une fenêtre noire.
func spawnDetached(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | detachedProcess | createNoWindow,
	}
	return cmd
}

// pidAlive : alias de processAlive (API commune avec Unix).
func pidAlive(pid int) bool { return processAlive(pid) }

// killTree arrête le process et toute sa descendance (taskkill /T).
func killTree(pid int) {
	if pid <= 0 {
		return
	}
	_ = hideCmd(exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")).Run()
}
