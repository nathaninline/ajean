package embedder

import (
	"errors"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"ajean/internal/home"
)

// Le modèle proposé d'office : EmbeddingGemma, petit, multilingue, à l'aise sur
// un processeur.
const (
	defaultModelURL  = "https://huggingface.co/ggml-org/embeddinggemma-300M-GGUF/resolve/main/embeddinggemma-300M-Q8_0.gguf"
	defaultModelSize = 333590944
	modelHost        = "huggingface.co"
	modelExt         = ".gguf"
)

// modelNameRe : un nom de fichier sans surprise, puisqu'il vient d'une adresse
// saisie et finit sur le disque.
var modelNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,120}\.gguf$`)

// model : un modèle à télécharger.
type model struct {
	url  string
	file string
	size int64 // 0 : inconnue avant le téléchargement
}

// parseModelURL valide l'adresse d'un modèle (vide : celui proposé d'office).
// Seul un fichier GGUF de Hugging Face est accepté : ce fichier sera chargé par
// le moteur, on ne le prend pas n'importe où.
func parseModelURL(raw string) (model, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultModelURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != modelHost || u.User != nil {
		return model{}, errors.New("l'adresse doit être celle d'un fichier sur https://" + modelHost)
	}
	file := path.Base(u.Path)
	if !strings.HasSuffix(u.Path, "/"+file) || !modelNameRe.MatchString(file) {
		return model{}, errors.New("l'adresse doit désigner un fichier " + modelExt)
	}
	// L'adresse copiée depuis la page du fichier (« /blob/ ») mène à la page,
	// pas au fichier.
	u.Path = strings.Replace(u.Path, "/blob/", "/resolve/", 1)
	m := model{url: u.String(), file: file}
	if m.url == defaultModelURL {
		m.size = defaultModelSize
	}
	return m, nil
}

func modelsDir() string { return home.State("embedder", "models") }

// installedModel : le modèle en place ("" s'il n'y en a pas). S'il en reste
// deux (un remplacement interrompu), le plus récent est le bon.
func installedModel() (file string, size int64) {
	entries, _ := os.ReadDir(modelsDir())
	var newest os.FileInfo
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() || filepath.Ext(e.Name()) != modelExt {
			continue
		}
		if newest == nil || fi.ModTime().After(newest.ModTime()) {
			newest = fi
		}
	}
	if newest == nil {
		return "", 0
	}
	return newest.Name(), newest.Size()
}

// prefixes : ce qu'un modèle attend devant une question et devant un texte à
// retrouver. EmbeddingGemma a été entraîné avec ces consignes et cherche
// nettement moins bien sans elles ; pour un modèle inconnu, rien n'est ajouté.
func prefixes(file string) (query, document string) {
	if strings.Contains(strings.ToLower(file), "embeddinggemma") {
		return "task: search result | query: ", "title: none | text: "
	}
	return "", ""
}
