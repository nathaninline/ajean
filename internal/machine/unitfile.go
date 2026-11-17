package machine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Def : ce qu'on règle d'un service depuis l'écran Machine. Le fichier
// ~/.config/systemd/user/<unit> est réécrit avec ces champs ; ses autres lignes
// (limites, délais…) sont gardées telles quelles.
type Def struct {
	Unit        string   `json:"unit"`
	Description string   `json:"description"`
	Command     string   `json:"command"` // ExecStart
	WorkDir     string   `json:"workdir"` // WorkingDirectory
	Env         []string `json:"env"`     // NOM=valeur
	Restart     bool     `json:"restart"` // Restart=on-failure
	Boot        bool     `json:"boot"`    // démarre avec la machine (WantedBy=default.target)
	Editable    bool     `json:"editable"`
	Path        string   `json:"path"` // fichier lu (ou à écrire)
	Transient   bool     `json:"transient"`
}

// unitDir : le dossier des services de l'utilisateur.
func unitDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "systemd", "user"), nil
}

// keys réécrites par Save ; toute autre ligne du fichier est conservée.
var managed = map[string]map[string]bool{
	"Unit":    {"Description": true},
	"Service": {"ExecStart": true, "WorkingDirectory": true, "Environment": true, "Restart": true},
	"Install": {"WantedBy": true},
}

// Load lit la définition d'un service. Un service qui n'a pas de fichier dans
// le dossier de l'utilisateur (temporaire, système, paquet) n'est pas modifiable.
func Load(unit string) (Def, error) {
	d := Def{Unit: unit}
	dir, err := unitDir()
	if err != nil {
		return d, err
	}
	d.Path = filepath.Join(dir, unit)
	fragment := ""
	if raw, _ := systemctl(context.Background(), "show", unit, "-p", "FragmentPath,Transient"); raw != "" {
		for _, l := range strings.Split(raw, "\n") {
			if v, ok := strings.CutPrefix(l, "Transient="); ok {
				d.Transient = v == "yes"
			}
			if v, ok := strings.CutPrefix(l, "FragmentPath="); ok {
				fragment = v
			}
		}
	}
	b, err := os.ReadFile(d.Path)
	switch {
	case err == nil:
		d.Editable = true
	case errors.Is(err, os.ErrNotExist) && fragment != "":
		// Pas de fichier à soi (service temporaire, ou d'ailleurs) : on lit sa
		// définition pour pré-remplir ; l'enregistrer créera le fichier.
		if b, err = os.ReadFile(fragment); err != nil {
			return d, nil
		}
	case errors.Is(err, os.ErrNotExist):
		return d, nil
	default:
		return d, err
	}
	section := ""
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			section = l[1 : len(l)-1]
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok || strings.HasPrefix(l, "#") {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch section + "." + k {
		case "Unit.Description":
			d.Description = v
		case "Service.ExecStart":
			if v != "" {
				d.Command = v
			}
		case "Service.WorkingDirectory":
			d.WorkDir = v
		case "Service.Environment":
			d.Env = append(d.Env, splitEnv(v)...)
		case "Service.Restart":
			d.Restart = v != "" && v != "no"
		}
	}
	// Démarrer avec la machine : ce que systemd fera vraiment, pas seulement ce
	// que dit le fichier (un WantedBy sans « enable » ne lance rien).
	if out, _ := systemctl(context.Background(), "is-enabled", unit); strings.TrimSpace(out) == "enabled" {
		d.Boot = true
	}
	return d, nil
}

// splitEnv découpe une ligne Environment= (plusieurs NOM=valeur, entre guillemets ou non).
func splitEnv(v string) []string {
	var out []string
	var cur strings.Builder
	quote := byte(0)
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case quote != 0 && c == '\\' && i+1 < len(v):
			i++
			cur.WriteByte(v[i])
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == 0 && (c == ' ' || c == '\t'):
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func quoteEnv(kv string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(kv) + `"`
}

// Save écrit le fichier du service, recharge systemd et règle le démarrage avec
// la machine. Le service n'est pas relancé ici.
func Save(d Def) error {
	unit, err := Unit(d.Unit)
	if err != nil {
		return err
	}
	cmd := strings.Join(strings.Fields(strings.ReplaceAll(strings.ReplaceAll(d.Command, "\\\n", " "), "\n", " ")), " ")
	if cmd == "" {
		return errors.New("la commande de lancement est vide")
	}
	desc := strings.TrimSpace(strings.ReplaceAll(d.Description, "\n", " "))
	work := strings.TrimSpace(d.WorkDir)
	var env []string
	for _, e := range d.Env {
		e = strings.TrimSpace(e)
		if e == "" || strings.HasPrefix(e, "#") {
			continue
		}
		k, _, ok := strings.Cut(e, "=")
		if !ok || k == "" || strings.ContainsAny(k, " \t\"'") {
			return fmt.Errorf("variable invalide : %q (attendu NOM=valeur)", e)
		}
		env = append(env, e)
	}
	dir, err := unitDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, unit)
	old, _ := os.ReadFile(path)

	// Les lignes gardées, rangées par section, puis les champs réglés ici.
	keep := map[string][]string{}
	order := []string{"Unit", "Service", "Install"}
	section := ""
	for _, l := range strings.Split(strings.ReplaceAll(string(old), "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			section = t[1 : len(t)-1]
			if !contains(order, section) {
				order = append(order, section)
			}
			continue
		}
		if k, _, ok := strings.Cut(t, "="); ok && managed[section][strings.TrimSpace(k)] {
			continue
		}
		keep[section] = append(keep[section], t)
	}
	set := map[string][]string{
		"Unit":    {"Description=" + desc},
		"Service": {"ExecStart=" + cmd},
	}
	if work != "" {
		set["Service"] = append(set["Service"], "WorkingDirectory="+work)
	}
	for _, e := range env {
		set["Service"] = append(set["Service"], "Environment="+quoteEnv(e))
	}
	if d.Restart {
		set["Service"] = append(set["Service"], "Restart=on-failure")
	}
	if d.Boot {
		set["Install"] = []string{"WantedBy=default.target"}
	}
	var b strings.Builder
	for _, s := range order {
		lines := append(set[s], keep[s]...)
		if len(lines) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[" + s + "]\n" + strings.Join(lines, "\n") + "\n")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if _, err := systemctl(context.Background(), "daemon-reload"); err != nil {
		return err
	}
	if d.Boot {
		_, err = systemctl(context.Background(), "enable", unit)
	} else {
		_, err = systemctl(context.Background(), "disable", unit)
	}
	return err
}

// Remove arrête un service, le retire du démarrage et supprime son fichier.
func Remove(unit string) error {
	for _, s := range Status([]string{unit}) {
		if s.Self {
			return errors.New("AJEAN ne peut pas se supprimer lui-même")
		}
	}
	dir, err := unitDir()
	if err != nil {
		return err
	}
	_, _ = systemctl(context.Background(), "stop", unit)
	_, _ = systemctl(context.Background(), "disable", unit)
	if err := os.Remove(filepath.Join(dir, unit)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err = systemctl(context.Background(), "daemon-reload")
	return err
}

// Apply relance un service après modification. Un service temporaire (créé par
// systemd-run) garde sa définition tant qu'il tourne : il faut l'arrêter puis
// le démarrer pour que le fichier prenne le relais.
func Apply(unit string) error {
	d, _ := Load(unit)
	if d.Transient {
		_, _ = systemctl(context.Background(), "stop", unit)
		_, err := systemctl(context.Background(), "start", unit)
		return err
	}
	return Act(unit, "restart")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
