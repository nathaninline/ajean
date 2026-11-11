package ajean

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// web_hf.go — recherche de modèles GGUF sur Hugging Face, depuis l'éditeur de
// preset : on cherche un modèle, on choisit une quantification, AJEAN le
// télécharge avec son téléchargeur habituel (reprise, tranches, espace libre).
// Plus besoin de quitter AJEAN pour aller copier un lien.
//
//	GET /api/hf/search?q=qwen        → dépôts GGUF, les plus téléchargés d'abord
//	GET /api/hf/files?repo=owner/nom → fichiers .gguf du dépôt, avec leur taille
//
// Seul le texte recherché part chez Hugging Face. HF_TOKEN (variable
// d'environnement), s'il existe, donne accès aux dépôts privés ou à accès
// restreint.

// hfAPIBase : variable pour les tests.
var hfAPIBase = "https://huggingface.co"

var hfClient = &http.Client{Timeout: 20 * time.Second}

var hfRepoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

func hfGet(path string, out any) error {
	req, err := http.NewRequest("GET", hfAPIBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ajean/"+Version)
	if tok := strings.TrimSpace(os.Getenv("HF_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := hfClient.Do(req)
	if err != nil {
		return fmt.Errorf("Hugging Face injoignable : %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("dépôt privé ou à accès restreint (définir HF_TOKEN pour y accéder)")
	case http.StatusNotFound:
		return fmt.Errorf("dépôt introuvable sur Hugging Face")
	default:
		return fmt.Errorf("Hugging Face : HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type hfRepo struct {
	ID        string `json:"id"`
	Downloads int    `json:"downloads"`
	Likes     int    `json:"likes"`
}

// handleHFSearch : dépôts contenant des GGUF qui correspondent à la recherche.
func handleHFSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		sendJSON(w, 200, map[string]any{"ok": true, "repos": []hfRepo{}})
		return
	}
	var raw []struct {
		ID        string `json:"id"`
		ModelID   string `json:"modelId"`
		Downloads int    `json:"downloads"`
		Likes     int    `json:"likes"`
	}
	path := "/api/models?filter=gguf&sort=downloads&direction=-1&limit=25&search=" + url.QueryEscape(q)
	if err := hfGet(path, &raw); err != nil {
		sendJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	repos := make([]hfRepo, 0, len(raw))
	for _, m := range raw {
		id := m.ID
		if id == "" {
			id = m.ModelID
		}
		if hfRepoRe.MatchString(id) {
			repos = append(repos, hfRepo{ID: id, Downloads: m.Downloads, Likes: m.Likes})
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "repos": repos})
}

type hfFile struct {
	Name   string `json:"name"`             // chemin dans le dépôt
	Size   int64  `json:"size"`             // toutes les tranches comprises
	URL    string `json:"url"`              // lien direct, à donner au téléchargeur
	Parts  int    `json:"parts,omitempty"`  // nombre de tranches (modèle découpé)
	Mmproj bool   `json:"mmproj,omitempty"` // projecteur vision, pas un modèle
}

var hfShardRe = regexp.MustCompile(`^(.*)-(\d{5})-of-(\d{5})\.gguf$`)

// hfGGUFFiles réduit l'arborescence d'un dépôt à ses .gguf téléchargeables : un
// modèle découpé en tranches n'apparaît qu'une fois (sa première tranche, que
// le téléchargeur sait compléter), avec la taille de l'ensemble.
func hfGGUFFiles(repo string, tree []hfTreeEntry) []hfFile {
	byKey := map[string]*hfFile{}
	var order []string
	for _, e := range tree {
		if e.Type != "file" || !strings.HasSuffix(strings.ToLower(e.Path), ".gguf") {
			continue
		}
		size := e.Size
		if e.LFS != nil && e.LFS.Size > 0 {
			size = e.LFS.Size
		}
		key, first := e.Path, true
		if m := hfShardRe.FindStringSubmatch(e.Path); m != nil {
			key, first = m[1]+"-of-"+m[3], m[2] == "00001"
		}
		f := byKey[key]
		if f == nil {
			f = &hfFile{}
			byKey[key] = f
			order = append(order, key)
		}
		f.Size += size
		f.Parts++
		if first {
			f.Name = e.Path
			f.URL = hfAPIBase + "/" + repo + "/resolve/main/" + (&url.URL{Path: e.Path}).EscapedPath()
		}
	}
	out := make([]hfFile, 0, len(order))
	for _, k := range order {
		f := byKey[k]
		if f.Name == "" {
			continue // tranches sans la première : inutilisable
		}
		if f.Parts == 1 {
			f.Parts = 0
		}
		base := strings.ToLower(f.Name[strings.LastIndex(f.Name, "/")+1:])
		f.Mmproj = strings.Contains(base, "mmproj")
		out = append(out, *f)
	}
	// Les modèles du plus léger au plus lourd, les projecteurs vision à la fin.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Mmproj != out[j].Mmproj {
			return !out[i].Mmproj
		}
		return out[i].Size < out[j].Size
	})
	return out
}

type hfTreeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	LFS  *struct {
		Size int64 `json:"size"`
	} `json:"lfs"`
}

// handleHFFiles : les fichiers .gguf d'un dépôt.
func handleHFFiles(w http.ResponseWriter, r *http.Request) {
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	if !hfRepoRe.MatchString(repo) {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "dépôt invalide (attendu : auteur/nom)"})
		return
	}
	var tree []hfTreeEntry
	if err := hfGet("/api/models/"+repo+"/tree/main?recursive=true", &tree); err != nil {
		sendJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "repo": repo, "files": hfGGUFFiles(repo, tree)})
}
