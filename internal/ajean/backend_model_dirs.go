package ajean

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Les .gguf vivent historiquement dans JEAN_HOME, mais un modèle de 40 Go tient
// rarement sur le disque système : on autorise des dossiers supplémentaires
// (disque dur externe, second SSD, montage réseau…). La liste est persistée
// dans $JEAN_HOME/model_dirs.json — PAS dans config.env, que le changement de
// preset réécrit — et peut aussi venir de $JEAN_MODEL_DIRS.
func modelDirsPath() string { return filepath.Join(AjeanHome(), "model_dirs.json") }

// modelDirsListSep sépare les dossiers dans $JEAN_MODEL_DIRS (':' sous Unix,
// ';' sous Windows, comme le PATH).
func modelDirsListSep() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}

// extraModelDirs renvoie les dossiers déclarés par l'utilisateur (fichier puis
// variable d'environnement), nettoyés et dédoublonnés, JEAN_HOME exclu.
func extraModelDirs() []string {
	var raw []string
	if b, err := os.ReadFile(modelDirsPath()); err == nil {
		var dirs []string
		if json.Unmarshal(b, &dirs) == nil {
			raw = append(raw, dirs...)
		}
	}
	if v := strings.TrimSpace(os.Getenv("JEAN_MODEL_DIRS")); v != "" {
		raw = append(raw, strings.Split(v, modelDirsListSep())...)
	}
	return dedupDirs(raw, AjeanHome())
}

// dedupDirs nettoie une liste de chemins : vides retirés, chemins absolus
// normalisés, doublons et `skip` écartés (comparaison insensible à la casse
// sous Windows, comme le système de fichiers).
func dedupDirs(list []string, skip string) []string {
	seen := map[string]bool{normDir(skip): true}
	out := []string{}
	for _, d := range list {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		k := normDir(abs)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, abs)
	}
	return out
}

// normDir met un chemin sous forme comparable (séparateurs unifiés, casse
// neutralisée sous Windows, slash final retiré).
func normDir(p string) string {
	// Clean d'abord (il réintroduit le séparateur natif), puis on unifie en '/'.
	p = strings.ReplaceAll(filepath.Clean(strings.TrimSpace(p)), "\\", "/")
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// modelDirs renvoie tous les dossiers où chercher un .gguf : JEAN_HOME d'abord
// (il reste la destination des téléchargements), puis les dossiers ajoutés.
func modelDirs() []string {
	return append([]string{AjeanHome()}, extraModelDirs()...)
}

// saveExtraModelDirs écrit la liste des dossiers supplémentaires sur disque.
func saveExtraModelDirs(dirs []string) error {
	clean := dedupDirs(dirs, AjeanHome())
	b, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(AjeanHome(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(modelDirsPath(), append(b, '\n'), 0o644)
}

// pathWithin dit si le fichier p est dans le dossier dir (ou un sous-dossier).
func pathWithin(dir, p string) bool {
	d, f := normDir(dir), normDir(p)
	return f == d || strings.HasPrefix(f, d+"/")
}

// baseName renvoie le nom de fichier d'un chemin en acceptant les DEUX
// séparateurs : un config.env écrit sous Windows (C:\models\x.gguf) peut être
// relu par un jean Linux, où filepath.Base ne coupe pas sur '\'.
func baseName(p string) string {
	p = strings.TrimRight(strings.TrimSpace(p), `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// resolveModelPath transforme une valeur MODEL= (nom de fichier OU chemin
// absolu) en chemin absolu vers un .gguf. Un chemin absolu n'est accepté que
// s'il est dans un des dossiers déclarés — sinon l'UI web deviendrait un moyen
// de lire/supprimer n'importe quel fichier de la machine. Un simple nom de
// fichier est cherché dans chaque dossier, JEAN_HOME en premier.
func resolveModelPath(name string) (string, error) {
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(name), `"`))
	if s == "" {
		return "", fmt.Errorf("nom de modèle invalide")
	}
	if !strings.HasSuffix(strings.ToLower(s), ".gguf") {
		return "", fmt.Errorf("le modèle doit être un fichier .gguf")
	}
	if filepath.IsAbs(s) {
		abs := filepath.Clean(s)
		for _, d := range modelDirs() {
			if pathWithin(d, abs) {
				return abs, nil
			}
		}
		return "", fmt.Errorf("dossier non autorisé : %s — ajoute-le dans « Dossiers de modèles »", filepath.Dir(abs))
	}
	base := baseName(s)
	if base == "" || base == "." {
		return "", fmt.Errorf("nom de modèle invalide")
	}
	for _, d := range modelDirs() {
		p := filepath.Join(d, base)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return filepath.Join(AjeanHome(), base), nil // introuvable : l'appelant décidera
}

// modelDirCount compte les .gguf lisibles d'un dossier (-1 si illisible).
func modelDirCount(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return -1
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
			n++
		}
	}
	return n
}

// handleModelDirs : GET liste les dossiers de modèles (JEAN_HOME + ajoutés),
// POST {path, action:"add"|"remove"} en ajoute ou en retire un.
func handleModelDirs(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var req struct {
			Path   string `json:"path"`
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		p := strings.TrimSpace(req.Path)
		if p == "" {
			sendJSON(w, 400, map[string]any{"ok": false, "error": "chemin vide"})
			return
		}
		cur := extraModelDirs()
		if req.Action == "remove" {
			kept := []string{}
			for _, d := range cur {
				if normDir(d) != normDir(p) {
					kept = append(kept, d)
				}
			}
			cur = kept
		} else {
			abs, err := filepath.Abs(p)
			if err != nil {
				sendJSON(w, 400, map[string]any{"ok": false, "error": "chemin invalide"})
				return
			}
			st, err := os.Stat(abs)
			if err != nil || !st.IsDir() {
				sendJSON(w, 400, map[string]any{"ok": false, "error": "dossier introuvable : " + abs})
				return
			}
			cur = append(cur, abs)
		}
		if err := saveExtraModelDirs(cur); err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true})
		return
	}
	out := []map[string]any{{"path": AjeanHome(), "home": true, "count": modelDirCount(AjeanHome()), "exists": true}}
	for _, d := range extraModelDirs() {
		n := modelDirCount(d)
		out = append(out, map[string]any{"path": d, "home": false, "count": n, "exists": n >= 0})
	}
	sendJSON(w, 200, map[string]any{"ok": true, "dirs": out, "env": os.Getenv("JEAN_MODEL_DIRS") != ""})
}
