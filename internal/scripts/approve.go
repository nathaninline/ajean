package scripts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ajean/internal/home"
	"ajean/internal/secrets"
)

// La validation des outils : l'assistant modifie et essaie ses outils pendant sa
// réponse, puis l'utilisateur valide le tout en une fois, à la fin. Une copie de
// chaque outil validé est gardée : la différence avec TOOLS/ dit ce qui attend,
// quelle que soit la façon dont le fichier a changé. Une tâche planifiée ou une
// app ne lance jamais une version qui n'a pas été validée.

const versionsKept = 10

func approvedDir() string             { return home.State("approved", "tools") }
func approvedFile(name string) string { return filepath.Join(approvedDir(), name+".js") }
func approvedMark() string            { return filepath.Join(approvedDir(), ".ready") }

func clean(raw []byte) string { return strings.ReplaceAll(string(raw), "\r\n", "\n") }

// Init reprend, au premier démarrage, les outils déjà présents : ils tournaient
// avant, les tâches qui s'en servent ne doivent pas casser.
func Init() error {
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(approvedMark()); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := acceptLocked(); err != nil {
		return err
	}
	return home.WriteFile(approvedMark(), []byte("1"))
}

// Validated : cette version de l'outil a-t-elle été validée ?
func Validated(name string) bool {
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(approvedMark()); err != nil {
		return true // validation pas encore en place (premier démarrage, essais)
	}
	cur, err := os.ReadFile(file(name))
	if err != nil {
		return false
	}
	ok, err := os.ReadFile(approvedFile(name))
	return err == nil && clean(cur) == clean(ok)
}

// Exists : un outil de ce nom est-il déjà enregistré ?
func Exists(name string) bool {
	name, err := normalize(name)
	if err != nil {
		return false
	}
	_, err = os.Stat(file(name))
	return err == nil
}

// Change : un outil qui attend la validation de l'utilisateur.
type Change struct {
	Name   string
	Kind   string // new, changed, deleted
	Detail string
}

// Changes : ce qui diffère de la dernière version validée.
func Changes() []Change {
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(approvedMark()); err != nil {
		return nil
	}
	names := map[string]bool{}
	for _, dir := range []string{home.Tools(), approvedDir()} {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if n, ok := strings.CutSuffix(e.Name(), ".js"); ok && !e.IsDir() && nameRe.MatchString(n) {
				names[n] = true
			}
		}
	}
	var out []Change
	for n := range names {
		cur, curErr := os.ReadFile(file(n))
		old, oldErr := os.ReadFile(approvedFile(n))
		switch {
		case curErr != nil && oldErr == nil:
			out = append(out, Change{n, "deleted", "Cet outil sera supprimé : " + parse(n, clean(old)).Description})
		case curErr == nil && oldErr != nil:
			s := parse(n, clean(cur))
			out = append(out, Change{n, "new", secrets.Redact("Ce qu'il fait : " + s.Description + "\n\nCode exécuté :\n" + s.Code)})
		case curErr == nil && clean(cur) != clean(old):
			a, b := parse(n, clean(old)), parse(n, clean(cur))
			var d strings.Builder
			if a.Description != b.Description {
				d.WriteString("Description avant : " + a.Description + "\nDescription après : " + b.Description + "\n\n")
			}
			d.WriteString("Lignes retirées (-) et ajoutées (+) :\n" + lineDiff(a.Code, b.Code))
			out = append(out, Change{n, "changed", secrets.Redact(d.String())})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// lineDiff : les lignes retirées et ajoutées, dans l'ordre du texte.
func lineDiff(before, after string) string {
	a, b := strings.Split(before, "\n"), strings.Split(after, "\n")
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			i++
			j++
		case j < len(b) && (i == len(a) || lcs[i][j+1] >= lcs[i+1][j]):
			out = append(out, "+ "+b[j])
			j++
		default:
			out = append(out, "- "+a[i])
			i++
		}
	}
	return strings.Join(out, "\n")
}

func acceptLocked() error {
	if err := os.MkdirAll(approvedDir(), 0o755); err != nil {
		return err
	}
	ents, _ := os.ReadDir(approvedDir())
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".js"); ok {
			if _, err := os.Stat(file(n)); errors.Is(err, os.ErrNotExist) {
				if err := os.Remove(approvedFile(n)); err != nil {
					return err
				}
			}
		}
	}
	ents, _ = os.ReadDir(home.Tools())
	for _, e := range ents {
		n, ok := strings.CutSuffix(e.Name(), ".js")
		if !ok || e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(file(n))
		if err != nil {
			return err
		}
		if err := home.WriteFile(approvedFile(n), raw); err != nil {
			return err
		}
	}
	return nil
}

// Accept valide tous les outils tels qu'ils sont.
func Accept() error {
	mu.Lock()
	defer mu.Unlock()
	return acceptLocked()
}

// Reject remet chaque outil à sa dernière version validée. L'essai refusé reste
// dans les versions gardées.
func Reject() error {
	changes := Changes()
	mu.Lock()
	defer mu.Unlock()
	for _, c := range changes {
		if c.Kind != "deleted" {
			if err := keepVersion(c.Name); err != nil {
				return err
			}
		}
		if c.Kind == "new" {
			if err := os.Remove(file(c.Name)); err != nil {
				return err
			}
			continue
		}
		raw, err := os.ReadFile(approvedFile(c.Name))
		if err != nil {
			return err
		}
		if err := home.WriteFile(file(c.Name), raw); err != nil {
			return err
		}
	}
	return nil
}

// ---- versions ------------------------------------------------------------------

func versionDir(name string) string { return home.State("versions", "tools", name) }

// keepVersion garde le fichier actuel de l'outil avant son remplacement.
func keepVersion(name string) error {
	raw, err := os.ReadFile(file(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	stamp := time.Now().Format("20060102-150405.000")
	if err := home.WriteFile(filepath.Join(versionDir(name), strings.Replace(stamp, ".", "-", 1)+".js"), raw); err != nil {
		return err
	}
	all := versionsOf(name)
	for i := 0; i < len(all)-versionsKept; i++ {
		_ = os.Remove(filepath.Join(versionDir(name), all[i]+".js"))
	}
	return nil
}

// versionsOf : les versions gardées, de la plus ancienne à la plus récente.
func versionsOf(name string) []string {
	ents, _ := os.ReadDir(versionDir(name))
	var out []string
	for _, e := range ents {
		if v, ok := strings.CutSuffix(e.Name(), ".js"); ok {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// restore remet une version gardée (la plus récente si version est vide).
func restore(name, version string) (string, error) {
	name, err := normalize(name)
	if err != nil {
		return "", err
	}
	mu.Lock()
	defer mu.Unlock()
	all := versionsOf(name)
	if len(all) == 0 {
		return "", fmt.Errorf("aucune ancienne version de « %s »", name)
	}
	if version == "" {
		version = all[len(all)-1]
	}
	found := false
	for _, v := range all {
		found = found || v == version
	}
	if !found {
		return "", fmt.Errorf("version inconnue : %s", strings.Join(all, ", "))
	}
	raw, err := os.ReadFile(filepath.Join(versionDir(name), version+".js"))
	if err != nil {
		return "", err
	}
	if err := keepVersion(name); err != nil {
		return "", err
	}
	return version, home.WriteFile(file(name), raw)
}
