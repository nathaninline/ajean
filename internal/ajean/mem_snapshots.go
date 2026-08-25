package ajean

// mem_snapshots.go — filet de sécurité local : des copies horodatées du dossier
// memory/, indépendantes du chiffrement et du relais. Un bug qui corromprait la
// mémoire courante se rejoue depuis un snapshot antérieur, même pour un
// utilisateur non abonné.
//
// Les snapshots copient TELS QUELS les fichiers de memory/ (clairs ou chiffrés,
// keyvault compris) : ce sont des copies fidèles, jamais une transformation.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Nombre de snapshots conservés (rotation). Au-delà, les plus anciens sont
// purgés — sauf ceux marqués « pré-bascule », gardés plus longtemps.
const memSnapshotsKept = 20

func snapshotsRoot() string { return filepath.Join(AjeanHome(), "backups", "memory") }

// MemSnapshot décrit une copie horodatée.
type MemSnapshot struct {
	ID     string    `json:"id"`     // nom de dossier (horodaté)
	Reason string    `json:"reason"` // pourquoi elle a été prise
	When   time.Time `json:"when"`
	Pages  int       `json:"pages"` // nb de fichiers .md/.md.enc
}

// snapshotMemory prend une copie du dossier memory/. reason est un libellé court
// (ex. "auto", "avant-chiffrement"). Renvoie l'ID de la copie. Ne fait rien (ID
// vide) si le dossier mémoire est absent ou vide.
func snapshotMemory(reason string) (string, error) {
	entries, err := os.ReadDir(memoryDir())
	if err != nil || len(entries) == 0 {
		return "", nil // rien à sauvegarder
	}
	id := time.Now().UTC().Format("20060102-150405") + "-" + sanitizeReason(reason)
	dst := filepath.Join(snapshotsRoot(), id)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue // memory/ est plat ; on ignore un éventuel sous-dossier
		}
		src := filepath.Join(memoryDir(), e.Name())
		b, err := os.ReadFile(src)
		if err != nil {
			return "", fmt.Errorf("lecture %s : %w", e.Name(), err)
		}
		if err := memWriteFileVerified(filepath.Join(dst, e.Name()), b, 0o600); err != nil {
			return "", fmt.Errorf("copie %s : %w", e.Name(), err)
		}
	}
	pruneSnapshots()
	return id, nil
}

// listSnapshots renvoie les snapshots existants, du plus récent au plus ancien.
func listSnapshots() []MemSnapshot {
	entries, err := os.ReadDir(snapshotsRoot())
	if err != nil {
		return nil
	}
	var out []MemSnapshot
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		pages := 0
		if inner, err := os.ReadDir(filepath.Join(snapshotsRoot(), id)); err == nil {
			for _, f := range inner {
				n := strings.ToLower(f.Name())
				if strings.HasSuffix(n, ".md") || strings.HasSuffix(n, ".md.enc") {
					pages++
				}
			}
		}
		out = append(out, MemSnapshot{ID: id, Reason: reasonOf(id), When: whenOf(id), Pages: pages})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// restoreSnapshot remplace le contenu de memory/ par celui d'un snapshot. Pour
// ne rien perdre en cas d'erreur, on prend D'ABORD un snapshot de sécurité de
// l'état courant, puis on écrit chaque fichier de façon vérifiée avant de retirer
// les fichiers surnuméraires.
func restoreSnapshot(id string) error {
	src := filepath.Join(snapshotsRoot(), filepath.Base(id))
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("snapshot introuvable : %w", err)
	}
	if _, err := snapshotMemory("avant-restauration"); err != nil {
		return fmt.Errorf("snapshot de sécurité impossible, restauration annulée : %w", err)
	}
	if err := os.MkdirAll(memoryDir(), 0o755); err != nil {
		return err
	}
	restored := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := memWriteFileVerified(filepath.Join(memoryDir(), e.Name()), b, 0o600); err != nil {
			return err
		}
		restored[e.Name()] = true
	}
	// Retire de memory/ ce qui n'est pas dans le snapshot (fichiers apparus après).
	if cur, err := os.ReadDir(memoryDir()); err == nil {
		for _, f := range cur {
			if !f.IsDir() && !restored[f.Name()] {
				os.Remove(filepath.Join(memoryDir(), f.Name()))
			}
		}
	}
	return nil
}

// pruneSnapshots garde les memSnapshotsKept plus récents, en conservant en plus
// tous les snapshots « avant-* » (pris avant une opération sensible).
func pruneSnapshots() {
	all := listSnapshots()
	if len(all) <= memSnapshotsKept {
		return
	}
	kept := 0
	for _, s := range all {
		if strings.Contains(s.Reason, "avant") {
			continue // on garde les snapshots de sécurité
		}
		kept++
		if kept > memSnapshotsKept {
			os.RemoveAll(filepath.Join(snapshotsRoot(), s.ID))
		}
	}
}

// --- Petits utilitaires d'ID --------------------------------------------------

func sanitizeReason(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	var b strings.Builder
	for _, r := range reason {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else if r == ' ' || r == '_' {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "snap"
	}
	return b.String()
}

func reasonOf(id string) string {
	// format : 20060102-150405-<reason>
	parts := strings.SplitN(id, "-", 3)
	if len(parts) == 3 {
		return parts[2]
	}
	return ""
}

func whenOf(id string) time.Time {
	parts := strings.SplitN(id, "-", 3)
	if len(parts) >= 2 {
		if t, err := time.Parse("20060102-150405", parts[0]+"-"+parts[1]); err == nil {
			return t
		}
	}
	return time.Time{}
}
