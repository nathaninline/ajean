// web_devices.go — liste des GPU tels que les voit UN moteur donné, pour que
// l'éditeur de modèle propose « quel(s) GPU utiliser » et le tensor split.
//
// Pourquoi interroger le binaire plutôt que nvidia-smi : les noms de device
// dépendent du backend compilé dans CE moteur, et l'ordre aussi. Sur la même
// machine, un build CUDA annonce « CUDA0 = RTX 5060 Ti, CUDA1 = GTX 1650 » là où
// le binaire précompilé (Vulkan) annonce l'inverse, « Vulkan0 = GTX 1650 ».
// Proposer une liste issue de nvidia-smi ferait donc choisir la mauvaise carte.
// C'est aussi pour ça que le réglage vit dans le PRESET (--device, compris par
// tous les backends) et non dans CUDA_VISIBLE_DEVICES, qui n'a aucun effet sur
// un moteur Vulkan.
package ajean

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// deviceLine matche « CUDA0: NVIDIA GeForce RTX 5060 Ti (15849 MiB, 15579 MiB free) ».
var deviceLine = regexp.MustCompile(`^\s*([A-Za-z]+\d+):\s*(.+?)\s*\((\d+)\s*MiB,\s*(\d+)\s*MiB free\)\s*$`)

// parseListDevices extrait les devices de la sortie de `llama-server --list-devices`.
func parseListDevices(out string) []map[string]any {
	devs := []map[string]any{}
	for _, line := range strings.Split(out, "\n") {
		m := deviceLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		total, _ := strconv.Atoi(m[3])
		free, _ := strconv.Atoi(m[4])
		devs = append(devs, map[string]any{
			"id": m[1], "name": m[2], "total_mib": total, "free_mib": free,
		})
	}
	return devs
}

// Cache mémoire : lister les devices lance le moteur (init CUDA/Vulkan +
// énumération), soit une à trois secondes. Sans cache, l'encart « cartes
// graphiques » de l'éditeur apparaissait plusieurs secondes après le reste.
type devCacheEntry struct {
	devices []map[string]any
	at      time.Time
}

var (
	devCacheMu sync.Mutex
	devCache   = map[string]devCacheEntry{}
)

const devCacheTTL = 10 * time.Minute

func devCacheGet(bin string) ([]map[string]any, bool) {
	devCacheMu.Lock()
	defer devCacheMu.Unlock()
	e, ok := devCache[bin]
	if !ok || time.Since(e.at) > devCacheTTL {
		return nil, false
	}
	return e.devices, true
}

func devCachePut(bin string, devs []map[string]any) {
	devCacheMu.Lock()
	devCache[bin] = devCacheEntry{devices: devs, at: time.Now()}
	devCacheMu.Unlock()
}

// Cache PERSISTANT de la dernière énumération RÉUSSIE (exit 0) de --list-devices,
// par clé moteur+CVD. But : quand le moteur tourne et sature déjà une carte, un
// nouvel appel --list-devices peut PLANTER (CUDA out of memory en initialisant le
// device plein) et ne renvoyer qu'une partie des cartes — l'UI perdait alors le
// tensor split (slider caché faute de 2e GPU) après un simple redémarrage de
// l'interface, qui vide le cache mémoire. On garde donc sur disque la dernière
// liste complète : identité, ordre et mémoire TOTALE sont des faits matériels
// stables issus du moteur lui-même (pas de nvidia-smi, dont l'ordre peut différer,
// voir l'en-tête de ce fichier). Seule la mémoire LIBRE y est périmée, ce qui est
// sans importance pour choisir les cartes et régler la répartition.
var devPersistMu sync.Mutex

func devPersistPath() string { return filepath.Join(AjeanHome(), "devices.json") }

func devPersistLoad() map[string][]map[string]any {
	m := map[string][]map[string]any{}
	if b, err := os.ReadFile(devPersistPath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func devPersistGet(key string) ([]map[string]any, bool) {
	devPersistMu.Lock()
	defer devPersistMu.Unlock()
	d, ok := devPersistLoad()[key]
	return d, ok && len(d) > 0
}

func devPersistPut(key string, devs []map[string]any) {
	devPersistMu.Lock()
	defer devPersistMu.Unlock()
	m := devPersistLoad()
	m[key] = devs
	if b, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(devPersistPath(), b, 0o644)
	}
}

// fillMissingMemory complète les mémoires que le moteur n'a pas su lire. Quand
// une carte est déjà saturée par le modèle en cours, llama.cpp annonce 0 Mio —
// l'UI n'avait alors rien à afficher pour elle, ce qui donnait une liste
// incohérente (une carte avec sa taille, l'autre sans).
//
// On complète depuis nvidia-smi PAR CORRESPONDANCE DE NOM, et uniquement pour
// ça : la mémoire totale est une donnée matérielle fixe, indépendante du
// backend. L'ordre et les identifiants des devices, eux, appartiennent au
// moteur (CUDA0 et Vulkan0 ne désignent pas la même carte) et ne doivent jamais
// venir de nvidia-smi. Un nom en double (deux cartes identiques) rend la
// correspondance ambiguë : on préfère alors ne rien dire.
func fillMissingMemory(devs []map[string]any) {
	missing := false
	for _, d := range devs {
		if n, _ := d["total_mib"].(int); n <= 0 {
			missing = true
		}
	}
	if !missing {
		return
	}
	gpus, err := detectGPUs()
	if err != nil {
		return
	}
	totals := map[string]int{}
	dup := map[string]bool{}
	for _, g := range gpus {
		name := strings.TrimSpace(g.Name)
		if _, seen := totals[name]; seen {
			dup[name] = true
			continue
		}
		if mb, err := strconv.Atoi(strings.TrimSpace(g.MemTotal)); err == nil {
			totals[name] = mb
		}
	}
	for _, d := range devs {
		if n, _ := d["total_mib"].(int); n > 0 {
			continue
		}
		name, _ := d["name"].(string)
		if mb, ok := totals[strings.TrimSpace(name)]; ok && !dup[strings.TrimSpace(name)] {
			d["total_mib"] = mb
		}
	}
}

// hasZeroMemory dit si au moins un device annonce une mémoire totale nulle.
func hasZeroMemory(devs []map[string]any) bool {
	for _, d := range devs {
		if n, _ := d["total_mib"].(int); n <= 0 {
			return true
		}
	}
	return false
}

// handleBackendDevices renvoie les devices vus par le moteur passé en `bin`
// (celui du preset en cours d'édition). Sans `bin`, on prend celui de la config
// active.
func handleBackendDevices(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Bin string `json:"bin"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	bin := strings.TrimSpace(req.Bin)
	if bin == "" {
		bin = ReadConfig()["BIN"]
	}
	bin = prebuiltResolveBin(bin)
	if bin == "" || !isFile(bin) {
		sendJSON(w, 200, map[string]any{"ok": false, "error": "moteur introuvable — choisissez d'abord un moteur"})
		return
	}
	// Garde-fou : on n'exécute que le serveur llama.cpp, pas n'importe quel
	// chemin qui passerait par cette requête.
	if base := strings.ToLower(filepath.Base(bin)); base != "llama-server" && base != "llama-server.exe" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "ce chemin n'est pas un llama-server"})
		return
	}
	// L'ordre d'énumération DOIT être celui du serveur en marche, sinon la liste
	// affichée (et donc le --tensor-split que l'utilisateur règle carte par carte)
	// se retrouve inversée par rapport à la réalité. Le moteur réel force
	// CUDA_DEVICE_ORDER=PCI_BUS_ID (+ le filtre CUDA_VISIBLE_DEVICES) dans
	// backend_serve.go ; par défaut CUDA classe « le plus rapide d'abord », ce qui
	// peut être l'ordre INVERSE. On reproduit donc le même environnement ici.
	cfg := ReadConfig()
	cvd := cfg["CUDA_VISIBLE_DEVICES"]
	cacheKey := bin + "\x00" + cvd
	if devs, ok := devCacheGet(cacheKey); ok {
		sendJSON(w, 200, map[string]any{"ok": true, "devices": devs})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	cmd := hideCmd(exec.CommandContext(ctx, bin, "--list-devices"))
	env := libraryPathEnv(filepath.Dir(bin))
	if cvd != "" {
		env = append(env, "CUDA_VISIBLE_DEVICES="+cvd, "CUDA_DEVICE_ORDER=PCI_BUS_ID")
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	devs := parseListDevices(string(out))
	// err != nil = le moteur est sorti en erreur (typiquement il a PLANTÉ en OOM
	// sur une carte déjà pleine pendant qu'il l'énumérait, cf. « CUDA error: out of
	// memory »). La sortie est alors TRONQUÉE : on ne peut pas s'y fier (il manque
	// des cartes). On rend plutôt la dernière liste complète connue, pour ne pas
	// perdre le tensor split pendant que le moteur tourne.
	if err != nil {
		if good, ok := devPersistGet(cacheKey); ok {
			sendJSON(w, 200, map[string]any{"ok": true, "devices": good, "stale": true})
			return
		}
		if len(devs) == 0 {
			sendJSON(w, 200, map[string]any{"ok": false, "error": "le moteur n'a pas répondu : " + err.Error()})
			return
		}
		// Pas de repli disponible : on rend ce qu'on a lu, sans le figer (ni cache
		// mémoire ni persistant) puisque la liste est probablement incomplète.
		sendJSON(w, 200, map[string]any{"ok": true, "devices": devs, "stale": true})
		return
	}
	fillMissingMemory(devs)
	// Quand une carte est déjà saturée par le modèle en cours, le moteur peut
	// annoncer 0 Mio de mémoire : c'est une lecture transitoire, on ne la fige
	// pas dans le cache (sinon l'UI affiche « 0 Go » pendant dix minutes).
	if !hasZeroMemory(devs) {
		devCachePut(cacheKey, devs)
	}
	// Énumération propre (exit 0) = liste complète et faisant foi : on la garde sur
	// disque comme repli pour les futurs appels où le moteur, chargé, ferait planter
	// --list-devices.
	if len(devs) > 0 {
		devPersistPut(cacheKey, devs)
	}
	sendJSON(w, 200, map[string]any{"ok": true, "devices": devs})
}

// vulkanVramGPUs renvoie les GPU au même format que handleVram ({name, used,
// total, util, temp}), en repli quand nvidia-smi/amd-smi/rocm-smi n'ont rien
// donné. Sous Windows, AUCUN de ces trois outils n'est fourni par les pilotes
// AMD ou Intel — la carte « GPU / VRAM » de l'UI affichait donc systématiquement
// « pas de GPU » sur toute machine Windows non-NVIDIA, même quand l'inférence
// tournait très bien dessus via Vulkan.
//
// On réutilise --list-devices du moteur configuré (déjà interrogé par
// handleBackendDevices ci-dessus, via parseListDevices) : il donne l'identité,
// la mémoire totale et libre de CHAQUE GPU que llama.cpp voit réellement — AMD,
// Intel intégré, tout ce qui expose Vulkan — sans dépendance à un outil externe.
// util/temp ne sont pas connus par cette voie (Vulkan n'expose ni l'un ni
// l'autre) : rendus à 0 plutôt qu'omis, pour garder la forme attendue par l'UI.
func vulkanVramGPUs() []map[string]any {
	bin := prebuiltResolveBin(ReadConfig()["BIN"])
	if bin == "" || !isFile(bin) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := hideCmd(exec.CommandContext(ctx, bin, "--list-devices"))
	cmd.Env = libraryPathEnv(filepath.Dir(bin))
	out, _ := cmd.CombinedOutput()
	devs := parseListDevices(string(out))
	if len(devs) == 0 {
		return nil
	}
	gpus := make([]map[string]any, 0, len(devs))
	for _, d := range devs {
		total, _ := d["total_mib"].(int)
		free, _ := d["free_mib"].(int)
		used := total - free
		if used < 0 {
			used = 0
		}
		gpus = append(gpus, map[string]any{
			"name": d["name"], "used": used, "total": total, "util": 0, "temp": 0,
		})
	}
	applyWindowsDedicatedUsage(gpus)
	return gpus
}

// applyWindowsDedicatedUsage corrige "used" et "util" sur Windows avec les
// valeurs RÉELLES de chaque carte, tout process confondu (compteurs système
// « GPU Adapter Memory » + « GPU Engine »). --list-devices, lui, sonde la
// mémoire depuis un process llama.cpp FRAÎCHEMENT lancé rien que pour
// l'énumération : sur AMD, ce process ne voit pas la VRAM déjà retenue par
// l'instance en cours d'exécution (le vrai serveur), donc "used" ressort
// proche de 0 pendant qu'un modèle de 14 Go tourne — et il n'a de toute façon
// aucune notion d'utilisation GPU. "temp" reste à 0 : Windows n'expose la
// température d'aucun GPU par WMI/perfmon (contrairement à nvidia-smi), seule
// une API DirectX bas niveau le permettrait (celle qu'utilise Task Manager
// depuis Windows 11 22H2) — hors de portée d'un simple appel PowerShell.
//
// Reste à savoir QUELLE carte (LUID) correspond à quel device Vulkan : Windows
// n'expose cette correspondance nulle part simplement en PowerShell/WMI. On
// s'appuie donc sur un fait d'architecture, pas une coïncidence du moment : un
// GPU INTÉGRÉ (Intel UHD, iGPU AMD) n'a pas de VRAM propre — Windows compte son
// usage dans "Shared Usage" (RAM système), "Dedicated Usage" y reste ~0 en
// permanence. Un GPU DISCRET (carte dédiée) a de la vraie VRAM : c'est lui qui
// concentre le "Dedicated Usage". On assigne donc les cartes dont le nom Vulkan
// ne contient pas "intel" aux LUID triés par Dedicated Usage décroissant ; les
// cartes intégrées gardent la valeur (proche de 0, donc déjà correcte) issue de
// --list-devices. Best-effort : une erreur PowerShell laisse "used"/"util" tels quels.
func applyWindowsDedicatedUsage(gpus []map[string]any) {
	if runtime.GOOS != "windows" || len(gpus) == 0 {
		return
	}
	adapters := windowsAdapterStats()
	if len(adapters) == 0 {
		return
	}
	sort.Slice(adapters, func(i, j int) bool { return adapters[i].DedicatedMiB > adapters[j].DedicatedMiB })
	// Température : via l'ADL d'AMD (voir web_devices_adl_windows.go). Deux
	// bugs y ont été trouvés et corrigés avant de rebrancher cet appel : une
	// fuite de callback Windows (table épuisée après ~2000 appels cumulés) et
	// un accès concurrent non sérialisé dans atiadlxx.dll (crash sous charge
	// réelle — plusieurs requêtes web en parallèle entrant dans la DLL en même
	// temps). adlAMDHotspotTempC tient maintenant un mutex sur tout son appel ;
	// voir web_devices_adl_windows.go pour le détail des deux incidents.
	amdTemp, amdTempOK := adlAMDHotspotTempC()
	amdTempUsed := false
	ai := 0
	for _, g := range gpus {
		name, _ := g["name"].(string)
		if strings.Contains(strings.ToLower(name), "intel") {
			continue
		}
		if amdTempOK && !amdTempUsed {
			g["temp"] = amdTemp
			amdTempUsed = true
		}
		if ai >= len(adapters) {
			continue
		}
		g["used"] = adapters[ai].DedicatedMiB
		g["util"] = adapters[ai].UtilPct
		ai++
	}
}

// windowsAdapterStat regroupe VRAM dédiée commise (MiB) et % d'utilisation
// max (tous types de moteur confondus : 3D, Compute, Copy…) pour un même LUID
// — les deux mesures viennent du même appel PowerShell pour rester appariées.
type windowsAdapterStat struct {
	DedicatedMiB int
	UtilPct      int
}

// windowsAdapterStats interroge les compteurs de performance Windows « GPU
// Adapter Memory » (VRAM dédiée, par LUID) et « GPU Engine » (utilisation,
// par process+LUID+moteur — on prend le MAX tous moteurs et process confondus
// pour un LUID donné, comme le fait le Gestionnaire des tâches). On filtre
// les adaptateurs dont le total commis est négligeable (<5 Mio) : un
// adaptateur d'affichage virtuel (Parsec, etc.) n'est pas un GPU de calcul et
// fausserait l'appariement.
func windowsAdapterStats() []windowsAdapterStat {
	const script = `$mem = Get-CimInstance Win32_PerfFormattedData_GPUPerformanceCounters_GPUAdapterMemory -ErrorAction Stop | ` +
		`Where-Object { $_.TotalCommitted -gt 5MB } | Select-Object Name, DedicatedUsage; ` +
		`$eng = Get-CimInstance Win32_PerfFormattedData_GPUPerformanceCounters_GPUEngine -ErrorAction SilentlyContinue; ` +
		`$r = @(foreach ($m in $mem) { ` +
		`  $u = 0; ` +
		`  if ($eng) { $hit = $eng | Where-Object { $_.Name -like "*$($m.Name)*" }; if ($hit) { $mx = ($hit | Measure-Object -Property UtilizationPercentage -Maximum).Maximum; if ($mx -gt $u) { $u = $mx } } }; ` +
		`  if ($u -gt 100) { $u = 100 }; ` +
		`  [PSCustomObject]@{dedicated=$m.DedicatedUsage; util=[int]$u} ` +
		`}); ` +
		`[PSCustomObject]@{items=$r} | ConvertTo-Json -Compress -Depth 3`
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := hideCmd(exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)).Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	var parsed struct {
		Items []struct {
			Dedicated int64 `json:"dedicated"`
			Util      int   `json:"util"`
		} `json:"items"`
	}
	if json.Unmarshal(out, &parsed) != nil {
		return nil
	}
	stats := make([]windowsAdapterStat, 0, len(parsed.Items))
	for _, it := range parsed.Items {
		stats = append(stats, windowsAdapterStat{DedicatedMiB: int(it.Dedicated / (1024 * 1024)), UtilPct: it.Util})
	}
	return stats
}
