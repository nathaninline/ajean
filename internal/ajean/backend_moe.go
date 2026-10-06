package ajean

// backend_moe.go : moteur MoE spécialisé de la famille Qwen3.8-Flash-Next,
// installé et piloté par ajean « en un clic ».
//
// Version FIGÉE : ajean livre une version testée (sources + moteur Linux compilé
// par nous) depuis SA propre release GitHub, vérifiée par SHA-256 ; on ne monte
// de version qu'après l'avoir validée sur le serveur de test. Périmètre de cette
// première version : Linux x86_64 + NVIDIA, une ou deux cartes.
//
// Déroulé :
//  1. installation (tâche « moe », même suivi que llama.cpp) : télécharge le
//     paquet figé, puis lance l'installeur du moteur en mode automatique en lui
//     imposant NOTRE binaire (--prebuilt) ; il télécharge le modèle depuis
//     Hugging Face à des révisions figées et le prépare ;
//  2. ajean écrit un preset ENGINE=moe et l'active ;
//  3. `ajean serve` (unité ajean-engine) lance le serveur du moteur au lieu de
//     llama-server, avec les réglages validés sur notre matériel (serveMoe).
//
// Le serveur expose les mêmes points d'entrée que llama-server (/health,
// /props, /slots, /metrics, /v1/*) : le reste d'ajean le voit comme un moteur
// local ordinaire.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Version du moteur AJEAN MoE et paquets correspondants. Le moteur est compilé
// par nous (CUDA 12.8, mode portable AVX2, sm_75/80/86/89/120 : RTX 20 à 50),
// avec les experts de la carte d'appoint gardés hors RAM (voir moeDropFits).
// Un moteur installé d'un autre paquet est remplacé au lancement (moeEnsureEngine).
const (
	moeVersion     = "1.0"
	moeReleaseBase = "https://github.com/nathaninline/ajean/releases/download/moe-v" + moeVersion + "/"
	moeSrcAsset    = "ajean-moe-src-" + moeVersion + ".tar.gz"
	moeEngineAsset = "ajean-moe-engine-" + moeVersion + "-linux-x64-cuda12.zip"
	// moeEngineLocal : le nom sous lequel l'installeur attend le moteur (CUDA12_ASSET).
	moeEngineLocal = "strata-linux-x64-cuda12.zip"
)

// moeAssetSHA : empreintes des fichiers de la release, vérifiées avant usage.
var moeAssetSHA = map[string]string{
	moeSrcAsset:    "5747f69426973d957e4f9894a8e3333c6ef0d6ac8565e26ddb8d36f52211cb3b",
	moeEngineAsset: "b5851bc138ab32ca3ffefc16bf91c8f557e4d503503473a22f2b6c9e9b8fd34c",
}

// moeFamily / moeQuant : ce que l'installeur du moteur sait installer,
// limité à ce qu'on propose en un clic.
type moeFamily struct {
	ID     string   `json:"id"`    // --family
	Label  string   `json:"label"` // affiché
	About  string   `json:"about"` // une ligne
	Quants []string `json:"quants"`
}

type moeQuant struct {
	ID         string  `json:"id"`          // --model
	About      string  `json:"about"`       // une ligne
	DownloadGB float64 `json:"download_gb"` // modèle + tête MTP + encodeur vision
	ArenaGB    float64 `json:"arena_gb"`    // experts à garder en RAM (ou en mmap)
}

// Valeurs reprises de la table MODELS de l'installeur du moteur.
var moeFamilies = []moeFamily{
	{ID: "swift", Label: "Swift 1.5", About: "réfléchit moins, répond plus vite", Quants: []string{"IQ2_XS", "IQ3_XXS"}},
	{ID: "qwen", Label: "Classique", About: "le Qwen3.8-Flash-Next original", Quants: []string{"Q2_0", "IQ2_XS", "IQ3_XXS", "IQ3_S"}},
}

var moeQuants = map[string]moeQuant{
	"Q2_0":    {ID: "Q2_0", About: "le plus rapide", DownloadGB: 66.4, ArenaGB: 34.0},
	"IQ2_XS":  {ID: "IQ2_XS", About: "un peu plus fidèle, presque aussi rapide", DownloadGB: 68.0, ArenaGB: 35.5},
	"IQ3_XXS": {ID: "IQ3_XXS", About: "99 % de la qualité du modèle complet", DownloadGB: 75.8, ArenaGB: 42.9},
	"IQ3_S":   {ID: "IQ3_S", About: "la meilleure qualité, le plus lent", DownloadGB: 83.6, ArenaGB: 50.3},
}

// moeLowRAMHeadroomGB : RAM à laisser à côté des experts (OS, moteur,
// serveur). En dessous, l'installeur passe en mode mmap (setup.py LOW_RAM_HEADROOM_GB).
const moeLowRAMHeadroomGB = 10

// moeDropHeadroomGB : RAM à laisser à côté des experts verrouillés quand la
// carte d'aide garde les siens hors RAM (mesuré sur le serveur de test :
// 46 Go de RAM, 34,7 Gio d'experts verrouillés, ~2,3 Go encore libres).
const moeDropHeadroomGB = 7

// moeHelperExpertsGB : ce que la carte d'aide garde d'experts (sa VRAM moins
// le contexte CUDA, l'encodeur d'images et la marge : 5,3 Gio sur une 3070 8 Go).
func moeHelperExpertsGB(vramGB float64) float64 { return max(vramGB-2.5, 0) }

// moeMmapExtraGB : le mode mmap écrit les experts dans un fichier à part
// (experts.bin), à compter en plus du téléchargement.
const moeMmapExtraGB = 45

func moeHome() string    { return filepath.Join(AjeanHome(), "moe") }
func moeSrcDir() string  { return filepath.Join(moeHome(), "ajean-moe-"+moeVersion) }
func moeDataDir() string { return filepath.Join(moeHome(), "data") }
func moeDLDir() string   { return filepath.Join(moeHome(), "dl") }

// moePython : l'interpréteur de l'environnement que l'installeur a créé.
func moePython() string { return filepath.Join(moeSrcDir(), ".venv", "bin", "python") }

// isMoeConfig : la configuration active fait tourner le moteur MoE.
func isMoeConfig(cfg map[string]string) bool { return cfg["ENGINE"] == "moe" }

// ---------------------------------------------------------------------------
// Matériel et recommandation
// ---------------------------------------------------------------------------

// moeGPU : une carte NVIDIA vue par nvidia-smi (index = numérotation nvidia-smi,
// alignée sur CUDA avec CUDA_DEVICE_ORDER=PCI_BUS_ID).
type moeGPU struct {
	Index  int     `json:"index"`
	Name   string  `json:"name"`
	VRAMGB float64 `json:"vram_gb"`
	Arch   int     `json:"arch"` // compute capability ×10 (8.6 → 86)
}

// moeEnv : ce que l'écran d'installation doit savoir de la machine.
type moeEnv struct {
	Supported  bool     `json:"supported"`
	Reason     string   `json:"reason,omitempty"` // pourquoi pas, en clair
	GPUs       []moeGPU `json:"gpus"`
	Main       int      `json:"main"`   // index nvidia-smi de la carte principale
	Helper     int      `json:"helper"` // index de la carte d'aide, -1 sans
	RAMGB      float64  `json:"ram_gb"`
	DiskFreeGB float64  `json:"disk_free_gb"`
	Python     string   `json:"python,omitempty"`
}

// moeMinArch : la plus ancienne carte pour laquelle notre moteur est compilé (RTX 20).
const moeMinArch = 75

func moeDetect() moeEnv {
	env := moeEnv{Main: -1, Helper: -1, RAMGB: totalRAMGB()}
	if f := diskFree(AjeanHome()); f > 0 {
		env.DiskFreeGB = float64(f) / 1e9
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		env.Reason = "ce moteur n'est proposé pour l'instant que sous Linux (x86_64)"
		return env
	}
	gpus, err := detectGPUs()
	if err != nil {
		env.Reason = "ce moteur demande une carte NVIDIA (" + err.Error() + ")"
		return env
	}
	for _, g := range gpus {
		mib, _ := strconv.ParseFloat(g.MemTotal, 64)
		cap, _ := strconv.ParseFloat(g.Cap, 64)
		env.GPUs = append(env.GPUs, moeGPU{Index: g.Index, Name: g.Name, VRAMGB: mib / 1024, Arch: int(cap*10 + 0.5)})
	}
	// Carte principale = la plus grosse VRAM (à égalité : la plus récente) ; la
	// suivante, si elle est assez récente, calcule une part des experts.
	cards := append([]moeGPU(nil), env.GPUs...)
	sort.SliceStable(cards, func(i, j int) bool {
		if cards[i].VRAMGB != cards[j].VRAMGB {
			return cards[i].VRAMGB > cards[j].VRAMGB
		}
		return cards[i].Arch > cards[j].Arch
	})
	if cards[0].Arch < moeMinArch {
		env.Reason = fmt.Sprintf("la carte %s est trop ancienne pour ce moteur (RTX 20 ou plus récente)", cards[0].Name)
		return env
	}
	if cards[0].VRAMGB < 7.5 {
		env.Reason = fmt.Sprintf("la carte %s n'a que %.0f Go de VRAM (8 Go minimum)", cards[0].Name, cards[0].VRAMGB)
		return env
	}
	env.Main = cards[0].Index
	if len(cards) > 1 && cards[1].Arch >= moeMinArch && cards[1].VRAMGB >= 5.5 {
		env.Helper = cards[1].Index
	}
	py, perr := moeCheckPython()
	env.Python = py
	if perr != nil {
		env.Reason = perr.Error()
		return env
	}
	env.Supported = true
	return env
}

var rePyVersion = regexp.MustCompile(`Python (\d+)\.(\d+)`)

// moeCheckPython : l'installeur du moteur est en Python (3.10 ou plus récent)
// et crée son propre environnement (module venv, paquet python3-venv sur Debian/Ubuntu).
func moeCheckPython() (string, error) {
	out, err := hideCmd(exec.Command("python3", "--version")).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Python 3 est introuvable : installe-le (sudo apt install python3 python3-venv)")
	}
	m := rePyVersion.FindStringSubmatch(string(out))
	if m == nil {
		return "", fmt.Errorf("version de Python illisible : %s", strings.TrimSpace(string(out)))
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	ver := m[1] + "." + m[2]
	if maj < 3 || (maj == 3 && min < 10) {
		return ver, fmt.Errorf("Python %s est trop ancien : il faut Python 3.10 ou plus récent", ver)
	}
	if e := hideCmd(exec.Command("python3", "-c", "import venv, ensurepip")).Run(); e != nil {
		return ver, fmt.Errorf("le module venv de Python manque : sudo apt install python%s-venv", ver)
	}
	return ver, nil
}

// moeTag : le nom que l'installeur donne aux dossiers d'un choix
// (models/<tag>, packs/<tag en minuscules>) : « swift-IQ3_XXS », « IQ3_XXS ».
func moeTag(family, quant string) string {
	if family == "swift" {
		return "swift-" + quant
	}
	return quant
}

func moeDirGB(dir string) float64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, e := d.Info(); e == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return float64(total) / 1e9
}

// moeHaveGB : ce qui est déjà sur le disque pour CE choix (installation
// reprise ou réinstallation) : ses fichiers de modèle, son experts.bin, et la
// tête MTP commune à tous les choix. Autant de téléchargement en moins.
func moeHaveGB(family, quant string) float64 {
	tag := moeTag(family, quant)
	return moeDirGB(filepath.Join(moeDataDir(), "models", tag)) +
		moeDirGB(filepath.Join(moeDataDir(), "packs", strings.ToLower(tag))) +
		moeDirGB(filepath.Join(moeDataDir(), "mtp"))
}

// moeNeedsMmap : les experts ne tiennent pas en RAM avec la marge nécessaire,
// ils seront lus en mmap depuis un fichier à part (experts.bin).
func moeNeedsMmap(q moeQuant, ramGB float64) bool {
	return ramGB < q.ArenaGB+moeLowRAMHeadroomGB
}

// moeDropFits : avec une carte d'aide, les experts qu'elle garde ne sont pas en
// RAM ; le reste est verrouillé en RAM (plus rapide que le mmap : génération
// ~64 au lieu de ~56 tok/s, lecture +17 à +38 %). Il faut que ce reste tienne.
func moeDropFits(q moeQuant, env moeEnv) bool {
	if env.Helper < 0 {
		return false
	}
	for _, g := range env.GPUs {
		if g.Index == env.Helper {
			return env.RAMGB >= q.ArenaGB-moeHelperExpertsGB(g.VRAMGB)+moeDropHeadroomGB
		}
	}
	return false
}

// moeFit : ce qu'un quant demande sur cette machine, et s'il y tient.
type moeFit struct {
	Quant  moeQuant `json:"quant"`
	DiskGB float64  `json:"disk_gb"`
	Mmap   bool     `json:"mmap"`
	Drop   bool     `json:"drop"` // experts de la carte d'aide hors RAM, le reste verrouillé
	OK     bool     `json:"ok"`
	Why    string   `json:"why,omitempty"`
	Preset string   `json:"preset,omitempty"` // id du preset si ce choix est déjà installé
	Active bool     `json:"active,omitempty"` // ... et si c'est le preset actif
	// Details : la config de lancement de ce preset, calculée par la MÊME
	// fonction que le lancement (moeBuildConfig) : ce qui est affiché tourne.
	Details *moeDetails `json:"details,omitempty"`
}

// moeDetails : la config d'un preset installé, en clair pour l'interface.
type moeDetails struct {
	Version    string `json:"version"`
	Ctx        string `json:"ctx"`
	KV         string `json:"kv"`
	KVResident string `json:"kv_resident,omitempty"`
	Spec       string `json:"spec"`
	Prefill    string `json:"prefill"`
	ShortRead  string `json:"short_read"`
	CacheRoot  string `json:"cache_root"`
	Mmap       bool   `json:"mmap"`
	Drop       bool   `json:"drop"`
	MainGPU    string `json:"main_gpu"`
	HelperGPU  string `json:"helper_gpu,omitempty"`
	VisionGPU  string `json:"vision_gpu,omitempty"`
	// pour les réglages : ce que la machine permet et l'état actuel
	HasHelper bool `json:"has_helper"` // une carte d'aide est disponible
	HelperOn  bool `json:"helper_on"`
	Vision    bool `json:"vision"`
}

// moeDetailsFor lit un preset installé et calcule sa config de lancement.
func moeDetailsFor(presetID string, env moeEnv) *moeDetails {
	b, err := os.ReadFile(filepath.Join(presetsDir(), presetID+".env"))
	if err != nil {
		return nil
	}
	pc := parseEnv(string(b))
	raw, err := os.ReadFile(pc["MOE_CONFIG"])
	if err != nil {
		return nil
	}
	var base map[string]any
	if json.Unmarshal(raw, &base) != nil {
		return nil
	}
	out, err := moeBuildConfig(base, pc, "")
	if err != nil {
		return nil
	}
	var args []string
	for _, a := range out["args"].([]any) {
		args = append(args, fmt.Sprint(a))
	}
	val := func(flag string) string {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == flag {
				return args[i+1]
			}
		}
		return ""
	}
	gpuName := func(idx string) string {
		for _, g := range env.GPUs {
			if strconv.Itoa(g.Index) == strings.TrimSpace(idx) {
				return fmt.Sprintf("%s (%.0f Go)", strings.TrimPrefix(g.Name, "NVIDIA GeForce "), g.VRAMGB)
			}
		}
		return ""
	}
	d := &moeDetails{
		Version: moeVersion, Ctx: val("--max-context"), KV: val("--kv"), KVResident: val("--kv-resident"),
		Spec: val("--spec"), Prefill: val("--prefill"), ShortRead: val("--short-read"),
		CacheRoot: val("--prompt-cache-root"), MainGPU: gpuName(pc["MOE_MAIN_GPU"]),
	}
	if e, ok := out["env"].(map[string]any); ok {
		d.Mmap = fmt.Sprint(e["STRATA_ARENA_MMAP"]) == "1"
		d.Drop = fmt.Sprint(e["STRATA_REMOTE_DROP"]) == "1"
	}
	if h := strings.TrimSpace(pc["MOE_HELPER_GPU"]); h != "" && h != "-1" {
		d.HasHelper = true
		if moeHelperOn(pc) {
			d.HelperOn = true
			d.HelperGPU = gpuName(h)
		}
	}
	d.Vision = pc["MOE_VISION"] != "0"
	if v, ok := out["vision"].(map[string]any); ok && d.Vision {
		if dev, has := v["cuda_device"]; has {
			d.VisionGPU = gpuName(fmt.Sprint(dev))
		} else {
			d.VisionGPU = d.MainGPU
		}
	}
	return d
}

func moeFitFor(q moeQuant, env moeEnv, haveGB float64) moeFit {
	f := moeFit{Quant: q, Mmap: moeNeedsMmap(q, env.RAMGB)}
	if f.Mmap && moeDropFits(q, env) {
		f.Mmap, f.Drop = false, true
	}
	f.DiskGB = q.DownloadGB
	// les deux modes lisent les experts dans experts.bin, écrit au premier lancement
	if f.Mmap || f.Drop {
		f.DiskGB += moeMmapExtraGB
	}
	// ce qui est déjà là ne se retélécharge pas ; il reste toujours les
	// environnements (Python, bibliothèques CUDA, moteur) : ~10 Go
	f.DiskGB = max(f.DiskGB-haveGB, 10)
	// En mmap, le système garde les experts en cache tant que la RAM le permet ;
	// au-delà, ils sont relus depuis le disque à chaque jeton : trop lent. On
	// demande que la RAM + la VRAM des cartes couvrent au moins les experts.
	vram := 0.0
	for _, g := range env.GPUs {
		if g.Index == env.Main || g.Index == env.Helper {
			vram += g.VRAMGB
		}
	}
	switch {
	case env.RAMGB+vram-12 < q.ArenaGB:
		f.Why = fmt.Sprintf("demande plus de mémoire : %.0f Go de RAM + VRAM, il en faut ~%.0f", env.RAMGB+vram, q.ArenaGB+12)
	case env.DiskFreeGB > 0 && env.DiskFreeGB < f.DiskGB+5:
		f.Why = fmt.Sprintf("pas assez d'espace disque : %.0f Go libres, il en faut ~%.0f", env.DiskFreeGB, f.DiskGB+5)
	default:
		f.OK = true
	}
	return f
}

// moeRecommend : le quant le plus fidèle qui tient, IQ3_XXS de préférence
// (meilleur compromis mesuré) ; IQ3_S seulement si la RAM le loge sans mmap.
func moeRecommend(family string, env moeEnv) string {
	var fam *moeFamily
	for i := range moeFamilies {
		if moeFamilies[i].ID == family {
			fam = &moeFamilies[i]
		}
	}
	if fam == nil {
		return ""
	}
	best := ""
	for _, id := range []string{"IQ3_XXS", "IQ2_XS", "Q2_0"} {
		if !moeHas(fam.Quants, id) {
			continue
		}
		if moeFitFor(moeQuants[id], env, moeHaveGB(family, id)).OK {
			best = id
			break
		}
	}
	if moeHas(fam.Quants, "IQ3_S") {
		q := moeQuants["IQ3_S"]
		if f := moeFitFor(q, env, moeHaveGB(family, "IQ3_S")); f.OK && !f.Mmap {
			best = "IQ3_S"
		}
	}
	return best
}

func moeHas(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Installation
// ---------------------------------------------------------------------------

// moeInstallReq : le choix de l'utilisateur. Le reste est déduit de la machine.
type moeInstallReq struct {
	Family string `json:"family"`
	Quant  string `json:"quant"`
}

// moePresetName : nom du fichier preset (et donc de son id) pour un choix donné.
func moePresetName(family, quant string) string {
	label := "Classique"
	if family == "swift" {
		label = "Swift 1.5"
	}
	return "FLASH NEXT " + strings.ToUpper(label) + " " + quant
}

// moeFetch télécharge un fichier de la release figée et vérifie son SHA-256.
func moeFetch(asset string) (string, error) {
	want := moeAssetSHA[asset]
	dst := filepath.Join(moeDLDir(), asset)
	if got, err := sha256File(dst); err == nil && got == want {
		lcAppend(asset + " déjà téléchargé")
		return dst, nil
	}
	if err := os.MkdirAll(moeDLDir(), 0o755); err != nil {
		return "", err
	}
	lcPhase("téléchargement de " + asset + "…")
	tmp := dst + ".part"
	if err := downloadWithProgress(moeReleaseBase+asset, tmp, 0, lcAppend); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("téléchargement de %s : %w", asset, err)
	}
	got, err := sha256File(tmp)
	if err != nil {
		return "", err
	}
	if got != want {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("%s : empreinte SHA-256 inattendue (fichier altéré ou incomplet)", asset)
	}
	return dst, os.Rename(tmp, dst)
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// moeRunInstall : le corps de la tâche d'installation.
func moeRunInstall(req moeInstallReq) {
	env := moeDetect()
	if !env.Supported {
		lcFail(fmt.Errorf("%s", env.Reason))
		return
	}
	q, ok := moeQuants[req.Quant]
	if !ok {
		lcFail(fmt.Errorf("quant inconnu : %s", req.Quant))
		return
	}
	fit := moeFitFor(q, env, moeHaveGB(req.Family, q.ID))
	if !fit.OK {
		lcFail(fmt.Errorf("%s ne tient pas sur cette machine : %s", q.ID, fit.Why))
		return
	}

	// 1. le paquet figé : sources (installeur + serveur) et moteur compilé
	src, err := moeFetch(moeSrcAsset)
	if err != nil {
		lcFail(err)
		return
	}
	engine, err := moeFetch(moeEngineAsset)
	if err != nil {
		lcFail(err)
		return
	}
	if _, err := os.Stat(filepath.Join(moeSrcDir(), "setup.py")); err != nil {
		lcPhase("décompression du moteur…")
		if err := extractArchive(src, moeHome()); err != nil {
			lcFail(fmt.Errorf("décompression : %w", err))
			return
		}
	}
	// L'installeur prend le moteur dans un dossier local (--prebuilt) : il le
	// décompresse dans engine-cuda12/ et contrôle qu'il couvre la carte.
	pre := filepath.Join(moeHome(), "prebuilt")
	_ = os.MkdirAll(pre, 0o755)
	if err := copyFile(engine, filepath.Join(pre, moeEngineLocal)); err != nil {
		lcFail(err)
		return
	}

	// 2. l'installeur du moteur, sans question : environnement Python, moteur,
	// modèle (Hugging Face, révisions figées), tête MTP, encodeur vision, pack.
	lcPhase(fmt.Sprintf("installation de %s %s (téléchargement de ~%.0f Go)…", req.Family, q.ID, q.DownloadGB))
	// setup.sh crée l'environnement Python (.venv) puis y relance setup.py :
	// Python 3.10+ et venv sont vérifiés plus haut (moeDetect), il ne
	// tombera donc jamais sur sa branche « sudo apt install ».
	args := []string{"setup.sh", "--setup", "--yes", "--no-start", "--no-browser",
		"--family", req.Family, "--model", q.ID,
		"--context", "131072", "--vision", "gpu",
		"--cuda", "12", "--prebuilt", pre + string(os.PathSeparator),
		"--data-dir", moeDataDir(),
		"--gpu", strconv.Itoa(env.Main),
	}
	// Le mode « peu de RAM » est décidé et appliqué par ajean (MOE_MMAP, voir
	// moeBuildConfig) : l'installeur tourne en mode normal, sans réserver sa
	// propre variante (qui ne voit pas un experts.bin déjà présent).
	args = append(args, "--low-ram", "off")
	extra := "CUDA_DEVICE_ORDER=PCI_BUS_ID\x00PYTHONUNBUFFERED=1"
	// Le journal affiché ne nomme pas le moteur amont (choix produit) : ses
	// lignes passent par moeNeutral avant d'atteindre l'interface.
	setBuildSink(func(l string) { lcAppend(moeNeutral(l)) })
	defer setBuildSink(lcAppend)
	if err := runStepEnv("installation du moteur", moeSrcDir(), extra, "bash", args...); err != nil {
		if buildWasCanceled() {
			lcFail(fmt.Errorf("installation annulée"))
		} else {
			lcFail(fmt.Errorf("l'installation a échoué : %w (voir le journal)", err))
		}
		return
	}
	cfgPath, err := moeFindConfig(q.ID)
	if err != nil {
		lcFail(err)
		return
	}

	// 3. le preset ajean, puis bascule dessus
	name := moePresetName(req.Family, q.ID)
	preset := filepath.Join(presetsDir(), name+".env")
	body := strings.Join([]string{
		"# NAME=" + moeFamilyLabel(req.Family) + " " + q.ID,
		"ENGINE=moe",
		"MOE_CONFIG=" + cfgPath,
		"MOE_MAIN_GPU=" + strconv.Itoa(env.Main),
		"MOE_HELPER_GPU=" + strconv.Itoa(env.Helper),
		"MOE_VISION=1",
		"MOE_MMAP=" + map[bool]string{true: "1", false: "0"}[fit.Mmap],
		"MOE_DROP=" + map[bool]string{true: "1", false: "0"}[fit.Drop],
		// la meilleure qualité validée sur le serveur de test : cache KV en fp16
		// (32K en VRAM, le reste en RAM pour ne pas prendre la place des experts)
		// et 3 jetons de brouillon MTP (mesuré meilleur que 4)
		"MOE_KV=fp16",
		"MOE_KV_RESIDENT=32768",
		"MOE_SPEC=3",
		"CTX=131072",
		"REASONING=off",
		"",
	}, "\n")
	if err := os.MkdirAll(presetsDir(), 0o755); err != nil {
		lcFail(err)
		return
	}
	if err := os.WriteFile(preset, []byte(body), 0o644); err != nil {
		lcFail(err)
		return
	}
	lcPhase("activation du modèle (premier chargement : quelques minutes)…")
	if err := SwitchToPreset(preset); err != nil {
		lcFail(fmt.Errorf("modèle installé mais bascule impossible : %w", err))
		return
	}
	lcDone(moeFamilyLabel(req.Family) + " " + q.ID + " installé")
}

var reUpstreamName = regexp.MustCompile(`(?i)strata`)

// moeNeutral retire le nom du moteur amont d'une ligne de journal.
func moeNeutral(line string) string {
	// le binaire annonce sa version d'origine (« engine 0.1.39 ») : c'est la 1.0 d'AJEAN MoE
	line = strings.ReplaceAll(line, "engine 0.1.39", "AJEAN MoE "+moeVersion)
	return reUpstreamName.ReplaceAllString(line, "moteur")
}

func moeFamilyLabel(id string) string {
	for _, f := range moeFamilies {
		if f.ID == id {
			if id == "qwen" {
				return "Qwen3.8 Flash Next"
			}
			return "Qwen3.8 Flash Next " + f.Label
		}
	}
	return id
}

// moeFindConfig : la config de lancement que l'installeur a écrite
// (<nom>-<tag>.json à la racine des sources), celle du quant demandé.
func moeFindConfig(quant string) (string, error) {
	matches, _ := filepath.Glob(filepath.Join(moeSrcDir(), "strata-*.json"))
	q := strings.ToLower(quant)
	for _, m := range matches {
		if strings.Contains(strings.ToLower(filepath.Base(m)), q) && !strings.HasSuffix(m, "-ajean.json") {
			return m, nil
		}
	}
	return "", fmt.Errorf("l'installeur n'a pas laissé de configuration pour %s dans %s", quant, moeSrcDir())
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ---------------------------------------------------------------------------
// Lancement (ajean serve)
// ---------------------------------------------------------------------------

// moeTuning : réglages validés sur le serveur de test (2026-10-05), ajoutés
// à la config de l'installeur. Chacun a été mesuré en A/B.
//
//   - --prefill 16384     : +15 % de lecture sur les longs prompts (40K) ;
//   - --short-read 150    : les petits ajouts (< 150 jetons) sont lus comme en
//     génération, sans le coût fixe du chemin par blocs : ~40 % plus rapides ;
//   - --prompt-cache-root 256 : point de reprise posé à la fin du prompt système
//     (défaut 2048, jamais atteint par ajean) : une nouvelle conversation
//     démarre en ~0,3 s au lieu de 4-5 s.
var moeTuning = map[string]string{
	"--prefill":           "16384",
	"--short-read":        "150",
	"--prompt-cache-root": "256",
}

// moeEnvTuning : variables d'environnement du moteur.
//   - STRATA_PLE_BATCH=0 : le PLE batché du prefill plante sur nos cartes
//     (« native PLE postops launch: illegal memory access ») ;
//   - STRATA_NO_LARGEPAGES=1 : sans, MADV_HUGEPAGE fige le démarrage de longues
//     minutes dans le compactage mémoire du noyau.
var moeEnvTuning = []string{"STRATA_PLE_BATCH=0", "STRATA_NO_LARGEPAGES=1"}

func setArg(args []string, flag, value string) []string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			args[i+1] = value
			return args
		}
	}
	return append(args, flag, value)
}

// dropFlag retire un drapeau SANS valeur.
func dropFlag(args []string, flag string) []string {
	out := args[:0:0]
	for _, a := range args {
		if a != flag {
			out = append(out, a)
		}
	}
	return out
}

// dropFlagValue retire un drapeau ET sa valeur.
func dropFlagValue(args []string, flag string) []string {
	out := args[:0:0]
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// moeBuildConfig lit la config de l'installeur et y applique les réglages
// d'ajean. Pure (testable) : ne lance rien.
func moeBuildConfig(base map[string]any, cfg map[string]string, apiKey string) (map[string]any, error) {
	raw, _ := json.Marshal(base)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	var args []string
	if list, ok := out["args"].([]any); ok {
		for _, a := range list {
			args = append(args, fmt.Sprint(a))
		}
	} else {
		return nil, fmt.Errorf("configuration du moteur sans « args »")
	}
	for flag, v := range moeTuning {
		args = setArg(args, flag, v)
	}
	helper := strings.TrimSpace(cfg["MOE_HELPER_GPU"])
	hasHelper := moeHelperOn(cfg)
	// Deuxième carte en aide : elle garde et CALCULE sa part des experts (le
	// chemin « helper » du moteur), la carte principale reste CUDA0. Les cartes
	// sont posées par CUDA_VISIBLE_DEVICES (serveMoe), PAS par la clé « gpu » :
	// avec deux cartes, le serveur y lirait une répartition des couches
	// (--layer-split), un autre mode que celui mesuré.
	if hasHelper {
		args = setArg(args, "--expert-cache-device1", "auto")
		if !hasArg(args, "--remote-expert-opt") {
			args = append(args, "--remote-expert-opt")
		}
		delete(out, "gpu")
		// réserve de VRAM posée par l'installeur : absente de la config mesurée,
		// elle retire du cache d'experts à la principale
		args = dropFlagValue(args, "--vram-reserve-mib")
		// l'encodeur d'images sur la carte d'aide : la principale garde toute sa
		// VRAM pour les experts (mesuré : -1,5 % au lieu de -3 % sur la principale)
		if v, ok := out["vision"].(map[string]any); ok {
			if n, err := strconv.Atoi(helper); err == nil {
				v["cuda_device"] = n
			}
		}
	}
	// Mode « peu de RAM » : l'installeur écrit --mmap-experts / --resident-experts.
	// ajean applique à la place la variante mesurée avec la carte d'aide : le pack
	// experts.bin mappé tel quel (STRATA_ARENA_MMAP, le système garde en cache ce
	// que la RAM permet), sans part PCIe (le mappage n'en donne pas l'adresse GPU).
	// Le moteur écrit experts.bin lui-même au premier démarrage s'il manque ;
	// l'installeur a réservé sa place sur le disque.
	env := map[string]any{}
	if e, ok := out["env"].(map[string]any); ok {
		env = e
	}
	if hasHelper && cfg["MOE_DROP"] == "1" && moeDropReady(args) {
		// Les experts de la carte d'aide hors RAM, le reste verrouillé en RAM
		// (moeDropFits) : la principale relit aussi par PCIe une part de ce qui
		// lui manque (part automatique) ; la réserve de la carte d'aide est figée.
		args = dropFlag(dropFlag(args, "--mmap-experts"), "--resident-experts")
		args = dropFlagValue(args, "--pcie-frac")
		delete(env, "STRATA_ARENA_MMAP")
		env["STRATA_REMOTE_DROP"] = "1"
		// ses experts sont relus de sa VRAM à la lecture d'un prompt, par les fils
		// de copie : 8 fils et 32 tampons (le moteur en prendrait 32 et 128)
		env["STRATA_STAGER_THREADS"] = "8"
		env["STRATA_STAGER_RING"] = "32"
	} else if cfg["MOE_MMAP"] == "1" || cfg["MOE_DROP"] == "1" || hasArg(args, "--mmap-experts") || hasArg(args, "--resident-experts") {
		// (MOE_DROP sans experts.bin : ce lancement l'écrit, en mmap)
		args = dropFlag(dropFlag(args, "--mmap-experts"), "--resident-experts")
		args = setArg(args, "--pcie-frac", "0")
		env["STRATA_ARENA_MMAP"] = "1"
	}
	for _, kv := range moeEnvTuning {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	out["env"] = env
	if ctx := strings.TrimSpace(cfg["CTX"]); ctx != "" {
		args = setArg(args, "--max-context", ctx)
	}
	// réglages imposés par le preset (sinon : ceux de l'installeur)
	if kv := strings.TrimSpace(cfg["MOE_KV"]); kv != "" {
		args = setArg(args, "--kv", kv)
	}
	if sp := strings.TrimSpace(cfg["MOE_SPEC"]); sp != "" {
		args = setArg(args, "--spec", sp)
	}
	if kr := strings.TrimSpace(cfg["MOE_KV_RESIDENT"]); kr != "" {
		args = setArg(args, "--kv-resident", kr)
	}
	// lecture des images coupée dans les réglages : ni encodeur ni --vision
	if cfg["MOE_VISION"] == "0" {
		args = dropFlag(args, "--vision")
		delete(out, "vision")
	}
	list := make([]any, len(args))
	for i, a := range args {
		list[i] = a
	}
	out["args"] = list
	out["port"], _ = strconv.Atoi(firstNonEmpty(cfg["PORT"], "8080"))
	out["host"] = firstNonEmpty(cfg["HOST"], "0.0.0.0")
	if apiKey != "" {
		out["api_key"] = apiKey
	}
	return out, nil
}

// moeDropReady : le mode « experts de la carte d'aide hors RAM » lit les experts
// dans experts.bin (écrit au premier lancement, en mmap) et demande notre moteur.
func moeDropReady(args []string) bool {
	pack := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--pack" {
			pack = args[i+1]
		}
	}
	if pack == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(pack, "experts.bin")); err != nil {
		return false
	}
	return moeEngineInstalled() == moeEngineAsset
}

// moeEngineMarker : le paquet dont vient le moteur installé.
func moeEngineMarker() string { return filepath.Join(moeSrcDir(), "engine-cuda12", ".ajean-engine") }

func moeEngineInstalled() string {
	b, _ := os.ReadFile(moeEngineMarker())
	return strings.TrimSpace(string(b))
}

// moeEnsureEngine : remplace un moteur installé d'une autre révision par celui
// du paquet attendu (téléchargé et vérifié si besoin). En cas d'échec l'ancien
// reste, et le mode qui demande le nouveau n'est pas activé (moeDropReady).
func moeEnsureEngine() error {
	if moeEngineInstalled() == moeEngineAsset {
		return nil
	}
	dir := filepath.Join(moeSrcDir(), "engine-cuda12")
	if _, err := os.Stat(dir); err != nil {
		return err
	}
	zp, err := moeFetch(moeEngineAsset)
	if err != nil {
		return err
	}
	z, err := zip.OpenReader(zp)
	if err != nil {
		return err
	}
	defer z.Close()
	for _, f := range z.File {
		name := filepath.Base(f.Name)
		if name != "strata" && name != "strata-vision" && name != "BUILD.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		tmp := filepath.Join(dir, name+".new")
		w, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err == nil {
			_, err = io.Copy(w, rc)
			if cerr := w.Close(); err == nil {
				err = cerr
			}
		}
		rc.Close()
		if err != nil {
			_ = os.Remove(tmp)
			return err
		}
		// rename : remplace le binaire même s'il tourne encore
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return os.WriteFile(moeEngineMarker(), []byte(moeEngineAsset+"\n"), 0o644)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// moeHelperOn : la carte d'aide existe et n'a pas été coupée dans les réglages
// (MOE_HELPER=0 la coupe en gardant son index pour la remettre).
func moeHelperOn(cfg map[string]string) bool {
	h := strings.TrimSpace(cfg["MOE_HELPER_GPU"])
	return h != "" && h != "-1" && cfg["MOE_HELPER"] != "0"
}

// moeCudaDevices : ordre des cartes vu par le moteur, la principale en CUDA0.
func moeCudaDevices(cfg map[string]string) string {
	main := strings.TrimSpace(cfg["MOE_MAIN_GPU"])
	if main == "" || main == "-1" {
		return ""
	}
	if moeHelperOn(cfg) {
		return main + "," + strings.TrimSpace(cfg["MOE_HELPER_GPU"])
	}
	return main
}

// serveMoe : la branche moteur MoE de `ajean serve`. Écrit la config finale à
// côté de celle de l'installeur puis remplace le process par le serveur du moteur
// (comme llama-server : systemd supervise directement le moteur).
func serveMoe(cfg map[string]string) error {
	path := strings.TrimSpace(cfg["MOE_CONFIG"])
	if path == "" {
		return fmt.Errorf("MOE_CONFIG non défini : réinstaller le modèle depuis l'interface")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("configuration du moteur introuvable : %s", path)
	}
	var base map[string]any
	if err := json.Unmarshal(raw, &base); err != nil {
		return fmt.Errorf("configuration du moteur illisible (%s) : %w", path, err)
	}
	if err := moeEnsureEngine(); err != nil {
		fmt.Fprintf(os.Stderr, "[ajean serve] moteur %s non installé (%v) : l'ancien reste en place\n", moeEngineAsset, err)
	}
	final, err := moeBuildConfig(base, cfg, readAPIKey())
	if err != nil {
		return err
	}
	srcDir := filepath.Dir(path)
	py := filepath.Join(srcDir, ".venv", "bin", "python")
	if _, err := os.Stat(py); err != nil {
		return fmt.Errorf("environnement Python du moteur absent (%s) : réinstaller le modèle", py)
	}
	// Tout ce qu'ajean lance porte le nom du moteur MoE d'AJEAN : binaires,
	// lanceur, config et journal (les noms d'origine restent dans le dossier
	// d'installation, qu'ils ne quittent pas).
	tag := moeConfigTag(path)
	final["log"] = filepath.Join(moeHome(), "ajean-moe-"+tag+".log")
	if exe, ok := final["exe"].(string); ok {
		final["exe"] = moeAlias(exe, "ajean-moe-engine")
		// le serveur lit la version du moteur dans BUILD.json, à côté du binaire
		moeAlias(filepath.Join(filepath.Dir(exe), "BUILD.json"), "BUILD.json")
	}
	_ = os.Remove(strings.TrimSuffix(path, ".json") + "-ajean.json") // ancien emplacement
	if v, ok := final["vision"].(map[string]any); ok {
		if exe, ok := v["exe"].(string); ok {
			v["exe"] = moeAlias(exe, "ajean-moe-vision")
		}
	}
	out := filepath.Join(moeHome(), "ajean-moe-"+tag+".json")
	data, _ := json.MarshalIndent(final, "", " ")
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	launcher, err := moeWriteLauncher(srcDir, py)
	if err != nil {
		return err
	}
	_ = os.Setenv("CUDA_DEVICE_ORDER", "PCI_BUS_ID")
	if dev := moeCudaDevices(cfg); dev != "" {
		_ = os.Setenv("CUDA_VISIBLE_DEVICES", dev)
	}
	port := fmt.Sprint(final["port"])
	if err := waitPortFree(fmt.Sprint(final["host"]), port, 5e9); err != nil {
		return err
	}
	args := []string{launcher, "--config", out, "--port", port}
	_ = os.Chdir(srcDir)
	fmt.Fprintf(os.Stderr, "[ajean serve] AJEAN MoE %s  config=%s  port=%s  gpu=%s\n", moeVersion, filepath.Base(out), port, os.Getenv("CUDA_VISIBLE_DEVICES"))
	_ = putBytes(bkState, engineCmdlineKey, []byte(engineCmdline(args)))
	return execServer(launcher, args)
}

// moeConfigTag : le modèle d'une config de l'installeur (strata-swift-iq3_xxs.json → swift-iq3_xxs).
func moeConfigTag(path string) string {
	b := strings.TrimSuffix(filepath.Base(path), ".json")
	if i := strings.Index(b, "-"); i >= 0 {
		b = b[i+1:]
	}
	return b
}

// moeAlias : un lien moeHome()/bin/<name> vers un binaire du moteur, recréé à
// chaque lancement ; le process apparaît sous ce nom. En cas d'échec, le chemin
// d'origine.
func moeAlias(target, name string) string {
	dir := filepath.Join(moeHome(), "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return target
	}
	link := filepath.Join(dir, name)
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return target
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return target
	}
	return link
}

// moeWriteLauncher : le lanceur moeHome()/ajean-moe, qui démarre le serveur du
// moteur (son nom technique d'origine reste à l'intérieur).
func moeWriteLauncher(srcDir, py string) (string, error) {
	p := filepath.Join(moeHome(), "ajean-moe")
	body := "#!/bin/sh\n# AJEAN MoE : serveur du moteur MoE d'AJEAN (lancé par ajean serve)\n" +
		"cd '" + srcDir + "' && exec '" + py + "' '" + filepath.Join(srcDir, "serve", "server.py") + "' --engine strata \"$@\"\n"
	if err := os.WriteFile(p+".new", []byte(body), 0o755); err != nil {
		return "", err
	}
	return p, os.Rename(p+".new", p)
}

// moeVisionActive : le preset MoE actif lit les images (encodeur installé).
func moeVisionActive() bool {
	cfg := ReadConfig()
	return isMoeConfig(cfg) && cfg["MOE_VISION"] != "0"
}

// ---------------------------------------------------------------------------
// API
// ---------------------------------------------------------------------------

// handleMoe (GET) : la machine, ce qui y tient, la recommandation par
// version, et les modèles déjà installés (presets ENGINE=moe).
func handleMoe(w http.ResponseWriter, r *http.Request) {
	env := moeDetect()
	type famView struct {
		moeFamily
		Fits      []moeFit `json:"fits"`
		Recommend string   `json:"recommend"`
	}
	fams := []famView{}
	for _, f := range moeFamilies {
		v := famView{moeFamily: f, Recommend: moeRecommend(f.ID, env)}
		for _, id := range f.Quants {
			v.Fits = append(v.Fits, moeFitFor(moeQuants[id], env, moeHaveGB(f.ID, id)))
		}
		fams = append(fams, v)
	}
	installed := []string{}
	active := map[string]bool{}
	if list, err := ListPresets(); err == nil {
		for _, p := range list {
			if b, err := os.ReadFile(filepath.Join(presetsDir(), p.ID+".env")); err == nil && isMoeConfig(parseEnv(string(b))) {
				installed = append(installed, p.ID)
				active[p.ID] = p.Active
			}
		}
	}
	// chaque choix déjà installé est marqué : la fenêtre propose alors de
	// l'activer au lieu de le réinstaller
	for i := range fams {
		for j := range fams[i].Fits {
			id := moePresetName(fams[i].ID, fams[i].Fits[j].Quant.ID)
			if a, ok := active[id]; ok {
				fams[i].Fits[j].Preset, fams[i].Fits[j].Active = id, a
				fams[i].Fits[j].Details = moeDetailsFor(id, env)
			}
		}
	}
	sendJSON(w, 200, map[string]any{
		"version": moeVersion, "env": env, "families": fams, "installed": installed,
	})
}

// moeSettingsReq : les réglages modifiables d'un modèle installé.
type moeSettingsReq struct {
	Preset string `json:"preset"`
	Ctx    int    `json:"ctx"`
	KV     string `json:"kv"`
	Spec   int    `json:"spec"`
	Vision bool   `json:"vision"`
	Helper bool   `json:"helper"`
}

// moeMaxCtx : contexte natif maximal de Qwen3.8 Flash Next.
const moeMaxCtx = 262144

// moeApplySettings réécrit les clés du preset (pure, testable).
func moeApplySettings(content string, r moeSettingsReq) (string, error) {
	switch {
	// contexte : de 32K au maximum du modèle (262 144), par pas de 4096
	case r.Ctx < 32768 || r.Ctx > moeMaxCtx || r.Ctx%4096 != 0:
		return "", fmt.Errorf("contexte hors limites : %d", r.Ctx)
	case r.KV != "fp16" && r.KV != "int8":
		return "", fmt.Errorf("cache KV inconnu : %s", r.KV)
	case r.Spec < 2 || r.Spec > 4:
		return "", fmt.Errorf("MTP hors limites : %d", r.Spec)
	}
	set := map[string]string{
		"CTX": strconv.Itoa(r.Ctx), "MOE_KV": r.KV, "MOE_SPEC": strconv.Itoa(r.Spec),
		"MOE_VISION": map[bool]string{true: "1", false: "0"}[r.Vision],
		"MOE_HELPER": map[bool]string{true: "1", false: "0"}[r.Helper],
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		k, _, _ := strings.Cut(strings.TrimSpace(l), "=")
		if _, ok := set[k]; ok {
			continue
		}
		lines = append(lines, l)
	}
	for _, k := range []string{"CTX", "MOE_KV", "MOE_SPEC", "MOE_VISION", "MOE_HELPER"} {
		lines = append(lines, k+"="+set[k])
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// handleMoeSettings (POST) : enregistre les réglages d'un modèle installé ; s'il
// est actif, la config est réappliquée et le moteur relancé.
func handleMoeSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]any{"ok": false, "error": "POST attendu"})
		return
	}
	var req moeSettingsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if strings.ContainsAny(req.Preset, "/\\") || req.Preset == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "preset invalide"})
		return
	}
	path := filepath.Join(presetsDir(), req.Preset+".env")
	b, err := os.ReadFile(path)
	if err != nil || !isMoeConfig(parseEnv(string(b))) {
		sendJSON(w, 404, map[string]any{"ok": false, "error": "modèle introuvable"})
		return
	}
	next, err := moeApplySettings(string(b), req)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	restarted := false
	if list, err := ListPresets(); err == nil {
		for _, p := range list {
			if p.ID == req.Preset && p.Active {
				if err := SwitchToPreset(path); err != nil {
					sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
					return
				}
				restarted = true
			}
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "restarted": restarted})
}

// handleMoeInstall (POST {family, quant}) : lance la tâche d'installation.
// Une seule tâche à la fois, partagée avec llama.cpp (même suivi dans l'UI).
func handleMoeInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]any{"ok": false, "error": "POST attendu"})
		return
	}
	var req moeInstallReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ok := false
	for _, f := range moeFamilies {
		if f.ID == req.Family && moeHas(f.Quants, req.Quant) {
			ok = true
		}
	}
	if !ok {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "version ou quant inconnu"})
		return
	}
	if err := startLcJob("moe", func() { moeRunInstall(req) }); err != nil {
		sendJSON(w, 409, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}
