package jean

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Préférences d'apparence de l'UI web (thème, mode d'affichage). Stockées côté
// serveur — sur la machine jean — pour être partagées entre tous les appareils
// qui pilotent la même instance (téléphone, laptop, accès distant ajean.link).
// Avant ça, elles ne vivaient qu'en localStorage, donc par navigateur/appareil.

// webPrefsPath est le fichier JSON des préférences d'apparence.
func webPrefsPath() string { return filepath.Join(JeanHome(), "webprefs.json") }

var webPrefsMu sync.Mutex

// webPrefsAllowed liste les clés de préférence acceptées et, pour chacune, les
// valeurs valides. On ne stocke que ce qui est connu (pas de champ libre).
var webPrefsAllowed = map[string]map[string]bool{
	// Un seul thème, deux variantes. Les anciennes valeurs (ajean, gpt-dark…)
	// ne sont plus acceptées : le client les normalise en clair/sombre au
	// chargement puis réenregistre la valeur normalisée.
	"theme":          {"light": true, "dark": true},
	"hide_reasoning": {"0": true, "1": true},
	"hide_tools":     {"0": true, "1": true},
	"fold_tools":     {"0": true, "1": true},
	"hide_side":      {"0": true, "1": true},
}

// loadWebPrefs lit les préférences enregistrées (map vide si absent/illisible).
func loadWebPrefs() map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(webPrefsPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

// saveWebPrefs fusionne les valeurs valides de `in` dans le fichier et l'écrit.
func saveWebPrefs(in map[string]string) (map[string]string, error) {
	webPrefsMu.Lock()
	defer webPrefsMu.Unlock()
	prefs := loadWebPrefs()
	// Purge les clés/valeurs devenues invalides (anciens thèmes, réglage de
	// police retiré) : sans ça, un fichier écrit par une version précédente
	// renverrait indéfiniment des valeurs que le client ne sait plus traiter.
	for k, v := range prefs {
		if allowed, ok := webPrefsAllowed[k]; !ok || !allowed[v] {
			delete(prefs, k)
		}
	}
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
	b, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return prefs, err
	}
	if err := os.WriteFile(webPrefsPath(), b, 0o600); err != nil {
		return prefs, err
	}
	return prefs, nil
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
