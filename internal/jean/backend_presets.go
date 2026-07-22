package jean

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
			if v := strings.Trim(strings.TrimSpace(s[i+1:]), `"`); v != "" {
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

// presetFingerprint hashes config content while IGNORING the device-level keys
// (preservedKeys) that SwitchToPreset re-applies on top of a freshly copied
// preset. Without this, those injected MEM_MODE/CRAWL4AI_URL lines make
// config.env differ from every preset file, so no preset is ever detected as
// active (regression introduced when preservedKeys were added). We strip those
// assignment lines symmetrically from both sides before hashing so a plain
// content match still identifies the active preset.
func presetFingerprint(content []byte) string {
	var b strings.Builder
	for _, line := range strings.Split(string(content), "\n") {
		if isPreservedAssignment(line) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	h := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(h[:])
}

// isPreservedAssignment reports whether a line assigns one of the preservedKeys
// (commented or not), e.g. `MEM_MODE=off` or `# CRAWL4AI_URL=...`.
func isPreservedAssignment(line string) bool {
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
	eq := strings.IndexByte(t, '=')
	if eq < 0 {
		return false
	}
	key := strings.TrimSpace(t[:eq])
	for _, k := range preservedKeys {
		if key == k {
			return true
		}
	}
	return false
}

// ListPresets returns all configs/*.env, marking the one whose contents match
// the current config.env (ignoring device-level preservedKeys), sorted by name.
func ListPresets() ([]Preset, error) {
	dir := presetsDir()
	_ = os.MkdirAll(dir, 0o755)
	cur := ""
	if b, err := os.ReadFile(confPath()); err == nil {
		cur = presetFingerprint(b)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
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
		out = append(out, Preset{
			ID:     id,
			Name:   presetDisplayName(string(b), id),
			Path:   p,
			Active: presetFingerprint(b) == cur,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
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
var preservedKeys = []string{"MEM_MODE", "CRAWL4AI_URL"}

// SwitchToPreset backs up the current config and copies the target into place,
// then restarts the service. Les réglages « appareil » (preservedKeys) sont
// conservés à travers la bascule.
func SwitchToPreset(target string) error {
	src, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	// Capture les réglages appareil AVANT d'écraser config.env.
	cur := ReadConfig()
	if err := os.WriteFile(confPath(), src, 0o644); err != nil {
		return err
	}
	// Ré-applique les réglages appareil par-dessus le preset fraîchement copié.
	for _, k := range preservedKeys {
		if v, ok := cur[k]; ok {
			_ = SetConfigKey(k, v)
		}
	}
	fmt.Printf("%s config.env <- %s\n", green("[ok]"), filepath.Base(target))
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
	return id, os.WriteFile(p, []byte(content), 0o644)
}

// DeletePreset removes a preset by id; refuses if it is the active config.
func DeletePreset(id string) error {
	p, err := safePresetPath(id)
	if err != nil {
		return err
	}
	cur, _ := os.ReadFile(confPath())
	target, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("introuvable")
	}
	if sha1.Sum(cur) == sha1.Sum(target) {
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
