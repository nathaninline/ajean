package ajean

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A preset's IDENTITY is its filename (ID, without the .env suffix), which is
// always unique. Its DISPLAY name lives in an optional `# NAME=` line inside the
// file, so several presets can share the same display name without overwriting
// each other (their filenames differ — see uniquePresetID).
type Preset struct {
	ID     string // filename without .env — stable, unique identity
	Name   string // display name (# NAME= line, falls back to ID)
	Path   string
	Active bool
}

// presetDisplayName extracts the `# NAME=` value from a preset body, falling
// back to `fallback` (the filename id) when absent — keeps old presets working.
func presetDisplayName(content, fallback string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		i := strings.IndexByte(s, '=')
		if i >= 0 && strings.EqualFold(strings.TrimSpace(s[:i]), "NAME") {
			if v := unquoteValue(strings.TrimSpace(s[i+1:])); v != "" {
				return v
			}
		}
	}
	return fallback
}

// configFingerprint réduit une configuration à l'ENSEMBLE TRIÉ de ses
// affectations effectives, puis le hache. Les clés « appareil » (preservedKeys,
// réappliquées par SwitchToPreset) sont ignorées : sans ça, les MEM_MODE /
// HOST injectés rendraient la config active différente de TOUS les
// presets, et aucun ne serait jamais détecté comme actif.
func configFingerprint(m map[string]string) string {
	pairs := make([]string, 0, len(m))
	for k, v := range m {
		if isPreservedKey(k) {
			continue
		}
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	h := sha1.Sum([]byte(strings.Join(pairs, "\n")))
	return hex.EncodeToString(h[:])
}

// presetFingerprint est configFingerprint appliqué au contenu d'un preset.
func presetFingerprint(content []byte) string {
	return configFingerprint(parseEnv(string(content)))
}

// isPreservedKey reports whether key is one of the device-level preservedKeys.
func isPreservedKey(key string) bool {
	for _, k := range preservedKeys {
		if key == k {
			return true
		}
	}
	return false
}

// ListPresets renvoie tous les presets/*.env, en marquant celui qui correspond
// à la configuration active (clés « appareil » ignorées), triés par nom.
func ListPresets() ([]Preset, error) {
	dir := presetsDir()
	cur := configFingerprint(ReadConfig())
	// Preset explicitement chargé au dernier switch (voir applyPresetFile). Quand il
	// est renseigné et existe encore, IL décide de l'actif — insensible aux retouches
	// à chaud de la config. Sinon on retombe sur l'empreinte (presets d'avant cette
	// version, ou config posée hors switch).
	activeID := getStr(bkState, "active_preset")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Preset{}, nil // installation sans ancien preset
	}
	if err != nil {
		return nil, err
	}
	if activeID != "" {
		if _, e := os.Stat(filepath.Join(dir, activeID+".env")); e != nil {
			activeID = "" // le fichier a disparu : repli sur l'empreinte
		}
	}
	out := []Preset{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".env") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".env")
		active := presetFingerprint(b) == cur
		if activeID != "" {
			active = id == activeID
		}
		out = append(out, Preset{
			ID:     id,
			Name:   presetDisplayName(string(b), id),
			Path:   p,
			Active: active,
		})
	}
	// Auto-adoption : sur une base d'avant cette version (ou après une MAJ), aucun
	// id d'actif n'est mémorisé. Si l'empreinte désigne EXACTEMENT un preset, on
	// l'adopte comme actif mémorisé — ainsi une retouche à chaud (niveau de
	// réflexion) ne le désélectionnera pas, sans exiger un switch manuel d'abord.
	if activeID == "" {
		matched := ""
		n := 0
		for _, p := range out {
			if p.Active {
				matched = p.ID
				n++
			}
		}
		if n == 1 {
			_ = putStr(bkState, "active_preset", matched)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return applyPresetOrder(out), nil
}

// presetOrder : ordre d'affichage personnalisé (drag & drop dans l'UI), une liste
// d'IDs persistée en base. Les presets absents de la liste (fraîchement créés)
// restent classés après, par nom.
func loadPresetOrder() []string {
	var ids []string
	getJSON(bkState, "preset_order", &ids)
	return ids
}

// applyPresetOrder réordonne `list` (déjà trié par nom) selon l'ordre stocké :
// d'abord les presets classés dans l'ordre choisi, puis les non classés dans leur
// ordre alphabétique. Tri STABLE pour préserver l'alphabétique des non classés.
func applyPresetOrder(list []Preset) []Preset {
	order := loadPresetOrder()
	if len(order) == 0 {
		return list
	}
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	sort.SliceStable(list, func(i, j int) bool {
		pi, oki := pos[list[i].ID]
		pj, okj := pos[list[j].ID]
		if oki && okj {
			return pi < pj
		}
		if oki != okj {
			return oki // un preset classé passe avant un non classé
		}
		return false // deux non classés : on garde l'ordre alphabétique
	})
	return list
}

// validPresetName accepts any name (spaces, accents, parentheses…) as long as it
// stays a single safe filename: no path separators, no control chars, and not a
// reserved directory entry. Path containment is double-checked in safePresetPath.
func validPresetName(name string) error {
	if name == "" {
		return fmt.Errorf("nom vide")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("nom réservé")
	}
	if strings.ContainsAny(name, `/\`+"\x00") {
		return fmt.Errorf(`le nom ne peut pas contenir / ni \`)
	}
	for _, r := range name {
		if r < 0x20 {
			return fmt.Errorf("le nom contient un caractère de contrôle invalide")
		}
	}
	return nil
}

// safePresetPath validates name and returns its resolved path inside presetsDir.
func safePresetPath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if err := validPresetName(name); err != nil {
		return "", err
	}
	root, err := filepath.Abs(presetsDir())
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, name+".env")
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path invalide")
	}
	return abs, nil
}

// preservedKeys sont des réglages « appareil » (préférences utilisateur, pas des
// paramètres de modèle) qui doivent survivre à un changement de preset. Sans ça,
// écraser config.env avec le preset ré-imposerait le mode mémoire et effacerait
// l'URL du serveur internet à chaque bascule — ce qui obligeait à tout remettre.
// HOST y figure depuis la 0.8.4 : c'est un réglage de MACHINE (« le moteur
// est-il joignable depuis le réseau ? », voir sys_network.go), pas un réglage de
// modèle. Sans lui dans cette liste, basculer sur un preset écrit avant cette
// version effaçait la clé, et le moteur repartait sur le défaut « toutes les
// interfaces » : une machine volontairement fermée se rouvrait toute seule au
// premier changement de preset.
// ⚠️ Cette liste contient TOUS les réglages « utilisateur/appareil » qui vivent
// dans le bucket config mais ne sont PAS des paramètres de modèle : ils doivent
// survivre à un changement de preset (qui remplace TOUTE la config en bloc via
// WriteConfig). Un réglage utilisateur oublié ici se fait effacer à chaque
// bascule de preset et « se désactive tout seul » :
//   - MEM_ENCRYPTED : drapeau du chiffrement mémoire. Effacé = l'UI croit le
//     chiffrement off, ne propose plus le déverrouillage, et l'utilisateur se
//     retrouve enfermé dehors de sa mémoire pourtant chiffrée sur le disque.
//   - BACKUP_AUTO : sauvegarde ajean.link quotidienne. Effacé = elle « se
//     désactivait toute seule » à chaque changement de preset.
//   - COMPACT : compactage automatique du contexte (toggle utilisateur).
//   - MACHINES : gestion autonome des machines (toggle utilisateur).
//
// Les vrais réglages de modèle (BIN, MODEL, CTX, NGL, REASONING…) ne sont PAS ici :
// ils appartiennent au preset. Les bascules agent/internet/oai/tâches, elles, sont
// déjà rangées dans le bucket état (bkState), hors config, donc à l'abri.
var preservedKeys = []string{
	"MEM_MODE", "CUDA_VISIBLE_DEVICES", "HOST",
	"MEM_ENCRYPTED", "BACKUP_AUTO", "COMPACT", "COMPACT_MAX", "MACHINES",
}

// softPreservedKeys : préservées SEULEMENT si le preset d'arrivée ne les définit
// pas lui-même. CUDA_VISIBLE_DEVICES est dans ce cas : c'est d'ordinaire un
// choix de machine (`ajean gpu`) qui doit survivre aux bascules, MAIS un preset
// a le droit de l'imposer — « QWEN 3.6 27B FABLE 2 GPU » réclame `1,0` pour que
// son `--tensor-split 0.965,0.035` ait bien deux cartes en face. Le préserver
// inconditionnellement écrasait cette valeur par l'ancienne (mono-GPU) : le
// modèle se retrouvait entièrement sur une seule carte et mourait sur
// « cudaMalloc failed: out of memory ». Le preset explicite gagne.
var softPreservedKeys = map[string]bool{"CUDA_VISIBLE_DEVICES": true, "HOST": true}

// presetEffectiveConfig : la config qu'un preset donne une fois chargé (le
// preset + les réglages « appareil » de la config courante).
func presetEffectiveConfig(src string) map[string]string {
	next := parseEnv(src)
	// Un preset ExLlama choisit ses cartes par EXL3_GPUS. Une valeur
	// CUDA_VISIBLE_DEVICES laissée par la 0.18.13 y est reprise, mais ne doit pas
	// remplacer le réglage machine : il serait ensuite reporté sur les presets
	// llama.cpp, avec un ordre de cartes qui n'est pas le leur.
	if isExl3Config(next) {
		if v, ok := next["CUDA_VISIBLE_DEVICES"]; ok {
			if strings.TrimSpace(next["EXL3_GPUS"]) == "" {
				next["EXL3_GPUS"] = v
			}
			delete(next, "CUDA_VISIBLE_DEVICES")
		}
	}
	// Ré-applique les réglages appareil par-dessus le preset — sauf ceux que le
	// preset revendique explicitement (softPreservedKeys).
	cur := ReadConfig()
	for _, k := range preservedKeys {
		v, ok := cur[k]
		if !ok {
			continue
		}
		if softPreservedKeys[k] {
			if _, claimed := next[k]; claimed {
				continue // le preset a son mot à dire sur cette clé : il gagne
			}
		}
		next[k] = v
	}
	return next
}

// ReadPreset returns the contents of a preset by id (filename).
func ReadPreset(id string) (string, error) {
	p, err := safePresetPath(id)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
