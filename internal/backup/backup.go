// Package backup fait des archives chiffrées des données d'AJEAN, à la demande
// ou chaque jour vers AJEAN_BACKUP_DIR (un disque ou un dossier synchronisé hors
// de la machine). Le WORKSPACE n'y est pas : il peut être très gros.
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ajean/internal/home"
)

const (
	magic      = "AJBK1"
	iterations = 600_000
	kept       = 7
	prefix     = "ajean-"
	suffix     = ".ajbk"
)

func sealKey(pass string, salt []byte) (cipher.AEAD, error) {
	k, err := pbkdf2.Key(sha256.New, pass, salt, iterations, 32)
	if err != nil {
		return nil, err
	}
	b, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// skipped : ce qui se recrée tout seul.
func skipped(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, p := range []string{".ajean/browser", ".ajean/convsearch"} {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return strings.HasSuffix(rel, ".tmp")
}

func archive() ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	root := home.Dir()
	for _, item := range []string{"MEMORY", "TOOLS", "APPS", ".ajean", "config.json"} {
		err := filepath.WalkDir(filepath.Join(root, item), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if skipped(rel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(rel)
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.CopyN(tw, f, hdr.Size)
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Create écrit une archive chiffrée dans dir et renvoie son chemin.
func Create(dir, pass string) (string, error) {
	if len(pass) < 12 {
		return "", errors.New("phrase de sauvegarde trop courte (12 caractères au moins)")
	}
	plain, err := archive()
	if err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	a, err := sealKey(pass, salt)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := append(append(append([]byte(magic), salt...), nonce...), a.Seal(nil, nonce, plain, []byte(magic))...)
	path := filepath.Join(dir, prefix+time.Now().Format("20060102-150405")+suffix)
	if err := home.WriteFile(path, out); err != nil {
		return "", err
	}
	prune(dir)
	return path, nil
}

func prune(dir string) {
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), suffix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for i := 0; i < len(names)-kept; i++ {
		_ = os.Remove(filepath.Join(dir, names[i]))
	}
}

// Restore déchiffre une archive dans dest, un dossier vide ou absent.
func Restore(path, dest, pass string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if ents, err := os.ReadDir(dest); err == nil && len(ents) > 0 {
		return fmt.Errorf("le dossier %s doit être vide", dest)
	}
	if len(raw) < len(magic)+16+12 || string(raw[:len(magic)]) != magic {
		return errors.New("ce fichier n'est pas une sauvegarde AJEAN")
	}
	salt := raw[len(magic) : len(magic)+16]
	a, err := sealKey(pass, salt)
	if err != nil {
		return err
	}
	rest := raw[len(magic)+16:]
	plain, err := a.Open(nil, rest[:a.NonceSize()], rest[a.NonceSize():], []byte(magic))
	if err != nil {
		return errors.New("phrase incorrecte ou archive abîmée")
	}
	gz, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.FromSlash(h.Name))
		if !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return fmt.Errorf("chemin refusé dans l'archive : %s", h.Name)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := home.WriteFile(target, data); err != nil {
			return err
		}
	}
}

// Schedule fait une sauvegarde par jour si AJEAN_BACKUP_DIR et AJEAN_BACKUP_KEY
// sont définis.
func Schedule(ctx context.Context) {
	dir, pass := strings.TrimSpace(os.Getenv("AJEAN_BACKUP_DIR")), os.Getenv("AJEAN_BACKUP_KEY")
	if dir == "" || pass == "" {
		return
	}
	run := func() {
		if _, err := Create(dir, pass); err != nil {
			log.Printf("sauvegarde automatique impossible : %v", err)
		}
	}
	run()
	tick := time.NewTicker(24 * time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			run()
		}
	}
}
