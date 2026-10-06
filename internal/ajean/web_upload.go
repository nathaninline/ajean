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
	// Libre ici ET chez Jean : ses pièces jointes y sont déplacées après coup
	// (moveUploadsToJean), un même nom écraserait la précédente.
	free := func(n string) bool {
		_, e1 := os.Stat(filepath.Join(dir, n))
		_, e2 := os.Stat(filepath.Join(jeanWorkspace(), "uploads", n))
		return os.IsNotExist(e1) && os.IsNotExist(e2)
	}
	if free(name) {
		return filepath.Join(dir, name)
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	n := name
	for i := 2; i < 1000; i++ {
		n = fmt.Sprintf("%s-%d%s", base, i, ext)
		if free(n) {
			break
		}
	}
	return filepath.Join(dir, n)
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

// moveUploadsToJean : en mode Jean, les pièces jointes passent dans SON dossier
// de travail. Déposées dans celui des projets, « uploads/x.jpg » ne se résolvait
// pas chez lui (cloisonnement, chat_space.go) : vu en test, il a fouillé tout le
// disque pour retrouver les photos qu'on venait de lui envoyer. Le chemin
// relatif annoncé reste « uploads/… », juste chez lui ; /api/chat/file cherche
// déjà dans les deux dossiers.
func moveUploadsToJean(files []attachInfo) []attachInfo {
	src, err := uploadsDir()
	if err != nil || len(files) == 0 {
		return files
	}
	dst := filepath.Join(jeanWorkspace(), "uploads")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return files
	}
	for _, f := range files {
		from, to := filepath.Join(src, f.Name), filepath.Join(dst, f.Name)
		if err := os.Rename(from, to); err != nil {
			if b, rerr := os.ReadFile(from); rerr == nil && os.WriteFile(to, b, 0o644) == nil {
				_ = os.Remove(from)
			}
		}
	}
	return files
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
	video := false
	for _, f := range files {
		lines = append(lines, fmt.Sprintf("- %s (%s)", f.Path, humanBytes(f.Size)))
		video = video || videoMime(f.Name) != ""
	}
	note := head + "\n" + strings.Join(lines, "\n") + "\n"
	// Vidéo jointe que le moteur ne sait pas lire : dire comment la regarder
	// quand même, au lieu de laisser le modèle tâtonner.
	if video && visionEnabled() && !videoInputSupported() {
		note += videoFallbackNote
	}
	return note + "\n"
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

// videoMimes : les extensions qu'on peut ENVOYER AU MODÈLE comme vidéo (contenu
// multimodal input_video), quand la vision est active. Le moteur (llama-server)
// décode les frames lui-même via ffmpeg ; un format hors de cette liste reste
// un simple fichier du workspace.
var videoMimes = map[string]string{
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".mkv":  "video/x-matroska",
	".avi":  "video/x-msvideo",
}

// videoMime renvoie le type MIME vidéo d'un nom de fichier, ou "" si ce n'est
// pas une vidéo qu'on sait montrer au modèle.
func videoMime(name string) string {
	return videoMimes[strings.ToLower(filepath.Ext(name))]
}

// visionEnabled dit si le modèle actif sait recevoir des images. Deux cas :
//   - preset LOCAL : un projecteur multimodal est configuré (clé MMPROJ), ce qui
//     fait passer --mmproj au moteur (backend_serve.go) ;
//   - preset EXTERNE : l'API distante est déclarée multimodale (EXTERNAL_VISION=1),
//     puisqu'il n'y a pas de MMPROJ local à sonder.
//
// Sans l'un ou l'autre, envoyer une image n'a pas de sens (llama-server la
// rejetterait) : on s'en tient alors au dépôt-fichier.
func visionEnabled() bool {
	if strings.TrimSpace(ReadConfig()["MMPROJ"]) != "" {
		return true
	}
	return externalVisionActive() || cloudVisionActive() || moeVisionActive()
}

// visionMediaNote annonce au modèle les médias (images + vidéos) qu'il VOIT déjà
// en ligne (parties image_url / input_video ajoutées juste après le texte). Elle
// est distincte de attachNote : on dit explicitement que le média est sous ses
// yeux et qu'il n'a PAS à le rouvrir avec see_image/see_video (sinon, en mode
// agent, il rappelle l'outil pour « re-regarder » un média déjà affiché). Le
// chemin reste donné, mais seulement pour le manipuler comme fichier (le
// convertir, le recadrer avec un outil).
func visionMediaNote(files []attachInfo) string {
	if len(files) == 0 {
		return ""
	}
	head := "Média joint à ce message, que tu vois directement ci-dessous : ne le rouvre PAS avec see_image ou see_video, tu l'as déjà sous les yeux. Son chemin ne sert que si tu dois le manipuler comme fichier :"
	if len(files) > 1 {
		head = "Médias joints à ce message, que tu vois directement ci-dessous : ne les rouvre PAS avec see_image ou see_video, tu les as déjà sous les yeux. Leur chemin ne sert que si tu dois les manipuler comme fichiers :"
	}
	var lines []string
	for _, f := range files {
		lines = append(lines, fmt.Sprintf("- %s (%s)", f.Path, humanBytes(f.Size)))
	}
	return head + "\n" + strings.Join(lines, "\n") + "\n\n"
}

// userMessageContent construit le champ Content du message utilisateur. Cas
// courant : une simple chaîne (note des fichiers joints + texte). Quand la vision
// est active ET qu'au moins une pièce jointe est une image ou une vidéo, on
// renvoie le format multimodal — une partie `text` suivie d'une partie
// `image_url` par image (data URI base64) et d'une partie `input_video` par
// vidéo — que llama-server comprend une fois --mmproj chargé : le modèle VOIT
// alors l'image / la vidéo.
//
// Deux besoins distincts : voir le média (il part en ligne) et pouvoir agir sur
// le FICHIER (le convertir, l'analyser avec un outil). D'où deux notes séparées —
// les médias via visionMediaNote (déjà visibles, chemin pour manipulation), les
// autres fichiers via attachNote — pour que le modèle ne confonde pas « voir » et
// « ouvrir », et ne rappelle pas see_image/see_video sur un média qu'il a déjà.
func userMessageContent(files []attachInfo, prompt string) any {
	if !visionEnabled() {
		return attachNote(files) + prompt
	}
	dir, err := uploadsDir()
	if err != nil {
		return attachNote(files) + prompt
	}
	// Une pièce jointe peut être passée chez Jean (moveUploadsToJean).
	readAttach := func(name string) ([]byte, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			b, err = os.ReadFile(filepath.Join(jeanWorkspace(), "uploads", name))
		}
		return b, err
	}
	var imgParts, vidParts []map[string]any
	var mediaFiles, otherFiles []attachInfo
	for _, f := range files {
		if mime := imageMime(f.Name); mime != "" {
			b, err := readAttach(f.Name)
			if err != nil {
				otherFiles = append(otherFiles, f) // illisible ici : au moins l'annoncer comme fichier
				continue
			}
			// Redresse l'orientation EXIF et redimensionne les images trop grandes
			// avant l'envoi (base64 + tokens visuels) — voir prepareImageForModel.
			b, mime = prepareImageForModel(b, mime)
			imgParts = append(imgParts, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b),
				},
			})
			mediaFiles = append(mediaFiles, f)
			continue
		}
		if mime := videoMime(f.Name); mime != "" && videoInputSupported() {
			b, err := readAttach(f.Name)
			if err != nil {
				otherFiles = append(otherFiles, f)
				continue
			}
			// Pas de redimensionnement : le moteur décode et échantillonne les
			// frames lui-même (ffmpeg). On n'envoie que le fichier.
			vidParts = append(vidParts, map[string]any{
				"type": "input_video",
				"input_video": map[string]any{
					"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b),
				},
			})
			mediaFiles = append(mediaFiles, f)
			continue
		}
		otherFiles = append(otherFiles, f)
	}
	if len(imgParts) == 0 && len(vidParts) == 0 {
		return attachNote(files) + prompt
	}
	note := attachNote(otherFiles) + visionMediaNote(mediaFiles)
	parts := []map[string]any{{"type": "text", "text": note + prompt}}
	parts = append(parts, imgParts...)
	parts = append(parts, vidParts...)
	return parts
}

// dirRel dit si `abs` se trouve DANS `root` et, si oui, renvoie son chemin
// relatif en séparateurs '/' (liens symboliques résolus).
//
// Les dossiers de travail (projets et Jean) sont le seul périmètre téléchargeable. L'agent peut écrire n'importe où quand
// on lui donne un chemin absolu (voir resolveAgentPath) ; ouvrir le téléchargement
// à ces fichiers-là ferait de /api/chat/file un « lis-moi ce fichier du serveur »
// à usage général — l'API n'a pas forcément de clé et écoute sur 0.0.0.0.
func dirRel(root, abs string) (string, bool) {
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
	// Deux dossiers téléchargeables : celui des projets et celui de Jean (son espace
	// à part, voir chat_space.go). « jean-workspace/… » vise explicitement le second ;
	// un chemin relatif est cherché dans le premier, puis dans le second.
	roots := []string{agentWorkspace(), jeanWorkspace()}
	if r2, ok := strings.CutPrefix(filepath.ToSlash(rel), "jean-workspace/"); ok {
		rel, roots = r2, roots[1:]
	}
	abs := filepath.Join(roots[0], filepath.FromSlash(rel))
	localOK, inside := false, false
	for _, root := range roots {
		cand := filepath.Join(root, filepath.FromSlash(rel))
		if _, ok := dirRel(root, cand); !ok {
			continue
		}
		inside = true
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			abs, localOK = cand, true
			break
		}
	}
	if !localOK {
		if !inside {
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
	if t := q.Get("thumb"); t != "" {
		// Vignette légère (JSON, donc elle traverse aussi le tunnel E2E) : le fil
		// n'a besoin que de ~560 px, pas d'une capture de 3 Mo. Derrière
		// ajean.link, chaque image partait en entier, en base64 chiffré : lent,
		// et certaines n'arrivaient jamais. L'original reste servi à la demande.
		px, _ := strconv.Atoi(t)
		data, mime, err := chatThumb(abs, st, px)
		if err != nil {
			sendJSON(w, 415, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true, "mime": mime, "data": base64.StdEncoding.EncodeToString(data)})
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
