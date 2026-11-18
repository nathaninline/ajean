package memory

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ajean/internal/home"
	"ajean/internal/secrets"
)

// Chaque fiche garde ses dernières versions : une réécriture appauvrie se défait
// sans remettre toute la mémoire en arrière. Aucun secret n'y est conservé.

const ficheVersionsKept = 10

func ficheVersionDir(name string) string { return statePath("versions", "fiches", name) }

func ficheVersions(name string) []string {
	ents, _ := os.ReadDir(ficheVersionDir(name))
	var out []string
	for _, e := range ents {
		if v, ok := strings.CutSuffix(e.Name(), ".md"); ok {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// keepFicheVersionLocked garde la version actuelle avant son remplacement.
func keepFicheVersionLocked(path, name string, next []byte) {
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) == string(next) {
		return
	}
	stamp := strings.Replace(time.Now().Format("20060102-150405.000"), ".", "-", 1)
	if err := home.WriteFile(filepath.Join(ficheVersionDir(name), stamp+".md"), []byte(secrets.Redact(string(raw)))); err != nil {
		fmt.Fprintf(os.Stderr, "[memory] version de la fiche %s non gardée : %v\n", name, err)
		return
	}
	all := ficheVersions(name)
	for i := 0; i < len(all)-ficheVersionsKept; i++ {
		_ = os.Remove(filepath.Join(ficheVersionDir(name), all[i]+".md"))
	}
}

// restoreFiche : sans version, liste celles qui sont gardées ; avec, la remet.
func restoreFiche(name, version string) (string, error) {
	name = normFicheName(name)
	mu.Lock()
	defer mu.Unlock()
	all := ficheVersions(name)
	if len(all) == 0 {
		return "", fmt.Errorf("aucune ancienne version de la fiche « %s »", name)
	}
	if version == "" {
		return "Versions gardées de « " + name + " » (la dernière est la plus récente) : " + strings.Join(all, ", "), nil
	}
	raw, err := os.ReadFile(filepath.Join(ficheVersionDir(name), filepath.Base(version)+".md"))
	if err != nil {
		return "", fmt.Errorf("version inconnue : %s", strings.Join(all, ", "))
	}
	cur, exists := findFicheLocked(name)
	f := parseFiche(name, string(raw))
	f.Archived = exists && cur.Archived
	if err := writeFicheLocked(f); err != nil {
		return "", err
	}
	logLocked(kindFiche, fmt.Sprintf("Fiche « %s » remise à sa version %s", name, version))
	return "[ok] fiche « " + name + " » remise à la version " + version, nil
}

// PurgeSecretCopies masque les secrets dans les copies déjà prises par les sommeils.
func PurgeSecretCopies() error {
	mu.Lock()
	defer mu.Unlock()
	err := filepath.WalkDir(statePath("sleeps"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if clean := secrets.Redact(string(raw)); clean != string(raw) {
			return home.WriteFile(path, []byte(clean))
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// RecoverSleep ferme un sommeil coupé par l'arrêt du programme : le garde-fou
// passe, et ce que le ménage avait perdu en route est remis.
func RecoverSleep() {
	mu.Lock()
	var open *sleepMeta
	for _, m := range sleepMetas() {
		if m.Status == statusRunning {
			c := m
			open = &c
		}
	}
	if open == nil || sleeping != nil {
		mu.Unlock()
		return
	}
	before, err := loadCopy(open.ID)
	if err != nil {
		mu.Unlock()
		fmt.Fprintf(os.Stderr, "[memory] sommeil interrompu non repris : %v\n", err)
		return
	}
	s := &Sleep{id: open.ID, started: open.Started, before: before, removed: map[string]bool{}, dropped: map[string]map[string]bool{}}
	sleeping = s
	mu.Unlock()
	s.Finish("interrompu", "Sommeil coupé par l'arrêt du programme : vérifié au redémarrage.")
}
