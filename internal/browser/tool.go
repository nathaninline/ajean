package browser

import (
	"slices"
	"strconv"
	"strings"

	"ajean/internal/tool"
)

// actions : ce que l'outil sait faire, dans l'ordre où le modèle les lit.
var actions = []string{"open", "read", "click", "type", "select", "key", "scroll", "back", "close"}

// Tools : l'outil « browser », le seul du paquet.
func Tools() []tool.Def {
	return []tool.Def{{
		Name: "browser",
		Description: "A real web browser you drive, visible to the user. Every action returns the page view: title, URL, the visible text, and each interactive element on its own line with a number, e.g. \"[12] button: Send\", \"[14] input (text, placeholder \\\"Search\\\")\". Use that number as ref. " +
			"action=open (url), read (the current page again, e.g. after waiting), click (ref), type (ref, text; replaces the field content; submit=true presses Enter), select (ref, text = option label of a <select>), key (text = Enter, Escape, Tab, ArrowDown, PageDown...), scroll (direction; down continues where the view stopped), back, close. " +
			"Numbers are only valid for the last view: after any change, use the numbers of the new view. Public http(s) sites only: no local files, no localhost. Downloads go to downloads/ in your workspace. No file upload.",
		Params: tool.Params(map[string]any{
			"action":    tool.Enum("what to do", actions...),
			"url":       tool.String("open: the address, e.g. https://example.com"),
			"ref":       tool.Integer("click, type, select: the element number from the last page view"),
			"text":      tool.String("type: the text to enter; select: the option to choose; key: the key name"),
			"submit":    tool.Boolean("type: press Enter after typing"),
			"direction": tool.Enum("scroll: default down", "up", "down"),
		}, "action"),
		Run: run,
	}}
}

// run exécute une action du modèle et rend la nouvelle vue de la page, ou
// l'erreur.
func run(c tool.Call) string {
	action := strings.ToLower(c.Str("action"))
	ref := refArg(c)
	needRef := action == "click" || action == "type" || action == "select"
	switch {
	case !slices.Contains(actions, action):
		return tool.Err("action inconnue : %s", strings.Join(actions, ", "))
	case action == "close":
		Shutdown()
		return tool.OK("navigateur fermé")
	case needRef && ref <= 0:
		return tool.Err("ref manquant : le numéro entre crochets de l'élément, pris dans la dernière vue")
	case action == "select" && c.Str("text") == "":
		return tool.Err("text manquant : le libellé de l'option à choisir")
	}
	mu.Lock()
	defer mu.Unlock()
	b, err := get()
	if err == nil {
		err = b.syncTarget()
	}
	if err != nil {
		return tool.Result("", err)
	}
	switch action {
	case "open":
		err = b.open(c.Str("url"))
	case "click":
		err = b.click(ref)
	case "type":
		err = b.typeText(ref, c.Text("text"), c.Bool("submit"))
	case "select":
		err = b.selectOption(ref, c.Str("text"))
	case "key":
		err = b.press(c.Str("text"))
	case "scroll":
		err = b.scroll(strings.ToLower(c.Str("direction")))
	case "back":
		err = b.back()
	}
	var view string
	if err == nil {
		view, err = b.view()
	}
	return report(view, err, b.takeNotes())
}

// refArg lit le numéro d'élément. Il arrive en nombre, parfois en texte, et
// parfois recopié avec ses crochets (« [12] »).
func refArg(c tool.Call) int {
	if n, ok := c.Int("ref"); ok {
		return n
	}
	n, _ := strconv.Atoi(strings.Trim(c.Str("ref"), "[] "))
	return n
}

// report assemble le résultat rendu au modèle. Les notes précèdent la vue,
// qu'elles expliquent ; mais elles suivent une erreur, dont le préfixe doit
// rester en tête pour être reconnu.
func report(view string, err error, notes []string) string {
	var sb strings.Builder
	for _, n := range notes {
		sb.WriteString("[note] " + n + "\n")
	}
	if err != nil {
		return strings.TrimSpace(tool.Result("", err) + "\n" + sb.String())
	}
	return sb.String() + view
}
