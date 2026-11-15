package embedder

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Les archives officielles de llama.cpp contiennent une vingtaine de
// programmes. AJEAN n'en lance qu'un : seuls le serveur et les bibliothèques
// qu'il charge sont extraits, tous dans le même dossier (l'arborescence
// interne varie d'une archive à l'autre, et le serveur cherche ses
// bibliothèques à côté de lui).

// extract sort d'une archive (.zip, sinon .tar.gz) le serveur et ses
// bibliothèques, à plat dans dir. Une archive dont une entrée désigne un chemin
// hors d'elle-même est refusée en entier.
func extract(archive, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if strings.HasSuffix(archive, ".zip") {
		return extractZip(archive, dir)
	}
	return extractTarGz(archive, dir)
}

// wanted : ce fichier est-il le serveur ou une bibliothèque partagée ?
func wanted(base string) bool {
	name := strings.ToLower(base)
	return name == "llama-server" || name == "llama-server.exe" ||
		strings.HasSuffix(name, ".dll") || strings.HasSuffix(name, ".dylib") || strings.Contains(name, ".so")
}

// target : où ranger une entrée de l'archive ("" : on ne la garde pas). Comme
// seul le nom du fichier sert, rien ne peut s'écrire hors de dir ; une entrée
// qui essaie quand même d'en sortir trahit une archive fabriquée.
func target(dir, name string) (string, error) {
	local := filepath.FromSlash(name)
	if !filepath.IsLocal(local) {
		return "", fmt.Errorf("archive refusée : chemin suspect %q", name)
	}
	base := filepath.Base(local)
	if !wanted(base) {
		return "", nil
	}
	return filepath.Join(dir, base), nil
}

func writeFile(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func extractZip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		dest, err := target(dir, f.Name)
		if err != nil {
			return err
		}
		if dest == "" || !f.Mode().IsRegular() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(dest, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	// Sous macOS et Linux, une bibliothèque est livrée sous son nom complet
	// (libllama.0.0.1.dylib) avec un lien au nom que le serveur cherche
	// (libllama.0.dylib). Les liens sont posés à la fin : leur cible n'est pas
	// forcément déjà extraite quand on les croise.
	links := map[string]string{}
	for tr := tar.NewReader(gz); ; {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		dest, err := target(dir, h.Name)
		if err != nil {
			return err
		}
		switch {
		case dest == "":
		case h.Typeflag == tar.TypeReg:
			if err := writeFile(dest, tr); err != nil {
				return err
			}
		case h.Typeflag == tar.TypeSymlink:
			// Tout est à plat : la cible d'un lien ne peut être qu'un fichier
			// voisin, quel que soit le chemin écrit dans l'archive.
			links[dest] = filepath.Base(filepath.FromSlash(h.Linkname))
		}
	}
	for dest, name := range links {
		if err := link(dest, name); err != nil {
			return err
		}
	}
	return nil
}

// link fait de dest un lien vers son voisin name. Là où les liens sont
// refusés, une copie fait l'affaire. Une cible absente est ignorée : elle
// n'était pas à garder.
func link(dest, name string) error {
	src := filepath.Join(filepath.Dir(dest), name)
	if fi, err := os.Stat(src); err != nil || !fi.Mode().IsRegular() || src == dest {
		return nil
	}
	_ = os.Remove(dest)
	if os.Symlink(name, dest) == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeFile(dest, in)
}
