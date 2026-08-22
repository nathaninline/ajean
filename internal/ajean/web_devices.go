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
