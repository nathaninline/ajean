// engine_detect.go — ce qu'AJEAN regarde sur la machine pour choisir la variante
// du moteur épinglé (carte NVIDIA, version CUDA du pilote, ROCm, Vulkan). Les
// sondes elles-mêmes (hasNvidiaGPU, driverCudaVersion, hasVulkanLib…) sont
// celles de la compilation locale et du précompilé.
package ajean

import "runtime"

// engineHW : les seuls faits matériels dont dépend le choix de la variante.
// Une structure (et non des appels directs) pour que engineCandidates reste une
// fonction pure, testable pour chaque système sans la machine correspondante.
type engineHW struct {
	OS, Arch   string
	Nvidia     bool
	DriverCuda float64 // 0 = inconnue
	ROCm       bool
	Vulkan     bool
}

func detectEngineHW() engineHW {
	hw := engineHW{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if hw.OS == "darwin" {
		return hw
	}
	hw.Nvidia = hasNvidiaGPU()
	if hw.Nvidia {
		hw.DriverCuda = driverCudaVersion()
	}
	switch hw.OS {
	case "windows":
		// Vulkan est fourni par les pilotes AMD, Intel et NVIDIA : on le tente
		// toujours, la sonde écarte la variante si aucune carte ne répond.
		hw.Vulkan = true
	default:
		hw.ROCm = hasTool("hipcc") || isDir("/opt/rocm")
		hw.Vulkan = hw.Nvidia || hasTool("vulkaninfo") || hasVulkanLib()
	}
	return hw
}
