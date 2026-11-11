package ajean

// jean_files_web.go — le gestionnaire de fichiers du mode Jean : son dossier de
// travail, parcouru, rempli et vidé depuis l'app (téléphone compris). Repris de
// Sillage (cmd/sillage/webfiles.go).
//
// Tout est en JSON : c'est la seule forme qui traverse le tunnel chiffré
// d'app.ajean.link (relay_e2e.go). L'envoi passe par /api/chat/upload (champ
// dest), le téléchargement et les vignettes par /api/chat/file
// (« jean-workspace/… »). Les chemins reçus sont relatifs au dossier de Jean et
// n'en sortent jamais, ni par leur texte ni par un lien symbolique. Chaque
// changement relance l'indexation (jean_files.go).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// jeanWSPrefix : préfixe d'un chemin du dossier de Jean côté interface
// (/api/chat/file le comprend, voir handleChatFile).
const jeanWSPrefix = "jean-workspace/"

var errJeanFilesOut = errors.New("en dehors du dossier d'AJEAN")

// jeanFilesDir : le dossier de Jean, créé au besoin.
func jeanFilesDir() (string, error) {
	root := jeanFilesRoot()
	if root == "" {
		return "", errors.New("dossier d'AJEAN introuvable")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return root, nil
}

// jeanFilesPath : le chemin absolu de rel dans le dossier de Jean, ou une erreur
// s'il en sortirait, par son texte ou par un lien symbolique (Jean peut en créer
// avec bash).
func jeanFilesPath(rel string) (string, error) {
	root, err := jeanFilesDir()
	if err != nil {
		return "", err
	}
	return jeanInSpace(root, rel)
}

func jeanInSpace(root, rel string) (string, error) {
	p := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(strings.ReplaceAll(rel, `\`, "/"), "/")))
	if !jeanWithin(root, p) {
		return "", errJeanFilesOut
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	// La vraie place de la partie la plus profonde qui existe (un nouveau fichier
	// n'existe pas encore).
	q, rest := p, ""
	for {
		if r, err := filepath.EvalSymlinks(q); err == nil {
			q = filepath.Join(r, rest)
			break
		}
		parent := filepath.Dir(q)
		if parent == q {
			return "", errJeanFilesOut
		}
		rest, q = filepath.Join(filepath.Base(q), rest), parent
	}
	if !jeanWithin(realRoot, q) {
		return "", errJeanFilesOut
	}
	return p, nil
}

// jeanWithin : p est-il root ou dedans ?
func jeanWithin(root, p string) bool {
	r, err := filepath.Rel(root, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func jeanIsRoot(root, p string) bool {
	r, err := filepath.Rel(root, p)
	return err == nil && r == "."
}

// jeanReserved : les noms auxquels Windows donne un sens.
var jeanReserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com\d|lpt\d)(\..*)?$`)

// jeanCleanName : un nom de fichier ou de dossier tapé dans l'app, sans chemin.
// Refusé plutôt que corrigé en douce : la personne voit ce qui ne va pas.
func jeanCleanName(name string) (string, error) {
	name = strings.TrimSpace(path.Base(path.Clean("/" + strings.ReplaceAll(name, `\`, "/"))))
	if name == "" || name == "." || name == "/" || name == ".." || strings.HasPrefix(name, ".") ||
		strings.ContainsAny(name, `:*?"<>|()`) || strings.HasSuffix(name, ".") || jeanReserved.MatchString(name) || len(name) > 160 {
		return "", errors.New(`nom non valide (sans / \ : * ? " < > | ( ), ni point au début ou à la fin)`)
	}
	return name, nil
}

// jeanFreeName : name, ou « nom-2.ext », « nom-3.ext »… quand il est pris dans
// dir : rien n'écrase un fichier. Pas de parenthèses : un lien Markdown
// ![…](photo (2).jpg) se couperait à la première.
func jeanFreeName(dir, name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	cand := name
	for n := 2; n < 10000; n++ {
		if _, err := os.Lstat(filepath.Join(dir, cand)); errors.Is(err, os.ErrNotExist) {
			return cand
		}
		cand = fmt.Sprintf("%s-%d%s", base, n, ext)
	}
	return cand
}

// jeanSameContent : a et b sont-ils deux fichiers ordinaires aux mêmes octets ?
func jeanSameContent(a, b string) bool {
	ia, err1 := os.Stat(a)
	ib, err2 := os.Stat(b)
	if err1 != nil || err2 != nil || !ib.Mode().IsRegular() || ia.Size() != ib.Size() {
		return false
	}
	ha, hb := jeanFileHash(a), jeanFileHash(b)
	return ha != nil && bytes.Equal(ha, hb)
}

func jeanFileHash(p string) []byte {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil
	}
	return h.Sum(nil)
}

// jeanMoveFile : renomme, ou copie puis efface quand les deux dossiers ne sont
// pas sur le même disque.
func jeanMoveFile(from, to string) error {
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		src.Close()
		return err
	}
	_, err = io.Copy(dst, src)
	src.Close()
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(to)
		return err
	}
	return os.Remove(from)
}

// jeanSaveUpload range un fichier reçu (tmp) dans dir sous name, et renvoie son
// chemin. Un fichier du même nom et du même contenu est le même, renvoyé : il
// est gardé, pas copié en « nom-2 ». Un autre contenu prend un nom libre.
func jeanSaveUpload(tmp, dir, name string) (string, error) {
	if same := filepath.Join(dir, name); jeanSameContent(tmp, same) {
		_ = os.Remove(tmp)
		return same, nil
	}
	dst := filepath.Join(dir, jeanFreeName(dir, name))
	if err := jeanMoveFile(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dst, nil
}

type jeanFileEntry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
	Mod  int64  `json:"mod"` // unix millisecondes
}

type jeanFilesReq struct {
	Dir   string   `json:"dir"`
	Path  string   `json:"path"`
	Paths []string `json:"paths"`
	To    string   `json:"to"`
	Name  string   `json:"name"`
	Q     string   `json:"q"`
}

func jeanFilesDecode(w http.ResponseWriter, r *http.Request) (jeanFilesReq, bool) {
	var req jeanFilesReq
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]any{"ok": false, "error": "POST attendu"})
		return req, false
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "requête illisible"})
		return req, false
	}
	return req, true
}

func jeanFilesFail(w http.ResponseWriter, err error) {
	sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
}

// handleJeanFiles : POST {dir} → le contenu d'un dossier (dossiers d'abord), et
// combien de fichiers Jean a lus.
func handleJeanFiles(w http.ResponseWriter, r *http.Request) {
	req, ok := jeanFilesDecode(w, r)
	if !ok {
		return
	}
	dir, err := jeanFilesPath(req.Dir)
	if err != nil {
		jeanFilesFail(w, err)
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		jeanFilesFail(w, errors.New("dossier introuvable"))
		return
	}
	list := []jeanFileEntry{}
	for _, e := range ents {
		info, err := e.Info()
		if err != nil || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 { // un lien : ce vers quoi il pointe, s'il reste chez Jean
			if _, err := jeanInSpace(jeanFilesRoot(), jeanRelSlash(jeanFilesRoot(), filepath.Join(dir, e.Name()))); err != nil {
				continue
			}
			if st, err := os.Stat(filepath.Join(dir, e.Name())); err == nil {
				info, isDir = st, st.IsDir()
			}
		}
		list = append(list, jeanFileEntry{e.Name(), isDir, info.Size(), info.ModTime().UnixMilli()})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Dir != list[j].Dir {
			return list[i].Dir
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	indexed, total := jeanFilesCount()
	sendJSON(w, 200, map[string]any{"ok": true, "root": jeanFilesRoot(), "entries": list,
		"indexed": indexed, "total": total, "meaning": jeanEmbedEnabled()})
}

// handleJeanFilesMkdir : POST {dir, name} → nouveau dossier.
func handleJeanFilesMkdir(w http.ResponseWriter, r *http.Request) {
	req, ok := jeanFilesDecode(w, r)
	if !ok {
		return
	}
	dir, err := jeanFilesPath(req.Dir)
	if err != nil {
		jeanFilesFail(w, err)
		return
	}
	name, err := jeanCleanName(req.Name)
	if err == nil {
		err = os.Mkdir(filepath.Join(dir, name), 0o755)
		if errors.Is(err, os.ErrExist) {
			err = errors.New(name + " existe déjà ici")
		}
	}
	if err != nil {
		jeanFilesFail(w, err)
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "name": name})
}

// handleJeanFilesDelete : POST {paths} → supprimés (un dossier avec tout ce
// qu'il contient). Le dossier de Jean lui-même reste.
func handleJeanFilesDelete(w http.ResponseWriter, r *http.Request) {
	req, ok := jeanFilesDecode(w, r)
	if !ok {
		return
	}
	if req.Path != "" {
		req.Paths = append(req.Paths, req.Path)
	}
	root := jeanFilesRoot()
	var failed []string
	done := 0
	for _, rel := range req.Paths {
		p, err := jeanFilesPath(rel)
		if err == nil && jeanIsRoot(root, p) {
			err = errors.New("le dossier d'AJEAN lui-même reste")
		}
		if err == nil {
			err = os.RemoveAll(p)
		}
		if err != nil {
			failed = append(failed, rel+" : "+err.Error())
			continue
		}
		done++
	}
	if done > 0 {
		jeanEmbedKick() // ses passages sortent de l'index
	}
	if len(failed) > 0 {
		sendJSON(w, 400, map[string]any{"ok": false, "done": done, "error": strings.Join(failed, " ; ")})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "done": done})
}

// handleJeanFilesMove : POST {paths, to} → rangés dans un autre dossier (glisser-
// déposer). Un nom déjà pris là-bas en prend un libre.
func handleJeanFilesMove(w http.ResponseWriter, r *http.Request) {
	req, ok := jeanFilesDecode(w, r)
	if !ok {
		return
	}
	if req.Path != "" {
		req.Paths = append(req.Paths, req.Path)
	}
	to, err := jeanFilesPath(req.To)
	if err != nil {
		jeanFilesFail(w, err)
		return
	}
	if st, err := os.Stat(to); err != nil || !st.IsDir() {
		jeanFilesFail(w, errors.New("ce n'est pas un dossier"))
		return
	}
	root := jeanFilesRoot()
	var failed []string
	done := 0
	for _, rel := range req.Paths {
		from, err := jeanFilesPath(rel)
		switch {
		case err != nil:
		case jeanIsRoot(root, from) || jeanWithin(from, to):
			err = errors.New("un dossier ne peut pas aller dans lui-même")
		case filepath.Dir(from) == to:
			continue // déjà là
		default:
			err = os.Rename(from, filepath.Join(to, jeanFreeName(to, filepath.Base(from))))
		}
		if err != nil {
			failed = append(failed, rel+" : "+err.Error())
			continue
		}
		done++
	}
	if done > 0 {
		jeanEmbedKick() // réindexé sous son nouveau chemin
	}
	if len(failed) > 0 {
		sendJSON(w, 400, map[string]any{"ok": false, "done": done, "error": strings.Join(failed, " ; ")})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "done": done})
}

// handleJeanFilesRename : POST {path, name} → nouveau nom dans le même dossier.
// Un fichier renommé sans extension garde la sienne : « photo » pour
// « IMG_1.jpeg » donne « photo.jpeg ».
func handleJeanFilesRename(w http.ResponseWriter, r *http.Request) {
	req, ok := jeanFilesDecode(w, r)
	if !ok {
		return
	}
	from, err := jeanFilesPath(req.Path)
	if err != nil {
		jeanFilesFail(w, err)
		return
	}
	name, err := jeanCleanName(req.Name)
	info, serr := os.Stat(from)
	switch {
	case err != nil:
	case jeanIsRoot(jeanFilesRoot(), from):
		err = errors.New("le dossier d'AJEAN garde son nom")
	case serr != nil:
		err = errors.New("introuvable")
	}
	if err != nil {
		jeanFilesFail(w, err)
		return
	}
	if !info.IsDir() && filepath.Ext(name) == "" {
		name += filepath.Ext(from)
	}
	to := filepath.Join(filepath.Dir(from), name)
	if to != from {
		// Jamais par-dessus un autre fichier ; le même sous une autre casse est un
		// renommage (sous Windows, « a » et « A » sont un seul fichier).
		if other, err := os.Stat(to); err == nil && !os.SameFile(info, other) {
			jeanFilesFail(w, errors.New(name+" existe déjà ici"))
			return
		}
		if err := os.Rename(from, to); err != nil {
			jeanFilesFail(w, err)
			return
		}
		jeanEmbedKick() // réindexé sous son nouveau nom
	}
	sendJSON(w, 200, map[string]any{"ok": true, "name": name})
}

// handleJeanFilesSearch : POST {q} → les fichiers qui en parlent. Par le sens
// quand le moteur répond, sinon par le nom.
func handleJeanFilesSearch(w http.ResponseWriter, r *http.Request) {
	req, ok := jeanFilesDecode(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(req.Q)
	if q == "" {
		sendJSON(w, 200, map[string]any{"ok": true, "hits": []jeanFileHit{}})
		return
	}
	if jeanEmbedEnabled() {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		vs, err := jeanEmbed(ctx, []string{jeanEmbedQueryText(q)})
		cancel()
		if err == nil {
			hits := jeanFileHits(vs[0], jeanSemanticFloor, 1, 30)
			for i := range hits {
				hits[i].Passage = clipRunes(squashSpaces(hits[i].Passage), 240)
			}
			if hits == nil {
				hits = []jeanFileHit{}
			}
			sendJSON(w, 200, map[string]any{"ok": true, "hits": hits, "by": "meaning"})
			return
		}
	}
	// Repli : les fichiers dont le chemin contient tous les mots.
	terms := uniqueTerms(foldSearch(q))
	hits := []jeanFileHit{}
	if root := jeanFilesRoot(); root != "" && len(terms) > 0 {
		jeanFilesWalk(root, func(p string, d fs.DirEntry) {
			rel := jeanRelSlash(root, p)
			f := foldSearch(rel)
			for _, t := range terms {
				if !strings.Contains(f, t) {
					return
				}
			}
			if len(hits) < 30 {
				hits = append(hits, jeanFileHit{Rel: rel, Kind: jeanFileKind(rel)})
			}
		})
	}
	sendJSON(w, 200, map[string]any{"ok": true, "hits": hits, "by": "name"})
}
