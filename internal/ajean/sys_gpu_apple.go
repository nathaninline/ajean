package ajean

import (
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// sys_gpu_apple.go — jauge GPU du panneau Machine sur Mac Apple Silicon.
//
// Un Mac Apple Silicon n'a ni nvidia-smi, ni amd-smi, ni rocm-smi : sampleVramGPUs
// retombait sur le repli « --list-devices du moteur », pensé pour Vulkan sous
// Windows. Sur Metal ce repli est faux trois fois : la mémoire « free » est
// mesurée depuis un llama-server fraîchement lancé qui n'a rien alloué (barre à
// zéro à vie, même avec un modèle de 17 Go chargé à côté), le « total » est la
// limite de travail que Metal s'autorise (~75-80 % de la RAM, pas une VRAM), et
// util/temp sont codés à 0. En prime, ce repli n'est mis en cache que sous
// Windows : sur Mac, l'UI relançait un llama-server (init Metal comprise) toutes
// les 3 s par onglet ouvert.
//
// Le système expose pourtant ce qu'il faut, sans root et sans outil externe :
// `ioreg -r -c IOAccelerator -d 1` renvoie, dans PerformanceStatistics, la
// mémoire unifiée tenue par le GPU tous process confondus (« Alloc system
// memory ») et son taux d'occupation (« Device Utilization % »). La température,
// elle, n'est lisible que par powermetrics, qui exige sudo : on la laisse à 0, et
// l'UI ne l'affiche pas dans ce cas.
//
// Une seule carte est renvoyée, au format de handleVram : la mémoire est
// UNIFIÉE, le « total » est donc la RAM physique (le même que la jauge RAM) et
// la clé unified=true permet à l'UI de le dire.

// ioregPerfStat matche une entrée « "Clé"=valeur » du dictionnaire
// PerformanceStatistics, que ioreg imprime sur une seule ligne :
//
//	"PerformanceStatistics" = {"Alloc system memory"=21343043584,"Device Utilization %"=22,…}
var ioregPerfStat = regexp.MustCompile(`"([^"]+)"=(\d+)`)

// appleGPUStat est ce que l'on retient de la sortie de ioreg.
type appleGPUStat struct {
	allocBytes int64 // mémoire unifiée allouée au GPU, tous process confondus
	util       int   // taux d'occupation du GPU, en %
	cores      int   // nombre de cœurs GPU (informatif)
}

// parseIOAccelerator lit la sortie de `ioreg -r -c IOAccelerator -d 1`. ok=false
// si aucune statistique de performance n'a été trouvée (sortie vide, autre
// matériel, format inattendu) : l'appelant retombe alors sur l'ancien chemin.
func parseIOAccelerator(out string) (s appleGPUStat, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, `"PerformanceStatistics"`):
			for _, m := range ioregPerfStat.FindAllStringSubmatch(t, -1) {
				n, err := strconv.ParseInt(m[2], 10, 64)
				if err != nil {
					continue
				}
				switch m[1] {
				case "Alloc system memory":
					s.allocBytes, ok = n, true
				case "Device Utilization %":
					s.util, ok = int(n), true
				}
			}
		case strings.HasPrefix(t, `"gpu-core-count"`):
			if _, v, found := strings.Cut(t, "="); found {
				s.cores, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
	}
	return s, ok
}

// appleChipName renvoie « Apple M1 Pro », « Apple M4 Max »… (sysctl), lu une fois.
var appleChipName = sync.OnceValue(func() string {
	out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
	if name := strings.TrimSpace(string(out)); err == nil && name != "" {
		return name
	}
	return "Apple GPU"
})

// appleGPUs renvoie la carte « mémoire unifiée » du Mac, au format de handleVram
// ({name, used, total, util, temp} en Mio), ou nil hors Apple Silicon et dès que
// ioreg ne donne rien d'exploitable. Appelé toutes les ~3 s (cache vramGPUs) :
// ioreg répond en quelques millisecondes, sans initialiser quoi que ce soit.
func appleGPUs() []map[string]any {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return nil
	}
	out, err := exec.Command("ioreg", "-r", "-c", "IOAccelerator", "-d", "1").Output()
	if err != nil {
		return nil
	}
	s, ok := parseIOAccelerator(string(out))
	if !ok {
		return nil
	}
	total := int(totalRAMGB() * 1024)
	if total <= 0 {
		return nil
	}
	used := int(s.allocBytes / (1024 * 1024))
	if used > total {
		used = total
	}
	return []map[string]any{{
		"name": appleChipName(), "used": used, "total": total, "util": s.util, "temp": 0,
		"unified": true,
	}}
}
