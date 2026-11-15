package embedder

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"ajean/internal/home"
)

// La version de llama.cpp est ÉPINGLÉE : AJEAN n'installe que ces archives-là,
// dont la taille et l'empreinte ont été relevées sur la page des versions
// officielles. Une archive différente, même servie par la bonne adresse, est
// refusée. Ce sont les versions « processeur » : un modèle d'embeddings de
// quelques centaines de Mo n'a pas besoin d'une carte graphique.
const (
	engineTag  = "b12257"
	releaseURL = "https://github.com/ggml-org/llama.cpp/releases/download/" + engineTag + "/"
)

// asset : une archive officielle.
type asset struct {
	name   string
	size   int64
	sha256 string
}

// engineAssets : l'archive de chaque système, par « GOOS/GOARCH ».
var engineAssets = map[string]asset{
	"windows/amd64": {"llama-b12257-bin-win-cpu-x64.zip", 25037286, "9dbb472a6a47cba710bc8a84df921312720e84d08af870df4398884f3d022aef"},
	"windows/arm64": {"llama-b12257-bin-win-cpu-arm64.zip", 13388253, "edbb41974209eaba92852fe242e9a026e17de900ab0d161a321f6f0526a9482c"},
	"linux/amd64":   {"llama-b12257-bin-ubuntu-x64.tar.gz", 23262474, "75c55c9d5c4b620cf305c034149ea2ed33832542af78b719f3888af1d1359996"},
	"linux/arm64":   {"llama-b12257-bin-ubuntu-arm64.tar.gz", 18909286, "8fc0879acbd5a312d3c2abe98a99311c9f8facf68ed4335da901138d4dfd873c"},
	"darwin/arm64":  {"llama-b12257-bin-macos-arm64.tar.gz", 16850701, "e77905e5c5393414e0d0345d5921ed5d8836a2d9fe1ad3dd38d57e8d1ea292e2"},
	"darwin/amd64":  {"llama-b12257-bin-macos-x64.tar.gz", 12593777, "b91196a0687be92041d580e0ee3d57b05c58576587557b6ac7d2f21f57608e34"},
}

func enginesDir() string { return home.State("embedder", "engine") }

// engineDir : le dossier de la version épinglée. Une autre version installée
// auparavant n'est pas reconnue : elle est remplacée à la prochaine installation.
func engineDir() string { return filepath.Join(enginesDir(), engineTag) }

func serverName() string {
	if runtime.GOOS == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

// engineBinary : le serveur installé ("" s'il ne l'est pas).
func engineBinary() string {
	bin := filepath.Join(engineDir(), serverName())
	if fi, err := os.Stat(bin); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return bin
}

// unpackEngine installe le serveur à partir d'une archive déjà vérifiée.
// L'extraction se fait à côté et le dossier ne prend son nom qu'à la fin : un
// dossier qui porte le nom de la version est toujours complet.
func unpackEngine(archive string) error {
	tmp := engineDir() + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := extract(archive, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if _, err := os.Stat(filepath.Join(tmp, serverName())); err != nil {
		_ = os.RemoveAll(tmp)
		return errors.New("l'archive ne contient pas " + serverName())
	}
	if err := os.RemoveAll(engineDir()); err != nil {
		return err
	}
	if err := os.Rename(tmp, engineDir()); err != nil {
		return err
	}
	// L'archive et les versions précédentes ne servent plus.
	entries, err := os.ReadDir(enginesDir())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != engineTag {
			_ = os.RemoveAll(filepath.Join(enginesDir(), e.Name()))
		}
	}
	return nil
}
