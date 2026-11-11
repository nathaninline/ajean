package ajean

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Accès internet de l'IA — port Go de l'extension pi ~/.pi/agent/extensions/web.ts.
//
// Le moteur web est intégré (web_fetch_go.go) : rien à installer. Quand le mode
// agent ET l'accès internet sont actifs, l'IA dispose de 4 outils :
//
//   - web_search : recherche DuckDuckGo, liste {title, url, snippet}.
//   - web_open   : récupère une URL (cache 10 min), renvoie SEULEMENT les métadonnées
//                  (nb de lignes, taille, plan des titres) — pas le contenu.
//   - web_read   : lit une plage de lignes d'une URL déjà ouverte (offset + limit).
//   - web_grep   : recherche regex dans une URL déjà ouverte, lignes + contexte.
//
// Workflow attendu : web_open(url) → web_read/web_grep. Même logique que pi.

// ─── configuration & état ───────────────────────────────────────────────────

// internetEnabled : l'accès web fait partie des outils de l'IA, toujours
// disponible (il n'y a plus d'interrupteur depuis la 0.20). Le moteur intégré
// ne demande aucun réglage. Les outils restent liés au mode agent.
func internetEnabled() bool { return true }

// ─── URL & normalisation ────────────────────────────────────────────────────

// normalizeCrawlURL : github.com/owner/repo → README brut. Port de web.ts.
func normalizeCrawlURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Host == "github.com" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 2 {
			return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/HEAD/README.md", parts[0], parts[1])
		}
	}
	return raw
}

// normalizeLines : \r\n → \n, trim trailing, réduit les runs de lignes vides et
// les doublons consécutifs, retire les vides en tête/queue. Port de web.ts.
func normalizeLines(raw string) []string {
	src := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	out := []string{}
	prev := ""
	blankRun := 0
	trailSpace := regexp.MustCompile(`[ \t]+$`)
	for _, l := range src {
		l = trailSpace.ReplaceAllString(l, "")
		if strings.TrimSpace(l) == "" {
			blankRun++
			if blankRun > 1 {
				continue
			}
			out = append(out, "")
			prev = ""
			continue
		}
		blankRun = 0
		if l == prev {
			continue
		}
		out = append(out, l)
		prev = l
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// ─── cache mémoire (TTL 10 min) ─────────────────────────────────────────────

type cacheEntry struct {
	url       string
	lines     []string
	fetchedAt time.Time
}

var (
	pageCacheMu sync.Mutex
	pageCache   = map[string]*cacheEntry{}
)

const pageCacheTTL = 10 * time.Minute

type fetchOptions struct {
	force bool // ignore le cache de 10 min
}

// findCached : entrée de cache la plus récente pour une URL (toutes options).
func findCached(rawURL string) *cacheEntry {
	u := normalizeCrawlURL(rawURL)
	pageCacheMu.Lock()
	defer pageCacheMu.Unlock()
	var best *cacheEntry
	for k, v := range pageCache {
		if k == u || strings.HasPrefix(k, u+"#") {
			if time.Since(v.fetchedAt) > pageCacheTTL {
				continue
			}
			if best == nil || v.fetchedAt.After(best.fetchedAt) {
				best = v
			}
		}
	}
	return best
}

func getPage(rawURL string, opts fetchOptions) (*cacheEntry, error) {
	u := normalizeCrawlURL(rawURL)
	key := u
	pageCacheMu.Lock()
	cached := pageCache[key]
	pageCacheMu.Unlock()
	if !opts.force && cached != nil && time.Since(cached.fetchedAt) < pageCacheTTL {
		return cached, nil
	}

	// Pas de DOM vivant : la page servie, sans JavaScript (voir web_fetch_go.go).
	md, err := goFetchMarkdown(u)
	if err != nil {
		return nil, err
	}
	// Le diagnostic « probablement du JavaScript » est posé par goFetchMarkdown,
	// qui seul connaît la taille du HTML brut. Ici on n'attrape que le cas trivial.
	if strings.TrimSpace(md) == "" {
		return nil, fmt.Errorf("page vide")
	}
	entry := &cacheEntry{url: u, lines: normalizeLines(md), fetchedAt: time.Now()}
	pageCacheMu.Lock()
	pageCache[key] = entry
	pageCacheMu.Unlock()
	return entry, nil
}

// ─── helpers de formatage ───────────────────────────────────────────────────

var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)

func extractOutline(lines []string) string {
	var out []string
	for i, l := range lines {
		if m := headingRe.FindStringSubmatch(l); m != nil {
			indent := strings.Repeat("  ", len(m[1])-1)
			out = append(out, fmt.Sprintf("%5d | %s%s", i+1, indent, m[2]))
		}
	}
	if len(out) == 0 {
		return "(aucun titre markdown trouvé)"
	}
	return strings.Join(out, "\n")
}

func formatLines(lines []string, startLine int) string {
	var b strings.Builder
	for i, l := range lines {
		fmt.Fprintf(&b, "%5d | %s\n", startLine+i, l)
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.2f MB", float64(n)/1024/1024)
	}
}

// ─── recherche DuckDuckGo ───────────────────────────────────────────────────

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

var wsRe = regexp.MustCompile(`\s+`)

// decodeUddg : DDG enrobe les résultats en //duckduckgo.com/l/?uddg=URL_ENCODÉE
func decodeUddg(raw string) string {
	s := raw
	if strings.HasPrefix(s, "//") {
		s = "https:" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	if q := u.Query().Get("uddg"); q != "" {
		if dec, e := url.QueryUnescape(q); e == nil {
			return dec
		}
	}
	return s
}

// ─── définitions d'outils (schémas OpenAI, comme llm_client.go) ────────────────────
