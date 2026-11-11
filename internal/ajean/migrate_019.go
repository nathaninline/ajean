package ajean

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// migrate_019.go — retour d'une installation passée par la 0.19.
//
// La 0.19 avait remplacé les presets par des « services » (services.json), un
// par modèle, chacun avec son unité système (ajean-svc-<id>). Elle n'a ni
// modifié ni supprimé les fichiers de presets : ceux de la 0.18 sont donc
// toujours là. Reste à reprendre, une seule fois :
//
//   - les services CRÉÉS en 0.19 (sans preset d'origine) : ils deviennent des
//     presets Service (commande) ou API (adresse seule) ;
//   - les unités ajean-svc-* : arrêtées et désactivées, sinon elles gardent le
//     port et la mémoire du modèle à côté du moteur de la 0.20 ;
//   - la configuration active : remise sur le preset du modèle que le chat
//     utilisait.
//
// services.json n'est pas touché : revenir en 0.19 reste possible.

const migrated019Key = "migrated_019"

// service019 : ce qu'on relit d'une entrée de services.json.
type service019 struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Command    string            `json:"command"`
	Exec       string            `json:"exec"`
	Args       []string          `json:"args"`
	Workdir    string            `json:"workdir"`
	Env        map[string]string `json:"env"`
	URL        string            `json:"url"`
	APIKey     string            `json:"api_key"`
	Model      string            `json:"model"`
	Vision     bool              `json:"vision"`
	Context    int               `json:"context"`
	Preset     string            `json:"preset"`
	ChatParams map[string]string `json:"chat_params"`
}

func (s service019) commandLine() string {
	if c := strings.TrimSpace(s.Command); c != "" {
		return strings.Join(strings.Fields(strings.ReplaceAll(c, "\n", " ")), " ")
	}
	if s.Exec == "" {
		return ""
	}
	parts := []string{quoteArg019(s.Exec)}
	for _, a := range s.Args {
		parts = append(parts, quoteArg019(a))
	}
	return strings.Join(parts, " ")
}

func quoteArg019(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\"'") {
		return a
	}
	return `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
}

var envKey019 = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// presetContent019 écrit le preset 0.20 équivalent à un service 0.19.
func presetContent019(s service019) string {
	ctx := ""
	if s.Context > 0 {
		ctx = strconv.Itoa(s.Context)
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(externalPresetContent(strings.TrimSpace(s.URL), strings.TrimSpace(s.Model), strings.TrimSpace(s.APIKey), ctx, s.Vision), "\n"))
	b.WriteString("\n")
	line := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" && !strings.ContainsAny(v, "\r\n") {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
		}
	}
	if cmd := s.commandLine(); cmd != "" {
		line("SERVICE_CMD", cmd)
		line("SERVICE_WORKDIR", s.Workdir)
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var env []string
		for _, k := range keys {
			env = append(env, k+"="+quoteArg019(s.Env[k]))
		}
		line("SERVICE_ENV", strings.Join(env, " "))
	}
	// Réglages de génération du service (TEMP, TOP_P…, REASONING_EFFORT).
	keys := make([]string, 0, len(s.ChatParams))
	for k := range s.ChatParams {
		if envKey019.MatchString(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		line(k, s.ChatParams[k])
	}
	return b.String()
}

// retire019Unit arrête et désactive l'unité système d'un service 0.19. Au mieux :
// sans les droits, elle reste en place et retire019Units (appelée par
// « ajean install », en administrateur) la reprendra.
var retire019Unit = func(id string) error {
	name := "ajean-svc-" + id
	run := func(bin string, args ...string) error { return exec.Command(bin, args...).Run() }
	switch runtime.GOOS {
	case "linux":
		if _, err := os.Stat("/etc/systemd/system/" + name + ".service"); err != nil {
			return nil
		}
		ctl := func(op string) error {
			if os.Geteuid() == 0 {
				return run("systemctl", op, name)
			}
			return run("sudo", "-n", "/bin/systemctl", op, name)
		}
		err := ctl("stop")
		if e := ctl("disable"); err == nil {
			err = e
		}
		return err
	case "windows":
		if run("sc.exe", "query", name) != nil {
			return nil // pas de service de ce nom
		}
		err := run("sc.exe", "stop", name)
		if e := run("sc.exe", "config", name, "start=", "demand"); e != nil {
			return e
		}
		_ = err // « déjà arrêté » n'est pas un échec
		return nil
	case "darwin":
		label := "com.ajean.svc." + id
		_ = run("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+label)
		if h, err := os.UserHomeDir(); err == nil {
			_ = os.Remove(filepath.Join(h, "Library", "LaunchAgents", label+".plist"))
		}
	}
	return nil
}

// read019Services relit services.json. nil = jamais passé par la 0.19.
func read019Services() []service019 {
	b, err := os.ReadFile(filepath.Join(AjeanHome(), "services.json"))
	if err != nil {
		return nil
	}
	var list []service019
	if json.Unmarshal(b, &list) != nil {
		var wrap struct {
			Services []service019 `json:"services"`
		}
		if json.Unmarshal(b, &wrap) != nil {
			return nil
		}
		list = wrap.Services
	}
	return list
}

// retire019Units arrête les unités de tous les services 0.19. Renvoie celles
// qui n'ont pas pu l'être.
func retire019Units() []string {
	var left []string
	for _, s := range read019Services() {
		if s.ID == "" || s.commandLine() == "" {
			continue
		}
		if err := retire019Unit(s.ID); err != nil {
			left = append(left, "ajean-svc-"+s.ID)
		}
	}
	return left
}

// migrateFrom019 : appelée au démarrage de l'interface et du moteur. Sans effet
// sur une installation qui n'a jamais connu la 0.19, ni une seconde fois.
func migrateFrom019() {
	list := read019Services()
	if list == nil || getStr(bkState, migrated019Key) == "1" {
		return
	}
	have := map[string]bool{}
	names := map[string]bool{}
	if ps, err := ListPresets(); err == nil {
		for _, p := range ps {
			have[p.ID] = true
			names[strings.ToLower(p.Name)] = true
		}
	}
	chatID := strings.TrimSpace(ReadConfig()["AJEAN_SERVICE"])
	chatPreset := ""
	created := 0
	for _, s := range list {
		if s.ID == "" {
			continue
		}
		if s.Preset != "" && have[s.Preset] {
			// Issu d'un preset 0.18 toujours présent : rien à recréer.
			if s.ID == chatID {
				chatPreset = s.Preset
			}
			continue
		}
		if strings.TrimSpace(s.URL) == "" && s.commandLine() == "" {
			continue
		}
		name := strings.TrimSpace(s.Name)
		if name == "" {
			name = s.ID
		}
		if names[strings.ToLower(name)] {
			name += " (0.19)"
		}
		id, err := SavePreset("", name, presetContent019(s))
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ajean] reprise du service 0.19 %s : %v\n", s.ID, err)
			continue
		}
		have[id], names[strings.ToLower(name)] = true, true
		created++
		if s.ID == chatID {
			chatPreset = id
		}
	}
	if left := retire019Units(); len(left) > 0 {
		fmt.Fprintf(os.Stderr, "[ajean] unités de la 0.19 encore actives (droits insuffisants) : %s. Lancez « ajean install » en administrateur pour les retirer.\n", strings.Join(left, ", "))
	}
	// Configuration active : le preset du modèle que le chat utilisait en 0.19,
	// sinon celui qui était actif avant elle.
	target := chatPreset
	if target == "" {
		if id := getStr(bkState, "active_preset"); have[id] {
			target = id
		}
	}
	if target != "" {
		if err := applyPresetFile(filepath.Join(presetsDir(), target+".env")); err != nil {
			fmt.Fprintf(os.Stderr, "[ajean] reprise du preset actif %s : %v\n", target, err)
		}
	}
	_ = putStr(bkState, migrated019Key, "1")
	fmt.Fprintf(os.Stderr, "[ajean] installation 0.19 reprise : %d service(s) converti(s) en preset\n", created)
}
