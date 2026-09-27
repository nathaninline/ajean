package ajean

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Issue #92 : le Vulkan SDK Windows n'est retenu que s'il est COMPLET, faute de
// repli CPU quand un build GPU casse.
func TestVulkanSDKComplete(t *testing.T) {
	sdk, sys := t.TempDir(), t.TempDir()
	touch := func(p string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if vulkanSDKComplete(sdk, sys) || vulkanSDKComplete("", sys) {
		t.Fatal("SDK vide accepté")
	}
	touch(filepath.Join(sdk, "Include", "vulkan", "vulkan.h"))
	touch(filepath.Join(sdk, "Lib", "vulkan-1.lib"))
	touch(filepath.Join(sdk, "Bin", "glslc.exe"))
	if vulkanSDKComplete(sdk, sys) {
		t.Fatal("accepté sans le loader vulkan-1.dll du pilote")
	}
	touch(filepath.Join(sys, "System32", "vulkan-1.dll"))
	if !vulkanSDKComplete(sdk, sys) {
		t.Fatal("SDK complet refusé")
	}
	if err := os.Remove(filepath.Join(sdk, "Lib", "vulkan-1.lib")); err != nil {
		t.Fatal(err)
	}
	if vulkanSDKComplete(sdk, sys) {
		t.Fatal("accepté sans la bibliothèque d'import")
	}
}

func TestCpuPlanHints(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("pas de conseil sous macOS (Metal)")
	}
	defer func(f func() []string) { displayGPUs = f }(displayGPUs)
	cpu := buildPlan{backend: "cpu"}

	displayGPUs = func() []string { return []string{"AMD Radeon RX 7600 XT"} }
	h := cpuPlanHints(cpu)
	if len(h) < 2 || !strings.Contains(strings.Join(h, " "), "--backend=vulkan") {
		t.Fatalf("GPU AMD en CPU : pas de conseil Vulkan : %q", h)
	}
	if cpuPlanHints(buildPlan{backend: "cpu", forced: true}) != nil {
		t.Fatal("conseil affiché alors que le CPU a été imposé")
	}
	if cpuPlanHints(buildPlan{backend: "vulkan"}) != nil {
		t.Fatal("conseil affiché alors qu'un GPU est déjà utilisé")
	}
	displayGPUs = func() []string { return []string{"NVIDIA GeForce RTX 3070"} }
	if h := strings.Join(cpuPlanHints(cpu), " "); !strings.Contains(h, "CUDA") {
		t.Fatalf("GPU NVIDIA en CPU : pas de conseil CUDA : %q", h)
	}
	for _, none := range [][]string{nil, {"Microsoft Basic Display Adapter"}, {"Parallels Display Adapter (search)"}} {
		displayGPUs = func() []string { return none }
		if h := cpuPlanHints(cpu); h != nil {
			t.Fatalf("conseil sans carte dédiée (%q) : %q", none, h)
		}
	}
	displayGPUs = func() []string { return []string{"Intel(R) Arc(TM) A770 Graphics"} }
	if cpuPlanHints(cpu) == nil {
		t.Fatal("Intel Arc non reconnue")
	}
}

// « --backend vulkan » (forme à espace) doit être compris comme « --backend=vulkan ».
func TestLlamacppInstallBackendArg(t *testing.T) {
	err := llamacppInstall([]string{"--backend"})
	if err == nil || !strings.Contains(err.Error(), "attend une valeur") {
		t.Fatalf("--backend sans valeur : %v", err)
	}
	err = llamacppInstall([]string{"--backend", "pouet"})
	if err == nil || !strings.Contains(err.Error(), "backend inconnu: pouet") {
		t.Fatalf("la forme à espace n'est pas lue comme --backend=pouet : %v", err)
	}
	err = llamacppInstall([]string{"--backend", "--force"})
	if err == nil || !strings.Contains(err.Error(), "attend une valeur") {
		t.Fatalf("--backend suivi d'une option : %v", err)
	}
}
