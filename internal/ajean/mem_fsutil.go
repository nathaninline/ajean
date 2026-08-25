package ajean

// mem_fsutil.go — écritures fichier sûres pour la mémoire : temporaire + fsync +
// rename atomique. Aucune donnée n'est jamais écrite « en place » par-dessus une
// ancienne : on écrit à côté, on force sur disque, puis on bascule d'un rename
// (atomique au niveau du système de fichiers).

import (
	"fmt"
	"os"
	"path/filepath"
)

// memWriteFileAtomic écrit data dans path de façon atomique et durable. En cas
// d'échec, path conserve son ancien contenu (le temporaire est nettoyé).
func memWriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil { // durabilité : sur disque avant rename
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// memWriteFileVerified écrit data atomiquement PUIS relit le fichier et compare
// octet pour octet. C'est la garantie « ce qui est sur le disque est bien ce
// qu'on voulait » avant toute suppression de l'original. En cas d'écart, le
// fichier écrit est retiré et une erreur est renvoyée.
func memWriteFileVerified(path string, data []byte, perm os.FileMode) error {
	if err := memWriteFileAtomic(path, data, perm); err != nil {
		return err
	}
	back, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("relecture de vérification impossible : %w", err)
	}
	if len(back) != len(data) {
		os.Remove(path)
		return fmt.Errorf("vérification échouée : %d octets relus, %d écrits", len(back), len(data))
	}
	for i := range back {
		if back[i] != data[i] {
			os.Remove(path)
			return fmt.Errorf("vérification échouée : octet %d diffère", i)
		}
	}
	return nil
}
