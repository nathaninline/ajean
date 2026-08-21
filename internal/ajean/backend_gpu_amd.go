package ajean

// backend_gpu_amd.go — télémétrie GPU AMD pour l'UI, via amd-smi.
//
// Pourquoi : la carte « GPU / VRAM » de l'interface (handleVram) n'interrogeait
// que nvidia-smi. Sur une machine AMD (ex. RX 7800 XT sous Bazzite/Fedora), la
// carte n'apparaissait donc pas du tout, alors qu'amd-smi est présent et sait
// tout donner : nom, VRAM totale/utilisée, charge, température (issue #32).
//
// On reste volontairement en LECTURE SEULE et best-effort : le pilotage du GPU
// (sélection de carte, tensor-split) passe toujours par le moteur llama.cpp
// (voir web_devices.go), jamais par cet outil. Ici on ne fait qu'afficher.
//
// amd-smi expose deux sous-commandes JSON complémentaires :
//   - `amd-smi static --json`  → asic.market_name (le nom lisible de la carte)
//   - `amd-smi metric --json`  → mem_usage (total/used), usage (gfx_activity),
//                                temperature (edge/hotspot)
// On les joint par l'index de GPU. Le schéma JSON d'amd-smi varie d'une version
// à l'autre (valeurs tantôt scalaires, tantôt {value,unit}) : l'extraction est
// donc tolérante et ne suppose jamais un type précis.

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// amdVramGPUs renvoie les GPU AMD au même format que handleVram
// ({name, used, total, util, temp}), ou nil si amd-smi est absent / muet.
func amdVramGPUs() []map[string]any {
	if !hasTool("amd-smi") {
		return nil
	}
	names := amdSmiNames()
	metric := amdSmiJSON("metric")
	if len(metric) == 0 {
		return nil
	}
	out := []map[string]any{}
	for i, e := range metric {
		idx := amdNum(e["gpu"])
		if idx < 0 {
			idx = i
		}
		name := names[idx]
		if name == "" {
			name = fmt.Sprintf("AMD GPU %d", idx)
		}
		out = append(out, map[string]any{
			"name":  name,
			"used":  amdPos(amdDig(e, "mem_usage", "used_vram")),
			"total": amdPos(amdDig(e, "mem_usage", "total_vram")),
			"util":  amdPos(amdDig(e, "usage", "gfx_activity")),
			"temp":  amdPos(amdTemp(e)),
		})
	}
	return out
}

// amdPos ramène « inconnu » (-1) à 0 pour les métriques affichées, où les deux
// se lisent pareil (rien à montrer).
func amdPos(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// amdSmiNames lit le nom lisible de chaque carte (asic.market_name), indexé par
// numéro de GPU. Vide si `amd-smi static` échoue : on retombe alors sur un nom
// générique, sans empêcher l'affichage du reste.
func amdSmiNames() map[int]string {
	names := map[int]string{}
	for i, e := range amdSmiJSON("static") {
		idx := amdNum(e["gpu"])
		if idx < 0 {
			idx = i
		}
		if asic, ok := e["asic"].(map[string]any); ok {
			if s, ok := asic["market_name"].(string); ok && strings.TrimSpace(s) != "" {
				names[idx] = strings.TrimSpace(s)
			}
		}
	}
	return names
}

// amdSmiJSON lance `amd-smi <sub> --json` et décode la sortie en liste
// d'objets. Accepte aussi un objet seul (certaines versions n'enveloppent pas
// dans un tableau quand il n'y a qu'un GPU).
func amdSmiJSON(sub string) []map[string]any {
	out, err := hideCmd(exec.Command("amd-smi", sub, "--json")).Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	var arr []map[string]any
	if json.Unmarshal(out, &arr) == nil && len(arr) > 0 {
		return arr
	}
	var one map[string]any
	if json.Unmarshal(out, &one) == nil && len(one) > 0 {
		return []map[string]any{one}
	}
	return nil
}

// amdDig descend une suite de clés dans des objets imbriqués puis lit la valeur
// finale en nombre (via amdNum). Renvoie 0 si le chemin n'existe pas.
func amdDig(m map[string]any, keys ...string) int {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur, ok = mm[k]
		if !ok {
			return 0
		}
	}
	return amdNum(cur)
}

// amdTemp choisit une température représentative parmi celles exposées, dans
// l'ordre où elles ont un sens pour « la carte chauffe » : bord, point chaud,
// puis mémoire.
func amdTemp(e map[string]any) int {
	for _, k := range []string{"edge", "hotspot", "junction", "mem"} {
		if v := amdDig(e, "temperature", k); v > 0 {
			return v
		}
	}
	return 0
}

// amdNum extrait un entier d'une valeur JSON amd-smi, quelle que soit sa forme :
// nombre brut, chaîne (« 42 », « 42 % »), ou objet {value, unit}. Renvoie -1
// pour une valeur absente/illisible afin de distinguer « inconnu » de « zéro »
// là où l'appelant en a besoin (l'index de GPU) ; les métriques, elles,
// traitent -1 comme 0 côté affichage.
func amdNum(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n)
		}
	case string:
		s := strings.TrimSpace(t)
		s = strings.TrimRight(s, " %°CcF")
		s = strings.TrimSpace(s)
		if f, err := strconv.ParseFloat(strings.Fields(s + " ")[0], 64); err == nil {
			return int(f)
		}
	case map[string]any:
		if inner, ok := t["value"]; ok {
			return amdNum(inner)
		}
	}
	return -1
}
