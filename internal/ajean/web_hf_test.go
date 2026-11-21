package ajean

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHFSearchEtFichiers(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/models":
			gotQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`[{"id":"bartowski/Qwen3-GGUF","downloads":1200,"likes":9},{"id":"pas un dépôt"}]`))
		case strings.HasSuffix(r.URL.Path, "/tree/main"):
			_, _ = w.Write([]byte(`[
				{"type":"directory","path":"big"},
				{"type":"file","path":"README.md","size":10},
				{"type":"file","path":"Qwen3-Q8_0.gguf","size":130,"lfs":{"size":9000}},
				{"type":"file","path":"Qwen3-Q4_K_M.gguf","size":130,"lfs":{"size":5000}},
				{"type":"file","path":"mmproj-Qwen3-F16.gguf","size":130,"lfs":{"size":800}},
				{"type":"file","path":"big/Qwen3-F16-00002-of-00002.gguf","lfs":{"size":7000}},
				{"type":"file","path":"big/Qwen3-F16-00001-of-00002.gguf","lfs":{"size":8000}}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := hfAPIBase
	hfAPIBase = srv.URL
	defer func() { hfAPIBase = old }()

	get := func(h http.HandlerFunc, target string) map[string]any {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest("GET", target, nil))
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	out := get(handleHFSearch, "/api/hf/search?q=qwen+3")
	repos, _ := out["repos"].([]any)
	if out["ok"] != true || len(repos) != 1 || repos[0].(map[string]any)["id"] != "bartowski/Qwen3-GGUF" {
		t.Fatalf("recherche : %v", out)
	}
	if !strings.Contains(gotQuery, "filter=gguf") || !strings.Contains(gotQuery, "search=qwen+3") {
		t.Errorf("requête envoyée à Hugging Face : %s", gotQuery)
	}
	if out := get(handleHFSearch, "/api/hf/search?q=a"); len(out["repos"].([]any)) != 0 {
		t.Error("une recherche d'un caractère ne doit rien interroger")
	}

	out = get(handleHFFiles, "/api/hf/files?repo=bartowski/Qwen3-GGUF")
	files, _ := out["files"].([]any)
	var names []string
	for _, f := range files {
		names = append(names, f.(map[string]any)["name"].(string))
	}
	// Du plus léger au plus lourd, le modèle découpé une seule fois, la vision à la fin.
	want := "Qwen3-Q4_K_M.gguf Qwen3-Q8_0.gguf big/Qwen3-F16-00001-of-00002.gguf mmproj-Qwen3-F16.gguf"
	if strings.Join(names, " ") != want {
		t.Fatalf("fichiers : %v", names)
	}
	split := files[2].(map[string]any)
	if split["size"].(float64) != 15000 || split["parts"].(float64) != 2 ||
		split["url"] != srv.URL+"/bartowski/Qwen3-GGUF/resolve/main/big/Qwen3-F16-00001-of-00002.gguf" {
		t.Errorf("modèle découpé : %v", split)
	}
	if files[3].(map[string]any)["mmproj"] != true || files[0].(map[string]any)["mmproj"] != nil {
		t.Errorf("repérage du projecteur vision : %v", files)
	}
	w := httptest.NewRecorder()
	handleHFFiles(w, httptest.NewRequest("GET", "/api/hf/files?repo=../../etc", nil))
	if w.Code != 400 {
		t.Errorf("dépôt invalide accepté (%d)", w.Code)
	}
}
