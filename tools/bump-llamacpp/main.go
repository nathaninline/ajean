// bump-llamacpp épingle la version de llama.cpp que livre AJEAN.
//
// AJEAN fonctionne comme Ollama : UNE version de llama.cpp par version d'AJEAN,
// choisie et testée par le mainteneur, jamais par l'utilisateur. Cette version
// tient en deux fichiers versionnés avec le code :
//
//	internal/ajean/LLAMA_CPP_VERSION      le tag amont (ex. b12257)
//	internal/ajean/llama_cpp_assets.json  les binaires de ce tag, avec taille et SHA-256
//
// Pour changer de version :
//
//	go run ./tools/bump-llamacpp b12345
//
// puis suivre la liste de contrôle de docs/moteur.md (drapeaux de lancement,
// /health, lignes de journal lues par AJEAN, tests avec un vrai modèle) AVANT
// de publier. Les empreintes viennent de l'API GitHub (champ « digest ») : le
// binaire qu'installe une machine est exactement celui qui a été validé.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type apiAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
	State  string `json:"state"`
}

type asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Tag    string  `json:"tag"`
	Assets []asset `json:"assets"`
}

// wanted : les seules archives qu'AJEAN sait installer (voir engineCandidates).
// Les autres (android, s390x, sycl, openvino, xcframework…) n'entrent pas dans
// le manifeste.
func wanted(name string) bool {
	if strings.HasPrefix(name, "cudart-") {
		return strings.Contains(name, "-win-cuda-") || strings.Contains(name, "-ubuntu-cuda-")
	}
	if !strings.HasPrefix(name, "llama-") {
		return false
	}
	for _, frag := range []string{
		"-bin-win-cuda-", "-bin-win-vulkan-x64", "-bin-win-cpu-",
		"-bin-macos-",
		"-bin-ubuntu-cuda-", "-bin-ubuntu-rocm-", "-bin-ubuntu-vulkan-",
		"-bin-ubuntu-x64.", "-bin-ubuntu-arm64.",
	} {
		if strings.Contains(name, frag) {
			return true
		}
	}
	return false
}

func main() {
	if len(os.Args) != 2 || strings.HasPrefix(os.Args[1], "-") {
		fmt.Fprintln(os.Stderr, "usage : go run ./tools/bump-llamacpp <tag>   (ex. b12257)")
		os.Exit(2)
	}
	if err := run(os.Args[1], filepath.Join("internal", "ajean")); err != nil {
		fmt.Fprintln(os.Stderr, "bump-llamacpp:", err)
		os.Exit(1)
	}
}

func run(tag, dir string) error {
	req, err := http.NewRequest("GET", "https://api.github.com/repos/ggml-org/llama.cpp/releases/tags/"+tag, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("release %s : HTTP %d", tag, resp.StatusCode)
	}
	var rel struct {
		TagName string     `json:"tag_name"`
		Assets  []apiAsset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return err
	}
	m := manifest{Tag: rel.TagName}
	for _, a := range rel.Assets {
		if !wanted(a.Name) {
			continue
		}
		if a.State != "" && a.State != "uploaded" {
			return fmt.Errorf("%s : envoi encore en cours côté llama.cpp, réessaie dans quelques minutes", a.Name)
		}
		sha, ok := strings.CutPrefix(a.Digest, "sha256:")
		if !ok || len(sha) != 64 {
			return fmt.Errorf("%s : empreinte SHA-256 absente de l'API GitHub", a.Name)
		}
		m.Assets = append(m.Assets, asset{Name: a.Name, Size: a.Size, SHA256: sha})
	}
	if len(m.Assets) == 0 {
		return fmt.Errorf("release %s : aucun binaire utilisable", tag)
	}
	sort.Slice(m.Assets, func(i, j int) bool { return m.Assets[i].Name < m.Assets[j].Name })
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "llama_cpp_assets.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "LLAMA_CPP_VERSION"), []byte(m.Tag+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("llama.cpp épinglé sur %s (%d archives). Reste à valider : voir docs/moteur.md\n", m.Tag, len(m.Assets))
	return nil
}
