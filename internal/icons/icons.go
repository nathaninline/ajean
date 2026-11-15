// Package icons fixe le petit jeu d'icônes et la forme des couleurs dont
// AJEAN habille ce qu'il crée (apps, tâches). Il choisit un NOM dans cette
// liste et une couleur ; le dessin de chaque icône vit dans l'interface, qui
// doit connaître exactement les mêmes noms.
package icons

import (
	"regexp"
	"slices"
	"strings"
)

// Default : l'icône de ce qui n'en a pas (ou en a une inconnue).
const Default = "app"

// Names : les icônes disponibles.
var Names = []string{
	"app", "wallet", "chart", "calendar", "checklist", "note", "timer", "clock", "bell", "heart",
	"fitness", "health", "food", "book", "school", "music", "camera", "photo", "map", "globe",
	"plane", "car", "home", "cart", "gift", "star", "target", "game", "calculator", "weather",
	"leaf", "bolt", "code", "tools", "mail", "people", "briefcase", "search", "eye", "sparkles",
}

var colorRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// Icon : le nom d'icône retenu pour ce que donne le modèle (Default s'il est
// vide ou inconnu : un emoji, par exemple).
func Icon(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if slices.Contains(Names, name) {
		return name
	}
	return Default
}

// Color : la couleur retenue, sous la forme #rrggbb ("" si elle est absente ou
// mal écrite : l'interface prend alors sa teinte neutre).
func Color(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	if len(c) == 4 && c[0] == '#' { // #abc → #aabbcc
		c = "#" + strings.Repeat(c[1:2], 2) + strings.Repeat(c[2:3], 2) + strings.Repeat(c[3:4], 2)
	}
	if colorRe.MatchString(c) {
		return c
	}
	return ""
}

// Help : la phrase donnée au modèle dans la description de ses outils.
func Help() string {
	return "icon: one of " + strings.Join(Names, ", ") + ". color: a hex color you choose to fit the subject (e.g. #2f7d5b), readable behind a white icon: avoid very light colors."
}
