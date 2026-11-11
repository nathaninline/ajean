// engine_detect.go — ce qu'AJEAN regarde sur la machine pour choisir la variante
// du moteur (carte NVIDIA, version CUDA du pilote, ROCm, Vulkan), et quelques
// utilitaires de fichiers partagés.
package ajean

import (
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func hasTool(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func hasNvidiaGPU() bool {
	if !hasTool("nvidia-smi") {
		return false
	}
	out, err := hideCmd(exec.Command("nvidia-smi", "-L")).Output()
	return err == nil && strings.Contains(string(out), "GPU")
}

// driverCudaVersion renvoie la version CUDA max supportée par le pilote NVIDIA
// (bandeau de nvidia-smi : « CUDA Version: 12.8 »), ou 0 si inconnue.
func driverCudaVersion() float64 {
	out, err := hideCmd(exec.Command("nvidia-smi")).Output()
	if err != nil {
		return 0
	}
	m := regexp.MustCompile(`CUDA Version:\s*([0-9]+\.[0-9]+)`).FindSubmatch(out)
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseFloat(string(m[1]), 64)
	return v
}

// hasVulkanLib détecte la présence du loader Vulkan (libvulkan) sans supposer un
// unique chemin de distribution : Debian le range en multiarch, Fedora/RHEL dans
// /usr/lib64 (issues #28, #29). On interroge d'abord ldconfig (la source de
// vérité du linker dynamique), puis une liste de chemins connus.
func hasVulkanLib() bool {
	if out, err := exec.Command("ldconfig", "-p").Output(); err == nil {
		if strings.Contains(string(out), "libvulkan.so") {
			return true
		}
	}
	for _, p := range []string{
		"/usr/lib/x86_64-linux-gnu/libvulkan.so.1", // Debian/Ubuntu multiarch
		"/usr/lib/aarch64-linux-gnu/libvulkan.so.1",
		"/usr/lib64/libvulkan.so.1", // Fedora/RHEL/Bazzite
		"/usr/lib/libvulkan.so.1",   // Arch et divers
		"/lib64/libvulkan.so.1",
	} {
		if isFile(p) {
			return true
		}
	}
	return false
}

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
