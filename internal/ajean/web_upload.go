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
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	// uploadMaxBytes borne la taille d'UN fichier. Le fichier n'est jamais tenu en
	// mémoire (voir upSession) : cette limite protège le disque, pas la RAM.
	uploadMaxBytes = 1 << 30 // 1 Go

	// uploadChunkMax borne UN morceau, et c'est LUI qui décide de la mémoire du
	// process : un morceau est reçu en JSON, donc entièrement en RAM, puis décodé.
	// 8 Mo décodés ≈ 11 Mo de base64. C'est aussi la taille demandée au client ;
	// un morceau plus gros est refusé plutôt que d'être avalé.
	uploadChunkMax = 8 << 20

	// downloadChunkMax borne UNE tranche de téléchargement encodée en base64 (voir
	// handleChatFile). Même logique que pour l'envoi : c'est la mémoire du process
	// qu'on protège, pas la taille du fichier.
	downloadChunkMax = 8 << 20
)

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

// imageMimes : les extensions qu'on peut ENVOYER AU MODÈLE comme image (contenu
// multimodal), quand la vision est active. Un format hors de cette liste reste
// un simple fichier du workspace, que le modèle ouvre avec ses outils.
var imageMimes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
}

// imageMime renvoie le type MIME image d'un nom de fichier, ou "" si ce n'est
// pas une image qu'on sait montrer au modèle.
func imageMime(name string) string {
	return imageMimes[strings.ToLower(filepath.Ext(name))]
}

// visionEnabled dit si un projecteur multimodal est configuré (clé MMPROJ) —
// c'est la condition pour que backend_serve.go passe --mmproj au moteur, donc la
// seule où envoyer une image AU MODÈLE a un sens. Sans projecteur, llama-server
// rejetterait un contenu image ; on s'en tient alors au dépôt-fichier.
func visionEnabled() bool {
	return strings.TrimSpace(ReadConfig()["MMPROJ"]) != ""
}

// userMessageContent construit le champ Content du message utilisateur. Cas
// courant : une simple chaîne (note des fichiers joints + texte). Quand la vision
// est active ET qu'au moins une pièce jointe est une image, on renvoie le format
// multimodal d'OpenAI — une partie `text` suivie d'une partie `image_url` par
// image (data URI base64) — que llama-server comprend une fois --mmproj chargé :
// le modèle VOIT alors l'image au lieu de devoir l'ouvrir comme un fichier binaire.
// Les images ainsi intégrées sortent de la note textuelle (inutile de dire au
// modèle d'aller ouvrir un fichier qu'il a déjà sous les yeux) ; les autres
// fichiers, eux, restent annoncés comme avant.
func userMessageContent(files []attachInfo, prompt string) any {
	if !visionEnabled() {
		return attachNote(files) + prompt
	}
	dir, err := uploadsDir()
	if err != nil {
		return attachNote(files) + prompt
	}
	var imgParts []map[string]any
	var textFiles []attachInfo
	for _, f := range files {
		mime := imageMime(f.Name)
		if mime == "" {
			textFiles = append(textFiles, f)
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, f.Name))
		if err != nil {
			textFiles = append(textFiles, f) // illisible ici : au moins l'annoncer comme fichier
			continue
		}
		imgParts = append(imgParts, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b),
			},
		})
	}
	if len(imgParts) == 0 {
		return attachNote(files) + prompt
	}
	parts := []map[string]any{{"type": "text", "text": attachNote(textFiles) + prompt}}
	return append(parts, imgParts...)
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

// e2eInnerHeader marque une requête dispatchée depuis le proxy chiffré
// (relay_e2e.go). Cf. handleChatFile : c'est le seul moyen pour un handler de
// savoir que sa réponse sera réemballée en JSON.
const e2eInnerHeader = "X-Ajean-E2E"

// handleChatFile sert un fichier du dossier de travail, en pièce jointe. Le
// client passe le chemin RELATIF que le modèle a écrit dans sa réponse.
//
// Trois formes de réponse, pour une raison de transport :
//   - par défaut, le fichier brut — le chemin direct, en local ou sur le LAN ;
//   - `meta=1`, une fiche {name, size, e2e} : le client y apprend s'il est
//     derrière le tunnel, donc quelle forme demander ensuite ;
//   - `b64=1&offset=&len=`, une tranche encodée en base64.
//
// La raison : à travers app.ajean.link, TOUTE réponse est réemballée en JSON par
// le proxy chiffré. Du binaire n'y survit pas — les octets non-UTF8 sont
// massacrés, et on téléchargeait une enveloppe JSON au lieu du fichier. Le
// base64 traverse, et le découpage en tranches évite de tenir un gigaoctet en
// mémoire pour le transporter.
func handleChatFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if strings.TrimSpace(rel) == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "chemin manquant"})
		return
	}
	// Un chemin ABSOLU fourni par le client ne doit pas être suivi : on le traite
	// comme relatif au dossier de travail, et le contrôle ci-dessous tranche.
	abs := filepath.Join(agentWorkspace(), filepath.FromSlash(rel))
	localOK := false
	if _, ok := workspaceRel(abs); ok {
		if st, err := os.Stat(abs); err == nil && !st.IsDir() {
			localOK = true
		}
	}
	// Le fichier n'est pas dans le workspace LOCAL mais l'IA pilote un POSTE
	// DISTANT : les fichiers qu'elle y produit vivent là-bas, on les rapatrie depuis
	// le poste (issue #33 remote). Les fichiers locaux (pièces jointes de
	// l'utilisateur, par ex.) restent servis en local — priorité au local, le poste
	// n'est qu'un repli.
	if !localOK {
		if slug := agentTargetSlug(); slug != "" {
			handleChatFileNode(w, r, slug, rel)
			return
		}
	}
	if !localOK {
		if _, ok := workspaceRel(abs); !ok {
			sendJSON(w, 403, map[string]any{"ok": false, "error": "hors du dossier de travail"})
			return
		}
		sendJSON(w, 404, map[string]any{"ok": false, "error": "fichier introuvable"})
		return
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		sendJSON(w, 404, map[string]any{"ok": false, "error": "fichier introuvable"})
		return
	}
	name := filepath.Base(abs)
	q := r.URL.Query()
	if q.Get("meta") != "" {
		sendJSON(w, 200, map[string]any{
			"ok": true, "name": name, "size": st.Size(),
			// Le client ne peut pas deviner seul qu'il passe par le tunnel : le
			// proxy lui rend des réponses JSON parfaitement ordinaires.
			"e2e": r.Header.Get(e2eInnerHeader) != "",
		})
		return
	}
	if q.Get("b64") != "" {
		off, _ := strconv.ParseInt(q.Get("offset"), 10, 64)
		length, _ := strconv.ParseInt(q.Get("len"), 10, 64)
		if length <= 0 || length > downloadChunkMax {
			length = downloadChunkMax
		}
		if off < 0 || off > st.Size() {
			sendJSON(w, 400, map[string]any{"ok": false, "error": "position hors du fichier"})
			return
		}
		if off+length > st.Size() {
			length = st.Size() - off
		}
		f, err := os.Open(abs)
		if err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		defer f.Close()
		buf := make([]byte, length)
		// ReadAt : positionne et lit en une fois, et remplit tout le tampon (ce
		// qu'un simple Read ne garantit pas). io.EOF sur la dernière tranche est
		// normal, pas une erreur.
		n, err := f.ReadAt(buf, off)
		if err != nil && err != io.EOF {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		sendJSON(w, 200, map[string]any{
			"ok": true, "name": name, "size": st.Size(), "offset": off,
			"data": base64.StdEncoding.EncodeToString(buf[:n]),
			"eof":  off+int64(n) >= st.Size(),
		})
		return
	}
	// Toujours en TÉLÉCHARGEMENT, jamais rendu : un .html écrit par le modèle ne
	// doit pas s'exécuter dans l'origine de l'UI (il y lirait la clé de pilotage).
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	http.ServeFile(w, r, abs)
}

// handleChatFileNode sert un fichier d'un POSTE DISTANT (l'IA y opère). Même
// contrat que handleChatFile mais la source est le poste : meta renvoie la taille
// + `remote:true` (le client sait qu'il doit passer par les tranches base64, le
// binaire brut ne peut pas être servi depuis ici), b64 relaie une tranche
// rapatriée du poste. Le confinement au dossier autorisé est fait CÔTÉ POSTE
// (ResolvePath), on ne suit donc pas de chemin local ici.
func handleChatFileNode(w http.ResponseWriter, r *http.Request, slug, rel string) {
	q := r.URL.Query()
	if q.Get("meta") != "" {
		f, err := nodeFetchFile(slug, rel, 0, 0)
		if err != nil {
			sendJSON(w, 404, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		sendJSON(w, 200, map[string]any{
			"ok": true, "name": f.Name, "size": f.Size,
			// remote → le navigateur récupère par tranches base64 (comme e2e) ; e2e
			// signale en plus le tunnel chiffré, les deux peuvent coexister.
			"remote": true,
			"e2e":    r.Header.Get(e2eInnerHeader) != "",
		})
		return
	}
	if q.Get("b64") != "" {
		off, _ := strconv.ParseInt(q.Get("offset"), 10, 64)
		length, _ := strconv.ParseInt(q.Get("len"), 10, 64)
		if length <= 0 || length > downloadChunkMax {
			length = downloadChunkMax
		}
		f, err := nodeFetchFile(slug, rel, off, length)
		if err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		sendJSON(w, 200, map[string]any{
			"ok": true, "name": f.Name, "size": f.Size, "offset": f.Offset,
			"data": f.Data, "eof": f.EOF,
		})
		return
	}
	// Téléchargement binaire direct impossible depuis un poste (pas de fichier local
	// à streamer) : le client passe toujours par meta puis b64 quand remote=true.
	sendJSON(w, 400, map[string]any{"ok": false, "error": "fichier distant : utiliser le mode base64"})
}

type uploadReq struct {
	Name string `json:"name"`
	Data string `json:"data"` // base64 d'UN morceau (accepte un data: URL complet)
	// Envoi en plusieurs morceaux. Le premier appel n'a pas d'ID et reçoit celui
	// que le serveur attribue ; les suivants le rappellent. `More` à false ferme
	// le fichier. Un envoi en un seul morceau (More absent) reste valable.
	ID   string `json:"id"`
	More bool   `json:"more"`
	// Size = taille totale annoncée au PREMIER morceau, pour vérifier l'espace
	// disque avant d'entamer un envoi d'un gigaoctet. Purement indicatif : le
	// vrai plafond reste vérifié morceau par morceau.
	Size int64 `json:"size"`
}

// upSession = un envoi en cours, adossé à un fichier .part sur le disque.
//
// Rien n'est accumulé en mémoire : chaque morceau est décodé puis écrit tout de
// suite, et seul le descripteur reste ouvert. C'est ce qui permet de passer à
// 1 Go — la version d'avant gardait le fichier entier en RAM, deux fois (le
// base64 reçu et le binaire décodé), ce qui plafonnait l'envoi à quelques Mo
// utilisables sans faire gonfler le process.
type upSession struct {
	// mu sérialise les écritures d'UNE session. Le verrou global ne couvre que la
	// table : le tenir pendant l'écriture disque mettait tous les envois à la
	// queue leu leu, alors que le client en lance plusieurs de front (un par
	// fichier joint).
	mu      sync.Mutex
	busy    bool // écriture en cours : le ménage doit passer son tour
	f       *os.File
	name    string
	written int64
	last    time.Time
}

var (
	upMu       sync.Mutex
	upSessions = map[string]*upSession{}
)

// uploadSpaceMargin : place qu'on refuse d'entamer sur le disque. Remplir le
// volume de la machine qui fait tourner le modèle est autrement plus grave que
// de refuser un envoi — llama-server, la base et les journaux vivent dessus.
const uploadSpaceMargin = 512 << 20

// upSessionTTL : au-delà, un envoi interrompu (onglet fermé, réseau coupé) est
// abandonné et son .part supprimé. Sans ça, un fichier à moitié transféré
// resterait ouvert et occuperait le disque indéfiniment.
const upSessionTTL = 10 * time.Minute

// sweepUploadSessions ferme et efface les envois abandonnés. Appelé à chaque
// nouveau morceau : pas de goroutine de ménage à faire vivre.
func sweepUploadSessions() {
	now := time.Now()
	for id, s := range upSessions {
		// `busy` : une écriture est en cours dans cette session. La balayer
		// fermerait le fichier sous les pieds de la requête qui l'écrit.
		if !s.busy && now.Sub(s.last) > upSessionTTL {
			name := s.f.Name()
			s.f.Close()
			os.Remove(name)
			delete(upSessions, id)
		}
	}
}

// handleChatUpload reçoit un fichier, en un ou plusieurs morceaux, et renvoie
// son chemin relatif — celui que le client joindra au message (/api/chat/send,
// champ files).
func handleChatUpload(w http.ResponseWriter, r *http.Request) {
	var body uploadReq
	// Le plafond porte sur UN morceau, pas sur le fichier : c'est la seule borne
	// qui compte pour la mémoire du process.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*uploadChunkMax)).Decode(&body); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "morceau trop gros ou requête invalide"})
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

	// Le verrou global ne protège QUE la table des sessions. L'écriture, elle, se
	// fait sous le verrou de la session — sinon deux fichiers envoyés en même
	// temps (le client les lance de front) s'attendraient l'un l'autre.
	upMu.Lock()
	sweepUploadSessions()
	s := upSessions[body.ID]
	if s == nil {
		if body.ID != "" {
			// L'envoi a expiré ou le serveur a redémarré en cours de route : le dire,
			// plutôt que de recommencer un fichier à partir de son milieu.
			upMu.Unlock()
			sendJSON(w, 409, map[string]any{"ok": false, "error": "envoi expiré — recommence le fichier"})
			return
		}
		dir, err := uploadsDir()
		if err != nil {
			upMu.Unlock()
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		// Espace disque : refuser franchement vaut mieux que remplir le volume de
		// la machine qui fait tourner le modèle. La taille annoncée par le client
		// n'engage que lui — le plafond réel reste vérifié morceau par morceau.
		if free := diskFree(dir); free > 0 && body.Size > 0 && free < body.Size+uploadSpaceMargin {
			upMu.Unlock()
			sendJSON(w, 507, map[string]any{"ok": false,
				"error": fmt.Sprintf("espace insuffisant : %s libres, %s nécessaires", humanBytes(free), humanBytes(body.Size+uploadSpaceMargin))})
			return
		}
		f, err := os.CreateTemp(dir, ".upload-*.part")
		if err != nil {
			upMu.Unlock()
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		s = &upSession{f: f, name: safeUploadName(body.Name)}
		body.ID = filepath.Base(f.Name())
		upSessions[body.ID] = s
	}
	s.last = time.Now()
	s.busy = true
	upMu.Unlock()

	s.mu.Lock()
	defer func() {
		s.mu.Unlock()
		upMu.Lock()
		s.busy = false
		upMu.Unlock()
	}()

	// drop ferme et oublie la session : sur erreur, et à la fin de l'envoi.
	drop := func() string {
		name := s.f.Name()
		s.f.Close()
		upMu.Lock()
		delete(upSessions, body.ID)
		upMu.Unlock()
		return name
	}
	abort := func(code int, msg string) {
		os.Remove(drop())
		sendJSON(w, code, map[string]any{"ok": false, "error": msg})
	}
	if s.written+int64(len(raw)) > uploadMaxBytes {
		abort(413, fmt.Sprintf("fichier trop gros (max %s)", humanBytes(uploadMaxBytes)))
		return
	}
	if len(raw) > 0 {
		if _, err := s.f.Write(raw); err != nil {
			abort(500, err.Error())
			return
		}
		s.written += int64(len(raw))
	}
	if body.More {
		// Morceau intermédiaire : on rend l'ID pour la suite et l'avancement, qui
		// alimente la barre de progression côté client.
		sendJSON(w, 200, map[string]any{"ok": true, "id": body.ID, "received": s.written})
		return
	}

	// Dernier morceau : on ferme et on donne au fichier son vrai nom. Le Close est
	// dans drop() ; son erreur éventuelle est celle d'un tampon non vidé, donc
	// d'un fichier incomplet — on ne le publie pas dans ce cas.
	part := s.f.Name()
	syncErr := s.f.Sync()
	drop()
	if syncErr != nil {
		os.Remove(part)
		sendJSON(w, 500, map[string]any{"ok": false, "error": syncErr.Error()})
		return
	}
	if s.written == 0 {
		os.Remove(part)
		sendJSON(w, 400, map[string]any{"ok": false, "error": "fichier vide"})
		return
	}
	dest := uniqueUploadPath(filepath.Dir(part), s.name)
	if err := os.Rename(part, dest); err != nil {
		os.Remove(part)
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{
		"ok":   true,
		"path": "uploads/" + filepath.Base(dest),
		"abs":  dest, // affiché à l'utilisateur, jamais renvoyé au serveur
		"size": s.written,
	})
}

// cleanStaleUploadParts efface les .part laissés par un envoi que le process n'a
// pas pu terminer (arrêt du service, crash). Appelé au démarrage : les sessions
// vivent en mémoire, aucun de ces fichiers n'est reprenable.
func cleanStaleUploadParts() {
	dir, err := uploadsDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, ".upload-") && strings.HasSuffix(n, ".part") {
			os.Remove(filepath.Join(dir, n))
		}
	}
}
