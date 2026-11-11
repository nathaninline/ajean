// engine.go — le moteur llama.cpp d'AJEAN.
//
// AJEAN fonctionne comme Ollama : il lance le llama-server officiel de
// llama.cpp, à UNE version précise, choisie et validée par le mainteneur pour
// chaque version d'AJEAN. L'utilisateur ne choisit ni ne met à jour le moteur :
//
//   - la version tient dans LLAMA_CPP_VERSION (un tag amont, ex. b12257), et
//     llama_cpp_assets.json liste ses binaires avec leur empreinte SHA-256.
//     Les deux sont embarqués dans le binaire d'AJEAN ; on les change avec
//     « go run ./tools/bump-llamacpp <tag> » (voir docs/moteur.md) ;
//   - le moteur s'installe tout seul (au premier lancement de l'interface, et
//     au démarrage du service s'il manque) et se remplace tout seul quand une
//     mise à jour d'AJEAN change la version épinglée ;
//   - la variante (CUDA, ROCm, Vulkan, Metal, CPU) est choisie d'après la
//     machine, puis SONDÉE dans un processus jetable : une variante qui plante
//     ou ne voit aucune carte est écartée et la suivante est essayée, jusqu'au
//     CPU qui marche partout ;
//   - il n'y a plus de compilation locale, de moteur personnalisé, ni de clé
//     BIN : une machine fait tourner exactement le moteur qui a été testé.
package ajean

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed LLAMA_CPP_VERSION
var llamaCppVersionFile string

//go:embed llama_cpp_assets.json
var llamaCppAssetsFile []byte

// engineDownloadBase : d'où viennent les archives. Variable pour les tests.
var engineDownloadBase = "https://github.com/ggml-org/llama.cpp/releases/download"

// enginePin : la version de llama.cpp livrée par cette version d'AJEAN.
func enginePin() string {
	if engineManifestTest != nil {
		return engineManifestTest.Tag
	}
	return strings.TrimSpace(llamaCppVersionFile)
}

type engineAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type engineManifest struct {
	Tag    string        `json:"tag"`
	Assets []engineAsset `json:"assets"`
}

// Tests seulement : un manifeste et une machine factices.
var (
	engineManifestTest *engineManifest
	engineHWTest       *engineHW
)

func engineMachine() engineHW {
	if engineHWTest != nil {
		return *engineHWTest
	}
	return detectEngineHW()
}

func engineAssets() (engineManifest, error) {
	if engineManifestTest != nil {
		return *engineManifestTest, nil
	}
	var m engineManifest
	if err := json.Unmarshal(llamaCppAssetsFile, &m); err != nil {
		return m, fmt.Errorf("manifeste du moteur illisible : %w", err)
	}
	if m.Tag != enginePin() {
		return m, fmt.Errorf("manifeste du moteur (%s) différent de la version épinglée (%s) : relance tools/bump-llamacpp", m.Tag, enginePin())
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Emplacements
// ---------------------------------------------------------------------------

// engineRoot : backends/engine. Un sous-dossier par version épinglée ; il n'en
// reste qu'un après une installation réussie.
func engineRoot() string { return filepath.Join(backendsDir(), "engine") }

func engineDir(tag string) string { return filepath.Join(engineRoot(), tag) }

const engineMarker = "engine.json"

// engineInfo : ce qui est installé (engine.json, écrit en DERNIER : sa présence
// vaut « installation complète et sondée »).
type engineInfo struct {
	Tag         string   `json:"tag"`
	Variant     string   `json:"variant"` // cuda | rocm | vulkan | metal | cpu
	Label       string   `json:"label"`
	Asset       string   `json:"asset"`
	Bin         string   `json:"bin"`                    // relatif au dossier du moteur
	LibDirs     []string `json:"lib_dirs,omitempty"`     // dossiers des bibliothèques GPU livrées (relatifs)
	ExtraSHA256 string   `json:"extra_sha256,omitempty"` // archive des bibliothèques GPU (cudart)
	InstalledAt string   `json:"installed_at"`

	dir string
}

func (e *engineInfo) binPath() string { return filepath.Join(e.dir, filepath.FromSlash(e.Bin)) }

func (e *engineInfo) libPaths() []string {
	out := make([]string, 0, len(e.LibDirs))
	for _, d := range e.LibDirs {
		out = append(out, filepath.Join(e.dir, filepath.FromSlash(d)))
	}
	return out
}

func readEngineInfo(dir string) *engineInfo {
	b, err := os.ReadFile(filepath.Join(dir, engineMarker))
	if err != nil {
		return nil
	}
	var e engineInfo
	if json.Unmarshal(b, &e) != nil || e.Bin == "" {
		return nil
	}
	e.dir = dir
	if !isFile(e.binPath()) {
		return nil
	}
	return &e
}

// engineInstalled : le moteur de la version épinglée, ou nil s'il manque.
func engineInstalled() *engineInfo {
	e := readEngineInfo(engineDir(enginePin()))
	if e == nil || e.Tag != enginePin() {
		return nil
	}
	return e
}

// engineDevBin : AJEAN_ENGINE_BIN désigne un llama-server à utiliser à la place
// du moteur livré. Réservé au développement (valider une version candidate
// avant de l'épingler) : rien dans l'interface ne le propose.
func engineDevBin() string {
	if p := strings.TrimSpace(os.Getenv("AJEAN_ENGINE_BIN")); p != "" && isFile(p) {
		return p
	}
	return ""
}

// engineBin : chemin du llama-server à lancer, "" si le moteur n'est pas
// (encore) installé. C'est LE point d'entrée pour tout le code qui a besoin du
// binaire : plus personne ne lit la clé BIN.
func engineBin() string {
	if p := engineDevBin(); p != "" {
		return p
	}
	if e := engineInstalled(); e != nil {
		return e.binPath()
	}
	return ""
}

// engineLibDirsFor : dossiers de bibliothèques à ajouter au chemin de recherche
// quand on lance le binaire situé dans binDir (les bibliothèques CUDA livrées
// avec le moteur vivent dans leur propre sous-dossier).
func engineLibDirsFor(binDir string) []string {
	e := engineInstalled()
	if e == nil || !samePath(filepath.Dir(e.binPath()), binDir) {
		return nil
	}
	return e.libPaths()
}

// samePath compare deux chemins sans se laisser piéger par la casse ou les
// séparateurs (Windows).
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	na, nb := filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(na, nb)
	}
	return na == nb
}

// ---------------------------------------------------------------------------
// Choix de la variante
// ---------------------------------------------------------------------------

// engineVariant : une façon d'installer le moteur sur cette machine.
type engineVariant struct {
	ID    string // cuda | rocm | vulkan | metal | cpu
	Label string
	Main  engineAsset
	Extra *engineAsset // bibliothèques GPU livrées à part (cudart), nil sinon
}

var engineVariantIDs = []string{"cuda", "rocm", "vulkan", "metal", "cpu"}

func validEngineVariant(id string) bool {
	for _, v := range engineVariantIDs {
		if v == id {
			return true
		}
	}
	return false
}

var reCudaAssetVer = regexp.MustCompile(`cuda-([0-9]+\.[0-9]+)`)

// engineCandidates renvoie les variantes à essayer, de la plus rapide à la plus
// sûre. Fonction pure : tout ce qui dépend de la machine est dans hw.
//
//	Windows x64 : CUDA (carte NVIDIA) → Vulkan → CPU
//	Windows arm : CPU
//	macOS       : Metal
//	Linux       : ROCm (si installé) → CUDA (carte NVIDIA) → Vulkan → CPU
//
// forced (cuda | rocm | vulkan | cpu…) restreint la liste à cette variante.
func engineCandidates(assets []engineAsset, hw engineHW, forced string) ([]engineVariant, error) {
	find := func(frags ...string) *engineAsset {
		for i, a := range assets {
			if !strings.HasPrefix(a.Name, "llama-") {
				continue
			}
			ok := true
			for _, f := range frags {
				if !strings.Contains(a.Name, f) {
					ok = false
					break
				}
			}
			if ok {
				return &assets[i]
			}
		}
		return nil
	}
	var out []engineVariant
	add := func(id, label string, a *engineAsset) {
		if a != nil {
			out = append(out, engineVariant{ID: id, Label: label, Main: *a})
		}
	}
	// CUDA : toutes les versions que le pilote sait faire tourner, la plus récente
	// d'abord (pilote de version inconnue : la plus ancienne d'abord, la plus
	// compatible). Chacune vient avec son archive de bibliothèques (cudart).
	addCuda := func(osTag, arch, ext, sys string) {
		type cv struct {
			ver  float64
			s    string
			main engineAsset
		}
		var list []cv
		for _, a := range assets {
			if !strings.HasPrefix(a.Name, "llama-") || !strings.Contains(a.Name, "bin-"+osTag+"-cuda-") ||
				!strings.HasSuffix(a.Name, "-"+arch+ext) {
				continue
			}
			m := reCudaAssetVer.FindStringSubmatch(a.Name)
			if m == nil {
				continue
			}
			v, _ := strconv.ParseFloat(m[1], 64)
			if hw.DriverCuda > 0 && v > hw.DriverCuda {
				continue
			}
			list = append(list, cv{v, m[1], a})
		}
		sort.Slice(list, func(i, j int) bool {
			if hw.DriverCuda > 0 {
				return list[i].ver > list[j].ver
			}
			return list[i].ver < list[j].ver
		})
		for _, c := range list {
			v := engineVariant{ID: "cuda", Label: "CUDA " + c.s + " (" + sys + ")", Main: c.main}
			for i, a := range assets {
				if strings.HasPrefix(a.Name, "cudart-") && strings.Contains(a.Name, osTag+"-cuda-"+c.s+"-"+arch) {
					v.Extra = &assets[i]
					break
				}
			}
			if v.Extra == nil {
				continue // sans ses bibliothèques, la variante ne démarrerait pas
			}
			out = append(out, v)
		}
	}

	switch hw.OS {
	case "windows":
		if hw.Arch == "arm64" {
			add("cpu", "CPU (Windows arm64)", find("bin-win-cpu-arm64"))
			break
		}
		if hw.Nvidia {
			addCuda("win", "x64", ".zip", "Windows x64")
		}
		if hw.Vulkan {
			add("vulkan", "Vulkan (Windows x64)", find("bin-win-vulkan-x64"))
		}
		add("cpu", "CPU (Windows x64)", find("bin-win-cpu-x64"))
	case "darwin":
		arch := "x64"
		if hw.Arch == "arm64" {
			arch = "arm64"
		}
		add("metal", "Metal (macOS "+arch+")", find("bin-macos-"+arch))
	case "linux":
		arch := "x64"
		if hw.Arch == "arm64" {
			arch = "arm64"
		}
		if hw.ROCm {
			add("rocm", "ROCm (Linux "+arch+")", find("bin-ubuntu-rocm-", "-"+arch+".tar.gz"))
		}
		if hw.Nvidia {
			addCuda("ubuntu", arch, ".tar.gz", "Linux "+arch)
		}
		if hw.Vulkan {
			add("vulkan", "Vulkan (Linux "+arch+")", find("bin-ubuntu-vulkan-"+arch))
		}
		add("cpu", "CPU (Linux "+arch+")", find("bin-ubuntu-"+arch+".tar.gz"))
	default:
		return nil, fmt.Errorf("AJEAN ne fournit pas de moteur pour %s/%s", hw.OS, hw.Arch)
	}
	if forced != "" {
		var kept []engineVariant
		for _, v := range out {
			if v.ID == forced {
				kept = append(kept, v)
			}
		}
		if len(kept) == 0 {
			return nil, fmt.Errorf("la variante « %s » n'existe pas pour cette machine (%s/%s)", forced, hw.OS, hw.Arch)
		}
		return kept, nil
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("aucun binaire du moteur pour cette machine (%s/%s)", hw.OS, hw.Arch)
	}
	return out, nil
}

// engineForcedVariant : la variante imposée sur cette machine ("" = automatique).
// Réglage machine (« ajean engine repair --variant=vulkan »), l'équivalent du
// OLLAMA_LLM_LIBRARY d'Ollama : pour une carte que la détection sert mal.
func engineForcedVariant() string {
	v := strings.ToLower(strings.TrimSpace(getStr(bkState, "engine_variant")))
	if !validEngineVariant(v) {
		return ""
	}
	return v
}

func setEngineForcedVariant(v string) error {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "auto" {
		v = ""
	}
	if v != "" && !validEngineVariant(v) {
		return fmt.Errorf("variante inconnue : %s (auto | %s)", v, strings.Join(engineVariantIDs, " | "))
	}
	return putStr(bkState, "engine_variant", v)
}

// ---------------------------------------------------------------------------
// Restes des versions précédentes
// ---------------------------------------------------------------------------

// engineLegacy : un dossier laissé par l'ancienne gestion des moteurs.
type engineLegacy struct {
	Path string `json:"path"`
	What string `json:"what"`
}

// engineLegacyDirs liste ce que les versions d'avant la 0.20 ont pu laisser et
// qu'AJEAN n'utilise plus : moteur compilé, moteurs personnalisés, ExLlama.
// AJEAN ne les supprime pas de lui-même (ils peuvent contenir des modèles ou
// des sources modifiées) : il les signale.
func engineLegacyDirs() []engineLegacy {
	var out []engineLegacy
	if entries, err := os.ReadDir(backendsDir()); err == nil {
		for _, e := range entries {
			n := e.Name()
			if !e.IsDir() || strings.HasPrefix(n, ".") || n == filepath.Base(engineRoot()) {
				continue
			}
			what := "moteur personnalisé (avant 0.20)"
			switch n {
			case "llama.cpp":
				what = "llama.cpp compilé localement (avant 0.20)"
			case "llama.cpp-prebuilt":
				what = "llama.cpp précompilé (avant 0.20)"
			}
			out = append(out, engineLegacy{Path: filepath.Join(backendsDir(), n), What: what})
		}
	}
	if d := filepath.Join(AjeanHome(), "exl3"); isDir(d) {
		out = append(out, engineLegacy{Path: d, What: "moteur ExLlama (retiré en 0.20)"})
	}
	return out
}

// removedEngine : nom du moteur retiré que réclame cette configuration, "" si
// elle utilise llama.cpp. Les presets ENGINE=moe / ENGINE=exl3 / CLOUD=modal d'avant la 0.20
// ne peuvent plus démarrer : on le dit clairement au lieu de lancer llama.cpp
// sur un modèle qui n'est pas un GGUF.
func removedEngine(cfg map[string]string) string {
	switch strings.ToLower(strings.TrimSpace(cfg["ENGINE"])) {
	case "moe":
		return "MoE"
	case "exl3":
		return "ExLlama"
	}
	if strings.TrimSpace(cfg["CLOUD"]) != "" {
		return "GPU Cloud"
	}
	return ""
}

func removedEngineError(name string) error {
	return fmt.Errorf("ce preset utilise %s, retiré d'AJEAN 0.20. Un preset tourne désormais en Local (llama.cpp, modèles GGUF), par une API, ou comme Service système : choisis-en ou crées-en un autre", name)
}

func nowStamp() string { return time.Now().Format(time.RFC3339) }
