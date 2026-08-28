package ajean

// backend_gpu_rocm.go — télémétrie GPU AMD via `rocm-smi`, en DERNIER repli après
// nvidia-smi puis amd-smi (voir handleVram).
//
// Pourquoi : sur beaucoup d'installations ROCm (Debian, Bazzite, Arch…), l'outil
// présent est `rocm-smi`, PAS `amd-smi` (plus récent, pas toujours packagé). Sans
// ce repli, la carte « GPU / VRAM » de l'UI affichait « (pas de GPU) » alors que
// l'inférence tournait bien sur le GPU AMD (issue #49). On reste en LECTURE SEULE
// et best-effort : le pilotage (choix de carte, tensor-split) passe toujours par
// le moteur llama.cpp (--device), jamais par cet outil.
//
// Le schéma JSON de rocm-smi varie beaucoup d'une version à l'autre (libellés de
// clés différents, valeurs en octets ou en chaînes suffixées). L'extraction est
// donc TOLÉRANTE : on repère les champs par sous-chaîne insensible à la casse
// plutôt que par un nom exact, et on ignore ce qu'on ne sait pas lire.

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// rocmVramGPUs renvoie les GPU AMD au même format que handleVram
// ({name, used, total, util, temp}, VRAM en MiB), ou nil si rocm-smi est absent
// ou muet. Les valeurs VRAM de rocm-smi sont en OCTETS → converties en MiB pour
// s'aligner sur nvidia-smi (que l'UI attend en MiB).
func rocmVramGPUs() []map[string]any {
	if !hasTool("rocm-smi") {
		return nil
	}
	// --json rend un objet { "card0": {...}, "card1": {...} }. Les drapeaux
	// demandent explicitement nom, VRAM, charge et température ; une version qui
	// ignore un drapeau inconnu rend juste moins de champs, sans casser le JSON.
	out, err := hideCmd(exec.Command("rocm-smi",
		"--showproductname", "--showmeminfo", "vram", "--showuse", "--showtemp", "--json")).Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	return rocmParseJSON(out)
}

// rocmParseJSON décode la sortie `rocm-smi --json` en liste de GPU. Séparé pour
// être testable sans rocm-smi installé. rocm-smi peut émettre une exception AVANT
// le JSON sur certaines machines multi-GPU (« Exception caught: map::at », issue
// #49) : on saute donc tout ce qui précède la première accolade.
func rocmParseJSON(out []byte) []map[string]any {
	if i := strings.IndexByte(string(out), '{'); i > 0 {
		out = out[i:]
	}
	var root map[string]map[string]any
	if json.Unmarshal(out, &root) != nil || len(root) == 0 {
		return nil
	}
	// Clés « card0 », « card1 »… triées pour un ordre stable (map non ordonnée).
	cards := make([]string, 0, len(root))
	for k := range root {
		if strings.HasPrefix(strings.ToLower(k), "card") {
			cards = append(cards, k)
		}
	}
	if len(cards) == 0 {
		return nil
	}
	sort.Strings(cards)
	gpus := []map[string]any{}
	for i, key := range cards {
		e := root[key]
		name := rocmStr(e, "card series", "card model", "gpu id")
		if name == "" {
			name = fmt.Sprintf("AMD GPU %d", i)
		}
		gpus = append(gpus, map[string]any{
			"name":  name,
			"used":  rocmBytesToMiB(rocmNum(e, "vram total used memory")),
			"total": rocmBytesToMiB(rocmNum(e, "vram total memory")),
			"util":  rocmPos(rocmNum(e, "gpu use (%)", "gpu use")),
			"temp":  rocmTemp(e),
		})
	}
	return gpus
}

// rocmStr renvoie la première valeur chaîne d'une clé dont le libellé CONTIENT
// l'un des fragments (insensible à la casse). "" si aucune.
func rocmStr(e map[string]any, frags ...string) string {
	for _, frag := range frags {
		for k, v := range e {
			if strings.Contains(strings.ToLower(k), frag) {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
	}
	return ""
}

// rocmNum lit la première valeur NUMÉRIQUE d'une clé contenant l'un des fragments.
// rocm-smi rend le plus souvent des chaînes (« 25753026560 », « 45.0 », « 3 »).
// -1 = introuvable.
func rocmNum(e map[string]any, frags ...string) int64 {
	for _, frag := range frags {
		for k, v := range e {
			if !strings.Contains(strings.ToLower(k), frag) {
				continue
			}
			switch t := v.(type) {
			case float64:
				return int64(t)
			case json.Number:
				if n, err := t.Int64(); err == nil {
					return n
				}
			case string:
				s := strings.TrimSpace(t)
				// Suffixes possibles (« 45.0 C », « 3 % »…) : on ne garde que le nombre.
				if f, err := strconv.ParseFloat(strings.Fields(s+" ")[0], 64); err == nil {
					return int64(f)
				}
			}
		}
	}
	return -1
}

// rocmTemp choisit une température représentative : point chaud (junction) de
// préférence — c'est lui qui déclenche le throttling — puis edge, puis mémoire.
func rocmTemp(e map[string]any) int {
	for _, frag := range []string{"junction", "edge", "memory"} {
		if v := rocmNum(e, "temperature (sensor "+frag+")"); v > 0 {
			return int(v)
		}
	}
	if v := rocmNum(e, "temperature"); v > 0 {
		return int(v)
	}
	return 0
}

// rocmBytesToMiB convertit des octets en MiB (unité attendue par l'UI, comme
// nvidia-smi). Une valeur inconnue (-1) ou nulle rend 0.
func rocmBytesToMiB(b int64) int {
	if b <= 0 {
		return 0
	}
	return int(b / (1024 * 1024))
}

// rocmPos ramène « inconnu » (-1) à 0 pour les métriques affichées.
func rocmPos(n int64) int {
	if n < 0 {
		return 0
	}
	return int(n)
}
