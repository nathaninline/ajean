package ajean

// computer_use.go — le « computer use » : donner à Jean la capacité d'agir sur
// une interface graphique de la machine hôte. Étape 1 : le WEB, via un Chromium
// piloté en CDP (computer_cdp.go). Le modèle lit l'arbre d'accessibilité (des
// éléments interactifs NUMÉROTÉS, façon set-of-marks) et agit par numéro —
// aucune vision requise, ce qui marche même avec de petits modèles texte à
// faible contexte. browser_screenshot n'est proposé que si la vision est active, pour
// les cas sans a11y (canvas, jeux).
//
// C'est un axe de capacité indépendant, sous le mode agent (comme l'accès
// internet) : un outil qui pilote un navigateur exécute des actions réelles, donc
// même niveau de confiance que bash. Interrupteur : clé bkState "computer".

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func computerUseEnabled() bool { return getBool(bkState, "computer") }

func setComputerUseEnabled(on bool) error { return putBool(bkState, "computer", on) }

// cuMaxOutput borne ce qu'un appel d'outil computer use injecte dans le contexte
// (aligné sur le web et le shell : 8000).
const cuMaxOutput = 8000

func capCUOutput(s string) string {
	if r := []rune(s); len(r) > cuMaxOutput {
		return string(r[:cuMaxOutput]) + "\n…[tronqué]"
	}
	return s
}

// ─── schémas d'outils (annoncés au modèle) ───────────────────────────────────

func cuOpenTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "browser_open",
		Description: "Open a URL in the controlled browser and return the page's interactive elements, each numbered [n]. Start here. Pass 'back' or 'forward' instead of a URL to move through history.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"url": map[string]any{"type": "string", "description": "URL to open, or 'back' / 'forward'"}},
			"required":   []string{"url"},
		},
	}}
}

func cuSnapshotTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "browser_snapshot",
		Description: "Re-read the current page's interactive elements (numbered). Use it after the page changes to get fresh [n] refs.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}}
}

func cuClickTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "browser_click",
		Description: "Click the element with the given number from the latest snapshot. Returns the page state afterwards.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"ref": map[string]any{"type": "integer", "description": "Element number [n]"}},
			"required":   []string{"ref"},
		},
	}}
}

func cuTypeTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "browser_type",
		Description: "Type text into a field. ALWAYS give 'ref' (the field's number) so it targets the right field and REPLACES its current content — no need to browser_click the field first. For a dropdown/select (role 'combobox'), pass the option's text and it picks that option directly (don't click it open). Without a ref it types into whatever is focused, which is unreliable.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string", "description": "Text to type"},
				"ref":  map[string]any{"type": "integer", "description": "Optional: field number [n] to focus first"},
			},
			"required": []string{"text"},
		},
	}}
}

func cuKeyTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "browser_key",
		Description: "Press a special key: enter, tab, backspace, escape, delete, up, down, left, right, home, end, pageup, pagedown.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"key": map[string]any{"type": "string", "description": "Key name"}},
			"required":   []string{"key"},
		},
	}}
}

func cuFindTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "browser_find",
		Description: "Find interactive elements ANYWHERE on the page (not just the visible part) whose label contains the text, and scroll the first into view. Use it to locate an off-screen link or button (e.g. 'créer un compte', 'panier') instead of scrolling blindly or guessing URLs.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"text": map[string]any{"type": "string", "description": "Text to look for in element labels"}},
			"required":   []string{"text"},
		},
	}}
}

func cuScrollTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "browser_scroll",
		Description: "Scroll the page (up, down, left, right), then return the now-visible interactive elements.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"direction": map[string]any{"type": "string", "description": "up, down, left or right"}},
			"required":   []string{"direction"},
		},
	}}
}

func cuClickXYTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "browser_click_xy",
		Description: fmt.Sprintf("Click at pixel coordinates (x from the left, y from the top) read off the latest browser_screenshot — %dx%d pixels, 1:1 with the page, with a coordinate grid drawn every 100px to read x/y precisely. Use it ONLY for something the numbered elements can't reach: a cookie/consent banner inside a cross-origin iframe, a canvas, a map. Otherwise always prefer browser_click(ref).", cuViewW, cuViewH),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"x": map[string]any{"type": "number", "description": "pixels from the left edge"},
				"y": map[string]any{"type": "number", "description": "pixels from the top edge"},
			},
			"required": []string{"x", "y"},
		},
	}}
}

func cuScreenshotTool() Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name:        "browser_screenshot",
		Description: "Capture the current page as an image. It is shown to the user immediately AND saved to your working folder; the result gives the filename to link with Markdown if they want the file. Use it to see a visual layout the numbered elements don't convey, or when the user asks for a screenshot — never hunt the disk for it.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}}
}

// computerUseTools est la liste annoncée au modèle quand le computer use est
// actif. browser_screenshot n'y figure que si la vision est réellement active.
func computerUseTools() []Tool {
	tools := []Tool{cuOpenTool(), cuSnapshotTool(), cuFindTool(), cuClickTool(), cuTypeTool(), cuKeyTool(), cuScrollTool()}
	// Vision active : la capture d'écran ET le clic par coordonnées (qui n'a de sens
	// que si le modèle peut lire l'image pour viser).
	if visionEnabled() {
		tools = append(tools, cuScreenshotTool(), cuClickXYTool())
	}
	return tools
}

// cuPromptLine est la consigne d'usage ajoutée au prompt système quand le
// computer use est actif (l'ordre d'appel que les schémas isolés ne disent pas).
func cuPromptLine() string {
	line := "\nComputer use (a browser you control on this machine): browser_open a URL, then act on the NUMBERED elements — browser_click(ref), browser_type(text, ref), browser_key, browser_scroll. Each action returns the fresh numbered elements; after the page changes, trust the new numbers, not old ones. The list shows only what's in view: to reach an off-screen link or button (e.g. 'créer un compte'), use browser_find(text) to jump straight to it rather than scrolling blindly or, worse, guessing URLs — inventing paths mostly 404s. If a page shows an error (404, 'introuvable'), browser_open goes 'back' or try another link. Prefer the numbered elements over screenshots."
	if visionEnabled() {
		line += " Use browser_screenshot when the numbers aren't enough (canvas, visual layout). If a button is visible but NOT in the numbered list (e.g. a cookie banner inside an iframe), browser_screenshot then browser_click_xy(x,y) at its pixel position."
	}
	line += "\n"
	return line
}

// ─── exécution (appelée par le dispatch de llm_client.go) ────────────────────

func toolCUOpen(args map[string]any) string {
	url, _ := args["url"].(string)
	if strings.TrimSpace(url) == "" {
		return "[erreur] url manquante"
	}
	s, err := cdpGet()
	if err != nil {
		return "[erreur] " + err.Error()
	}
	if err := s.navigate(url); err != nil {
		return "[erreur] navigation : " + err.Error()
	}
	snap, err := s.snapshotDedup()
	if err != nil {
		return "[erreur] " + err.Error()
	}
	return snap
}

func toolCUSnapshot() string {
	s, err := cdpGet()
	if err != nil {
		return "[erreur] " + err.Error()
	}
	snap, err := s.snapshotDedup()
	if err != nil {
		return "[erreur] " + err.Error()
	}
	return snap
}

func toolCUFind(args map[string]any) string {
	text, _ := args["text"].(string)
	if strings.TrimSpace(text) == "" {
		return "[erreur] texte de recherche manquant"
	}
	s, err := cdpGet()
	if err != nil {
		return "[erreur] " + err.Error()
	}
	res, err := s.find(text)
	if err != nil {
		return "[erreur] " + err.Error()
	}
	return res
}

func toolCUClick(args map[string]any) string {
	ref, ok := intArg(args, "ref")
	if !ok {
		return "[erreur] ref manquant (numéro de l'élément)"
	}
	s, err := cdpGet()
	if err != nil {
		return "[erreur] " + err.Error()
	}
	if err := s.clickRef(ref); err != nil {
		return "[erreur] " + err.Error()
	}
	snap, _ := s.snapshotDedup()
	return fmt.Sprintf("[ok] cliqué [%d]\n\n%s", ref, snap)
}

func toolCUType(args map[string]any) string {
	text, _ := args["text"].(string)
	s, err := cdpGet()
	if err != nil {
		return "[erreur] " + err.Error()
	}
	if ref, ok := intArg(args, "ref"); ok {
		if err := s.typeInto(ref, text); err != nil {
			return "[erreur] " + err.Error()
		}
	} else if err := s.typeText(text); err != nil {
		return "[erreur] " + err.Error()
	}
	snap, _ := s.snapshotDedup()
	return fmt.Sprintf("[ok] saisi « %s »\n\n%s", text, snap)
}

func toolCUKey(args map[string]any) string {
	key, _ := args["key"].(string)
	s, err := cdpGet()
	if err != nil {
		return "[erreur] " + err.Error()
	}
	if err := s.pressKey(key); err != nil {
		return "[erreur] " + err.Error()
	}
	snap, _ := s.snapshotDedup()
	return fmt.Sprintf("[ok] touche %s\n\n%s", key, snap)
}

func toolCUClickXY(args map[string]any) string {
	x, okx := floatArg(args, "x")
	y, oky := floatArg(args, "y")
	if !okx || !oky {
		return "[erreur] coordonnées x et y requises (en pixels du screenshot)"
	}
	s, err := cdpGet()
	if err != nil {
		return "[erreur] " + err.Error()
	}
	if err := s.clickXY(x, y); err != nil {
		return "[erreur] " + err.Error()
	}
	snap, _ := s.snapshotDedup()
	return fmt.Sprintf("[ok] clic en (%.0f, %.0f)\n\n%s", x, y, snap)
}

func toolCUScroll(args map[string]any) string {
	dir, _ := args["direction"].(string)
	s, err := cdpGet()
	if err != nil {
		return "[erreur] " + err.Error()
	}
	if err := s.scroll(dir); err != nil {
		return "[erreur] " + err.Error()
	}
	snap, _ := s.snapshotDedup()
	return snap
}

// toolCUScreenshot renvoie (accusé texte, partie image_url) comme see_image :
// l'image est réinjectée dans un message utilisateur multimodal par l'appelant.
//
// La capture PLEINE RÉSOLUTION est aussi ENREGISTRÉE dans le dossier de travail :
// sans ça, le modèle qui reçoit « envoie-moi une capture » partait chercher un
// PNG inexistant sur tout le disque (vécu sur le test de navigation, ~12 bash find pour
// rien). Avec le chemin en retour, il n'a plus qu'à le donner en lien Markdown.
func toolCUScreenshot() (string, map[string]any) {
	if !visionEnabled() {
		return "[erreur] la vision n'est pas active sur ce modèle — impossible de capturer l'écran", nil
	}
	s, err := cdpGet()
	if err != nil {
		return "[erreur] " + err.Error(), nil
	}
	png, err := s.screenshot()
	if err != nil {
		return "[erreur] " + err.Error(), nil
	}
	// Enregistre le PNG plein format dans le workspace (chemin RELATIF renvoyé au
	// modèle : un lien Markdown [capture](nom.png) est servi par /api/chat/file).
	saved := ""
	name := "capture-" + time.Now().Format("20060102-150405") + ".png"
	if ws := agentWorkspace(); ws != "" {
		if err := os.WriteFile(filepath.Join(ws, name), png, 0o644); err == nil {
			saved = name
		}
	}
	// L'image du MODÈLE porte une GRILLE de coordonnées (lignes graduées tous les
	// 100 px) pour qu'il lise x/y avec précision ; celle sauvegardée pour l'utilisateur
	// (plus haut, `png`) reste PROPRE, sans grille.
	b, mime := prepareImageForModel(overlayGrid(png), "image/png")
	// ⚠️ TOI (le modèle) vois l'image ci-dessous, mais l'utilisateur NE la voit PAS
	// automatiquement dans le chat. Pour la lui afficher, il faut l'insérer en IMAGE
	// Markdown ![...](fichier) — un simple lien [texte](fichier) ne fait qu'un bouton
	// de téléchargement, pas d'aperçu.
	msg := fmt.Sprintf("[ok] capture prise (tu la vois ci-dessous, %dx%d px, 1:1 avec la page). Une GRILLE de repères y est surimprimée (lignes + chiffres tous les 100 px) POUR TOI seulement, afin de viser précisément — le fichier enregistré, lui, est PROPRE (sans grille), donc l'aperçu ![capture] montré à l'utilisateur est net. Si un élément visible n'est PAS dans la liste numérotée (bandeau cookies en iframe, canvas), clique-le du premier coup avec browser_click_xy(x,y), coordonnées lues sur la grille.", cuViewW, cuViewH)
	if saved != "" {
		msg += " Enregistrée sous « " + saved + " ». Pour l'AFFICHER à l'utilisateur dans le chat, insère-la en IMAGE Markdown : ![capture](" + saved + "). Un simple lien [capture](" + saved + ") ne donne qu'un bouton de téléchargement, sans aperçu. Ne cherche pas de fichier sur le disque, c'est celui-ci."
	}
	return msg, imageURLPart(b, mime)
}

// intArg extrait un entier d'un argument d'outil (le JSON décode les nombres en
// float64).
func intArg(args map[string]any, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

// floatArg extrait un nombre d'un argument d'outil (coordonnées de clic).
func floatArg(args map[string]any, key string) (float64, bool) {
	switch v := args[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	}
	return 0, false
}

// ─── CLI : ajean computer [on|off|status] ────────────────────────────────────

func cmdComputer(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "on":
		if err := setComputerUseEnabled(true); err != nil {
			return err
		}
		msg := " contrôle du navigateur activé — l'IA pilote un navigateur (browser_open/browser_click/browser_type/…) si le mode agent est actif"
		if chromePath() == "" {
			msg += "\n" + red("[!]") + " aucun Chrome/Chromium/Edge détecté — installe-le ou définis AJEAN_CHROME=<chemin>"
		}
		fmt.Println(green("[ok]") + msg)
	case "off":
		if err := setComputerUseEnabled(false); err != nil {
			return err
		}
		cdpShutdown()
		fmt.Println(green("[ok]") + " contrôle du navigateur désactivé")
	case "", "status":
		state := dim("off")
		if computerUseEnabled() {
			state = green("on")
		}
		fmt.Printf("%s  état: %s\n", cyan("Contrôle du navigateur"), state)
		bin := chromePath()
		if bin == "" {
			fmt.Printf("  navigateur : %s — installe Chrome/Chromium/Edge ou AJEAN_CHROME=<chemin>\n", red("introuvable"))
		} else {
			fmt.Printf("  navigateur : %s\n", bold(bin))
		}
		fmt.Printf("  outils     : browser_open, browser_snapshot, browser_find, browser_click, browser_type, browser_key, browser_scroll")
		if visionEnabled() {
			fmt.Printf(", browser_screenshot, browser_click_xy")
		}
		fmt.Println()
	default:
		return fmt.Errorf("usage: ajean computer [on|off|status]")
	}
	return nil
}
