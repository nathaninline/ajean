package ajean

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// quantSegRe matches a single name segment that looks like a GGUF quantization
// token: Q8_0, Q6_K, Q5_K_M, Q4_K_XL, IQ4_XS, IQ3_XXS, Q4, 4bpw, BF16, F16…
var quantSegRe = regexp.MustCompile(`(?i)^(I?Q\d+(_[A-Za-z0-9]+)*|\d+BPW|BF16|FP16|F16|FP32|F32)$`)

// quantFromName extracts a quantization tag from a model filename by splitting
// on '-' and '.' and keeping the longest segment that looks like a quant token.
// Returns "" when nothing matches.
func quantFromName(name string) string {
	base := name
	if dot := strings.LastIndexByte(base, '.'); dot >= 0 && strings.EqualFold(base[dot:], ".gguf") {
		base = base[:dot]
	}
	segs := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '.' })
	best := ""
	for _, seg := range segs {
		if quantSegRe.MatchString(seg) && len(seg) > len(best) {
			best = seg
		}
	}
	return strings.ToUpper(best)
}

// presetReasoning returns the raw REASONING= value from a preset's config.env
// body, or "" if absent.
func presetReasoning(content string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		i := strings.IndexByte(s, '=')
		if i < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(s[:i]), "REASONING") {
			return unquoteValue(strings.TrimSpace(s[i+1:]))
		}
	}
	return ""
}

// reasoningActive reports whether a REASONING= value enables reasoning. backend_serve.go
// passes the flag whenever the value is non-empty, but an explicit off/none is
// treated here as disabled so the UI badge isn't misleading.
func reasoningActive(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "off", "none", "false", "0", "no", "disable", "disabled":
		return false
	}
	return true
}

// detectQuant returns the quantization tag for a preset: an explicit QUANT= line
// (manual override, with or without a leading '#') wins; otherwise it is
// auto-detected from the MODEL= filename. Returns "" when unknown.
func detectQuant(content string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		i := strings.IndexByte(s, '=')
		if i >= 0 && strings.EqualFold(strings.TrimSpace(s[:i]), "QUANT") {
			if v := unquoteValue(strings.TrimSpace(s[i+1:])); v != "" {
				return strings.ToUpper(v)
			}
		}
	}
	return quantFromName(baseName(modelFromPresetContent(content)))
}

// presetCtx returns the context window of a preset formatted for the UI badge
// (ex. "32K", "128K", "1M"). Reads the CTX= line, falling back to the same
// 32768 default the engine uses (backend_serve.go). Returns "" if CTX is set
// to something non-numeric.
func presetCtx(content string) string {
	raw := "32768"
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		i := strings.IndexByte(s, '=')
		if i < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(s[:i]), "CTX") {
			if v := unquoteValue(strings.TrimSpace(s[i+1:])); v != "" {
				raw = v
			}
			break
		}
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return ""
	}
	return fmtCtx(n)
}

// fmtCtx formats a token count for the badge in decimal-K/M (ce que l'utilisateur
// tape : 40000→"40K", 128000→"128K", 1000000→"1M"). Base 1000, arrondi à l'entier
// pour les K (pas de "39.1K" quand on a saisi 40000) et une décimale pour les M.
func fmtCtx(n int) string {
	switch {
	case n >= 1000000:
		return trimDot(float64(n)/1000000) + "M"
	case n >= 1000:
		return strconv.Itoa((n+500)/1000) + "K"
	default:
		return strconv.Itoa(n)
	}
}

// trimDot renders a float with at most one decimal, dropping a trailing ".0".
func trimDot(f float64) string {
	s := strconv.FormatFloat(f, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

// downloadDestPath resolves the destination of a downloaded model : le dossier
// demandé (vide = AJEAN_HOME) parmi les dossiers de modèles déclarés, en
// refusant tout ce qui en sortirait (path traversal).
func downloadDestPath(name, dir string) (string, error) {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", fmt.Errorf("nom de modèle invalide")
	}
	if !strings.HasSuffix(strings.ToLower(base), ".gguf") {
		return "", fmt.Errorf("seuls les fichiers .gguf sont acceptés")
	}
	d, err := resolveDownloadDir(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, base), nil
}

// modelFromPresetContent extracts the MODEL= value from a preset's config.env
// body (nom de fichier ou chemin absolu, tel quel), or "" if absent.
func modelFromPresetContent(content string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		i := strings.IndexByte(s, '=')
		if i < 0 {
			continue
		}
		if strings.TrimSpace(s[:i]) == "MODEL" {
			return unquoteValue(strings.TrimSpace(s[i+1:]))
		}
	}
	return ""
}

// deleteModelFile removes a .gguf file from one of the declared model folders
// after validating the name. Un modèle découpé emporte TOUTES ses tranches :
// n'effacer que la première laissait des dizaines de Go de fichiers que plus
// rien ne référence, et que rien ne sait plus supprimer depuis l'interface (les
// tranches suivantes n'y apparaissent pas).
func deleteModelFile(name string) error {
	p, err := resolveModelPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("modèle introuvable: %s", filepath.Base(p))
		}
		return err
	}
	dir := filepath.Dir(p)
	for _, n := range shardFamily(filepath.Base(p)) {
		_ = os.Remove(filepath.Join(dir, n)) // déjà supprimée ou absente = rien à faire
	}
	return nil
}

// handleModelDelete deletes a single .gguf from AJEAN_HOME.
func handleModelDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := deleteModelFile(req.Name); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// ---- Hugging Face downloads -------------------------------------------------

// dlState tracks a single in-flight (or finished) model download.
type dlState struct {
	Filename  string `json:"filename"`
	URL       string `json:"url"`
	Dir       string `json:"dir"` // dossier de destination
	Total     int64  `json:"total"`
	Done      int64  `json:"done"`
	Speed     int64  `json:"speed"` // bytes/s, smoothed over the last samples
	Conns     int    `json:"conns"` // parallel connections actually used
	Parts     int    `json:"parts"` // nombre de tranches (1 pour un modèle en un seul fichier)
	Part      int    `json:"part"`  // tranche en cours (1-based)
	Finished  bool   `json:"finished"`
	Canceled  bool   `json:"canceled"`
	Err       string `json:"error"`
	StartedAt int64  `json:"started_at"`

	cancel context.CancelFunc `json:"-"` // set while in flight, cleared on finish
}

var (
	dlMu        sync.Mutex
	dlDownloads = map[string]*dlState{} // keyed by filename
)

// dlClient is shared by all download workers so connections to the HF CDN are
// pooled and reused across chunks instead of re-handshaking TLS each time.
var dlClient = &http.Client{
	Timeout: 0, // large files: no overall timeout
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   64,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// HTTP/1.1: truly parallel sockets, no shared h2 flow-control window.
		ForceAttemptHTTP2: false,
		WriteBufferSize:   64 << 10,
		ReadBufferSize:    256 << 10,
	},
}

// dlConns is the number of parallel range requests used per download.
// Overridable with AJEAN_DL_CONNS (1 disables parallelism).
func dlConns() int {
	n := 8
	if v := os.Getenv("AJEAN_DL_CONNS"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			n = p
		}
	}
	if n > 16 {
		n = 16
	}
	return n
}

// dlMinChunk is the smallest slice worth a dedicated connection (16 MiB), so a
// small file doesn't get split into a swarm of tiny requests.
const dlMinChunk = 16 << 20

// normalizeHFURL turns a Hugging Face "blob" page URL into a direct "resolve"
// download URL, and leaves already-direct URLs untouched. Returns the URL to
// fetch and the target filename.
func normalizeHFURL(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("lien vide")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("lien invalide: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("lien invalide (http/https attendu)")
	}
	// huggingface.co/<repo>/blob/<rev>/<file> → /resolve/<rev>/<file>
	if strings.Contains(u.Host, "huggingface.co") {
		u.Path = strings.Replace(u.Path, "/blob/", "/resolve/", 1)
	}
	name := path.Base(u.Path)
	if name == "" || name == "/" || name == "." {
		return "", "", fmt.Errorf("impossible de déduire le nom du fichier depuis le lien")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".gguf") {
		return "", "", fmt.Errorf("le lien doit pointer vers un fichier .gguf")
	}
	return u.String(), name, nil
}

// handleModelDownload kicks off a background download of a .gguf from a URL
// (typically Hugging Face) into AJEAN_HOME. Progress is polled via
// /api/models/download/status.
func handleModelDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	dlURL, name, err := normalizeHFURL(req.URL)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Modèle découpé : le lien ne désigne qu'une tranche, on rapatrie la famille.
	// Le modèle porte le nom de sa PREMIÈRE tranche — c'est elle qu'on passe à
	// llama-server, et c'est donc elle qui identifie le téléchargement.
	urls, names := shardURLSet(dlURL, name)
	name = names[0]
	dests := make([]string, len(names))
	for i, n := range names {
		if dests[i], err = downloadDestPath(n, req.Dir); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(dests[0]), 0o755); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "dossier de destination inaccessible : " + err.Error()})
		return
	}

	// Tranches déjà là : on ne les retélécharge pas. C'est aussi ce qui permet de
	// relancer un téléchargement en plusieurs fichiers interrompu à la deuxième
	// tranche sans repayer les 15 Go de la première.
	var todoURLs, todoDests []string
	for i := range names {
		if st, err := os.Stat(dests[i]); err == nil && !st.IsDir() {
			continue
		}
		todoURLs = append(todoURLs, urls[i])
		todoDests = append(todoDests, dests[i])
	}

	dlMu.Lock()
	if st, ok := dlDownloads[name]; ok && !st.Finished {
		dlMu.Unlock()
		sendJSON(w, 409, map[string]any{"ok": false, "error": "téléchargement déjà en cours pour " + name})
		return
	}
	if len(todoURLs) == 0 {
		dlMu.Unlock()
		sendJSON(w, 409, map[string]any{"ok": false, "error": "le modèle existe déjà: " + name})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	st := &dlState{
		Filename: name, URL: urls[0], Dir: filepath.Dir(dests[0]),
		Parts: len(todoURLs), StartedAt: time.Now().Unix(), cancel: cancel,
	}
	dlDownloads[name] = st
	dlMu.Unlock()

	go runDownloadSet(ctx, st, todoURLs, todoDests)
	sendJSON(w, 200, map[string]any{"ok": true, "filename": name, "parts": len(todoURLs)})
}

// dlSpaceMargin est la marge laissée libre après le téléchargement : un disque
// rempli à ras bord met en danger tout le reste (logs, .part d'un autre
// modèle, swap).
const dlSpaceMargin = 256 << 20

// checkDiskSpace refuse le téléchargement si le fichier ne tient pas dans le
// dossier visé. free < 0 = mesure impossible : on laisse passer plutôt que de
// bloquer sur un système de fichiers exotique.
func checkDiskSpace(dir string, size int64) error {
	free := diskFree(dir)
	if size <= 0 || free < 0 {
		return nil
	}
	if free < size+dlSpaceMargin {
		return fmt.Errorf("espace insuffisant sur %s : %s libres, %s nécessaires", dir, humanBytes(free), humanBytes(size+dlSpaceMargin))
	}
	return nil
}

// humanBytes formate une taille en Go/Mo pour les messages d'erreur.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f Go", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f Mo", float64(n)/float64(1<<20))
	default:
		return fmt.Sprintf("%d o", n)
	}
}

// handleModelDownloadProbe renseigne l'UI avant de lancer quoi que ce soit :
// taille du fichier distant, espace libre du dossier visé, et si ça tient. Une
// seule requête d'un octet côté CDN, donc c'est gratuit.
func handleModelDownloadProbe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	dlURL, name, err := normalizeHFURL(req.URL)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	dir, err := resolveDownloadDir(req.Dir)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Un modèle découpé se sonde EN ENTIER : annoncer les 15 Go de la première
	// tranche alors qu'il en faut 45 sur le disque, c'est promettre que ça tient
	// puis échouer au deux tiers du transfert.
	urls, names := shardURLSet(dlURL, name)
	name = names[0]
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var total int64
	for _, u := range urls {
		n, _, err := dlProbe(ctx, u)
		if err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		total += n
	}
	out := map[string]any{"ok": true, "filename": name, "dir": dir, "size": total, "free": diskFree(dir), "enough": true, "parts": len(urls)}
	if err := checkDiskSpace(dir, total); err != nil {
		out["enough"] = false
		out["error"] = err.Error()
	}
	sendJSON(w, 200, out)
}

// dlRequest builds a GET for the download URL, carrying the HF token when set
// (gated/private repos) and an optional Range header.
func dlRequest(ctx context.Context, dlURL, rng string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", dlURL, nil)
	if err != nil {
		return nil, err
	}
	// HF gated/private repos may need a token; reuse the same key store if set.
	if k := os.Getenv("HF_TOKEN"); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	req.Header.Set("User-Agent", "ajean/"+Version)
	req.Header.Set("Accept-Encoding", "identity") // never gzip a .gguf: it breaks ranges
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	return req, nil
}

// contentRangeTotal parses the total size out of a "bytes 0-0/12345" header.
func contentRangeTotal(v string) int64 {
	i := strings.LastIndexByte(v, '/')
	if i < 0 {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v[i+1:]), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// dlProbe asks the server for the first byte to learn the total size and
// whether ranges are supported (206 + Content-Range).
func dlProbe(ctx context.Context, dlURL string) (total int64, ranged bool, err error) {
	req, err := dlRequest(ctx, dlURL, "bytes=0-0")
	if err != nil {
		return 0, false, err
	}
	resp, err := dlClient.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch resp.StatusCode {
	case 206:
		if t := contentRangeTotal(resp.Header.Get("Content-Range")); t > 0 {
			return t, true, nil
		}
		return 0, false, nil
	case 200:
		// Server ignored the Range: single stream, ContentLength is the size.
		return resp.ContentLength, false, nil
	default:
		return 0, false, fmt.Errorf("HTTP %d depuis la source", resp.StatusCode)
	}
}

// runDownloadSet fetches one model, qui peut tenir en plusieurs fichiers (un
// GGUF découpé en tranches). Les tranches se suivent SÉQUENTIELLEMENT — chacune
// sature déjà le lien à elle seule grâce aux connexions parallèles, les mener de
// front ne ferait que multiplier les fichiers à jeter en cas d'annulation.
// La progression publiée (Done / Total) couvre l'ENSEMBLE : une barre unique du
// début à la fin, pas trois barres qui repartent de zéro.
//
// Annulation ou échec : toutes les tranches déjà écrites par CE téléchargement
// sont supprimées. Un modèle amputé d'une tranche ne démarre pas ; le laisser
// sur le disque n'offrirait qu'un modèle mort et des dizaines de Go occupés.
func runDownloadSet(ctx context.Context, st *dlState, urls, dests []string) {
	var done int64 // atomique : octets écrits, toutes tranches confondues

	finish := func(e error) {
		dlMu.Lock()
		switch {
		case ctx.Err() != nil:
			st.Canceled = true
			st.Speed = 0
		case e != nil:
			st.Err = e.Error()
			st.Speed = 0
		default:
			st.Done = atomic.LoadInt64(&done)
		}
		st.Finished = true
		st.cancel = nil
		dlMu.Unlock()
	}

	// Sonde de TOUTES les tranches avant d'écrire quoi que ce soit : la place
	// disque se vérifie sur le total, pas tranche par tranche.
	totals := make([]int64, len(urls))
	rangeds := make([]bool, len(urls))
	var grand int64
	for i, u := range urls {
		n, ranged, err := dlProbe(ctx, u)
		if err != nil {
			finish(err)
			return
		}
		totals[i], rangeds[i] = n, ranged
		grand += n
	}
	// Vérification serveur : l'UI a déjà prévenu, mais rien ne garantit qu'elle
	// l'ait fait (autre client, disque rempli entre-temps).
	if err := checkDiskSpace(filepath.Dir(dests[0]), grand); err != nil {
		finish(err)
		return
	}

	dlMu.Lock()
	st.Total = grand
	st.Parts = len(urls)
	dlMu.Unlock()

	// Publication de la progression + vitesse lissée, une fois par seconde.
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		last, lastAt := int64(0), time.Now()
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				cur := atomic.LoadInt64(&done)
				dt := now.Sub(lastAt).Seconds()
				dlMu.Lock()
				st.Done = cur
				if dt > 0 {
					inst := int64(float64(cur-last) / dt)
					if st.Speed == 0 {
						st.Speed = inst
					} else {
						st.Speed = (st.Speed*2 + inst) / 3 // EMA, lisse les à-coups du CDN
					}
				}
				dlMu.Unlock()
				last, lastAt = cur, now
			}
		}
	}()

	var written []string // tranches menées à bien par CE téléchargement
	for i := range urls {
		dlMu.Lock()
		st.Part = i + 1
		dlMu.Unlock()
		if err := dlOnePart(ctx, st, urls[i], dests[i], totals[i], rangeds[i], &done); err != nil {
			close(stop)
			for _, p := range written {
				_ = os.Remove(p)
			}
			finish(err) // annulation comprise : le .part est supprimé dans tous les cas
			return
		}
		written = append(written, dests[i])
	}
	close(stop)
	finish(nil)
}

// dlOnePart télécharge UN fichier dans un .part puis le renomme au succès.
// Quand la source honore les plages d'octets (le CDN de Hugging Face le fait),
// le fichier est réparti sur plusieurs connexions écrites en place via WriteAt :
// c'est ce qui fait qu'un .gguf de plusieurs Go sature le lien au lieu de se
// traîner sur un seul flux TCP. done est incrémenté globalement (il compte pour
// tout le modèle, pas seulement pour cette tranche).
func dlOnePart(ctx context.Context, st *dlState, dlURL, dest string, total int64, ranged bool, done *int64) error {
	tmp := dest + ".part"
	conns := 1
	if ranged && total > 0 {
		conns = dlConns()
		if max := int((total + dlMinChunk - 1) / dlMinChunk); conns > max {
			conns = max
		}
		if conns < 1 {
			conns = 1
		}
	}
	dlMu.Lock()
	st.Conns = conns
	dlMu.Unlock()

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if conns > 1 {
		// Préallocation : le système de fichiers pose le fichier d'un bloc et les
		// WriteAt concurrents n'ont jamais à l'étendre en même temps.
		if err := f.Truncate(total); err != nil {
			f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := dlFetch(ctx, f, dlURL, total, conns, done); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// dlFetch writes the whole body into f, either as one stream or as `conns`
// parallel byte ranges. done is incremented atomically as bytes land on disk.
func dlFetch(ctx context.Context, f *os.File, dlURL string, total int64, conns int, done *int64) error {
	if conns <= 1 {
		return dlChunk(ctx, f, dlURL, 0, total-1, total <= 0, done)
	}
	size := total / int64(conns)
	var wg sync.WaitGroup
	errs := make([]error, conns)
	for i := 0; i < conns; i++ {
		start := int64(i) * size
		end := start + size - 1
		if i == conns-1 {
			end = total - 1
		}
		wg.Add(1)
		go func(i int, start, end int64) {
			defer wg.Done()
			errs[i] = dlChunk(ctx, f, dlURL, start, end, false, done)
		}(i, start, end)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// dlChunk downloads [start,end] into f at the right offset, retrying from where
// it stopped if the connection drops mid-chunk. With whole=true it streams the
// entire body sequentially (server without range support, unknown size).
func dlChunk(ctx context.Context, f *os.File, dlURL string, start, end int64, whole bool, done *int64) error {
	const attempts = 4
	pos := start
	var lastErr error
	for try := 0; try < attempts; try++ {
		if err := ctx.Err(); err != nil {
			return err // cancelled: never retry
		}
		if try > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(try) * time.Second):
			}
		}
		rng := ""
		if !whole {
			if pos > end {
				return nil
			}
			rng = fmt.Sprintf("bytes=%d-%d", pos, end)
		} else if pos > start {
			rng = fmt.Sprintf("bytes=%d-", pos) // best-effort resume
		}
		req, err := dlRequest(ctx, dlURL, rng)
		if err != nil {
			return err
		}
		resp, err := dlClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != 200 && resp.StatusCode != 206 {
			resp.Body.Close()
			return fmt.Errorf("HTTP %d depuis la source", resp.StatusCode)
		}
		if resp.StatusCode == 200 && pos > start {
			// Resume refused: the body restarts from 0, rewind our bookkeeping.
			atomic.AddInt64(done, start-pos)
			pos = start
		}
		n, cerr := dlCopy(f, resp.Body, pos, done)
		resp.Body.Close()
		pos += n
		if cerr == nil {
			return nil
		}
		lastErr = cerr
	}
	return lastErr
}

// dlCopy streams src into f starting at off, reporting bytes written. It
// returns the byte count even on error so the caller can resume.
func dlCopy(f *os.File, src io.Reader, off int64, done *int64) (int64, error) {
	buf := make([]byte, 1<<20) // 1 MiB
	var written int64
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := f.WriteAt(buf[:n], off+written); werr != nil {
				return written, werr
			}
			written += int64(n)
			atomic.AddInt64(done, int64(n))
		}
		if rerr == io.EOF {
			return written, nil
		}
		if rerr != nil {
			return written, rerr
		}
	}
}

// handleModelDownloadCancel aborts an in-flight download; runDownload then
// deletes its .part file, so nothing partial survives.
func handleModelDownloadCancel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	name := filepath.Base(strings.TrimSpace(req.Filename))
	dlMu.Lock()
	st, ok := dlDownloads[name]
	var cancel context.CancelFunc
	if ok {
		cancel = st.cancel
	}
	dlMu.Unlock()
	if !ok {
		sendJSON(w, 404, map[string]any{"ok": false, "error": "aucun téléchargement pour " + name})
		return
	}
	if cancel != nil {
		cancel()
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// cleanStalePartFiles removes leftover *.gguf.part files in every model dir at
// startup. A download killed by a crash or a service restart can't be resumed
// (its state lived in memory), so the partial file would otherwise sit there
// forever eating disk.
func cleanStalePartFiles() {
	for _, dir := range modelDirs() {
		matches, err := filepath.Glob(filepath.Join(dir, "*.gguf.part"))
		if err != nil {
			continue
		}
		for _, p := range matches {
			if err := os.Remove(p); err == nil {
				fmt.Printf("[models] téléchargement incomplet supprimé : %s\n", p)
			}
		}
	}
}

// handleModelDownloadStatus returns the state of all known downloads this run.
func handleModelDownloadStatus(w http.ResponseWriter, r *http.Request) {
	dlMu.Lock()
	out := make([]dlState, 0, len(dlDownloads))
	for _, st := range dlDownloads {
		out = append(out, *st)
	}
	dlMu.Unlock()
	sendJSON(w, 200, out)
}
