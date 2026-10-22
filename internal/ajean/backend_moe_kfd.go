package ajean

// backend_moe_kfd.go — détection des GPU AMD par la topologie KFD du kernel.
//
// Pourquoi : amd-smi et rocm-smi ne sont pas installés par défaut — sur une
// machine au pilote amdgpu propre, les deux peuvent être absents (vérifié sur
// CachyOS : la RX 9070 XT est bien là, /dev/kfd chargé, aucun des deux outils).
// L'installeur du moteur amont détecte les cartes AMD de la même façon : il lit
// la topologie KFD du kernel, sans ROCm. C'est donc la source PRINCIPALE de
// moeAMDGPUs ; amd-smi puis rocm-smi ne restent que des replis.
//
// La numérotation des nœuds GPU (ordre croissant, nœuds CPU sautés) est celle
// de HIP : l'index renvoyé est donc directement celui à passer à --gpu N.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// moeKFDNames : arch → nom, quand le kernel ne donne pas de product_name
// (même table que l'installeur du moteur).
var moeKFDNames = map[string]string{
	"gfx1100": "AMD Radeon RX 7900 series (gfx1100)",
	"gfx1101": "AMD Radeon RX 7800 XT / 7700 XT (gfx1101)",
	"gfx1102": "AMD Radeon RX 7600 / 7600 XT (gfx1102)",
	"gfx1200": "AMD Radeon RX 9060 series (gfx1200)",
	"gfx1201": "AMD Radeon RX 9070 series / AI PRO R9700 (gfx1201)",
	"gfx1030": "AMD Radeon RX 6800 / 6900 series (gfx1030)",
	"gfx1031": "AMD Radeon RX 6700 XT series (gfx1031)",
	"gfx1151": "AMD Radeon 8060S / 8050S / 8040S (Ryzen AI Max, Strix Halo, gfx1151)",
}

// moeKFDBySimds : le nombre de CU dit lequel des trois Strix Halo c'est
// (simd_count = 2 par CU), la même table que l'installeur du moteur.
var moeKFDBySimds = map[int]string{80: "8060S", 64: "8050S", 32: "8040S"}

// moeStrixHalo : Strix Halo = gfx1151 (gfx_arch_is de setup.py : jamais un
// préfixe, gfx1150 et gfx1152 ne sont pas gfx1151).
func moeStrixHalo(arch string) bool { return arch == "gfx1151" }

// moeUMAOSLeftGB : la part de la mémoire unifiée que le moteur laisse à l'OS
// (device_free_bytes de l'amont, UMA_OS_LEFT_GB de setup.py).
const moeUMAOSLeftGB = 6

// moeApplyUMA : la mémoire d'un APU à mémoire unifiée (Strix Halo). Sa « VRAM »
// lue par le kernel n'est que le carve-out BIOS (de 512 Mo à 96 Go selon le
// réglage, 2 Go sur les machines des mainteneurs). Le GPU atteint aussi la
// mémoire partagée du système (le pool GTT, la moitié de la RAM sauf réglage du
// noyau) : la mémoire utilisable par le GPU est la somme des deux moins ce que
// l'OS garde. La mémoire partagée n'est PAS une place en plus à côté de la RAM —
// elle EST la RAM où vivent les experts, donc elle ne s'ajoute pas à la RAM.
// Séparée de moeKFDGPUs pour être testable sans arbre sysfs.
func moeApplyUMA(carve, gtt, ram float64) (vram, dedicated, shared float64) {
	shared = gtt
	if shared <= 0 {
		shared = ram / 2 // le noyau n'expose pas le pool : la moitié de la RAM
	}
	shared = max(0.0, min(shared, ram-moeUMAOSLeftGB))
	return carve + shared, carve, shared
}

// moeKFDGB : une valeur sysfs d'octets, en Go.
func moeKFDGB(path string) float64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	bts, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0
	}
	return float64(bts) / (1 << 30)
}

// moeKFDGPUs : les GPU AMD de la topologie KFD, en numérotation HIP.
// sysfs : racine de l'arbre (les tests passent un arbre factice).
// Les nœuds CPU (simd_count 0) sont sautés ; la VRAM vient de
// mem_info_vram_total (octets) du device DRM du nœud.
func moeKFDGPUs(sysfs string) []moeGPU {
	base := filepath.Join(sysfs, "class/kfd/kfd/topology/nodes")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var nodes []string
	for _, e := range entries {
		if e.IsDir() && moeIsDigits(e.Name()) {
			nodes = append(nodes, e.Name())
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		a, _ := strconv.Atoi(nodes[i])
		b, _ := strconv.Atoi(nodes[j])
		return a < b
	})
	var out []moeGPU
	for _, n := range nodes {
		props, err := os.ReadFile(filepath.Join(base, n, "properties"))
		if err != nil {
			continue
		}
		p := map[string]string{}
		for _, line := range strings.Split(string(props), "\n") {
			if k, v, ok := strings.Cut(line, " "); ok {
				p[k] = strings.TrimSpace(v)
			}
		}
		ver, _ := strconv.Atoi(p["gfx_target_version"])
		simd, _ := strconv.Atoi(p["simd_count"])
		if ver == 0 || simd == 0 {
			continue // nœud CPU
		}
		arch := fmt.Sprintf("gfx%d%x%x", ver/10000, (ver/100)%100, ver%100)
		dev := filepath.Join(sysfs, "class/drm/renderD"+p["drm_render_minor"], "device")
		vram := moeKFDGB(filepath.Join(dev, "mem_info_vram_total"))
		name := ""
		if b, err := os.ReadFile(filepath.Join(dev, "product_name")); err == nil {
			name = strings.TrimSpace(string(b))
		}
		g := moeGPU{Index: len(out), Name: name, VRAMGB: vram, AMDArch: arch}
		if moeStrixHalo(arch) {
			ram := totalRAMGB()
			gtt := moeKFDGB(filepath.Join(dev, "mem_info_gtt_total"))
			g.VRAMGB, g.DedicatedGB, _ = moeApplyUMA(vram, gtt, ram)
			g.UMA = true
			// le nom vient du nombre de CU (8060S / 8050S / 8040S), comme chez
			// l'amont : il ne remplace pas le nom que le kernel donne
			if name == "" {
				if cu, ok := moeKFDBySimds[simd]; ok {
					name = fmt.Sprintf("AMD Radeon %s (Ryzen AI Max, Strix Halo, gfx1151)", cu)
				}
			}
		}
		if name == "" {
			name = moeKFDNames[arch]
			if name == "" {
				name = "AMD Radeon (" + arch + ")"
			}
		}
		g.Name = name
		out = append(out, g)
	}
	return out
}

func moeIsDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
