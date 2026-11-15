package browser

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const navTimeout = 30 * time.Second // Page.navigate : jusqu'à la réponse du site

var (
	schemeRe   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
	hostPortRe = regexp.MustCompile(`^[^/]+:\d+(/|$)`)
)

// address complète une adresse donnée par le modèle : « exemple.com » et
// « exemple.com:8080 » n'ont pas de schéma, on leur donne https. « file:… » en
// a un : il est gardé tel quel, pour être refusé ensuite.
func address(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("url manquante")
	}
	if !strings.Contains(raw, "://") && (!schemeRe.MatchString(raw) || hostPortRe.MatchString(raw)) {
		raw = "https://" + raw
	}
	return raw, nil
}

func (b *instance) open(raw string) error {
	url, err := address(raw)
	if err != nil {
		return err
	}
	if err := checkURL(url); err != nil {
		return fmt.Errorf("adresse refusée (%s) : %v", clip(url, 200), err)
	}
	var r struct{ ErrorText string }
	if err := b.conn.call(b.session, "Page.navigate", map[string]any{"url": url}, &r, navTimeout); err != nil {
		return err
	}
	// ERR_ABORTED : l'adresse était un téléchargement, pas une page.
	if r.ErrorText != "" && r.ErrorText != "net::ERR_ABORTED" {
		return errors.New("chargement impossible : " + r.ErrorText)
	}
	return nil
}

// click passe par la souris quand c'est possible : un vrai clic déclenche tout
// ce qu'un visiteur déclencherait (survol, focus, gestionnaires de la page).
func (b *instance) click(ref int) error {
	p, err := b.act(clickJS, ref)
	if err != nil || !p.Mouse {
		return err
	}
	for _, typ := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
		ev := map[string]any{"type": typ, "x": p.X, "y": p.Y}
		if typ != "mouseMoved" {
			ev["button"], ev["clickCount"] = "left", 1
		}
		if err := b.command("Input.dispatchMouseEvent", ev, nil); err != nil {
			return err
		}
	}
	return nil
}

func (b *instance) typeText(ref int, text string, submit bool) error {
	_, err := b.act(focusJS, ref)
	// Le contenu est sélectionné : le texte le remplace ; un texte vide l'efface.
	if err == nil && text == "" {
		err = b.press("Backspace")
	} else if err == nil {
		err = b.command("Input.insertText", map[string]any{"text": text}, nil)
	}
	if err == nil && submit {
		err = b.press("Enter")
	}
	return err
}

func (b *instance) selectOption(ref int, label string) error {
	_, err := b.act(selectJS, ref, label)
	return err
}

// key : une touche du clavier telle que CDP l'attend.
type key struct {
	name string // nom DOM (KeyboardEvent.key)
	code string // touche physique (KeyboardEvent.code)
	vk   int    // code virtuel Windows
	text string // caractère produit, s'il y en a un
}

// keys : les touches acceptées, par leur nom en minuscules.
var keys = map[string]key{
	"enter": {"Enter", "Enter", 13, "\r"}, "return": {"Enter", "Enter", 13, "\r"},
	"escape": {"Escape", "Escape", 27, ""}, "esc": {"Escape", "Escape", 27, ""},
	"tab": {"Tab", "Tab", 9, ""}, "backspace": {"Backspace", "Backspace", 8, ""},
	"delete": {"Delete", "Delete", 46, ""}, "space": {" ", "Space", 32, " "},
	"arrowup": {"ArrowUp", "ArrowUp", 38, ""}, "arrowdown": {"ArrowDown", "ArrowDown", 40, ""},
	"arrowleft": {"ArrowLeft", "ArrowLeft", 37, ""}, "arrowright": {"ArrowRight", "ArrowRight", 39, ""},
	"pageup": {"PageUp", "PageUp", 33, ""}, "pagedown": {"PageDown", "PageDown", 34, ""},
	"home": {"Home", "Home", 36, ""}, "end": {"End", "End", 35, ""},
}

// findKey accepte les écritures courantes d'un nom de touche (« Page Down », « ENTER »).
func findKey(name string) (key, error) {
	k, ok := keys[strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", ""))]
	if !ok {
		return key{}, errors.New("touche inconnue : Enter, Escape, Tab, Backspace, Delete, Space, ArrowUp, ArrowDown, ArrowLeft, ArrowRight, PageUp, PageDown, Home ou End")
	}
	return k, nil
}

func (b *instance) press(name string) error {
	k, err := findKey(name)
	if err != nil {
		return err
	}
	ev := map[string]any{"type": "rawKeyDown", "key": k.name, "code": k.code, "windowsVirtualKeyCode": k.vk, "nativeVirtualKeyCode": k.vk}
	if k.text != "" { // une touche qui produit un caractère (Entrée valide un formulaire)
		ev["type"], ev["text"] = "keyDown", k.text
	}
	if err := b.command("Input.dispatchKeyEvent", ev, nil); err != nil {
		return err
	}
	delete(ev, "text")
	ev["type"] = "keyUp"
	return b.command("Input.dispatchKeyEvent", ev, nil)
}

// scroll fait défiler d'un écran ; sans direction, vers le bas.
func (b *instance) scroll(direction string) error {
	if direction != "" && direction != "up" && direction != "down" {
		return errors.New("direction : up ou down")
	}
	p, err := b.act(scrollJS, direction != "up")
	if err != nil || p.Moved {
		return err
	}
	// La page elle-même ne défile pas : la molette atteint un cadre défilant interne.
	return b.command("Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": p.X, "y": p.Y, "deltaX": 0, "deltaY": p.DY}, nil)
}

func (b *instance) back() error {
	var h struct {
		CurrentIndex int
		Entries      []struct{ ID int }
	}
	if err := b.command("Page.getNavigationHistory", nil, &h); err != nil {
		return err
	}
	if h.CurrentIndex <= 0 || h.CurrentIndex >= len(h.Entries) {
		return errors.New("pas de page précédente")
	}
	return b.command("Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[h.CurrentIndex-1].ID}, nil)
}
