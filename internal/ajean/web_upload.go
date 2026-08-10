// web_upload.go — dépôt de fichiers depuis le chat.
//
// L'utilisateur glisse un fichier dans le composeur ; il est écrit dans
// uploads/ à l'intérieur du workspace de l'agent (chat_workspace.go), et son
// chemin RELATIF est joint au message. Le modèle en fait ce qu'il veut avec ses
// outils (read, bash, edit) — on ne tente ni extraction ni interprétation ici.
//
// Le transport est du JSON base64, et non du multipart : c'est la seule forme
// qui traverse le tunnel E2E d'app.ajean.link (relay_e2e.go ne dispatche que des
// corps JSON), donc l'accès distant marche sans code spécifique.
package ajean

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// uploadMaxBytes borne la taille d'UN fichier. Tout le chemin (corps JSON, E2E,
// écriture) passe par la mémoire : au-delà, un envoi maladroit ferait gonfler le
// process de plusieurs centaines de Mo. 24 Mo décodés ≈ 32 Mo de base64.
const uploadMaxBytes = 24 << 20

// uploadsDir renvoie (en le créant) le dossier de dépôt, dans le workspace agent.
func uploadsDir() (string, error) {
	dir := filepath.Join(agentWorkspace(), "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// safeUploadName réduit un nom fourni par le client à un nom de fichier simple :
// pas de dossier, pas de "..", pas de caractères de contrôle ni de séparateurs.
// C'est la seule barrière entre un client hostile et une écriture arbitraire sur
// le disque — l'API écoute sur 0.0.0.0 et n'a pas forcément de clé.
func safeUploadName(name string) string {
	// Coupe tout ce qui ressemble à un chemin, dans les deux conventions : un nom
	// Windows arrive tel quel sur un serveur Linux, où filepath.Base ne verrait
	// pas les antislashs.
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(filepath.FromSlash(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsControl(r), r == '/', r == '\\', r == ':', r == '*',
			r == '?', r == '"', r == '<', r == '>', r == '|':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimSpace(b.String())
	name = strings.Trim(name, ".") // ".." et les noms cachés/vides
	if name == "" {
		name = "fichier"
	}
	// Un nom démesuré casse l'écriture sur certains systèmes de fichiers ; on
	// tronque la BASE en gardant l'extension, qui porte le sens.
	if len(name) > 120 {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		name = name[:120-len(ext)] + ext
	}
	return name
}

// uniqueUploadPath évite d'écraser un dépôt précédent : rapport.pdf,
// rapport-2.pdf, rapport-3.pdf…
func uniqueUploadPath(dir, name string) string {
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; i < 1000; i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
	}
	return p
}

// attachInfo décrit une pièce jointe retenue : ce que le modèle lira dans le
// texte, et ce que l'UI affiche dans la bulle.
type attachInfo struct {
	Name string `json:"name"` // nom seul, pour l'affichage
	Path string `json:"path"` // "uploads/<nom>", ce que le modèle reçoit
	Size int64  `json:"size"`
}

// attachFiles valide les chemins envoyés par le CLIENT : on les re-normalise en
// uploads/<nom sûr> et on jette ceux qui ne désignent pas un dépôt existant,
// plutôt que d'annoncer au modèle un fichier qu'il ne trouvera pas — ou de le
// laisser pointer ailleurs sur le disque.
func attachFiles(files []string) []attachInfo {
	dir, err := uploadsDir()
	if err != nil {
		return nil
	}
	var out []attachInfo
	for _, f := range files {
		name := safeUploadName(f)
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil || st.IsDir() {
			continue
		}
		out = append(out, attachInfo{Name: name, Path: "uploads/" + name, Size: st.Size()})
	}
	return out
}

// attachNote est la phrase ajoutée en tête du message pour le MODÈLE. Elle ne
// s'affiche pas dans le chat : la bulle porte des pastilles de fichier (delta
// `files`), parce qu'une consigne interne recopiée dans le fil se lit comme un
// message que l'utilisateur n'a pas écrit.
func attachNote(files []attachInfo) string {
	if len(files) == 0 {
		return ""
	}
	head := "Fichier joint à ce message, déposé dans ton dossier de travail :"
	if len(files) > 1 {
		head = "Fichiers joints à ce message, déposés dans ton dossier de travail :"
	}
	var lines []string
	for _, f := range files {
		lines = append(lines, fmt.Sprintf("- %s (%s)", f.Path, humanBytes(f.Size)))
	}
	return head + "\n" + strings.Join(lines, "\n") + "\n\n"
}

// workspaceRel dit si `abs` se trouve DANS le dossier de travail de l'agent et,
// si oui, renvoie son chemin relatif en séparateurs '/'.
//
// C'est le seul périmètre téléchargeable. L'agent peut écrire n'importe où quand
// on lui donne un chemin absolu (voir resolveAgentPath) ; ouvrir le téléchargement
// à ces fichiers-là ferait de /api/chat/file un « lis-moi ce fichier du serveur »
// à usage général — l'API n'a pas forcément de clé et écoute sur 0.0.0.0.
func workspaceRel(abs string) (string, bool) {
	root := agentWorkspace()
	// EvalSymlinks des deux côtés : sans ça, un lien qui sort du dossier passerait
	// le test de préfixe.
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if a, err := filepath.EvalSymlinks(abs); err == nil {
		abs = a
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// handleChatFile sert un fichier du dossier de travail, en pièce jointe. Le
// client passe le chemin RELATIF que le modèle a écrit dans sa réponse.
func handleChatFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if strings.TrimSpace(rel) == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "chemin manquant"})
		return
	}
	// Un chemin ABSOLU fourni par le client ne doit pas être suivi : on le traite
	// comme relatif au dossier de travail, et le contrôle ci-dessous tranche.
	abs := filepath.Join(agentWorkspace(), filepath.FromSlash(rel))
	if _, ok := workspaceRel(abs); !ok {
		sendJSON(w, 403, map[string]any{"ok": false, "error": "hors du dossier de travail"})
		return
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		sendJSON(w, 404, map[string]any{"ok": false, "error": "fichier introuvable"})
		return
	}
	name := filepath.Base(abs)
	// Toujours en TÉLÉCHARGEMENT, jamais rendu : un .html écrit par le modèle ne
	// doit pas s'exécuter dans l'origine de l'UI (il y lirait la clé de pilotage).
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	http.ServeFile(w, r, abs)
}

type uploadReq struct {
	Name string `json:"name"`
	Data string `json:"data"` // base64 (accepte un data: URL complet)
}

// handleChatUpload écrit un fichier dans le workspace et renvoie son chemin
// relatif, celui que le client joindra au message (/api/chat/send, champ files).
func handleChatUpload(w http.ResponseWriter, r *http.Request) {
	var body uploadReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*uploadMaxBytes)).Decode(&body); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "fichier trop gros ou requête invalide"})
		return
	}
	data := body.Data
	// Les clients qui passent par FileReader.readAsDataURL envoient
	// "data:application/pdf;base64,JVBER…" : on ne garde que la charge utile.
	if strings.HasPrefix(data, "data:") {
		if i := strings.Index(data, ","); i >= 0 {
			data = data[i+1:]
		}
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "contenu illisible (base64 attendu)"})
		return
	}
	if len(raw) == 0 {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "fichier vide"})
		return
	}
	if len(raw) > uploadMaxBytes {
		sendJSON(w, 413, map[string]any{"ok": false, "error": fmt.Sprintf("fichier trop gros (max %s)", humanBytes(int64(uploadMaxBytes)))})
		return
	}
	dir, err := uploadsDir()
	if err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	dest := uniqueUploadPath(dir, safeUploadName(body.Name))
	if err := os.WriteFile(dest, raw, 0o644); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	rel := "uploads/" + filepath.Base(dest)
	sendJSON(w, 200, map[string]any{
		"ok":   true,
		"path": rel,  // ce que le modèle verra (résolu par resolveAgentPath)
		"abs":  dest, // affiché à l'utilisateur, jamais renvoyé au serveur
		"size": len(raw),
	})
}
