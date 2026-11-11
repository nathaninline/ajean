package ajean

// jean_files.go — les FICHIERS de Jean, retrouvés par ce qu'ils contiennent.
//
// Son dossier de travail (jean-workspace) se parcourt depuis l'app (gestionnaire
// de fichiers, jean_files_web.go) : ce que l'utilisateur y dépose, ce que Jean y
// fabrique. Tout y est indexé par le SENS, en fond, comme le journal : le texte
// des fichiers texte, des PDF et des documents Word, et pour une image ce que le
// modèle y voit (personnes, lieux, objets, texte visible). jean_search et le
// rappel retrouvent ainsi un fichier par son contenu, pas seulement par son nom.
// Repris de Sillage (memory/files.go).
//
// Garde-fous :
//   - l'index n'avance que quand aucune réponse n'est en cours (même boucle que
//     le journal, jean_embed.go) ; un fichier n'est relu que s'il a changé ;
//   - une image n'est décrite qu'après jeanFilesIdle sans activité du modèle : la
//     décrire passe par le modèle de chat, et une requête à part lui ferait
//     perdre le cache de la conversation en pleine discussion. La description est
//     coupée dès qu'un message arrive ;
//   - fichiers et dossiers cachés, liens symboliques, environnements Python,
//     node_modules et caches ne sont pas indexés (des milliers de fichiers qui
//     ne disent rien de l'utilisateur).

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	jeanFilesIndexFile = "files-index.json"
	jeanFileChunkChars = 1000           // un passage d'un fichier, vectorisé à part
	jeanFileMaxChunks  = 40             // les ~40 000 premiers caractères d'un fichier
	jeanFileTextMax    = 8 << 20        // fichier texte plus gros : son nom seul
	jeanFileImageMax   = maxVisionBytes // image plus lourde : son nom seul
	jeanFileDescribe   = 2 * time.Minute
	jeanFileTries      = 3                // lectures ratées avant de ne garder que le nom
	jeanFilesIdle      = 10 * time.Minute // sans activité du modèle, avant de décrire une image
	jeanFilesBudget    = 12               // fichiers par passe
	jeanFilesImages    = 3                // images décrites par passe (chacune prend le modèle un moment)
	jeanFileDescMax    = 1500             // une description plus longue est coupée
)

// jeanFilesRoot : le dossier parcouru et indexé. Variable pour les tests : le
// dossier de travail est figé au premier appel du process.
var jeanFilesRoot = jeanWorkspace

// jeanFileDoc : ce qu'on sait d'un fichier, valable tant que sa taille et sa
// date ne changent pas.
type jeanFileDoc struct {
	Size int64  `json:"size"`
	Mod  int64  `json:"mod"` // unix nanosecondes
	Kind string `json:"kind"`
	// Read : le contenu a été lu. Faux = connu par son nom seul (image sans
	// vision, PDF sans lecteur) : relu quand le moyen de le lire apparaît.
	Read   bool            `json:"read,omitempty"`
	Chunks []jeanFileChunk `json:"chunks"`
}

type jeanFileChunk struct {
	Text string    `json:"text"`
	Vec  string    `json:"vec"` // quantifié, comme le journal
	vec  []float32 // décodé une fois, pour les recherches
}

type jeanFilesStore struct {
	Tag   string                  `json:"tag"`
	Files map[string]*jeanFileDoc `json:"files"`
}

var jeanFiles struct {
	sync.Mutex
	index map[string]*jeanFileDoc // par chemin relatif (« / »), chargé une fois
	// failed : lectures ratées d'un fichier, par chemin et date ; après
	// jeanFileTries il est indexé par son nom.
	failed map[string]int
}

// jeanFilesLoadLocked lit l'index une fois. false si la mémoire est illisible
// (verrouillée) : rien ne doit alors être indexé, on écraserait le vrai index.
func jeanFilesLoadLocked() bool {
	if jeanFiles.index != nil {
		return true
	}
	s, err := jeanRead(jeanFilesIndexFile)
	if err != nil {
		return false
	}
	var st jeanFilesStore
	if s != "" && json.Unmarshal([]byte(s), &st) == nil && st.Tag == jeanEmbedTag && st.Files != nil {
		jeanFiles.index = st.Files
	} else {
		jeanFiles.index = map[string]*jeanFileDoc{} // vide, ou autre modèle : à refaire
	}
	if jeanFiles.failed == nil {
		jeanFiles.failed = map[string]int{}
	}
	return true
}

// jeanFilesReset oublie l'index en mémoire vive (tests, mémoire restaurée).
func jeanFilesReset() {
	jeanFiles.Lock()
	jeanFiles.index, jeanFiles.failed = nil, nil
	jeanFiles.Unlock()
}

// ---- ce qu'on lit d'un fichier --------------------------------------------------

var jeanTextExts = map[string]bool{}

func init() {
	for _, e := range strings.Fields("txt md markdown log csv tsv json jsonl yaml yml toml ini cfg conf env xml html htm css js mjs ts tsx jsx py go rs java kt c h cpp hpp cs rb php sh bash zsh ps1 bat sql lua swift r tex srt vtt") {
		jeanTextExts["."+e] = true
	}
}

// jeanFileKind : comment un fichier se lit, d'après son extension.
func jeanFileKind(name string) string {
	switch ext := strings.ToLower(filepath.Ext(name)); {
	case imageMime(name) != "":
		return "image"
	case ext == ".pdf":
		return "pdf"
	case ext == ".docx":
		return "docx"
	case jeanTextExts[ext]:
		return "text"
	}
	return "other"
}

var errJeanFilesBusy = errors.New("modèle occupé")

// jeanFileExtract : le texte d'un fichier pour l'index ("" s'il n'en a pas à
// donner). read=false : connu par son nom seul pour l'instant.
func jeanFileExtract(ctx context.Context, path, kind string, size int64) (text string, read bool, err error) {
	switch kind {
	case "image":
		if !jeanFilesLocalVision() || size > jeanFileImageMax {
			return "", false, nil
		}
		d, err := jeanDescribeImage(ctx, path)
		if err != nil {
			return "", false, err
		}
		return "Image : " + d, true, nil
	case "pdf":
		return jeanPdfText(ctx, path)
	case "docx":
		s, err := jeanDocxText(path)
		return s, err == nil, err
	case "text":
		if size > jeanFileTextMax {
			return "", true, nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", false, err
		}
		return jeanDecodeText(b), true, nil
	}
	return "", true, nil // rien de plus à en lire : son nom
}

// jeanPdfReader : de quoi lire un PDF. pdftotext (poppler) d'abord, sinon Python
// avec pypdf ; "" s'il n'y a ni l'un ni l'autre.
func jeanPdfReader() string {
	if p, err := exec.LookPath("pdftotext"); err == nil {
		return p
	}
	for _, n := range []string{"python3", "python"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// jeanPypdf : lit le texte des 80 premières pages avec pypdf (absent : erreur).
const jeanPypdf = "import sys\nfrom pypdf import PdfReader\nr = PdfReader(sys.argv[1])\nsys.stdout.write('\\n\\n'.join((p.extract_text() or '') for p in r.pages[:80]))\n"

// jeanPdfHasPypdf : pypdf est-il installé ? Vérifié une fois (dix secondes de
// Python à chaque PDF pour apprendre qu'il manque, ce serait du gâchis).
var jeanPdfHasPypdf = sync.OnceValue(func() bool {
	py := ""
	for _, n := range []string{"python3", "python"} {
		if p, err := exec.LookPath(n); err == nil {
			py = p
			break
		}
	}
	if py == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, "-E", "-c", "import pypdf")
	cmd.Dir = AjeanHome()
	hideCmd(cmd)
	return cmd.Run() == nil
})

func jeanPdfText(ctx context.Context, path string) (string, bool, error) {
	bin := jeanPdfReader()
	if bin == "" {
		return "", false, nil // aucun lecteur : son nom seul
	}
	var cmd *exec.Cmd
	if strings.HasPrefix(strings.ToLower(filepath.Base(bin)), "pdftotext") {
		cmd = exec.CommandContext(ctx, bin, "-enc", "UTF-8", path, "-")
	} else {
		if !jeanPdfHasPypdf() {
			return "", false, nil
		}
		// -E : sans variables PYTHON* ; lancé depuis le dossier d'AJEAN, pas depuis
		// un dossier où traînerait un pypdf.py.
		cmd = exec.CommandContext(ctx, bin, "-E", "-c", jeanPypdf, path)
		cmd.Dir = AjeanHome()
	}
	hideCmd(cmd)
	var out bytes.Buffer
	cmd.Stdout = &jeanLimitWriter{w: &out, n: jeanFileTextMax}
	if err := cmd.Run(); err != nil {
		if out.Len() > 0 {
			return out.String(), true, nil // coupé au plafond, ou une page illisible : ce qui a été lu compte
		}
		return "", false, err
	}
	return out.String(), true, nil
}

// jeanLimitWriter garde les n premiers octets et ignore le reste (sans erreur :
// le processus doit pouvoir finir).
type jeanLimitWriter struct {
	w io.Writer
	n int
}

func (l *jeanLimitWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		_, _ = l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}

// jeanDecodeText : le contenu d'un fichier texte en UTF-8 ("" si ce n'est pas du
// texte). Windows écrit aussi d'autres encodages : l'UTF-16 avec BOM du « > » de
// PowerShell 5.1, l'ANSI (cp1252) des vieux outils.
func jeanDecodeText(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		b = b[3:]
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return jeanUTF16(b[2:], binary.LittleEndian)
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return jeanUTF16(b[2:], binary.BigEndian)
	}
	if utf8.Valid(b) {
		return string(b)
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return "" // binaire sous un nom de texte, ou UTF-16 sans BOM : pas de devinette
	}
	var s strings.Builder
	for _, c := range b {
		if r, ok := jeanCP1252[c]; ok {
			s.WriteRune(r)
		} else {
			s.WriteRune(rune(c)) // le reste de cp1252 est du Latin-1
		}
	}
	return s.String()
}

func jeanUTF16(b []byte, order binary.ByteOrder) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = order.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

// jeanCP1252 : là où la page ANSI de Windows diffère du Latin-1.
var jeanCP1252 = map[byte]rune{
	0x80: '€', 0x82: '‚', 0x83: 'ƒ', 0x84: '„', 0x85: '…', 0x86: '†', 0x87: '‡', 0x88: 'ˆ', 0x89: '‰',
	0x8A: 'Š', 0x8B: '‹', 0x8C: 'Œ', 0x8E: 'Ž', 0x91: '‘', 0x92: '’', 0x93: '“', 0x94: '”', 0x95: '•',
	0x96: '–', 0x97: '—', 0x98: '˜', 0x99: '™', 0x9A: 'š', 0x9B: '›', 0x9C: 'œ', 0x9E: 'ž', 0x9F: 'Ÿ',
}

var (
	jeanDocxBreak = regexp.MustCompile(`</w:p>|<w:br/>|<w:tab/>`)
	jeanDocxTag   = regexp.MustCompile(`<[^>]+>`)
)

// jeanDocxText : le texte d'un document Word (word/document.xml dans le zip).
func jeanDocxText(path string) (string, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer z.Close()
	for _, zf := range z.File {
		if zf.Name != "word/document.xml" {
			continue
		}
		r, err := zf.Open()
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(io.LimitReader(r, jeanFileTextMax))
		r.Close()
		if err != nil {
			return "", err
		}
		return html.UnescapeString(jeanDocxTag.ReplaceAllString(jeanDocxBreak.ReplaceAllString(string(b), "\n"), "")), nil
	}
	return "", errors.New("pas un document Word")
}

// jeanDescribePrompt : ce qu'on demande au modèle pour l'index d'une image.
const jeanDescribePrompt = "Décris cette image pour un index de recherche : ce qu'elle montre (personnes, lieux, objets, scène, ambiance) " +
	"et tout texte visible, recopié tel quel. Trois phrases au plus, sans préambule."

// jeanDescribeImage demande au modèle de chat ce que montre une image. Coupée
// (errJeanFilesBusy) dès qu'une réponse, une tâche, une révision ou un sommeil
// démarre : l'utilisateur passe avant l'index.
func jeanDescribeImage(ctx context.Context, path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	b, mime := prepareImageForModel(b, imageMime(path))
	ctx, cancel := context.WithTimeout(ctx, jeanFileDescribe)
	defer cancel()
	var busy atomic.Bool
	go func() {
		t := time.NewTicker(300 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if jeanModelBusy() {
					busy.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	msgs := []Message{{Role: "user", Content: []map[string]any{{"type": "text", "text": jeanDescribePrompt}, imageURLPart(b, mime)}}}
	var out strings.Builder
	_, err = runChat(ctx, msgs, 0.2, Caps{}, func(ev StreamEvent) bool {
		if ev.Content != "" {
			out.WriteString(ev.Content)
			if out.Len() > jeanFileDescMax {
				cancel()
				return false
			}
		}
		return true
	}, nil)
	if busy.Load() {
		return "", errJeanFilesBusy
	}
	d := strings.TrimSpace(out.String())
	if d == "" {
		if err == nil {
			err = errors.New("description vide")
		}
		return "", err
	}
	return clipRunes(squashSpaces(d), jeanFileDescMax), nil
}

// jeanFilesLocalVision : le modèle voit ET tourne sur cette machine ou sur le
// réseau local. Un GPU cloud (facturé à la minute) ou une API distante (facturée
// au jeton) ne décrit pas des centaines d'images en fond sans qu'on le sache :
// l'image reste alors connue par son nom.
func jeanFilesLocalVision() bool {
	if !visionEnabled() {
		return false
	}
	ep := resolveChatEndpoint()
	if ep.Cloud {
		return false
	}
	if !ep.External {
		return true
	}
	u, err := url.Parse(ep.URL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

// jeanModelBusy : le modèle de chat sert quelqu'un (réponse, tâche, révision,
// sommeil). L'index des images attend.
func jeanModelBusy() bool {
	if jeanEmbedBusy() || jeanSleeping.Load() {
		return true
	}
	jeanReflect.mu.Lock()
	defer jeanReflect.mu.Unlock()
	return jeanReflect.cancel != nil
}

// jeanFilesBusyAt : la dernière fois que le modèle a été vu occupé (unix ns).
var (
	jeanFilesBusyAt    atomic.Int64
	jeanFilesWatchOnce sync.Once
	jeanFilesTimer     atomic.Bool // une relance déjà prévue pour les images en attente
)

func jeanFilesWatchBusy() {
	jeanFilesBusyAt.Store(time.Now().UnixNano()) // au démarrage, on laisse le temps de s'installer
	go func() {
		for {
			time.Sleep(5 * time.Second)
			if jeanModelBusy() {
				jeanFilesBusyAt.Store(time.Now().UnixNano())
			}
		}
	}()
}

func jeanFilesQuietFor() time.Duration {
	jeanFilesWatchOnce.Do(jeanFilesWatchBusy)
	return time.Since(time.Unix(0, jeanFilesBusyAt.Load()))
}

// jeanFilesLater relance l'indexeur quand le modèle sera resté assez longtemps
// au repos pour décrire les images en attente.
func jeanFilesLater() {
	if !jeanFilesTimer.CompareAndSwap(false, true) {
		return
	}
	wait := max(time.Minute, jeanFilesIdle-jeanFilesQuietFor()+5*time.Second)
	time.AfterFunc(wait, func() {
		jeanFilesTimer.Store(false)
		jeanEmbedKick()
	})
}

// jeanFileChunks coupe un texte en passages, en fin de paragraphe ou de ligne
// quand c'est possible.
func jeanFileChunks(text string) []string {
	r := []rune(strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n")))
	var out []string
	for len(r) > 0 && len(out) < jeanFileMaxChunks {
		if len(r) <= jeanFileChunkChars {
			if p := strings.TrimSpace(string(r)); p != "" {
				out = append(out, p)
			}
			break
		}
		cut := jeanFileChunkChars
		for _, sep := range []string{"\n\n", "\n", ". "} { // dernière fin de paragraphe, de ligne ou de phrase dans la seconde moitié
			if i := jeanLastIndex(r[:jeanFileChunkChars], sep); i > jeanFileChunkChars/2 {
				cut = i + len([]rune(sep))
				break
			}
		}
		if p := strings.TrimSpace(string(r[:cut])); p != "" {
			out = append(out, p)
		}
		r = r[cut:]
	}
	return out
}

func jeanLastIndex(r []rune, sep string) int {
	s := []rune(sep)
	for i := len(r) - len(s); i >= 0; i-- {
		if string(r[i:i+len(s)]) == sep {
			return i
		}
	}
	return -1
}

// ---- le parcours -------------------------------------------------------------------

// jeanFilesSkipDirs : des dossiers d'outils, pas des fichiers de l'utilisateur.
var jeanFilesSkipDirs = map[string]bool{"node_modules": true, "__pycache__": true, "site-packages": true, "dist-packages": true, "venv": true}

// jeanFilesSkipDir : un dossier que l'indexeur ne lit pas. Un environnement Python
// se reconnaît à son pyvenv.cfg (vu sur le 127 : deux environnements, 6 000
// fichiers, presque tout le dossier de Jean).
func jeanFilesSkipDir(path, name string) bool {
	return strings.HasPrefix(name, ".") || jeanFilesSkipDirs[strings.ToLower(name)] || isFile(filepath.Join(path, "pyvenv.cfg"))
}

// jeanFilesWalk appelle fn pour chaque fichier que l'indexeur lit. Pas les liens
// symboliques (l'un peut mener hors du dossier). Le décompte et l'index
// parcourent les mêmes fichiers.
func jeanFilesWalk(root string, fn func(path string, d fs.DirEntry)) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
		case path == root:
		case d.IsDir():
			if jeanFilesSkipDir(path, d.Name()) {
				return filepath.SkipDir
			}
		case strings.HasPrefix(d.Name(), "."), d.Type()&fs.ModeSymlink != 0, !d.Type().IsRegular():
		default:
			fn(path, d)
		}
		return nil
	})
}

func jeanRelSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// ---- l'indexation --------------------------------------------------------------------

// jeanFilesIndexOnce indexe au plus budget fichiers nouveaux ou changés et oublie
// ceux qui ont disparu. Renvoie le nombre de fichiers indexés.
func jeanFilesIndexOnce(ctx context.Context, budget int) (int, error) {
	root := jeanFilesRoot()
	if root == "" || !isDir(root) {
		return 0, nil
	}
	type todo struct {
		rel, path, kind string
		info            fs.FileInfo
	}
	var work []todo
	seen := map[string]bool{}
	vision := jeanFilesLocalVision()
	pdf := jeanPdfReader() != ""
	jeanFiles.Lock()
	if !jeanFilesLoadLocked() {
		jeanFiles.Unlock()
		return 0, errors.New("mémoire d'AJEAN illisible (verrouillée)")
	}
	jeanFilesWalk(root, func(path string, d fs.DirEntry) {
		info, err := d.Info()
		if err != nil {
			return
		}
		rel := jeanRelSlash(root, path)
		seen[rel] = true
		kind := jeanFileKind(rel)
		doc := jeanFiles.index[rel]
		changed := doc == nil || doc.Size != info.Size() || doc.Mod != info.ModTime().UnixNano()
		// Connu par son nom seul, et désormais lisible : relu.
		later := !changed && !doc.Read && (kind == "image" && vision && info.Size() <= jeanFileImageMax || kind == "pdf" && pdf) &&
			jeanFiles.failed[rel+"\x00"+info.ModTime().String()] < jeanFileTries
		if changed || later {
			work = append(work, todo{rel, path, kind, info})
		}
	})
	removed := false
	for rel := range jeanFiles.index {
		if !seen[rel] {
			delete(jeanFiles.index, rel)
			removed = true
		}
	}
	jeanFiles.Unlock()
	sort.Slice(work, func(i, j int) bool { return work[i].info.ModTime().After(work[j].info.ModTime()) }) // les plus récents d'abord

	done, images, waiting := 0, 0, false
	var firstErr error
	for _, w := range work {
		if done >= budget || ctx.Err() != nil {
			break
		}
		if jeanEmbedBusy() {
			break // une réponse a démarré : le processeur et la RAM sont à elle
		}
		if w.kind == "image" && vision && w.info.Size() <= jeanFileImageMax {
			if images >= jeanFilesImages || jeanFilesQuietFor() < jeanFilesIdle || jeanModelBusy() {
				waiting = true
				continue
			}
			images++
		}
		text, read, err := jeanFileExtract(ctx, w.path, w.kind, w.info.Size())
		if errors.Is(err, errJeanFilesBusy) {
			waiting = true
			break
		}
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			// Modèle occupé ou en panne, PDF illisible : réessayé aux passes
			// suivantes, quelques fois ; puis le fichier est connu par son nom,
			// jusqu'à ce qu'il change.
			key := w.rel + "\x00" + w.info.ModTime().String()
			jeanFiles.Lock()
			jeanFiles.failed[key]++
			tries := jeanFiles.failed[key]
			jeanFiles.Unlock()
			if tries < jeanFileTries {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			text, read = "", false
		}
		passages := jeanFileChunks(text)
		if len(passages) == 0 {
			passages = []string{""}
		}
		inputs := make([]string, len(passages))
		for i, p := range passages {
			inputs[i] = "title: " + w.rel + " | text: " + p
		}
		var vecs [][]float32
		for i := 0; i < len(inputs); i += jeanEmbedBatch {
			if i > 0 && jeanEmbedBusy() {
				break // une réponse a démarré : ce fichier sera repris à la prochaine passe
			}
			vs, err := jeanEmbed(ctx, inputs[i:min(i+jeanEmbedBatch, len(inputs))])
			if err != nil {
				jeanFilesSave(done > 0 || removed)
				return done, err // le moteur n'est pas là : à la prochaine passe
			}
			vecs = append(vecs, vs...)
		}
		if len(vecs) != len(passages) {
			break
		}
		doc := &jeanFileDoc{Size: w.info.Size(), Mod: w.info.ModTime().UnixNano(), Kind: w.kind, Read: read}
		for i, p := range passages {
			doc.Chunks = append(doc.Chunks, jeanFileChunk{Text: p, Vec: quantVec(vecs[i]), vec: vecs[i]})
		}
		jeanFiles.Lock()
		jeanFiles.index[w.rel] = doc
		jeanFiles.Unlock()
		done++
	}
	if waiting {
		jeanFilesLater()
	}
	if err := jeanFilesSave(done > 0 || removed); err != nil {
		return done, err
	}
	if done == 0 && firstErr != nil {
		return 0, firstErr
	}
	return done, nil
}

// jeanFilesSave écrit l'index (chiffré comme la mémoire) quand il a changé.
func jeanFilesSave(changed bool) error {
	if !changed {
		return nil
	}
	jeanFiles.Lock()
	raw, err := json.Marshal(jeanFilesStore{Tag: jeanEmbedTag, Files: jeanFiles.index})
	jeanFiles.Unlock()
	if err != nil {
		return err
	}
	jeanMu.Lock()
	err = jeanWrite(jeanFilesIndexFile, string(raw))
	jeanMu.Unlock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[jean] index des fichiers non écrit : %v\n", err)
	}
	return err
}

// ---- la recherche ------------------------------------------------------------------

// jeanFileHit : un fichier qui correspond à une requête, par son meilleur passage.
type jeanFileHit struct {
	Rel     string  `json:"path"` // relatif au dossier de Jean
	Kind    string  `json:"kind"`
	Passage string  `json:"passage"`
	Score   float32 `json:"score"`
}

// jeanFileHits : les fichiers dont le meilleur passage dépasse floor et reste à
// moins de band du meilleur fichier, le meilleur d'abord.
func jeanFileHits(q []float32, floor, band float32, limit int) []jeanFileHit {
	if q == nil {
		return nil
	}
	jeanFiles.Lock()
	if !jeanFilesLoadLocked() {
		jeanFiles.Unlock()
		return nil
	}
	var hits []jeanFileHit
	for rel, doc := range jeanFiles.index {
		best := jeanFileHit{Score: -1}
		for i := range doc.Chunks {
			c := &doc.Chunks[i]
			if c.vec == nil {
				c.vec = dequantVec(c.Vec) // une fois : chaque recherche les lit tous
			}
			if s := dotVec(q, c.vec); s > best.Score {
				best = jeanFileHit{Rel: rel, Kind: doc.Kind, Passage: c.Text, Score: s}
			}
		}
		if best.Score >= floor {
			hits = append(hits, best)
		}
	}
	jeanFiles.Unlock()
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	for k, h := range hits {
		if h.Score < hits[0].Score-band || k == limit {
			return hits[:k]
		}
	}
	return hits
}

// jeanFormatFileHits : les lignes des fichiers trouvés, pour le modèle. Le chemin
// est relatif à son dossier de travail (ses outils le résolvent là).
func jeanFormatFileHits(hits []jeanFileHit, passage int) string {
	var b strings.Builder
	for _, h := range hits {
		b.WriteString("- [file · " + h.Kind + "] " + h.Rel)
		if p := squashSpaces(h.Passage); p != "" {
			b.WriteString(": " + clipRunes(p, passage))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// jeanFilesCount : fichiers indexés, et fichiers que l'indexeur lit.
func jeanFilesCount() (indexed, total int) {
	root := jeanFilesRoot()
	if root == "" {
		return 0, 0
	}
	jeanFiles.Lock()
	if jeanFilesLoadLocked() {
		indexed = len(jeanFiles.index)
	}
	jeanFiles.Unlock()
	jeanFilesWalk(root, func(string, fs.DirEntry) { total++ })
	return min(indexed, total), total
}
