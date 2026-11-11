// chat_internet_tools.go — les 4 outils web exposés au modèle (web_search,
// web_open, web_read, web_grep) + la sous-commande `ajean internet`.
package ajean

import (
	"fmt"
	"regexp"
	"strings"
)

func webSearchTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "web_search",
		Description: "Search the web and return ranked results.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Query"},
				"limit": map[string]any{"type": "integer", "description": "Default 8, max 20"},
			},
			"required": []string{"query"},
		},
	}}
}

// webOpenTool : le schéma ne déclare que ce que le moteur sait faire.
//
// Le moteur intégré n'a pas de DOM vivant : actions / wait_for / dismiss_popups
// seraient acceptés puis ignorés en silence. Les déclarer reviendrait
// à mentir au modèle — il croirait pouvoir déplier une section ou fermer un
// bandeau, constaterait que rien ne change, et réessaierait en boucle (le failure
// mode classique, cf. le garde-fou anti-boucle de chat_agent.go). On ne déclare
// donc que ce que le moteur sait réellement faire, et on annonce la limite du JS
// dans la description pour que le modèle change de source au lieu d'insister.
func webOpenTool() Tool {
	desc := "Fetch a URL and return its metadata and outline, not the content; call this before web_read or web_grep."
	// La limite JavaScript vit dans la description du paramètre url (pas dans la
	// phrase de l'outil) : c'est un avertissement de comportement, pas un exemple,
	// et il évite que le modèle réessaie en boucle une page rendue côté client.
	urlDesc := "Full URL (this engine reads served HTML only, no JavaScript: a client-rendered page comes back empty, so switch source instead of retrying)"
	props := map[string]any{
		"url":     map[string]any{"type": "string", "description": urlDesc},
		"refresh": map[string]any{"type": "boolean", "description": "Bypass the 10-minute cache (default false)"},
		"focus":   map[string]any{"type": "string", "description": "What you look for: returns the matching passages instead of the outline"},
	}
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "web_open",
		Description: desc,
		Parameters: map[string]any{
			"type":       "object",
			"properties": props,
			"required":   []string{"url"},
		},
	}}
}

func webReadTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "web_read",
		Description: "Read a range of lines from a URL already opened with web_open.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":    map[string]any{"type": "string", "description": "URL opened with web_open"},
				"offset": map[string]any{"type": "integer", "description": "Start line (default 1)"},
				"limit":  map[string]any{"type": "integer", "description": "Default 80, max 500"},
			},
			"required": []string{"url"},
		},
	}}
}

func webGrepTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "web_grep",
		Description: "Find lines matching a regex in a URL already opened with web_open.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":         map[string]any{"type": "string", "description": "URL opened with web_open"},
				"pattern":     map[string]any{"type": "string", "description": "Regex (case-insensitive)"},
				"context":     map[string]any{"type": "integer", "description": "Context lines (default 2)"},
				"max_matches": map[string]any{"type": "integer", "description": "Cap (default 30)"},
			},
			"required": []string{"url", "pattern"},
		},
	}}
}

// ─── exécution des outils (appelée par le dispatch de llm_client.go) ───────────────

// webMaxOutput borne ce qu'UN appel d'outil web injecte dans le contexte, comme
// toolMaxOutput (8000) pour le shell. (La mémoire et MCP, eux, renvoient tout au
// modèle et laissent l'UI charger l'aperçu.) Sans ce
// plafond, un `web_read(limit=500)` sur une page dense pouvait pousser 25 000
// caractères d'un coup : la fenêtre partait en fumée en pleine recherche, ce qui
// déclenchait des compactages en cascade au milieu du raisonnement.
const webMaxOutput = 8000

// capWebOutput tronque en gardant le DÉBUT (contrairement au shell, où c'est la
// fin qui porte l'info) et dit au modèle comment lire la suite proprement.
func capWebOutput(s string) string {
	if r := []rune(s); len(r) > webMaxOutput {
		return string(r[:webMaxOutput]) +
			"\n…[tronqué : réponse trop longue. Relis par tranches avec web_read(offset, limit) ou cible avec web_grep.]"
	}
	return s
}

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
	// DuckDuckGo d'abord, puis d'autres moteurs sans clé s'il bloque ou répond
	// trop peu (web_search_engines.go).
	results, from, err := multiSearch(query, limit)
	if err != nil {
		return "❌ Recherche échouée : " + err.Error()
	}
	if len(results) == 0 {
		return fmt.Sprintf("Aucun résultat pour « %s » : cherche avec d'autres mots, ou moins de mots, ou réponds avec ce que tu sais", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Recherche : %s\n%d résultat(s) (%s)\n\n", query, len(results), strings.Join(from, ", "))
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet)
	}
	return strings.TrimRight(b.String(), "\n")
}

func toolWebOpen(args map[string]any) string {
	u, _ := args["url"].(string)
	opts := fetchOptions{}
	if v, ok := args["refresh"].(bool); ok {
		opts.force = v
	}
	entry, err := getPage(u, opts)
	if err != nil {
		return "❌ " + err.Error()
	}
	// Avec un focus : les passages qui répondent, au lieu du plan (web_focus.go).
	focus, _ := args["focus"].(string)
	nothing := ""
	if focus = strings.TrimSpace(focus); focus != "" {
		if out := focusedPage(entry, focus); out != "" {
			return capWebOutput(out)
		}
		nothing = fmt.Sprintf("Aucun passage ne contient les mots de « %s » : voici le plan.\n", focus)
	}
	total := len(entry.lines)
	chars := total
	for _, l := range entry.lines {
		chars += len(l)
	}
	return fmt.Sprintf("# Ouvert : %s\n%sTotal : %d lignes, %s (%d caractères)\nEn cache 10 min. Utilise web_read ou web_grep pour lire.\n\n## Plan (n° de ligne des titres)\n```\n%s\n```",
		entry.url, nothing, total, formatBytes(chars), chars, extractOutline(entry.lines))
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

// ─── CLI : ajean internet [on|off|status] ───────────────────────────────────

func cmdInternet(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "on", "off":
		fmt.Println(yellow("[info]") + " l'accès internet est toujours actif depuis la 0.20 : il n'y a plus d'interrupteur")
	case "", "status", "list":
		state := dim("off")
		if internetEnabled() {
			state = green("on")
		}
		fmt.Printf("%s  état: %s\n", cyan("Accès internet"), state)
		fmt.Printf("  moteur  : %s (aucune installation, pas de rendu JavaScript)\n", bold("intégré"))
		fmt.Printf("  outils  : web_search, web_open, web_read, web_grep\n")
	default:
		return fmt.Errorf("usage: ajean internet [on|off|status]")
	}
	return nil
}
