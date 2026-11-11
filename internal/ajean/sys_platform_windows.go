//go:build windows

package ajean

import (
	"context"
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
