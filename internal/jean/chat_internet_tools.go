// chat_internet_tools.go — les 4 outils web exposés au modèle (web_search,
// web_open, web_read, web_grep) + la sous-commande `jean internet`.
package jean

import (
	"fmt"
	"regexp"
	"strings"
)

func webSearchTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name: "web_search",
		Description: "Recherche sur le web via DuckDuckGo. Renvoie une liste classée de {title, url, snippet}. " +
			"À utiliser quand l'utilisateur pose une question sans URL, cherche un outil/une bibliothèque, " +
			"ou demande une information récente. À enchaîner avec web_open + web_read sur le meilleur résultat.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Requête (langage naturel ou mots-clés)"},
				"limit": map[string]any{"type": "integer", "description": "Nb max de résultats (défaut 8, max 20)"},
			},
			"required": []string{"query"},
		},
	}}
}

func webOpenTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name: "web_open",
		Description: "Récupère une URL et renvoie SEULEMENT les métadonnées (taille, nb de lignes, plan des titres). " +
			"Ne renvoie PAS le contenu. Toujours appeler ceci d'abord avant de lire. Résultat en cache 10 min " +
			"— les web_read / web_grep suivants le réutilisent.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":     map[string]any{"type": "string", "description": "URL complète à récupérer"},
				"refresh": map[string]any{"type": "boolean", "description": "Ignore le cache et re-fetch. Défaut false."},
				"actions": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "Snippets JS à exécuter sur la page AVANT extraction (déplier des sections, cliquer 'voir plus', etc.)."},
				"dismiss_popups": map[string]any{"type": "boolean", "description": "Ferme auto les bandeaux cookies/overlays. Défaut true."},
				"wait_for":       map[string]any{"type": "string", "description": "Sélecteur CSS ou expr JS à attendre après les actions."},
			},
			"required": []string{"url"},
		},
	}}
}

func webReadTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name: "web_read",
		Description: "Lit une plage de lignes d'une URL déjà ouverte avec web_open. Coût en tokens prévisible. " +
			"Lignes 1-indexées, préfixées par leur numéro.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":    map[string]any{"type": "string", "description": "URL précédemment ouverte avec web_open"},
				"offset": map[string]any{"type": "integer", "description": "Ligne de départ (1-indexée, défaut 1)"},
				"limit":  map[string]any{"type": "integer", "description": "Nb de lignes (défaut 80, max 500)"},
			},
			"required": []string{"url"},
		},
	}}
}

func webGrepTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name: "web_grep",
		Description: "Recherche regex dans une URL déjà ouverte avec web_open. Renvoie les lignes correspondantes " +
			"avec contexte et numéros. Idéal quand la page est longue et qu'on connaît un mot-clé.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":         map[string]any{"type": "string", "description": "URL précédemment ouverte avec web_open"},
				"pattern":     map[string]any{"type": "string", "description": "Motif regex (insensible à la casse)"},
				"context":     map[string]any{"type": "integer", "description": "Lignes de contexte autour de chaque match. Défaut 2."},
				"max_matches": map[string]any{"type": "integer", "description": "Plafond de matches renvoyés. Défaut 30."},
			},
			"required": []string{"url", "pattern"},
		},
	}}
}

// ─── exécution des outils (appelée par le dispatch de llm_client.go) ───────────────

func toolWebSearch(args map[string]any) string {
	query, _ := args["query"].(string)
	limit := 8
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 20 {
		limit = 20
	}
	results, err := duckduckgoSearch(query, limit)
	if err != nil {
		return "❌ Recherche échouée : " + err.Error()
	}
	if len(results) == 0 {
		return fmt.Sprintf("Aucun résultat pour « %s »", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Recherche : %s\n%d résultat(s) DuckDuckGo\n\n", query, len(results))
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet)
	}
	return strings.TrimRight(b.String(), "\n")
}

func toolWebOpen(args map[string]any) string {
	u, _ := args["url"].(string)
	opts := fetchOptions{dismissPopups: true}
	if v, ok := args["refresh"].(bool); ok {
		opts.force = v
	}
	if v, ok := args["dismiss_popups"].(bool); ok {
		opts.dismissPopups = v
	}
	if v, ok := args["wait_for"].(string); ok {
		opts.waitFor = v
	}
	if arr, ok := args["actions"].([]any); ok {
		for _, a := range arr {
			if s, ok := a.(string); ok {
				opts.actions = append(opts.actions, s)
			}
		}
	}
	entry, err := getPage(u, opts)
	if err != nil {
		return "❌ " + err.Error()
	}
	total := len(entry.lines)
	chars := total
	for _, l := range entry.lines {
		chars += len(l)
	}
	return fmt.Sprintf("# Ouvert : %s\nTotal : %d lignes, %s (%d caractères)\nEn cache 10 min. Utilise web_read ou web_grep pour lire.\n\n## Plan (n° de ligne des titres)\n```\n%s\n```",
		entry.url, total, formatBytes(chars), chars, extractOutline(entry.lines))
}

func toolWebRead(args map[string]any) string {
	u, _ := args["url"].(string)
	entry := findCached(u)
	if entry == nil {
		return fmt.Sprintf("❌ Page absente du cache. Appelle d'abord web_open(\"%s\").", u)
	}
	total := len(entry.lines)
	offset := 1
	if v, ok := args["offset"].(float64); ok {
		offset = int(v)
	}
	if offset < 1 {
		offset = 1
	}
	limit := 80
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	start := offset - 1
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	slice := entry.lines[start:end]
	remaining := total - end
	tail := " (fin de page)"
	if remaining > 0 {
		tail = fmt.Sprintf(" (%d de plus en dessous)", remaining)
	}
	return fmt.Sprintf("# %s\nLignes %d–%d sur %d%s\n\n```\n%s\n```",
		entry.url, offset, end, total, tail, formatLines(slice, offset))
}

func toolWebGrep(args map[string]any) string {
	u, _ := args["url"].(string)
	pattern, _ := args["pattern"].(string)
	entry := findCached(u)
	if entry == nil {
		return fmt.Sprintf("❌ Page absente du cache. Appelle d'abord web_open(\"%s\").", u)
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return "❌ Regex invalide : " + err.Error()
	}
	ctx := 2
	if v, ok := args["context"].(float64); ok {
		ctx = int(v)
	}
	if ctx < 0 {
		ctx = 0
	}
	maxMatches := 30
	if v, ok := args["max_matches"].(float64); ok {
		maxMatches = int(v)
	}
	if maxMatches < 1 {
		maxMatches = 1
	}
	lines := entry.lines
	var matchIdx []int
	for i := 0; i < len(lines) && len(matchIdx) < maxMatches; i++ {
		if re.MatchString(lines[i]) {
			matchIdx = append(matchIdx, i)
		}
	}
	if len(matchIdx) == 0 {
		return fmt.Sprintf("# %s\nAucun match pour /%s/i", entry.url, pattern)
	}
	// Fusionne les fenêtres de contexte qui se chevauchent.
	type rng struct{ s, e int }
	var ranges []rng
	for _, i := range matchIdx {
		s := i - ctx
		if s < 0 {
			s = 0
		}
		e := i + ctx
		if e > len(lines)-1 {
			e = len(lines) - 1
		}
		if n := len(ranges); n > 0 && s <= ranges[n-1].e+1 {
			if e > ranges[n-1].e {
				ranges[n-1].e = e
			}
		} else {
			ranges = append(ranges, rng{s, e})
		}
	}
	var blocks []string
	for _, r := range ranges {
		blocks = append(blocks, "```\n"+formatLines(lines[r.s:r.e+1], r.s+1)+"\n```")
	}
	capped := ""
	if len(matchIdx) == maxMatches {
		capped = fmt.Sprintf(" (plafonné à %d)", maxMatches)
	}
	return fmt.Sprintf("# %s\n%d match(es) pour /%s/i%s\n\n%s",
		entry.url, len(matchIdx), pattern, capped, strings.Join(blocks, "\n\n---\n\n"))
}

// ─── CLI : jean internet [on|off|status|url <url>] ──────────────────────────

func cmdInternet(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "on":
		if crawl4aiURL() == "" {
			return fmt.Errorf("configure d'abord l'URL : jean internet url <url>")
		}
		if err := setInternetEnabled(true); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " accès internet activé — l'IA dispose de web_search/web_open/web_read/web_grep (si le mode agent est actif)")
	case "off":
		if err := setInternetEnabled(false); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " accès internet désactivé")
	case "url":
		if len(args) < 2 {
			return fmt.Errorf("usage: jean internet url <url>  (ex: http://localhost:11235)")
		}
		u := strings.TrimRight(strings.TrimSpace(args[1]), "/")
		if err := SetConfigKey("CRAWL4AI_URL", u); err != nil {
			return err
		}
		reachMu.Lock()
		reachURL = "" // invalide le cache de reachability
		reachMu.Unlock()
		fmt.Printf("%s serveur Crawl4AI : %s\n", green("[ok]"), bold(u))
	case "", "status", "list":
		state := dim("off")
		if internetEnabled() {
			state = green("on")
		}
		fmt.Printf("%s  état: %s\n", cyan("Accès internet"), state)
		u := crawl4aiURL()
		if u == "" {
			fmt.Printf("  serveur : %s — configure : jean internet url <url>\n", dim("(non configuré)"))
			return nil
		}
		reach := red("injoignable")
		if crawlReachable() {
			reach = green("joignable")
		}
		fmt.Printf("  serveur : %s (%s)\n", bold(u), reach)
		fmt.Printf("  outils  : web_search, web_open, web_read, web_grep\n")
	default:
		return fmt.Errorf("usage: jean internet [on|off|status|url <url>]")
	}
	return nil
}
