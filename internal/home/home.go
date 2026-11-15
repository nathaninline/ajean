// Package home situe les données d'AJEAN sur le disque. Tout vit à côté de
// l'exécutable :
//
//	MEMORY/      toute la mémoire, en fichiers Markdown lisibles et modifiables
//	             (profil, leçons, règles, fiches, journal, conversations)
//	WORKSPACE/   le seul dossier où l'assistant peut lire et écrire des fichiers
//	TOOLS/       les outils que l'assistant s'est fabriqués (scripts .js)
//	APPS/        les petites applications qu'il a créées pour l'utilisateur
//	config.json  l'API à laquelle il est branché
//	.ajean/      l'état technique (compteurs, rappels, copies d'avant sommeil,
//	             profil du navigateur) : rien qu'on ait besoin de lire
package home

import (
	"os"
	"path/filepath"
	"strings"
)

// Dir : le dossier de l'exécutable, ou AJEAN_HOME s'il est défini.
func Dir() string {
	if d := strings.TrimSpace(os.Getenv("AJEAN_HOME")); d != "" {
		return d
	}
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

func Memory() string    { return filepath.Join(Dir(), "MEMORY") }
func Workspace() string { return filepath.Join(Dir(), "WORKSPACE") }
func Tools() string     { return filepath.Join(Dir(), "TOOLS") }
func Apps() string      { return filepath.Join(Dir(), "APPS") }
func Config() string    { return filepath.Join(Dir(), "config.json") }

// State : un fichier ou dossier de l'état technique (.ajean/…).
func State(elem ...string) string {
	return filepath.Join(append([]string{Dir(), ".ajean"}, elem...)...)
}

// WriteFile écrit dans un fichier temporaire puis le renomme : une coupure en
// pleine écriture ne laisse jamais un fichier à moitié écrit, et deux écritures
// simultanées ne se mélangent pas (chacune a son fichier temporaire). Les
// dossiers manquants sont créés.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
