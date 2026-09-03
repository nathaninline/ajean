package ajean

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

var nameLineRe = regexp.MustCompile(`(?mi)^[ \t]*#?[ \t]*NAME[ \t]*=.*$`)

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

// withDisplayName ensures the body carries a `# NAME=<name>` line (replacing an
// existing one, or prepended otherwise).
func withDisplayName(content, name string) string {
	line := "# NAME=" + name
	if nameLineRe.MatchString(content) {
		return nameLineRe.ReplaceAllString(content, line)
	}
	return line + "\n" + content
}

// configFingerprint réduit une configuration à l'ENSEMBLE TRIÉ de ses
// affectations effectives, puis le hache. Les clés « appareil » (preservedKeys,
// réappliquées par SwitchToPreset) sont ignorées : sans ça, les MEM_MODE /
// CRAWL4AI_URL injectés rendraient la config active différente de TOUS les
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
// activePresetName renvoie le nom d'affichage du preset actif, ou "" si
// indéterminable (aucun preset, erreur de lecture...) — jamais une erreur qui
// remonterait à l'appelant, juste une info de confort journalisée par tour
// (voir StartTurn dans chat_conversation.go).
func activePresetName() string {
	list, err := ListPresets()
	if err != nil {
		return ""
	}
	for _, p := range list {
		if p.Active {
			return p.Name
		}
	}
	return ""
}

func ListPresets() ([]Preset, error) {
	dir := presetsDir()
	_ = os.MkdirAll(dir, 0o755)
	cur := configFingerprint(ReadConfig())
	// Preset explicitement chargé au dernier switch (voir applyPresetFile). Quand il
	// est renseigné et existe encore, IL décide de l'actif — insensible aux retouches
	// à chaud de la config. Sinon on retombe sur l'empreinte (presets d'avant cette
	// version, ou config posée hors switch).
	activeID := getStr(bkState, "active_preset")
	entries, err := os.ReadDir(dir)
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

func savePresetOrder(ids []string) error { return putJSON(bkState, "preset_order", ids) }

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

// uniquePresetID derives a unique filename id from a display name, appending
// " (2)", " (3)"… on collision so duplicate names never overwrite.
func uniquePresetID(name string) (string, error) {
	base := strings.TrimSpace(strings.NewReplacer("/", "-", "\\", "-").Replace(name))
	if base == "" {
		base = "preset"
	}
	cand := base
	for n := 2; n < 10000; n++ {
		p, err := safePresetPath(cand)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return cand, nil
		}
		cand = fmt.Sprintf("%s (%d)", base, n)
	}
	return "", fmt.Errorf("impossible de générer un nom de fichier unique")
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
	"MEM_MODE", "CRAWL4AI_URL", "WEB_ENGINE", "CUDA_VISIBLE_DEVICES", "HOST",
	"MEM_ENCRYPTED", "BACKUP_AUTO", "COMPACT", "MACHINES",
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

// applyPresetFile installe le preset comme configuration active, en réinjectant
// les réglages « appareil » par-dessus. Séparé de SwitchToPreset pour être
// testable sans redémarrer le service (donc sans lancer un vrai llama-server).
func applyPresetFile(target string) error {
	src, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	next := parseEnv(string(src))
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
	if err := WriteConfig(next); err != nil {
		return err
	}
	// Mémorise QUEL preset a été chargé. Le marqueur « actif » se base dessus
	// (ListPresets), et non plus seulement sur une empreinte de la config vivante :
	// un réglage à chaud (ex. le raccourci « niveau de réflexion » qui écrit
	// REASONING_EFFORT) fait diverger la config du fichier preset, ce qui
	// désélectionnait le preset à tort. L'id explicite survit à ces retouches.
	_ = putStr(bkState, "active_preset", strings.TrimSuffix(filepath.Base(target), ".env"))
	return nil
}

// SwitchToPreset installe le preset et redémarre le service. Les réglages
// « appareil » (preservedKeys) sont conservés à travers la bascule.
func SwitchToPreset(target string) error {
	if err := applyPresetFile(target); err != nil {
		return err
	}
	fmt.Printf("%s configuration <- %s\n", green("[ok]"), filepath.Base(target))
	fmt.Println(dim("[info] redémarrage du service..."))
	return serviceAction("restart")
}

func cmdSwitch(args []string) error {
	list, err := ListPresets()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return fmt.Errorf("aucun preset dans %s", presetsDir())
	}
	fmt.Printf("\n  %s  (%s)\n\n", cyan("Presets disponibles"), presetsDir())
	for i, p := range list {
		mark := " "
		if p.Active {
			mark = green("●") + " actif"
		}
		fmt.Printf("  %2d) %-30s %s\n", i+1, p.Name, mark)
	}
	fmt.Println()
	choice := ""
	if len(args) > 0 {
		choice = args[0]
	} else {
		fmt.Print("Numéro à activer (vide = annuler) : ")
		sc := bufio.NewScanner(os.Stdin)
		if sc.Scan() {
			choice = strings.TrimSpace(sc.Text())
		}
	}
	if choice == "" {
		fmt.Println(dim("[info] annulé"))
		return nil
	}
	n, err := strconv.Atoi(choice)
	if err != nil || n < 1 || n > len(list) {
		return fmt.Errorf("choix invalide")
	}
	return SwitchToPreset(list[n-1].Path)
}

// newPresetSeedKeys : les SEULES clés reprises de la configuration active quand
// on crée un preset. Ce sont des réglages de MACHINE (quel moteur, où il écoute),
// pas des réglages de modèle.
//
// Issue #17 : le nouveau preset repartait d'une copie COMPLÈTE de la config
// active. Les réglages du modèle précédent (EXTRA_ARGS — --n-cpu-moe,
// --tensor-split, --flash-attn… — mais aussi CTX, NGL, KV_TYPE, MODEL) se
// mélangeaient donc aux options cochées pour le nouveau, et il fallait penser à
// tout nettoyer à la main. On repart d'une base vide : les valeurs non
// renseignées sont les défauts documentés (CTX 32768, NGL 999, BATCH 2048…).
var newPresetSeedKeys = []string{"BIN", "HOST", "PORT"}

// newPresetSeed renvoie la configuration de départ d'un preset créé depuis l'UI.
func newPresetSeed() map[string]string {
	cur := ReadConfig()
	seed := map[string]string{}
	for _, k := range newPresetSeedKeys {
		if v := cur[k]; v != "" {
			seed[k] = v
		}
	}
	return seed
}

// SavePreset writes a preset. When id == "" it creates a NEW preset under a
// freshly-generated unique filename (so duplicate display names never clash).
// When id != "" it updates that existing preset in place (filename unchanged —
// only the body and its `# NAME=` line change). Returns the resulting id.
func SavePreset(id, name, content string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("nom requis")
	}
	content = withDisplayName(content, name)
	if id == "" {
		newID, err := uniquePresetID(name)
		if err != nil {
			return "", err
		}
		p, err := safePresetPath(newID)
		if err != nil {
			return "", err
		}
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		return newID, os.WriteFile(p, []byte(content), 0o644)
	}
	p, err := safePresetPath(id)
	if err != nil {
		return "", err
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	old, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return "", err
	}
	// Modifier le preset ACTIF le désélectionne : le fichier enregistré diverge
	// alors de la config vivante (qui tourne encore l'ancienne définition), il faut
	// « switcher » pour l'appliquer. On efface l'id d'actif explicite pour que
	// ListPresets retombe sur l'empreinte, qui verra la divergence. On ne le fait
	// que sur un vrai changement de config : un simple renommage (seule la ligne
	// # NAME= bouge, ignorée par l'empreinte) ou un enregistrement à blanc ne
	// désélectionne pas. Le raccourci « niveau de réflexion » (REASONING_EFFORT)
	// ne passe pas par ici : il écrit la config vivante, donc reste sélectionné.
	if id == getStr(bkState, "active_preset") && presetFingerprint(old) != presetFingerprint([]byte(content)) {
		_ = putStr(bkState, "active_preset", "")
	}
	return id, nil
}

// DeletePreset removes a preset by id; refuses if it is the active config.
func DeletePreset(id string) error {
	p, err := safePresetPath(id)
	if err != nil {
		return err
	}
	target, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("introuvable")
	}
	if presetFingerprint(target) == configFingerprint(ReadConfig()) {
		return fmt.Errorf("preset actif, switche d'abord")
	}
	return os.Remove(p)
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
