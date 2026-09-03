//go:build windows

// web_procmem_windows.go — combien de RAM ajean lui-même (et llama-server,
// son moteur) utilisent réellement, par opposition au total système déjà
// affiché par ramUsageMB(). Demandé après usage réel : le panneau Mémoire
// vive dit "18,8 / 63,7 Gio, 29 % utilisée" mais jamais la part qui revient à
// ajean — impossible de savoir si une machine qui sature est due au modèle
// chargé ou à autre chose qui tourne à côté.
package ajean

import (
	"context"
	"encoding/json"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

// processRAM regroupe les deux mesures ; chacune a son propre indicateur de
// disponibilité — best-effort total, comme le reste de la télémétrie Windows
// de ce projet (voir web_devices_adl_windows.go).
type processRAM struct {
	AjeanMiB int
	HasAjean bool
	LlamaMiB int
	HasLlama bool
}

// processMemoryCounters reflète PROCESS_MEMORY_COUNTERS (psapi.h). Layout
// exact requis : GetProcessMemoryInfo écrit dans ce buffer selon cette forme
// précise, pas de champ en trop ni de réordonnancement.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

var (
	procmemPsapi                = syscall.NewLazyDLL("psapi.dll")
	procmemKernel32             = syscall.NewLazyDLL("kernel32.dll")
	procmemGetCurrentProcess    = procmemKernel32.NewProc("GetCurrentProcess")
	procmemGetProcessMemoryInfo = procmemPsapi.NewProc("GetProcessMemoryInfo")
)

// ajeanWorkingSetMiB lit la RAM du processus COURANT (working set — la même
// mesure que le Gestionnaire des tâches sous "Mémoire") via un appel direct,
// sans sous-processus : GetCurrentProcess() renvoie un pseudo-handle toujours
// valide pour l'appelant, jamais besoin de le fermer.
func ajeanWorkingSetMiB() (int, bool) {
	h, _, _ := procmemGetCurrentProcess.Call()
	var pmc processMemoryCounters
	pmc.cb = uint32(unsafe.Sizeof(pmc))
	r, _, _ := procmemGetProcessMemoryInfo.Call(h, uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.cb))
	if r == 0 {
		return 0, false
	}
	return int(pmc.workingSetSize / (1024 * 1024)), true
}

// llamaServerWorkingSetMiB cherche llama-server PAR NOM (pas par PID stocké :
// selon comment ajean a été lancé — service détaché, app, ou directement —
// aucun PID de ce processus enfant n'est forcément accessible depuis ici, et
// le chercher par nom marche dans tous les cas). Un seul appel PowerShell,
// même style que windowsAdapterStats (web_devices.go) : pas de nouvelle
// primitive d'énumération de process à écrire et maintenir pour ça seul.
func llamaServerWorkingSetMiB() (int, bool) {
	const script = `$p = Get-Process -Name llama-server -ErrorAction SilentlyContinue | Select-Object -First 1; ` +
		`if ($p) { [PSCustomObject]@{mib=[int][math]::Round($p.WorkingSet64/1MB)} | ConvertTo-Json -Compress }`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := hideCmd(exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)).Output()
	if err != nil || len(out) == 0 {
		return 0, false
	}
	var parsed struct {
		Mib int `json:"mib"`
	}
	if json.Unmarshal(out, &parsed) != nil {
		return 0, false
	}
	return parsed.Mib, true
}

func currentProcessRAM() processRAM {
	var r processRAM
	r.AjeanMiB, r.HasAjean = ajeanWorkingSetMiB()
	r.LlamaMiB, r.HasLlama = llamaServerWorkingSetMiB()
	return r
}
