//go:build !windows

package ajean

// processRAM mirrors the Windows-only version in web_procmem_windows.go (kept
// in sync manually — mutually exclusive build tags mean only one of the two
// ever compiles for a given OS).
type processRAM struct {
	AjeanMiB int
	HasAjean bool
	LlamaMiB int
	HasLlama bool
}

// currentProcessRAM is a no-op off Windows: per-process working-set memory
// isn't read anywhere else in this codebase on Linux/macOS (would mean
// /proc/self/status parsing on Linux, task_info on macOS — real work, not
// attempted here since it wasn't asked for and this project's other
// Windows-only telemetry already sets the precedent of "richer on Windows,
// silently absent elsewhere").
func currentProcessRAM() processRAM { return processRAM{} }
