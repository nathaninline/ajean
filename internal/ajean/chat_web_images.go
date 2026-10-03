package ajean

// web_images : la recherche d'images DuckDuckGo, pendant de web_search. Sans
// elle, « montre-moi des photos de X » obligeait le modèle à ouvrir des pages
// dans le navigateur et à en renvoyer des captures (lent, et des images de
// pages au lieu de photos).
//
// DuckDuckGo sert les images par i.js, qui exige un jeton « vqd » lu dans la
// page de recherche. Même client HTTP que le moteur intégré, quel que soit le
// moteur web actif : Crawl4AI n'apporterait rien à un appel JSON.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

func webImagesTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "web_images",
		Description: "Search images on the web.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Query"},
				"limit": map[string]any{"type": "integer", "description": "Default 6, max 20"},
			},
			"required": []string{"query"},
		},
	}}
}

type imageResult struct {
	Image  string `json:"image"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

var ddgVqdRe = regexp.MustCompile(`vqd=["']?([0-9-]+)`)

func ddgGet(ctx context.Context, target, referer string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", goFetchUA)
	req.Header.Set("Accept-Language", "fr-FR,fr;q=0.9,en;q=0.8")
	if referer != "" {
		req.Header.Set("Referer", referer)
		req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	}
	resp, err := goHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("échec de la requête : %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, goFetchMaxBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return b, nil
}

func duckduckgoImages(query string, limit int) ([]imageResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), goFetchTimeout)
	defer cancel()
	page := "https://duckduckgo.com/?q=" + url.QueryEscape(query) + "&iax=images&ia=images"
	body, err := ddgGet(ctx, page, "")
	if err != nil {
		return nil, err
	}
	m := ddgVqdRe.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("jeton DuckDuckGo introuvable (défi anti-bot ?)")
	}
	api := "https://duckduckgo.com/i.js?l=fr-fr&o=json&f=,,,,,&p=1&q=" + url.QueryEscape(query) + "&vqd=" + string(m[1])
	body, err = ddgGet(ctx, api, "https://duckduckgo.com/")
	if err != nil {
		return nil, err
	}
	var r struct {
		Results []imageResult `json:"results"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("réponse illisible : %v", err)
	}
	var out []imageResult
	seen := map[string]bool{}
	for _, it := range r.Results {
		if it.Image == "" || seen[it.Image] || !strings.HasPrefix(it.Image, "http") {
			continue
		}
		seen[it.Image] = true
		out = append(out, it)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func toolWebImages(args map[string]any) string {
	query, _ := args["query"].(string)
	limit := 6
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 20 {
		limit = 20
	}
	results, err := duckduckgoImages(query, limit)
	if err != nil {
		return "❌ Recherche d'images échouée : " + err.Error()
	}
	if len(results) == 0 {
		return fmt.Sprintf("Aucune image pour « %s »", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Images : %s\n%d résultat(s) DuckDuckGo\n\n", query, len(results))
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s (%dx%d)\n   image : %s\n   page : %s\n\n", i+1, r.Title, r.Width, r.Height, r.Image, r.URL)
	}
	return strings.TrimRight(b.String(), "\n")
}
