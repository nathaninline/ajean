// Package workspace donne accès au dossier de travail de l'assistant,
// WORKSPACE/ à côté de l'exécutable. C'est le SEUL endroit du disque où il peut
// lire et écrire des fichiers, et il ne peut pas en sortir : tous les accès
// passent par os.Root, qui refuse au niveau du système tout chemin menant
// ailleurs (« .. », chemin absolu, lien symbolique vers l'extérieur). Il n'y a
// volontairement aucun outil pour lancer une commande du système : une
// commande, elle, ne pourrait pas être enfermée.
package workspace

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"ajean/internal/home"
)

const (
	maxReadBytes = 2 << 20 // taille maximale d'un fichier lu
	maxListed    = 300     // entrées d'un dossier, au-delà on résume
)

var (
	mu   sync.RWMutex
	root *os.Root
)

// Init crée le dossier s'il manque et y ouvre la racine. À appeler avant tout
// autre accès.
func Init() error {
	dir := home.Workspace()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	return swap(r)
}

// Close referme la racine. Tant qu'elle est ouverte, Windows refuse de
// déplacer ou de supprimer le dossier.
func Close() error { return swap(nil) }

func swap(r *os.Root) error {
	mu.Lock()
	old := root
	root = r
	mu.Unlock()
	if old != nil {
		return old.Close()
	}
	return nil
}

// open ramène un chemin donné par le modèle (ou un script) à un chemin relatif
// au workspace et rend la racine. Tout ce qui tente d'en sortir est refusé
// ici, puis une seconde fois par os.Root.
func open(p string) (*os.Root, string, error) {
	mu.RLock()
	r := root
	mu.RUnlock()
	if r == nil {
		return nil, "", errors.New("workspace non initialisé")
	}
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	// Le « : » couvre les lettres de lecteur et les flux NTFS (fichier:flux).
	if strings.HasPrefix(p, "/") || strings.Contains(p, ":") || strings.ContainsRune(p, 0) {
		return nil, "", errors.New("chemin refusé : uniquement des chemins relatifs au workspace")
	}
	p = path.Clean(p)
	if p == ".." || strings.HasPrefix(p, "../") {
		return nil, "", errors.New("chemin refusé : impossible de sortir du workspace")
	}
	return r, p, nil
}

// explain traduit une erreur du système en message lisible par le modèle.
func explain(err error) error {
	var pe *fs.PathError
	if !errors.As(err, &pe) {
		return err
	}
	switch {
	case errors.Is(pe.Err, fs.ErrNotExist):
		return fmt.Errorf("introuvable : %s", pe.Path)
	case errors.Is(pe.Err, fs.ErrExist):
		return fmt.Errorf("existe déjà : %s", pe.Path)
	}
	return fmt.Errorf("%s : %v", pe.Path, pe.Err)
}

// Read lit un fichier texte. Les fichiers binaires ou trop gros sont refusés :
// ils noieraient le contexte du modèle sans rien lui apprendre.
func Read(p string) (string, error) {
	r, p, err := open(p)
	if err != nil {
		return "", err
	}
	info, err := r.Stat(p)
	if err != nil {
		return "", explain(err)
	}
	if info.IsDir() {
		return "", errors.New("c'est un dossier (ws_list pour le parcourir)")
	}
	if info.Size() > maxReadBytes {
		return "", fmt.Errorf("fichier trop gros (%d octets, max %d)", info.Size(), maxReadBytes)
	}
	b, err := r.ReadFile(p)
	if err != nil {
		return "", explain(err)
	}
	if !utf8.Valid(b) {
		return "", errors.New("fichier binaire : pas du texte lisible")
	}
	return string(b), nil
}

// Write crée ou remplace un fichier texte ; les dossiers parents sont créés.
func Write(p, content string) error { return WriteBytes(p, []byte(content)) }

// WriteBytes crée ou remplace un fichier quelconque (un fichier déposé par
// l'utilisateur n'est pas forcément du texte). L'écriture passe par un fichier
// temporaire renommé : une coupure ne laisse jamais un fichier à moitié écrit.
func WriteBytes(p string, data []byte) error {
	r, p, err := open(p)
	if err != nil {
		return err
	}
	if p == "." {
		return errors.New("donne un nom de fichier")
	}
	if info, err := r.Stat(p); err == nil && info.IsDir() {
		return fmt.Errorf("c'est un dossier : %s", p)
	}
	if err := mkParents(r, p); err != nil {
		return err
	}
	// Nom temporaire unique : il n'écrase aucun fichier de l'utilisateur, et deux
	// écritures simultanées du même chemin ne se mélangent pas.
	tmp := path.Join(path.Dir(p), ".ajean-"+rand.Text()+".tmp")
	if err := r.WriteFile(tmp, data, 0o644); err != nil {
		return explain(err)
	}
	if err := r.Rename(tmp, p); err != nil {
		_ = r.Remove(tmp)
		return explain(err)
	}
	return nil
}

func mkParents(r *os.Root, p string) error {
	if dir := path.Dir(p); dir != "." {
		return explain(r.MkdirAll(dir, 0o755))
	}
	return nil
}

// List donne le contenu d'un dossier ("" = la racine), trié : « nom/ » pour un
// dossier, « nom (N octets) » pour un fichier.
func List(p string) ([]string, error) {
	r, p, err := open(p)
	if err != nil {
		return nil, err
	}
	f, err := r.Open(p)
	if err != nil {
		return nil, explain(err)
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, explain(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	out := make([]string, 0, min(len(entries), maxListed+1))
	for i, e := range entries {
		if i >= maxListed {
			out = append(out, fmt.Sprintf("(+%d autres)", len(entries)-i))
			break
		}
		switch info, err := e.Info(); {
		case e.IsDir():
			out = append(out, e.Name()+"/")
		case err == nil:
			out = append(out, fmt.Sprintf("%s (%d octets)", e.Name(), info.Size()))
		default:
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// Move déplace ou renomme un fichier ou un dossier, sans jamais écraser.
func Move(from, to string) error {
	r, from, err := open(from)
	if err != nil {
		return err
	}
	_, to, err = open(to)
	if err != nil {
		return err
	}
	if from == "." || to == "." {
		return errors.New("donne deux chemins de fichiers ou de dossiers")
	}
	if _, err := r.Lstat(to); err == nil {
		return fmt.Errorf("existe déjà : %s", to)
	}
	if err := mkParents(r, to); err != nil {
		return err
	}
	return explain(r.Rename(from, to))
}

// Delete supprime un fichier, ou un dossier avec tout son contenu.
func Delete(p string) error {
	r, p, err := open(p)
	if err != nil {
		return err
	}
	if p == "." {
		return errors.New("refusé : on ne supprime pas le workspace lui-même")
	}
	if _, err := r.Lstat(p); err != nil {
		return explain(err)
	}
	return explain(r.RemoveAll(p))
}

// Exists : le chemin désigne-t-il quelque chose dans le workspace ?
func Exists(p string) bool {
	r, p, err := open(p)
	if err != nil {
		return false
	}
	_, err = r.Lstat(p)
	return err == nil
}

// Abs donne le chemin absolu sur le disque d'un chemin du workspace, pour un
// programme qui écrit sans passer par os.Root (le navigateur qui télécharge).
// Le fichier peut ne pas exister encore ; mais aucun dossier du chemin ne doit
// être un lien menant hors du workspace, sinon l'écriture en sortirait.
func Abs(p string) (string, error) {
	r, p, err := open(p)
	if err != nil {
		return "", err
	}
	for dir := p; dir != "."; dir = path.Dir(dir) {
		if _, err := r.Lstat(dir); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // pas encore créé : rien à suivre, on vérifie le parent
			}
			return "", explain(err)
		}
		// Stat suit les liens, et os.Root échoue s'ils mènent dehors (ou nulle part).
		if _, err := r.Stat(dir); err != nil {
			return "", fmt.Errorf("chemin refusé : %s mène hors du workspace", dir)
		}
		break
	}
	return filepath.Join(r.Name(), filepath.FromSlash(p)), nil
}

// Entry : un élément d'un dossier, pour l'écran « Fichiers ».
type Entry struct {
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // secondes Unix
}

// Entries donne le contenu d'un dossier ("" = la racine) : les dossiers d'abord,
// puis les fichiers, chacun par ordre alphabétique. Les fichiers temporaires
// d'une écriture en cours (.ajean-*.tmp) sont cachés.
func Entries(p string) ([]Entry, error) {
	r, p, err := open(p)
	if err != nil {
		return nil, err
	}
	f, err := r.Open(p)
	if err != nil {
		return nil, explain(err)
	}
	defer f.Close()
	des, err := f.ReadDir(-1)
	if err != nil {
		return nil, explain(err)
	}
	out := make([]Entry, 0, len(des))
	for _, d := range des {
		n := d.Name()
		if strings.HasPrefix(n, ".ajean-") && strings.HasSuffix(n, ".tmp") {
			continue
		}
		e := Entry{Name: n, Dir: d.IsDir()}
		if info, err := d.Info(); err == nil {
			e.Modified = info.ModTime().Unix()
			if !e.Dir {
				e.Size = info.Size()
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// OpenFile ouvre un fichier ordinaire en lecture (téléchargement depuis l'app).
func OpenFile(p string) (*os.File, fs.FileInfo, error) {
	r, p, err := open(p)
	if err != nil {
		return nil, nil, err
	}
	f, err := r.Open(p)
	if err != nil {
		return nil, nil, explain(err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, errors.New("ce n'est pas un fichier")
	}
	return f, info, nil
}

// Mkdir crée un dossier (et ses parents), sans rien écraser.
func Mkdir(p string) error {
	r, p, err := open(p)
	if err != nil {
		return err
	}
	if p == "." {
		return errors.New("donne un nom de dossier")
	}
	if _, err := r.Lstat(p); err == nil {
		return fmt.Errorf("existe déjà : %s", p)
	}
	return explain(r.MkdirAll(p, 0o755))
}
