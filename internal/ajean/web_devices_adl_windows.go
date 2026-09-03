//go:build windows

// web_devices_adl_windows.go — température GPU AMD réelle sous Windows, via
// l'ADL (AMD Display Library, atiadlxx.dll) chargée directement en mémoire.
//
// Pourquoi : aucun compteur de performance Windows (WMI/perfmon) n'expose la
// température d'un GPU — c'est une donnée que même le Gestionnaire des tâches
// ne lit que depuis une API DirectX privée ajoutée pour lui (Windows 11
// 22H2+), hors de portée d'un simple appel PowerShell (voir
// windowsAdapterStats dans web_devices.go, qui gère VRAM/utilisation par ce
// biais mais pas la température). L'ADL, elle, est l'API officielle qu'AMD
// fournit pour ça — les mêmes fonctions qu'utilisent GPU-Z, HWiNFO, etc.
// Layout des structures vérifié contre le header public d'AMD
// (github.com/GPUOpen-LibrariesAndSDKs/display-library, adl_structures.h /
// adl_defines.h), pas deviné : les fonctions ADL/ADL2 utilisées ici plantent
// silencieusement (ou renvoient n'importe quoi) si la moindre structure a la
// mauvaise taille, donc pas de place pour l'approximation.
//
// Best-effort total : n'importe quelle étape qui échoue (DLL absente, init
// ADL en erreur, capteur non supporté) rend juste "pas de température", jamais
// une erreur qui remonterait à l'appelant.
package ajean

import (
	"sync"
	"syscall"
	"unsafe"
)

var (
	adlKernel32       = syscall.NewLazyDLL("kernel32.dll")
	adlProcLocalAlloc = adlKernel32.NewProc("LocalAlloc")

	adlDLL                 = syscall.NewLazyDLL("atiadlxx.dll")
	adlProcMainCreate      = adlDLL.NewProc("ADL_Main_Control_Create")
	adlProcMainDestroy     = adlDLL.NewProc("ADL_Main_Control_Destroy")
	adlProcNumAdapters     = adlDLL.NewProc("ADL_Adapter_NumberOfAdapters_Get")
	adlProcAdapterInfo     = adlDLL.NewProc("ADL_Adapter_AdapterInfo_Get")
	adlProcMain2Create     = adlDLL.NewProc("ADL2_Main_Control_Create")
	adlProcMain2Destroy    = adlDLL.NewProc("ADL2_Main_Control_Destroy")
	adlProcPMLogStart      = adlDLL.NewProc("ADL2_Adapter_PMLog_Start")
	adlProcQueryPMLogData  = adlDLL.NewProc("ADL2_New_QueryPMLogData_Get")
)

const (
	adlVendorAMD      = 1002 // PCI vendor ID
	adlPMLogMaxSensor = 256
	adlSensorHotspot  = 27 // ADL_PMLOG_TEMPERATURE_HOTSPOT
	adlSensorEdge     = 8  // ADL_PMLOG_TEMPERATURE_EDGE
)

func adlMallocCallback(size uintptr) uintptr {
	ret, _, _ := adlProcLocalAlloc.Call(0 /* LMEM_FIXED */, size)
	return ret
}

// adlMallocCallbackPtr is the ONE native function pointer ADL's malloc
// callback ever uses, for the lifetime of the process. syscall.NewCallback
// hands out a fixed-size, never-freed slot each time it's called (~2000 per
// process on this Go runtime) — calling it fresh inside adlAMDHotspotTempC on
// every invocation exhausted that table after enough polls of /api/vram (the
// UI hits it periodically) and crashed the whole ajean-ui process with an
// access violation deep inside the DLL call. sync.OnceValue makes the actual
// syscall.NewCallback call happen exactly once, no matter how many times
// adlAMDHotspotTempC runs.
var adlMallocCallbackPtr = sync.OnceValue(func() uintptr {
	return syscall.NewCallback(adlMallocCallback)
})

// adlAdapterInfo mirrors ADL's AdapterInfo struct (adl_structures.h) exactly:
// every field is either a 4-byte int or a fixed byte array, so there's no
// hidden padding to get wrong on a 64-bit build.
type adlAdapterInfo struct {
	Size           int32
	AdapterIndex   int32
	UDID           [256]byte
	BusNumber      int32
	DeviceNumber   int32
	FunctionNumber int32
	VendorID       int32
	AdapterName    [256]byte
	DisplayName    [256]byte
	Present        int32
	Exist          int32
	DriverPath     [256]byte
	DriverPathExt  [256]byte
	PNPString      [256]byte
	OSDisplayIndex int32
}

// adlPMLogStartInput mirrors ADLPMLogStartInput.
type adlPMLogStartInput struct {
	Sensors    [adlPMLogMaxSensor]uint16
	SampleRate uint32
	Reserved   [15]int32
}

// adlPMLogStartOutput mirrors ADLPMLogStartOutput (a pointer-sized union
// followed by reserved ints — the union collapses to 8 bytes on x64).
type adlPMLogStartOutput struct {
	LoggingAddress uintptr
	Reserved       [14]int32
}

type adlSingleSensorData struct {
	Supported int32
	Value     int32
}

// adlPMLogDataOutput mirrors ADLPMLogDataOutput: a leading size field, then
// an array indexed DIRECTLY by ADL_PMLOG_SENSORS id (not a sparse id/value
// list) — sensors[adlSensorHotspot] is the hotspot reading, no scanning needed.
type adlPMLogDataOutput struct {
	Size    int32
	Sensors [adlPMLogMaxSensor]adlSingleSensorData
}

// adlCallMu serializes EVERY call into atiadlxx.dll. Required, not defensive
// paranoia: a concurrent-load test (50-80 requests in parallel against
// /api/vram, which is what a UI that polls this endpoint actually produces)
// crashed the whole ajean-ui process with an access violation inside the DLL
// — ajean's HTTP server runs each request on its own goroutine, so two
// requests landing on adlAMDHotspotTempC at the same instant is the normal
// case, not a rare edge case. AMD's ADL predates Go/goroutines by a decade
// and gives no documented thread-safety guarantee; holding this lock for the
// DLL's full create→query→destroy sequence is the only way to guarantee it's
// never entered from two goroutines at once. The calls inside are fast
// (single-digit ms), so serializing them isn't a meaningful bottleneck even
// under the same load that used to crash it.
var adlCallMu sync.Mutex

// adlAMDHotspotTempC returns the junction/hotspot temperature (°C) of the
// first present AMD adapter ADL reports, and whether a real reading was
// obtained. On a machine with more than one physical AMD GPU this only ever
// reports the first one — ADL enumerates one entry per display OUTPUT, not
// per physical card, so reliably telling two real cards apart would need
// deduplicating by bus/device/function, not attempted here since it's not
// this machine's situation.
func adlAMDHotspotTempC() (temp int, ok bool) {
	adlCallMu.Lock()
	defer adlCallMu.Unlock()

	// Ceinture-bretelles : adlDLL.Load() garantit que la DLL existe, PAS que
	// chacune de ces fonctions y est présente. Sur une atiadlxx.dll très ancienne
	// à qui manquerait une des API ADL2 récentes (ex. ADL2_New_QueryPMLogData_Get),
	// LazyProc.Call panique (mustFind). On récupère et on rend « pas de
	// température » plutôt que de laisser remonter la panique. Enregistré avant
	// les defer de destroy ci-dessous, donc il s'exécute APRÈS eux (LIFO) et
	// récupère la panique une fois le nettoyage ADL fait.
	defer func() {
		if recover() != nil {
			temp, ok = 0, false
		}
	}()

	if err := adlDLL.Load(); err != nil {
		return 0, false
	}

	if r, _, _ := adlProcMainCreate.Call(adlMallocCallbackPtr(), 1); int32(r) != 0 {
		return 0, false
	}
	defer adlProcMainDestroy.Call()

	var numAdapters int32
	adlProcNumAdapters.Call(uintptr(unsafe.Pointer(&numAdapters)))
	if numAdapters <= 0 {
		return 0, false
	}
	infos := make([]adlAdapterInfo, numAdapters)
	adlProcAdapterInfo.Call(uintptr(unsafe.Pointer(&infos[0])), uintptr(int(unsafe.Sizeof(adlAdapterInfo{}))*int(numAdapters)))

	var amdIdx int32 = -1
	for i := range infos {
		if infos[i].Exist != 0 && infos[i].VendorID == adlVendorAMD {
			amdIdx = infos[i].AdapterIndex
			break
		}
	}
	if amdIdx < 0 {
		return 0, false
	}

	var ctx uintptr
	if r, _, _ := adlProcMain2Create.Call(adlMallocCallbackPtr(), 1, uintptr(unsafe.Pointer(&ctx))); int32(r) != 0 || ctx == 0 {
		return 0, false
	}
	defer adlProcMain2Destroy.Call(ctx)

	// Ask for the hotspot + edge sensors; 0 (ADL_SENSOR_MAXTYPES) terminates
	// the requested list. Errors here are ignored on purpose: on this driver,
	// a repeat Start (logging already active from a previous call) errors out
	// while the query still returns live data just fine.
	var start adlPMLogStartInput
	start.Sensors[0] = adlSensorHotspot
	start.Sensors[1] = adlSensorEdge
	start.SampleRate = 1000
	var startOut adlPMLogStartOutput
	adlProcPMLogStart.Call(ctx, uintptr(amdIdx), uintptr(unsafe.Pointer(&start)), uintptr(unsafe.Pointer(&startOut)))

	var data adlPMLogDataOutput
	data.Size = int32(unsafe.Sizeof(data))
	if r, _, _ := adlProcQueryPMLogData.Call(ctx, uintptr(amdIdx), uintptr(unsafe.Pointer(&data))); int32(r) != 0 {
		return 0, false
	}
	if hot := data.Sensors[adlSensorHotspot]; hot.Supported != 0 {
		return int(hot.Value), true
	}
	if edge := data.Sensors[adlSensorEdge]; edge.Supported != 0 {
		return int(edge.Value), true
	}
	return 0, false
}
