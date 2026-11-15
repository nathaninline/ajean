package apps

import (
	"encoding/json"
	"fmt"
	"strings"

	"ajean/internal/config"
	"ajean/internal/icons"
	"ajean/internal/tool"
)

// description : le mode d'emploi du modèle. Tout ce qu'il sait d'une app et de
// son bac à sable vient de ce texte.
var description = "Apps: small web applications (HTML + CSS + JavaScript) you build for the user, who opens them from the Applications screen. Use it when the user asks for an app, a tracker, a dashboard, a calculator, a game…\n" +
	"Actions: list; read (current files, declared tools, pending errors); save (create with name, description, icon, color, html, css, js; or pass id to store a new version: omitted fields and files are kept); edit (replace `old`, which must match exactly once, by `new` in one file: prefer it to resending a large file); errors (JavaScript errors reported from the user's screen; clear=true once fixed). " +
	"Every save or edit creates a new version; older ones are kept and the user can roll back. Only the user can delete an app.\n" +
	"Look: give each app an icon and a color of its own. " + icons.Help() + "\n" +
	"What you write: html is a BODY FRAGMENT (no <html>, <head>, <script src> or <link>); css is the stylesheet; js is one classic script, run once the body is parsed (no modules, no import/export). Each file is limited to 200 KB.\n" +
	"Sandbox: the app runs in an isolated frame with no network and no origin. Everything must be inline: no CDN, no external script, stylesheet, font or image (inline SVG, emoji and data: URLs are fine). " +
	"These do NOT work: fetch, XMLHttpRequest, WebSocket, localStorage, sessionStorage, cookies, indexedDB, alert/confirm/prompt, eval, <form> submission (the submit event never fires: handle button clicks and the Enter key) and external links. Build dialogs and messages inside the page.\n" +
	"Bridge, the app's only way out. Every function returns a Promise (use await with try/catch):\n" +
	"- ajean.storage.get(key) (null when absent), ajean.storage.set(key, value), ajean.storage.remove(key), ajean.storage.keys(): the app's own persistent JSON data (200 keys, 1 MB per value, 5 MB in total). Use it instead of localStorage: load at start-up, save after each change.\n" +
	"- ajean.tools.run(name, args): runs one of your own tools (the `tools` tool) and resolves with its output as a string (JSON.parse it when the tool returns an object). Only the tools listed in the app's `tools` parameter are allowed.\n" +
	"- ajean.ai.ask(prompt): one answer from the model, as a string; no tools, no memory, takes seconds: show a loading state.\n" +
	"Web data or workspace files: first write and test a tool with `tools` (it has http.get and ws.read/ws.write), then declare its name in `tools` when saving the app, and call it with ajean.tools.run.\n" +
	"Quality bar: the app must look polished and deliberate, be comfortable on a phone and on a desktop (responsive layout, large touch targets), follow light and dark themes with prefers-color-scheme, and handle empty states, loading and errors visibly in the page.\n" +
	"After a save or edit, read the warnings in the result and fix them, and give the user the link exactly as the result shows it. JavaScript errors that happen on the user's screen are recorded: when the user reports a problem, call errors, fix with edit, then errors with clear=true."

// Tools : le seul outil par lequel le modèle crée et modifie les apps.
func Tools() []tool.Def {
	return []tool.Def{{
		Name:        "app",
		Description: description,
		Params: tool.Params(map[string]any{
			"action":      tool.Enum("What to do", "list", "read", "save", "edit", "errors"),
			"id":          tool.String("App id (read, edit, errors; save: the app to update, omit to create a new one)"),
			"name":        tool.String("save: the app's name, as shown to the user"),
			"description": tool.String("save: one line, what the app is for"),
			"icon":        tool.String("save: the app's icon, a name from the list above"),
			"color":       tool.String("save: the app's color, #rrggbb"),
			"html":        tool.String("save: the body fragment"),
			"css":         tool.String("save: the stylesheet"),
			"js":          tool.String("save: the script"),
			"tools": map[string]any{
				"type": "array", "items": map[string]any{"type": "string"},
				"description": "save: names of your tools the app may run with ajean.tools.run (replaces the previous list)",
			},
			"note":  tool.String("save, edit: one line, what changed in this version"),
			"file":  tool.Enum("edit: the file to change", "html", "css", "js"),
			"old":   tool.String("edit: the exact text to replace (must appear exactly once)"),
			"new":   tool.String("edit: the replacement text"),
			"clear": tool.Boolean("errors: forget the pending errors after returning them"),
		}, "action"),
		Run: dispatch,
	}}
}

func dispatch(c tool.Call) string {
	id := c.Str("id")
	switch strings.ToLower(c.Str("action")) {
	case "list", "":
		return listAll()
	case "read":
		return read(id)
	case "save":
		return saved(save(id, change{
			name: oneLine(c.Str("name")), description: oneLine(c.Str("description")), icon: c.Str("icon"), color: c.Str("color"),
			html: unix(c.Text("html")), css: unix(c.Text("css")), js: unix(c.Text("js")),
			tools: names(c.Args["tools"]), note: note(c),
		}))
	case "edit":
		return saved(edit(id, strings.ToLower(c.Str("file")), unix(c.Text("old")), unix(c.Text("new")), note(c)))
	case "errors":
		return pending(id, c.Bool("clear"))
	}
	return tool.Err("action inconnue : list, read, save, edit ou errors")
}

// unix : les fins de ligne d'un texte donné par le modèle, ramenées à « \n »
// pour que le passage d'un edit retrouve le fichier d'un save.
func unix(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// note : ce qui a changé, en une ligne bornée.
func note(c tool.Call) string {
	n := []rune(oneLine(c.Str("note")))
	if len(n) > maxNote {
		n = append(n[:maxNote], '…')
	}
	return string(n)
}

// names lit la liste d'outils déclarés : un tableau, ou un texte (tableau JSON
// ou noms séparés par des virgules), comme les modèles l'envoient parfois.
// nil veut dire « rien de dit », une liste vide « plus aucun outil ».
func names(v any) []string {
	var raw []any
	switch x := v.(type) {
	case []any:
		raw = x
	case string:
		if strings.TrimSpace(x) == "" {
			return nil
		}
		if json.Unmarshal([]byte(x), &raw) != nil {
			for _, name := range strings.FieldsFunc(x, func(r rune) bool { return r == ',' || r == ' ' }) {
				raw = append(raw, name)
			}
		}
	default:
		return nil
	}
	out := []string{}
	seen := map[string]bool{}
	for _, e := range raw {
		if name, ok := e.(string); ok && strings.TrimSpace(name) != "" && !seen[strings.TrimSpace(name)] {
			seen[strings.TrimSpace(name)] = true
			out = append(out, strings.TrimSpace(name))
		}
	}
	return out
}

func listAll() string {
	all := List()
	if len(all) == 0 {
		return "(aucune app pour l'instant : action=save pour en créer une)"
	}
	lines := make([]string, len(all))
	for i, a := range all {
		lines[i] = fmt.Sprintf("- %s : %s (version %d%s)", a.ID, a.Name, a.Version, errorCount(a.Errors))
		if a.Description != "" {
			lines[i] += " — " + a.Description
		}
	}
	return strings.Join(lines, "\n")
}

func errorCount(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d erreur(s) JavaScript en attente", n)
}

// read : tout ce qu'il faut au modèle pour reprendre une app.
func read(id string) string {
	mu.Lock()
	defer mu.Unlock()
	m, err := load(id)
	if err != nil {
		return tool.Err("%v", err)
	}
	s, err := readSource(id, m.Version)
	if err != nil {
		return tool.Err("%v", err)
	}
	app := m.app(id)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (id %s, version %d%s, icon %s, color %s)\n%s\ntools: %s\n", app.Name, id, app.Version, errorCount(app.Errors), app.Icon, app.Color, app.Description, strings.Join(app.Tools, ", "))
	for _, f := range []struct{ name, text string }{{"index.html", s.html}, {"style.css", s.css}, {"app.js", s.js}} {
		fmt.Fprintf(&b, "\n===== %s =====\n%s\n", f.name, f.text)
	}
	return b.String()
}

// saved : le résultat d'un enregistrement. Il dicte au modèle le lien à
// donner, que l'interface transforme en bouton d'ouverture.
func saved(app App, warnings []string, err error) string {
	if err != nil {
		return tool.Err("%v", err)
	}
	label := strings.NewReplacer("[", "(", "]", ")").Replace(app.Name)
	out := tool.OK("app « %s » enregistrée (id %s, version %d). Écris ce lien Markdown tel quel dans ta réponse, l'interface en fait un bouton qui ouvre l'app : [%s](app:%s)", app.Name, app.ID, app.Version, label, app.ID)
	if len(warnings) > 0 {
		out += "\nAvertissements, à corriger avec action=edit :\n- " + strings.Join(warnings, "\n- ")
	}
	if app.Errors > 0 {
		out += fmt.Sprintf("\n%d erreur(s) JavaScript en attente sur cette app : action=errors pour les lire, clear=true une fois corrigées.", app.Errors)
	}
	return out
}

// pending : les erreurs JavaScript remontées par l'app chez l'utilisateur.
func pending(id string, clear bool) string {
	app, list, err := takeErrors(id, clear)
	if err != nil {
		return tool.Err("%v", err)
	}
	if len(list) == 0 {
		return fmt.Sprintf("(aucune erreur JavaScript en attente pour « %s »)", app.Name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d erreur(s) JavaScript remontée(s) par « %s » (version courante : %d), la plus ancienne d'abord :\n", len(list), app.Name, app.Version)
	for _, e := range list {
		fmt.Fprintf(&b, "- %s, version %d : %s\n", e.At.In(config.Location()).Format("2006-01-02 15:04"), e.Version, e.Message)
	}
	if clear {
		b.WriteString("(effacées)")
	} else {
		b.WriteString("(une fois corrigées : action=errors avec clear=true)")
	}
	return b.String()
}
