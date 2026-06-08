package main

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
	"time"
)

type Preset struct {
	Name   string
	Path   string
	Active bool
}

// ListPresets returns all configs/*.env files sorted by name, marking the one
// whose contents match the current config.env (by SHA-1).
func ListPresets() ([]Preset, error) {
	dir := presetsDir()
	_ = os.MkdirAll(dir, 0o755)
	cur := ""
	if b, err := os.ReadFile(confPath()); err == nil {
		h := sha1.Sum(b)
		cur = hex.EncodeToString(h[:])
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
		h := sha1.Sum(b)
		out = append(out, Preset{
			Name:   strings.TrimSuffix(e.Name(), ".env"),
			Path:   p,
			Active: hex.EncodeToString(h[:]) == cur,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

var presetNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// safePresetPath validates name and returns its resolved path inside presetsDir.
func safePresetPath(name string) (string, error) {
	if !presetNameRe.MatchString(name) {
		return "", fmt.Errorf("nom invalide (alphanum, ._-)")
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

// SwitchToPreset backs up the current config and copies the target into place,
// then restarts the service.
func SwitchToPreset(target string) error {
	src, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	ts := time.Now().Format("20060102-150405")
	if cur, err := os.ReadFile(confPath()); err == nil {
		_ = os.WriteFile(confPath()+".bak.switch-"+ts, cur, 0o644)
	}
	if err := os.WriteFile(confPath(), src, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s config.env <- %s  (backup .bak.switch-%s)\n", green("[ok]"), filepath.Base(target), ts)
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

// SavePreset creates or overwrites a preset, handling rename when old != "".
func SavePreset(name, old, content string) error {
	if old != "" && old != name {
		if of, err := safePresetPath(old); err == nil {
			_ = os.Remove(of)
		}
	}
	p, err := safePresetPath(name)
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	return os.WriteFile(p, []byte(content), 0o644)
}

// DeletePreset removes a preset; refuses if it is the active config.
func DeletePreset(name string) error {
	p, err := safePresetPath(name)
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

// ReadPreset returns the contents of a preset by name.
func ReadPreset(name string) (string, error) {
	p, err := safePresetPath(name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
