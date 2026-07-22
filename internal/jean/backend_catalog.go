package jean

// backend_catalog.go — récupère la liste de modèles curatée servie par ajean.link et la
// combine avec les infos matérielles locales, pour que l'écran d'accueil (à
// venir) propose « en un clic » un modèle adapté à la machine.
//
// La liste vit sur https://ajean.link/models.json (éditée par l'opérateur, voir
// jean-relay/models_catalog.go). Si le réseau échoue, on retombe sur un
// catalogue minimal embarqué (fallbackCatalogJSON) pour ne jamais bloquer.

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"
)

const catalogURL = "https://ajean.link/models.json"

type catalogModel struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Params   string  `json:"params"`
	Quant    string  `json:"quant"`
	SizeGB   float64 `json:"size_gb"`
	MinRAMGB float64 `json:"min_ram_gb"`
	URL      string  `json:"url"`
	Note     string  `json:"note"`
}

type catalog struct {
	Version int            `json:"version"`
	Models  []catalogModel `json:"models"`
}

type hardwareInfo struct {
	OS    string  `json:"os"`
	Arch  string  `json:"arch"`
	RAMGB float64 `json:"ram_gb"`
}

// fetchCatalog récupère le catalogue distant, avec repli embarqué.
func fetchCatalog() catalog {
	var c catalog
	client := &http.Client{Timeout: 8 * time.Second}
	if resp, err := client.Get(catalogURL); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 && json.NewDecoder(resp.Body).Decode(&c) == nil && len(c.Models) > 0 {
			return c
		}
	}
	_ = json.Unmarshal([]byte(fallbackCatalogJSON), &c)
	return c
}

func detectHardware() hardwareInfo {
	return hardwareInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, RAMGB: totalRAMGB()}
}

// handleCatalog : renvoie le catalogue + le matériel local. L'UI s'en sert pour
// marquer chaque modèle « tient / trop lourd » et proposer le bon par défaut.
func handleCatalog(w http.ResponseWriter, r *http.Request) {
	resp := struct {
		Hardware hardwareInfo   `json:"hardware"`
		Models   []catalogModel `json:"models"`
	}{Hardware: detectHardware(), Models: fetchCatalog().Models}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// fallbackCatalogJSON — repli minimal si ajean.link/models.json est injoignable.
const fallbackCatalogJSON = `{
  "version": 1,
  "models": [
    {"id":"qwen2.5-3b-instruct-q4","name":"Qwen2.5 3B Instruct","params":"3B","quant":"Q4_K_M","size_gb":2.1,"min_ram_gb":6,"url":"https://huggingface.co/bartowski/Qwen2.5-3B-Instruct-GGUF/resolve/main/Qwen2.5-3B-Instruct-Q4_K_M.gguf","note":"Léger et rapide — idéal petites machines."},
    {"id":"qwen2.5-7b-instruct-q4","name":"Qwen2.5 7B Instruct","params":"7B","quant":"Q4_K_M","size_gb":4.7,"min_ram_gb":10,"url":"https://huggingface.co/bartowski/Qwen2.5-7B-Instruct-GGUF/resolve/main/Qwen2.5-7B-Instruct-Q4_K_M.gguf","note":"Plus capable — recommandé avec 16 Go de RAM ou un GPU."}
  ]
}`
