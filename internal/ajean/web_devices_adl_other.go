//go:build !windows

package ajean

// adlAMDHotspotTempC is a no-op off Windows: ADL (atiadlxx.dll) doesn't exist
// there, and Linux/macOS already get real GPU temperature through amd-smi /
// rocm-smi (see backend_gpu_amd.go, backend_gpu_rocm.go), so this fallback
// isn't needed outside Windows.
func adlAMDHotspotTempC() (int, bool) { return 0, false }
