package ajean

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// Préférences d'apparence de l'UI web (thème, mode d'affichage). Stockées côté
// serveur — sur la machine AJEAN — pour être partagées entre tous les appareils
// qui pilotent la même instance (téléphone, laptop, accès distant ajean.link).
// Avant ça, elles ne vivaient qu'en localStorage, donc par navigateur/appareil.

var webPrefsMu sync.Mutex

// webPrefsAllowed liste les clés de préférence acceptées et, pour chacune, les
// valeurs valides. On ne stocke que ce qui est connu (pas de champ libre).
var webPrefsAllowed = map[string]map[string]bool{
	"theme":          {"light": true, "dark": true},
	"hide_reasoning": {"0": true, "1": true},
	"hide_tools":     {"0": true, "1": true},
	"fold_tools":     {"0": true, "1": true},
	"hide_side":      {"0": true, "1": true},
	"hide_stats":     {"0": true, "1": true},
	"enter_newline":  {"0": true, "1": true},
	"lang":           {"en": true, "fr": true},
}

// loadWebPrefs lit les préférences enregistrées (map vide si aucune).
func loadWebPrefs() map[string]string {
	out := map[string]string{}
	for k, v := range allKV(bkPrefs) {
		out[k] = v
	}
	return out
}

// saveWebPrefs fusionne les valeurs valides de `in` puis enregistre le tout.
func saveWebPrefs(in map[string]string) (map[string]string, error) {
	webPrefsMu.Lock()
	defer webPrefsMu.Unlock()
	prefs := loadWebPrefs()
	for k, v := range in {
		allowed, ok := webPrefsAllowed[k]
		if !ok {
			continue
		}
		v = strings.ToLower(strings.TrimSpace(v))
		if !allowed[v] {
			continue
		}
		prefs[k] = v
	}
	return prefs, replaceKV(bkPrefs, prefs)
}

// handleWebPrefs expose les préférences d'apparence partagées entre appareils.
//
//	GET  → {ok, prefs:{theme, hide_*, fold_*}}
//	POST {theme?, hide_*?, fold_*?} → fusionne puis renvoie {ok, prefs}
func handleWebPrefs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		prefs, err := saveWebPrefs(req)
		if err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true, "prefs": prefs})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "prefs": loadWebPrefs()})
}
