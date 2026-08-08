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

// Les .gguf vivent dans $AJEAN_HOME/models, mais un modèle de 40 Go tient
// rarement sur le disque système : on autorise des dossiers supplémentaires
// (disque dur externe, second SSD, montage réseau…). La liste est persistée en
// base — PAS dans la configuration, que le changement de preset remplace — et
// peut aussi venir de $AJEAN_MODEL_DIRS.

// modelDirsListSep sépare les dossiers dans $AJEAN_MODEL_DIRS (':' sous Unix,
// ';' sous Windows, comme le PATH).
func modelDirsListSep() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}

// extraModelDirs renvoie les dossiers déclarés par l'utilisateur (base puis
// variable d'environnement), nettoyés et dédoublonnés, models/ exclu.
func extraModelDirs() []string {
	var raw []string
	getJSON(bkState, "model_dirs", &raw)
	if v := os.Getenv("AJEAN_MODEL_DIRS"); v != "" {
		raw = append(raw, strings.Split(v, modelDirsListSep())...)
	}
	return dedupDirs(raw, modelsDir())
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

// modelDirs renvoie tous les dossiers où chercher un .gguf : models/ d'abord
// (il reste la destination des téléchargements), puis les dossiers ajoutés.
func modelDirs() []string {
	return append([]string{modelsDir()}, extraModelDirs()...)
}

// saveExtraModelDirs enregistre la liste des dossiers supplémentaires.
func saveExtraModelDirs(dirs []string) error {
	return putJSON(bkState, "model_dirs", dedupDirs(dirs, modelsDir()))
}

// pathWithin dit si le fichier p est dans le dossier dir (ou un sous-dossier).
func pathWithin(dir, p string) bool {
	d, f := normDir(dir), normDir(p)
	return f == d || strings.HasPrefix(f, d+"/")
}

// baseName renvoie le nom de fichier d'un chemin en acceptant les DEUX
// séparateurs : un config.env écrit sous Windows (C:\models\x.gguf) peut être
// relu par un ajean Linux, où filepath.Base ne coupe pas sur '\'.
func baseName(p string) string {
	p = strings.TrimRight(strings.TrimSpace(p), `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// resolveServeModelPath résout MODEL= pour LANCER le moteur. Différence avec
// resolveModelPath : un chemin absolu qui pointe sur un .gguf existant est
// accepté même hors des dossiers déclarés. L'allowlist protège des opérations
// destructrices pilotées depuis l'UI (suppression d'un fichier) ; l'appliquer à
// « ouvrir ce modèle en lecture » rendait la config éditée à la main
// inutilisable : MODEL=/home/moi/models/x.gguf renvoyait « dossier non
// autorisé », le moteur mourait en boucle, et le seul remède connu était
// d'aller déclarer le dossier dans l'interface web.
func resolveServeModelPath(name string) (string, error) {
	p, err := resolveModelPath(name)
	if err == nil {
		return p, nil
	}
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(name), `"`))
	if filepath.IsAbs(s) && strings.HasSuffix(strings.ToLower(s), ".gguf") {
		abs := filepath.Clean(s)
		if st, e := os.Stat(abs); e == nil && !st.IsDir() {
			return abs, nil
		}
	}
	return "", err
}

// resolveModelPath transforme une valeur MODEL= (nom de fichier OU chemin
// absolu) en chemin absolu vers un .gguf. Un chemin absolu n'est accepté que
// s'il est dans un des dossiers déclarés — sinon l'UI web deviendrait un moyen
// de lire/supprimer n'importe quel fichier de la machine. Un simple nom de
// fichier est cherché dans chaque dossier, AJEAN_HOME en premier.
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
	return filepath.Join(modelsDir(), base), nil // introuvable : l'appelant décidera
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

// diskFree renvoie l'espace libre du volume qui porte dir, ou -1 s'il est
// inconnu. Le dossier n'existe pas forcément encore (models/ au premier
// lancement) : on remonte les parents jusqu'à en trouver un qui existe.
func diskFree(dir string) int64 {
	d, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil {
		return -1
	}
	for {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return diskFreeAt(d)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return -1
		}
		d = parent
	}
}

// resolveDownloadDir valide le dossier de destination demandé pour un
// téléchargement : vide = models/, sinon il doit être un des dossiers
// déclarés — sans quoi l'UI web permettrait d'écrire n'importe où sur la
// machine.
func resolveDownloadDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return modelsDir(), nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("chemin invalide")
	}
	for _, d := range modelDirs() {
		if normDir(d) == normDir(abs) {
			return d, nil
		}
	}
	return "", fmt.Errorf("dossier non autorisé : %s — ajoute-le dans « Dossiers de modèles »", abs)
}

// handleModelDirs : GET liste les dossiers de modèles (AJEAN_HOME + ajoutés),
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
	out := []map[string]any{{"path": modelsDir(), "home": true, "count": modelDirCount(modelsDir()), "exists": true, "free": diskFree(modelsDir())}}
	for _, d := range extraModelDirs() {
		n := modelDirCount(d)
		out = append(out, map[string]any{"path": d, "home": false, "count": n, "exists": n >= 0, "free": diskFree(d)})
	}
	sendJSON(w, 200, map[string]any{"ok": true, "dirs": out, "env": os.Getenv("AJEAN_MODEL_DIRS") != ""})
}
