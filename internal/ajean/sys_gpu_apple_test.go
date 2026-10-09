package ajean

import (
	"runtime"
	"testing"
)

// Sortie réelle (abrégée) de `ioreg -r -c IOAccelerator -d 1` sur un M1 Pro avec
// un modèle de 17 Go chargé par llama-server : c'est « Alloc system memory » qui
// reflète cette occupation, pas « In use system memory » (qui retombe près de 0
// dès que le GPU est inactif).
const ioregM1Pro = `+-o AGXAcceleratorG13X  <class AGXAcceleratorG13X, id 0x100000971, registered, matched, active, busy 0 (888 ms), retain 45>
    {
      "IOMatchedAtBoot" = Yes
      "vendor-id" = <6b100000>
      "MetalPluginClassName" = "AGXG13XDevice"
      "AGCInfo" = {"fLastSubmissionPID"=615,"fSubmissionsSinceLastCheck"=0,"fBusyCount"=0}
      "PerformanceStatistics" = {"In use system memory (driver)"=0,"Alloc system memory"=21343043584,"Tiler Utilization %"=22,"Renderer Utilization %"=20,"Device Utilization %"=22,"Allocated PB Size"=55181312,"In use system memory"=507330560}
      "gpu-core-count" = 14
      "IOClass" = "AGXAcceleratorG13X"
    }
`

func TestParseIOAccelerator(t *testing.T) {
	s, ok := parseIOAccelerator(ioregM1Pro)
	if !ok {
		t.Fatal("statistiques non trouvées")
	}
	if s.allocBytes != 21343043584 {
		t.Errorf("mémoire allouée mal lue : %d", s.allocBytes)
	}
	if s.util != 22 {
		t.Errorf("utilisation mal lue : %d", s.util)
	}
	if s.cores != 14 {
		t.Errorf("cœurs mal lus : %d", s.cores)
	}
}

// Sans PerformanceStatistics (sortie vide, autre matériel), ok=false : on ne
// doit surtout pas renvoyer une carte à zéro.
func TestParseIOAcceleratorRien(t *testing.T) {
	for _, out := range []string{"", "+-o Something\n    {\n      \"IOClass\" = \"X\"\n    }\n"} {
		if _, ok := parseIOAccelerator(out); ok {
			t.Errorf("ok=true pour %q", out)
		}
	}
}

// Sur un vrai Mac Apple Silicon, la carte doit être cohérente avec la RAM.
func TestAppleGPUsSurMachine(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Apple Silicon uniquement")
	}
	gpus := appleGPUs()
	if len(gpus) != 1 {
		t.Fatalf("%d carte(s), attendu 1 : %v", len(gpus), gpus)
	}
	g := gpus[0]
	used, total := g["used"].(int), g["total"].(int)
	if total <= 0 || used < 0 || used > total {
		t.Errorf("mémoire incohérente : %d / %d", used, total)
	}
	if g["unified"] != true || g["name"] == "" {
		t.Errorf("carte mal formée : %v", g)
	}
}
