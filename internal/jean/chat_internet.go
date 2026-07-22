package jean

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Accès internet de l'IA — port Go de l'extension pi ~/.pi/agent/extensions/web.ts.
//
// jean parle à un serveur Crawl4AI (Chrome headless, endpoint /crawl) dont l'URL
// est configurée dans config.env (CRAWL4AI_URL). Quand le mode agent ET l'accès
// internet sont actifs ET que le serveur répond, l'IA dispose de 4 outils :
//
//   - web_search : recherche DuckDuckGo (via crawl), liste {title, url, snippet}.
//   - web_open   : récupère une URL (cache 10 min), renvoie SEULEMENT les métadonnées
//                  (nb de lignes, taille, plan des titres) — pas le contenu.
//   - web_read   : lit une plage de lignes d'une URL déjà ouverte (offset + limit).
//   - web_grep   : recherche regex dans une URL déjà ouverte, lignes + contexte.
//
// Workflow attendu : web_open(url) → web_read/web_grep. Même logique que pi.

// ─── configuration & état ───────────────────────────────────────────────────

// crawl4aiURL renvoie l'URL du serveur Crawl4AI (config.env CRAWL4AI_URL), sans
// slash final. Vide si non configuré.
func crawl4aiURL() string {
	u := strings.TrimSpace(ReadConfig()["CRAWL4AI_URL"])
	return strings.TrimRight(u, "/")
}

// internetEnabled : accès internet actif = drapeau .internet_enabled présent ET
// une URL Crawl4AI configurée. Même modèle que agentEnabled() (chat_agent.go).
func internetEnabled() bool {
	if crawl4aiURL() == "" {
		return false
	}
	_, err := os.Stat(internetFlag())
	return err == nil
}

func setInternetEnabled(on bool) error {
	_ = os.MkdirAll(JeanHome(), 0o755)
	if on {
		f, err := os.Create(internetFlag())
		if err != nil {
			return err
		}
		return f.Close()
	}
	if err := os.Remove(internetFlag()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// crawlReachable teste que le serveur Crawl4AI répond, avec un cache court (~30 s)
// pour ne pas ralentir chaque tour de chat. Un serveur configuré mais injoignable
// => les outils web ne sont pas proposés (« actif ET fonctionnel »).
var (
	reachMu   sync.Mutex
	reachOK   bool
	reachURL  string
	reachWhen time.Time
)

const reachTTL = 30 * time.Second

func crawlReachable() bool {
	base := crawl4aiURL()
	if base == "" {
		return false
	}
	reachMu.Lock()
	defer reachMu.Unlock()
	if base == reachURL && time.Since(reachWhen) < reachTTL {
		return reachOK
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ok := false
	// Crawl4AI expose /health ; on tolère aussi une simple réponse HTTP sur la racine.
	for _, path := range []string{"/health", "/"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode < 500 {
			ok = true
			break
		}
	}
	reachOK, reachURL, reachWhen = ok, base, time.Now()
	return ok
}

// ─── invocation crwl ────────────────────────────────────────────────────────

// autoDismissJS : dismisser conservateur de bandeaux cookies/consentement, exécuté
// dans la page. N'agit que sur des éléments qui RESSEMBLENT à une UI cookie
// (position fixed/sticky ou id/class cookie/consent/gdpr). Port direct de web.ts.
const autoDismissJS = `(() => { try {
  const TEXT = /^(accept all|accept|i accept|agree|i agree|got it|i understand|j'accepte|tout accepter|accepter|d'accord|allow all|allow|consent|continue|ok)$/i;
  const isOverlayish = (el) => {
    try {
      let cur = el;
      for (let i = 0; i < 6 && cur; i++) {
        const s = getComputedStyle(cur);
        if (s.position === 'fixed' || s.position === 'sticky') return true;
        const idcls = ((cur.id || '') + ' ' + (cur.className || '')).toLowerCase();
        if (/cookie|consent|gdpr|cmp|privacy/.test(idcls)) return true;
        cur = cur.parentElement;
      }
    } catch {}
    return false;
  };
  const candidates = document.querySelectorAll('button, [role="button"], input[type="button"], input[type="submit"]');
  let clicked = 0;
  for (const b of candidates) {
    if (clicked >= 2) break;
    const t = (b.innerText || b.value || b.getAttribute('aria-label') || '').trim();
    if (!t || !TEXT.test(t)) continue;
    if (b.offsetParent === null) continue;
    if (!isOverlayish(b)) continue;
    try { b.click(); clicked++; } catch {}
  }
  document.querySelectorAll('[id*="cookie" i],[id*="consent" i],[class*="cookie" i],[class*="consent" i],[id*="gdpr" i],[class*="gdpr" i],[id*="cmp" i],[class*="cmp" i]')
    .forEach(el => { try {
      const s = getComputedStyle(el);
      if (s.position === 'fixed' || s.position === 'sticky') el.remove();
    } catch {} });
  document.documentElement.style.overflow = 'auto';
  if (document.body) document.body.style.overflow = 'auto';
} catch {} })();`

type crwlOptions struct {
	jsCode        []string
	waitFor       string
	pageTimeoutMs int
	rawMarkdown   bool // true => préfère raw_markdown (recherche) ; false => fit_markdown
}

// runCrwl appelle POST {base}/crawl et renvoie le markdown extrait. Port de web.ts.
func runCrwl(target string, opts crwlOptions) (string, error) {
	base := crawl4aiURL()
	if base == "" {
		return "", fmt.Errorf("aucun serveur Crawl4AI configuré (CRAWL4AI_URL)")
	}
	params := map[string]any{}
	if len(opts.jsCode) > 0 {
		params["js_code"] = opts.jsCode
	}
	if opts.waitFor != "" {
		params["wait_for"] = opts.waitFor
	}
	if opts.pageTimeoutMs > 0 {
		params["page_timeout"] = opts.pageTimeoutMs
	}
	body := map[string]any{
		"urls": []string{target},
		"browser_config": map[string]any{
			"type":   "BrowserConfig",
			"params": map[string]any{"headless": true},
		},
		"crawler_config": map[string]any{
			"type":   "CrawlerRunConfig",
			"params": params,
		},
	}
	buf, _ := json.Marshal(body)

	timeout := 60 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/crawl", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Crawl4AI injoignable (%s): %v", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Crawl4AI HTTP %d: %s", resp.StatusCode, tailRunes(string(b), 300))
	}
	var data struct {
		Results []crwlResult `json:"results"`
	}
	// La réponse peut être soit {results:[...]}, soit un objet unique. On décode
	// d'abord la forme {results}, sinon on retombe sur un résultat unique.
	raw, _ := io.ReadAll(resp.Body)
	if jerr := json.Unmarshal(raw, &data); jerr != nil || len(data.Results) == 0 {
		var single crwlResult
		if json.Unmarshal(raw, &single) == nil {
			data.Results = []crwlResult{single}
		}
	}
	if len(data.Results) == 0 {
		return "", fmt.Errorf("Crawl4AI : réponse vide")
	}
	r := data.Results[0]
	if !r.Success {
		msg := r.ErrorMessage
		if msg == "" {
			msg = "échec du crawl"
		}
		return "", fmt.Errorf("Crawl4AI : %s", tailRunes(msg, 300))
	}
	return r.markdown(opts.rawMarkdown), nil
}

// crwlResult modélise un résultat Crawl4AI. markdown peut être une string ou un
// objet {raw_markdown, fit_markdown} — on gère les deux via json.RawMessage.
type crwlResult struct {
	Success      bool            `json:"success"`
	ErrorMessage string          `json:"error_message"`
	Markdown     json.RawMessage `json:"markdown"`
}

func (r crwlResult) markdown(preferRaw bool) string {
	if len(r.Markdown) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(r.Markdown, &s) == nil {
		return s
	}
	var obj struct {
		Raw string `json:"raw_markdown"`
		Fit string `json:"fit_markdown"`
	}
	if json.Unmarshal(r.Markdown, &obj) == nil {
		if preferRaw {
			if obj.Raw != "" {
				return obj.Raw
			}
			return obj.Fit
		}
		if obj.Fit != "" {
			return obj.Fit
		}
		return obj.Raw
	}
	return ""
}

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
	force         bool
	actions       []string
	dismissPopups bool
	waitFor       string
}

func cacheKeyFor(u string, opts fetchOptions) string {
	fp, _ := json.Marshal(map[string]any{"a": opts.actions, "d": opts.dismissPopups, "w": opts.waitFor})
	if string(fp) == `{"a":null,"d":true,"w":""}` || string(fp) == `{"a":[],"d":true,"w":""}` {
		return u
	}
	h := md5.Sum(fp)
	return u + "#" + hex.EncodeToString(h[:])[:8]
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
	key := cacheKeyFor(u, opts)
	pageCacheMu.Lock()
	cached := pageCache[key]
	pageCacheMu.Unlock()
	if !opts.force && cached != nil && time.Since(cached.fetchedAt) < pageCacheTTL {
		return cached, nil
	}

	jsCode := []string{}
	if opts.dismissPopups {
		jsCode = append(jsCode, autoDismissJS)
	}
	jsCode = append(jsCode, opts.actions...)
	pageTimeout := 0
	if opts.waitFor != "" || len(opts.actions) > 0 {
		pageTimeout = 45000
	}
	md, err := runCrwl(u, crwlOptions{jsCode: jsCode, waitFor: opts.waitFor, pageTimeoutMs: pageTimeout})
	if err != nil {
		return nil, err
	}
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

var (
	htmlTagRe   = regexp.MustCompile(`<[^>]+>`)
	wsRe        = regexp.MustCompile(`\s+`)
	numEntityRe = regexp.MustCompile(`&#(\d+);`)
	ddgHeadRe   = regexp.MustCompile(`^##\s+\[([^\]]+)\]\(([^)]+)\)\s*$`)
	ddgLinkRe   = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	urlishRe    = regexp.MustCompile(`^[\w.-]+\.[a-z]{2,}`)
)

func decodeHTMLEntities(s string) string {
	s = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`,
		"&#39;", "'", "&#x2F;", "/", "&nbsp;", " ",
	).Replace(s)
	return numEntityRe.ReplaceAllStringFunc(s, func(m string) string {
		var n int
		fmt.Sscanf(m, "&#%d;", &n)
		if n > 0 {
			return string(rune(n))
		}
		return m
	})
}

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

func duckduckgoSearch(query string, limit int) ([]searchResult, error) {
	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	md, err := runCrwl(searchURL, crwlOptions{rawMarkdown: true, pageTimeoutMs: 30000})
	if err != nil {
		return nil, err
	}
	if strings.Contains(md, "anomaly-modal") || strings.Contains(md, "anomaly.js") {
		return nil, fmt.Errorf("DuckDuckGo a renvoyé un défi anti-bot")
	}
	var results []searchResult
	lines := strings.Split(md, "\n")
	for i := 0; i < len(lines) && len(results) < limit; i++ {
		h := ddgHeadRe.FindStringSubmatch(lines[i])
		if h == nil {
			continue
		}
		title := strings.TrimSpace(strings.ReplaceAll(decodeHTMLEntities(h[1]), "**", ""))
		u := decodeUddg(h[2])
		if title == "" || u == "" || strings.Contains(u, "duckduckgo.com") {
			continue
		}
		snippet := ""
		for j := i + 1; j < i+6 && j < len(lines); j++ {
			ln := strings.TrimSpace(lines[j])
			if ln == "" {
				continue
			}
			for _, lm := range ddgLinkRe.FindAllStringSubmatch(ln, -1) {
				text := strings.TrimSpace(lm[1])
				if text == "" || strings.HasPrefix(text, "!") || urlishRe.MatchString(text) {
					continue
				}
				snippet = strings.TrimSpace(strings.ReplaceAll(decodeHTMLEntities(text), "**", ""))
				break
			}
			if snippet != "" {
				break
			}
		}
		dup := false
		for _, r := range results {
			if r.URL == u {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		results = append(results, searchResult{Title: title, URL: u, Snippet: snippet})
	}
	return results, nil
}

// ─── définitions d'outils (schémas OpenAI, comme llm_client.go) ────────────────────
