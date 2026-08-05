package ajean

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// Moteur web INTÉGRÉ (pur Go) — alternative à Crawl4AI, sans rien à installer.
//
// Crawl4AI = Chromium headless piloté par Playwright : il exécute le JS de la
// page avant d'en extraire le markdown. C'est puissant mais ça demande à
// l'utilisateur de faire tourner un serveur Python/Docker — rédhibitoire pour
// un utilisateur non technique.
//
// Ce moteur-ci remplace UNIQUEMENT l'étape « URL → markdown » (runCrwl) par du
// Go pur :
//
//	http.Get → readability (port Go de Readability de Firefox, qui vire nav,
//	pubs, pieds de page) → html-to-markdown
//
// Ce qu'on perd : le rendu JavaScript. Une page 100 % React sans SSR ressort
// vide, et les options js_code / wait_for / actions n'ont plus de sens (pas de
// DOM vivant). En pratique, les sources que l'IA lit — docs, articles, blogs,
// Wikipédia, GitHub, forums — sont servies en HTML et passent très bien.
//
// Le choix se fait par la clé de config WEB_ENGINE ("go" ou "crawl4ai"). Voir
// webEngine(). Crawl4AI reste entièrement supporté.

// ─── sélection du moteur ────────────────────────────────────────────────────

const (
	engineGo    = "go"
	engineCrawl = "crawl4ai"
)

// webEngine renvoie le moteur web actif : engineGo ou engineCrawl.
//
// Par défaut on prend le moteur intégré. L'exception est l'installation qui a
// DÉJÀ une URL Crawl4AI configurée sans avoir jamais choisi de moteur : elle
// tournait sur Crawl4AI, on ne change pas son comportement dans son dos.
func webEngine() string {
	switch strings.ToLower(strings.TrimSpace(ReadConfig()["WEB_ENGINE"])) {
	case engineGo:
		return engineGo
	case engineCrawl:
		return engineCrawl
	}
	if crawl4aiURL() != "" {
		return engineCrawl
	}
	return engineGo
}

// setWebEngine enregistre le moteur web choisi et invalide le cache de
// reachability (l'état affiché dépend du moteur).
func setWebEngine(engine string) error {
	if engine != engineGo && engine != engineCrawl {
		return fmt.Errorf("moteur web inconnu : %q", engine)
	}
	if err := SetConfigKey("WEB_ENGINE", engine); err != nil {
		return err
	}
	reachMu.Lock()
	reachURL = ""
	reachMu.Unlock()
	return nil
}

// ─── récupération HTTP ──────────────────────────────────────────────────────

// Un vrai User-Agent : beaucoup de sites servent une page dégradée (ou un 403)
// aux clients qui s'annoncent comme des robots.
const goFetchUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

const (
	goFetchTimeout  = 30 * time.Second
	goFetchMaxBytes = 8 << 20 // 8 Mo : au-delà ce n'est plus une page à lire
)

// goHTTPClient : client dédié au moteur web. Timeout global, plafond de
// redirections, et un cookie jar — indispensable pour les sites qui posent un
// cookie de session puis redirigent (murs de consentement typiques). Le jar est
// en mémoire : il disparaît à l'arrêt du process, on ne persiste rien.
// On ne touche pas à http.DefaultClient (utilisé ailleurs).
var goHTTPClient = &http.Client{
	Timeout: goFetchTimeout,
	Jar:     newCookieJar(),
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("trop de redirections")
		}
		return nil
	},
}

func newCookieJar() http.CookieJar {
	j, err := cookiejar.New(nil)
	if err != nil {
		return nil // un client sans jar reste fonctionnel
	}
	return j
}

// consentCookies : cookies qui marquent le bandeau de consentement comme DÉJÀ
// TRAITÉ, afin d'atteindre la page derrière le mur.
//
// ⚠ On signale un REFUS, jamais une acceptation : ces valeurs disent « pas de
// suivi, bandeau fermé ». Le but est de lire la page, pas d'ouvrir des traceurs
// au nom de l'utilisateur. Les en-têtes DNT et Sec-GPC (refus de vente/partage,
// juridiquement reconnu en Californie et respecté par une partie des sites)
// vont dans le même sens.
//
// Envoyés à tout le monde : un cookie inconnu d'un site est simplement ignoré.
var consentCookies = []string{
	"gdpr=0",
	"gdpr_consent=0",
	"cookieconsent_status=deny",
	"OptanonAlertBoxClosed=2024-01-01T00:00:00.000Z",
	"SOCS=CAESHAgBEhJnd3NfMjAyNDAxMDEtMF9SQzEaAmZyIAEaBgiA_LyaBg", // Google : « refuser tout »
}

// goFetch effectue le GET et renvoie le corps (plafonné), le Content-Type et
// l'URL finale après redirections.
func goFetch(ctx context.Context, target string) (body []byte, contentType, finalURL string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("User-Agent", goFetchUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.7")
	req.Header.Set("Accept-Language", "fr-FR,fr;q=0.9,en;q=0.8")
	// Signaux de refus du pistage (voir consentCookies).
	req.Header.Set("DNT", "1")
	req.Header.Set("Sec-GPC", "1")
	for _, c := range consentCookies {
		req.Header.Add("Cookie", c)
	}
	// Pas d'Accept-Encoding manuel : le transport gère gzip tout seul.

	resp, err := goHTTPClient.Do(req)
	if err != nil {
		return nil, "", "", fmt.Errorf("échec de la requête : %v", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(io.LimitReader(resp.Body, goFetchMaxBytes))
	if err != nil {
		return nil, "", "", fmt.Errorf("lecture interrompue : %v", err)
	}
	if resp.StatusCode >= 400 {
		return nil, "", "", fmt.Errorf("HTTP %d sur %s", resp.StatusCode, target)
	}
	return b, resp.Header.Get("Content-Type"), resp.Request.URL.String(), nil
}

// goParseHTML décode le corps selon le charset annoncé (ou détecté) puis le
// parse en arbre DOM.
func goParseHTML(body []byte, contentType string) (*html.Node, error) {
	r, err := charset.NewReader(strings.NewReader(string(body)), contentType)
	if err != nil {
		// Charset inconnu : on tente en l'état plutôt que d'abandonner.
		r = strings.NewReader(string(body))
	}
	return html.Parse(r)
}

// ─── URL → markdown ─────────────────────────────────────────────────────────

// goFetchMarkdown est l'équivalent pur Go de runCrwl : elle récupère une URL et
// en renvoie le contenu en markdown.
//
// Les contenus non-HTML (texte brut, markdown, JSON…) sont renvoyés tels quels :
// c'est le cas des README GitHub, vers lesquels normalizeCrawlURL redirige.
func goFetchMarkdown(target string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), goFetchTimeout)
	defer cancel()

	body, contentType, finalURL, err := goFetch(ctx, target)
	if err != nil {
		return "", err
	}

	ct := strings.ToLower(contentType)
	isHTML := strings.Contains(ct, "html") || strings.Contains(ct, "xml")
	if ct == "" {
		// Certains serveurs n'annoncent rien : on devine sur le contenu.
		isHTML = strings.Contains(strings.ToLower(string(body[:min(len(body), 1024)])), "<html")
	}
	if !isHTML {
		if strings.Contains(ct, "text/") || strings.Contains(ct, "json") || ct == "" {
			return string(body), nil
		}
		return "", fmt.Errorf("type de contenu non lisible : %s", contentType)
	}

	doc, err := goParseHTML(body, contentType)
	if err != nil {
		return "", fmt.Errorf("HTML illisible : %v", err)
	}

	base, _ := url.Parse(finalURL)
	md, title := goArticleMarkdown(doc, base)
	if strings.TrimSpace(md) == "" {
		// Readability n'a rien trouvé d'« article » (page d'accueil, listing,
		// appli…). On convertit la page entière plutôt que de renvoyer vide.
		md, err = goConvert(doc, finalURL)
		if err != nil {
			return "", err
		}
	}
	if title != "" && !strings.HasPrefix(strings.TrimSpace(md), "# ") {
		md = "# " + title + "\n\n" + md
	}

	// Diagnostic du rendu client. Beaucoup de HTML (scripts, bundles, templates)
	// mais presque aucun texte extractible = coquille de SPA. Le rapport compte
	// plus que la taille absolue : une vraie petite page (example.com : ~1 Ko de
	// HTML, ~170 caractères de texte) n'est PAS suspecte, alors qu'une page de
	// 300 Ko qui ne rend que 80 caractères l'est clairement.
	//
	// Sans ce message le modèle relançait la même URL en boucle (cf. le garde-fou
	// anti-boucle de chat_agent.go) : on lui dit explicitement de changer de source.
	if len(strings.TrimSpace(md)) < jsShellTextMax && len(body) > jsShellHTMLMin {
		return "", fmt.Errorf("page récupérée (%s de HTML) mais seulement %d caractères de texte : "+
			"elle est très probablement rendue en JavaScript, ce que le moteur web intégré n'exécute pas. "+
			"Ne réessaie PAS cette URL — cherche une autre source (documentation officielle, dépôt du projet, "+
			"article qui en parle)", formatBytes(len(body)), len(strings.TrimSpace(md)))
	}
	return md, nil
}

// Seuils du diagnostic « rendu JavaScript » (voir goFetchMarkdown).
const (
	jsShellTextMax = 200   // texte extrait en dessous duquel il n'y a rien à lire
	jsShellHTMLMin = 15000 // HTML au-dessus duquel « rien à lire » devient suspect
)

// goArticleMarkdown applique Readability puis convertit le résultat. Renvoie du
// markdown vide (sans erreur) si la page n'a pas de contenu d'article isolable.
func goArticleMarkdown(doc *html.Node, base *url.URL) (md, title string) {
	// Readability mute l'arbre qu'on lui donne ; on lui passe une copie pour
	// pouvoir retomber sur le document complet en cas d'échec.
	clone, err := goCloneDoc(doc)
	if err != nil {
		return "", ""
	}
	art, err := readability.FromDocument(clone, base)
	if err != nil || art.Node == nil {
		return "", ""
	}
	out, err := goConvert(art.Node, baseString(base))
	if err != nil {
		return "", ""
	}
	return out, strings.TrimSpace(art.Title())
}

// goConvert rend un nœud HTML en markdown, en réécrivant les liens et images
// relatifs en URLs absolues (sinon l'IA ne peut pas les rouvrir).
func goConvert(node *html.Node, baseURL string) (string, error) {
	opts := []converter.ConvertOptionFunc{}
	if baseURL != "" {
		opts = append(opts, converter.WithDomain(baseURL))
	}
	b, err := htmltomarkdown.ConvertNode(node, opts...)
	if err != nil {
		return "", fmt.Errorf("conversion markdown : %v", err)
	}
	return string(b), nil
}

// goCloneDoc duplique un document HTML en le re-parsant depuis son rendu.
func goCloneDoc(doc *html.Node) (*html.Node, error) {
	var sb strings.Builder
	if err := html.Render(&sb, doc); err != nil {
		return nil, err
	}
	return html.Parse(strings.NewReader(sb.String()))
}

func baseString(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// ─── recherche DuckDuckGo (parsing HTML direct) ─────────────────────────────

// goSearch interroge le point d'entrée HTML de DuckDuckGo et lit les résultats
// directement dans le DOM.
//
// Le chemin Crawl4AI passe par une conversion en markdown avant de reparser le
// texte à coups de regex (duckduckgoSearch) ; ici on lit la structure réelle
// (.result__a, .result__snippet), ce qui est nettement plus robuste.
func goSearch(query string, limit int) ([]searchResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), goFetchTimeout)
	defer cancel()

	target := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	body, contentType, _, err := goFetch(ctx, target)
	if err != nil {
		return nil, err
	}
	if strings.Contains(string(body), "anomaly-modal") || strings.Contains(string(body), "anomaly.js") {
		return nil, fmt.Errorf("DuckDuckGo a renvoyé un défi anti-bot")
	}
	doc, err := goParseHTML(body, contentType)
	if err != nil {
		return nil, fmt.Errorf("HTML illisible : %v", err)
	}

	var results []searchResult
	seen := map[string]bool{}
	for _, block := range goFindByClass(doc, "result") {
		if len(results) >= limit {
			break
		}
		link := goFirstByClass(block, "result__a")
		if link == nil {
			continue
		}
		href := decodeUddg(goAttr(link, "href"))
		title := goText(link)
		if title == "" || href == "" || strings.Contains(href, "duckduckgo.com") || seen[href] {
			continue
		}
		snippet := ""
		if s := goFirstByClass(block, "result__snippet"); s != nil {
			snippet = goText(s)
		}
		seen[href] = true
		results = append(results, searchResult{Title: title, URL: href, Snippet: snippet})
	}
	return results, nil
}

// ─── petits helpers DOM (x/net/html, sans sélecteur CSS) ────────────────────

func goHasClass(n *html.Node, class string) bool {
	if n.Type != html.ElementNode {
		return false
	}
	for _, f := range strings.Fields(goAttr(n, "class")) {
		if f == class {
			return true
		}
	}
	return false
}

func goAttr(n *html.Node, name string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// goFindByClass renvoie tous les nœuds portant la classe, sans descendre dans
// un nœud déjà retenu (les résultats DDG ne s'imbriquent pas).
func goFindByClass(root *html.Node, class string) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if goHasClass(n, class) {
			out = append(out, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

func goFirstByClass(root *html.Node, class string) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if goHasClass(n, class) {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

// goText concatène le texte d'un sous-arbre, espaces normalisés.
func goText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(wsRe.ReplaceAllString(sb.String(), " "))
}
