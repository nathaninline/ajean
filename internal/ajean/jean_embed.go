package ajean

// jean_embed.go : recherche PAR LE SENS dans le journal de Jean.
//
// Pourquoi : jean_search ne trouvait un souvenir que si la requête reprenait ses
// mots. Mesuré sur un vrai journal (543 entrées, 60 questions reformulées comme
// on se souvient vaguement) : 43 % des souvenirs dans les 5 premiers résultats
// par mots, 93 % par le sens avec une requête en phrase.
//
// Comment : un petit modèle d'embeddings (EmbeddingGemma 300M, ~320 Mo) tourne
// dans un llama-server à part, SUR LE PROCESSEUR (aucune carte : elles sont au
// modèle de chat). Chaque entrée du journal reçoit un vecteur, rangé à côté de
// son mois (embed-AAAA-MM.txt, chiffré comme la mémoire), quantifié en int8.
// La recherche compare la requête aux vecteurs ; la recherche par mots ne sert
// plus qu'en appoint et en secours (moteur arrêté, mémoire verrouillée…).
//
// Garde-fous : le moteur ne démarre qu'avec assez de RAM libre, s'arrête sous un
// seuil, s'éteint après une période sans usage, et l'indexation n'avance que
// quand aucune réponse n'est en cours (le modèle de chat garde la mémoire vive).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	jeanEmbedModelFile = "embeddinggemma-300M-Q8_0.gguf"
	jeanEmbedModelURL  = "https://huggingface.co/ggml-org/embeddinggemma-300M-GGUF/resolve/main/" + jeanEmbedModelFile
	jeanEmbedModelSize = 333590944
	// jeanEmbedTag identifie l'espace des vecteurs : en changer (autre modèle,
	// autre préfixe) invalide l'index, reconstruit tout seul.
	jeanEmbedTag      = "egemma300-q8-v1"
	jeanEmbedDocChars = 1200 // une entrée au-delà est tronquée (le début porte le sujet)
	jeanEmbedBatch    = 8
	jeanEmbedStartMB  = 1500             // RAM disponible requise pour démarrer le moteur
	jeanEmbedStopMB   = 700              // en dessous, le moteur est arrêté
	jeanEmbedIdle     = 20 * time.Minute // moteur éteint après ce délai sans usage
	jeanEmbedPrefix   = "embed-"
)

func jeanEmbedModelPath() string { return filepath.Join(modelsDir(), jeanEmbedModelFile) }

// jeanEmbedEnabled : réglage de l'appareil (bucket état), ACTIF par défaut : la
// recherche par le sens retrouve deux fois plus de souvenirs que par mots, et
// ses garde-fous (RAM, inactivité, jamais pendant une réponse) la rendent sans
// risque. Seul un « 0 » enregistré (coupé dans le panneau) la désactive.
func jeanEmbedEnabled() bool { return getStr(bkState, "jean_embed") != "0" }

func setJeanEmbedEnabled(on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	if err := putStr(bkState, "jean_embed", v); err != nil {
		return err
	}
	if on {
		jeanEmbedKick()
	} else {
		jeanEmbedEngine.stop("désactivé")
	}
	return nil
}

func jeanEmbedDocText(e jeanEntry) string {
	return "title: none | text: " + clipRunes(e.Text, jeanEmbedDocChars)
}
func jeanEmbedQueryText(q string) string { return "task: search result | query: " + q }

// ---- le moteur ---------------------------------------------------------------

type jeanEmbedProc struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	port    int
	lastUse time.Time
	err     string    // dernière raison d'indisponibilité, pour l'interface
	until   time.Time // pas de nouvel essai avant (échec, RAM)
}

var jeanEmbedEngine jeanEmbedProc

// jeanEmbedTestPort : tests seulement, un faux moteur d'embeddings déjà prêt.
var jeanEmbedTestPort int

// jeanEmbedBin : un llama-server récent sait servir EmbeddingGemma. On prend le
// plus récent des précompilés, sinon celui de la config.
func jeanEmbedBin() string {
	exe := "llama-server"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if ents, err := os.ReadDir(prebuiltDir()); err == nil {
		var dirs []string
		for _, e := range ents {
			if e.IsDir() && isFile(filepath.Join(prebuiltDir(), e.Name(), exe)) {
				dirs = append(dirs, e.Name())
			}
		}
		sort.Strings(dirs)
		if len(dirs) > 0 {
			return filepath.Join(prebuiltDir(), dirs[len(dirs)-1], exe)
		}
	}
	if b := strings.TrimSpace(ReadConfig()["BIN"]); b != "" && strings.Contains(filepath.Base(b), "llama-server") && isFile(b) {
		return b
	}
	return ""
}

func availRAMMB() int {
	used, total := ramUsageMB()
	if total == 0 {
		return -1 // inconnu : on ne bloque pas
	}
	return total - used
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// running : port du moteur s'il tourne déjà (sans rien démarrer).
func (p *jeanEmbedProc) running() (int, bool) {
	if jeanEmbedTestPort > 0 {
		return jeanEmbedTestPort, true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.ProcessState == nil {
		p.lastUse = time.Now()
		return p.port, true
	}
	return 0, false
}

// ensure démarre le moteur au besoin et renvoie son port (bloquant : jusqu'à
// 90 s au démarrage à froid, réservé à l'indexeur en arrière-plan).
func (p *jeanEmbedProc) ensure() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastUse = time.Now()
	if p.cmd != nil && p.cmd.ProcessState == nil {
		return p.port, nil
	}
	if time.Now().Before(p.until) {
		return 0, fmt.Errorf("%s", p.err)
	}
	fail := func(msg string, wait time.Duration) (int, error) {
		p.err, p.until = msg, time.Now().Add(wait)
		return 0, fmt.Errorf("%s", msg)
	}
	if !isFile(jeanEmbedModelPath()) {
		return fail("modèle d'embeddings absent", time.Minute)
	}
	bin := jeanEmbedBin()
	if bin == "" {
		return fail("llama.cpp introuvable pour le modèle d'embeddings", 5*time.Minute)
	}
	if a := availRAMMB(); a >= 0 && a < jeanEmbedStartMB {
		return fail(fmt.Sprintf("RAM insuffisante (%d Mo libres, %d requis)", a, jeanEmbedStartMB), 2*time.Minute)
	}
	port, err := freePort()
	if err != nil {
		return fail(err.Error(), time.Minute)
	}
	threads := max(1, min(4, runtime.NumCPU()/4))
	cmd := exec.Command(bin, "-m", jeanEmbedModelPath(), "--embedding", "-ngl", "0",
		"-c", "2048", "-b", "2048", "-ub", "2048", "-np", "1", "-t", strconv.Itoa(threads),
		"--host", "127.0.0.1", "--port", strconv.Itoa(port))
	cmd.Dir = filepath.Dir(bin)
	// Aucune carte : elles appartiennent au modèle de chat.
	cmd.Env = append(os.Environ(), "CUDA_VISIBLE_DEVICES=", "HIP_VISIBLE_DEVICES=-1", "ROCR_VISIBLE_DEVICES=-1", "GGML_VK_VISIBLE_DEVICES=")
	jeanEmbedSysProc(cmd)
	hideCmd(cmd)
	if err := cmd.Start(); err != nil {
		return fail("lancement : "+err.Error(), 5*time.Minute)
	}
	go func() { _ = cmd.Wait() }()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if cmd.ProcessState != nil {
			return fail("le moteur d'embeddings s'est arrêté au démarrage", 5*time.Minute)
		}
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
		if err == nil {
			ok := resp.StatusCode == 200
			resp.Body.Close()
			if ok {
				p.cmd, p.port, p.err = cmd, port, ""
				fmt.Fprintf(os.Stderr, "[jean] moteur d'embeddings prêt (processeur, port %d)\n", port)
				go p.watch(cmd)
				return port, nil
			}
		}
	}
	_ = cmd.Process.Kill()
	return fail("le moteur d'embeddings ne répond pas", 5*time.Minute)
}

// watch arrête le moteur s'il manque de RAM ou s'il ne sert plus.
func (p *jeanEmbedProc) watch(cmd *exec.Cmd) {
	for {
		time.Sleep(2 * time.Second)
		p.mu.Lock()
		if p.cmd != cmd || cmd.ProcessState != nil {
			p.mu.Unlock()
			return
		}
		idle := time.Since(p.lastUse) > jeanEmbedIdle
		p.mu.Unlock()
		if a := availRAMMB(); a >= 0 && a < jeanEmbedStopMB {
			p.stopFor(fmt.Sprintf("arrêté : RAM trop basse (%d Mo)", a), 10*time.Minute)
			return
		}
		if idle {
			p.stop("inutilisé")
			return
		}
	}
}

func (p *jeanEmbedProc) stop(why string) { p.stopFor(why, 0) }

func (p *jeanEmbedProc) stopFor(why string, wait time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.ProcessState == nil {
		_ = p.cmd.Process.Kill()
		fmt.Fprintf(os.Stderr, "[jean] moteur d'embeddings %s\n", why)
	}
	p.cmd = nil
	if wait > 0 {
		p.err, p.until = why, time.Now().Add(wait)
	}
}

// jeanEmbed calcule les vecteurs (normalisés) de textes déjà préfixés. Le moteur
// est démarré s'il est arrêté (il s'éteint après jeanEmbedIdle) : ~2 s à froid.
// La recherche n'attend que jusqu'à l'échéance de ctx ; au-delà, le démarrage
// continue en fond et la recherche par mots répond pour cette fois.
func jeanEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	port, ok := jeanEmbedEngine.running()
	if !ok {
		type res struct {
			port int
			err  error
		}
		ch := make(chan res, 1)
		go func() { p, err := jeanEmbedEngine.ensure(); ch <- res{p, err} }()
		select {
		case r := <-ch:
			if r.err != nil {
				return nil, r.err
			}
			port = r.port
		case <-ctx.Done():
			return nil, fmt.Errorf("moteur d'embeddings en cours de démarrage")
		}
	}
	body, _ := json.Marshal(map[string]any{"model": "embed", "input": texts})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/v1/embeddings", port), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("embeddings : HTTP %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings : %d vecteurs pour %d textes", len(out.Data), len(texts))
	}
	vs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		vs[i] = normVec(d.Embedding)
	}
	return vs, nil
}

func normVec(v []float64) []float32 {
	s := 0.0
	for _, x := range v {
		s += x * x
	}
	s = math.Sqrt(s)
	out := make([]float32, len(v))
	if s == 0 {
		return out
	}
	for i, x := range v {
		out[i] = float32(x / s)
	}
	return out
}

func dotVec(a, b []float32) float32 {
	var s float32
	for i := range a {
		if i >= len(b) {
			break
		}
		s += a[i] * b[i]
	}
	return s
}

// ---- l'index -----------------------------------------------------------------
//
// Un fichier par mois du journal (seul le mois en cours change) :
//   ligne 1 : « jean-embed <tag> »
//   puis    : « <id> <base64(échelle float32 + composantes int8)> »

func jeanEmbedFile(journal string) string {
	return jeanEmbedPrefix + strings.TrimSuffix(strings.TrimPrefix(journal, "journal-"), ".md") + ".txt"
}

func quantVec(v []float32) string {
	var m float32
	for _, x := range v {
		if a := float32(math.Abs(float64(x))); a > m {
			m = a
		}
	}
	buf := make([]byte, 4+len(v))
	scale := m / 127
	binary.LittleEndian.PutUint32(buf, math.Float32bits(scale))
	for i, x := range v {
		q := 0.0
		if scale > 0 {
			q = math.Round(float64(x / scale))
		}
		buf[4+i] = byte(int8(max(-127, min(127, q))))
	}
	return base64.RawStdEncoding.EncodeToString(buf)
}

func dequantVec(s string) []float32 {
	buf, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil || len(buf) < 5 {
		return nil
	}
	scale := math.Float32frombits(binary.LittleEndian.Uint32(buf))
	v := make([]float64, len(buf)-4)
	for i, b := range buf[4:] {
		v[i] = float64(int8(b)) * float64(scale)
	}
	return normVec(v)
}

func parseEmbedIndex(s string) map[string][]float32 {
	out := map[string][]float32{}
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "jean-embed "+jeanEmbedTag {
		return out // autre modèle (ou vide) : à reconstruire
	}
	for _, l := range lines[1:] {
		if id, b64, ok := strings.Cut(strings.TrimSpace(l), " "); ok {
			if v := dequantVec(b64); v != nil {
				out[id] = v
			}
		}
	}
	return out
}

func formatEmbedIndex(m map[string][]float32) string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	b.WriteString("jean-embed " + jeanEmbedTag + "\n")
	for _, id := range ids {
		b.WriteString(id + " " + quantVec(m[id]) + "\n")
	}
	return b.String()
}

// Cache des vecteurs en mémoire vive, par fichier d'index.
var jeanEmbedCache struct {
	sync.Mutex
	files map[string]map[string][]float32
}

func jeanEmbedLoad(file string) map[string][]float32 {
	jeanEmbedCache.Lock()
	defer jeanEmbedCache.Unlock()
	if jeanEmbedCache.files == nil {
		jeanEmbedCache.files = map[string]map[string][]float32{}
	}
	if m, ok := jeanEmbedCache.files[file]; ok {
		return m
	}
	s, err := jeanRead(file)
	if err != nil {
		return nil // mémoire verrouillée : pas d'index
	}
	m := parseEmbedIndex(s)
	jeanEmbedCache.files[file] = m
	return m
}

// jeanJournalMonths : les mois du journal présents, du plus ancien au plus récent.
func jeanJournalMonths() []string {
	ents, _ := os.ReadDir(jeanDir())
	var out []string
	for _, e := range ents {
		if n := e.Name(); !e.IsDir() && strings.HasPrefix(n, "journal-") && strings.HasSuffix(n, ".md") {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// jeanEmbedIndexOnce ajoute les vecteurs manquants, au plus `budget` entrées.
// Renvoie le nombre d'entrées indexées (0 = tout est à jour ou rien n'a pu l'être).
func jeanEmbedIndexOnce(ctx context.Context, budget int) (int, error) {
	done := 0
	months := jeanJournalMonths()
	for k := len(months) - 1; k >= 0 && done < budget; k-- { // le mois en cours d'abord
		jm := months[k]
		jeanMu.Lock()
		s, err := jeanRead(jm)
		jeanMu.Unlock()
		if err != nil {
			continue
		}
		file := jeanEmbedFile(jm)
		idx := jeanEmbedLoad(file)
		if idx == nil {
			continue
		}
		var todo []jeanEntry
		for _, e := range parseJeanJournal(s) {
			if _, ok := idx[e.ID]; !ok && strings.TrimSpace(e.Text) != "" {
				todo = append(todo, e)
			}
		}
		changed := false
		for len(todo) > 0 && done < budget {
			if jeanEmbedBusy() {
				break // une réponse est en cours : on reprendra plus tard
			}
			n := min(jeanEmbedBatch, len(todo), budget-done)
			texts := make([]string, n)
			for i := range texts {
				texts[i] = jeanEmbedDocText(todo[i])
			}
			vs, err := jeanEmbed(ctx, texts)
			if err != nil {
				if changed {
					jeanEmbedSave(file, idx)
				}
				return done, err
			}
			jeanEmbedCache.Lock()
			for i := range vs {
				idx[todo[i].ID] = vs[i]
			}
			jeanEmbedCache.Unlock()
			todo, done, changed = todo[n:], done+n, true
		}
		if changed {
			jeanEmbedSave(file, idx)
		}
	}
	return done, nil
}

func jeanEmbedSave(file string, idx map[string][]float32) {
	jeanEmbedCache.Lock()
	s := formatEmbedIndex(idx)
	jeanEmbedCache.Unlock()
	jeanMu.Lock()
	err := jeanWrite(file, s)
	jeanMu.Unlock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[jean] index des embeddings non écrit : %v\n", err)
	}
}

// jeanEmbedBusy : une réponse est en cours de génération (le modèle de chat a
// besoin de la RAM et du processeur ; l'indexation attend).
func jeanEmbedBusy() bool {
	c := conv
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Generating
}

// ---- l'indexeur en arrière-plan ------------------------------------------------

var jeanEmbedKickCh = make(chan struct{}, 1)
var jeanEmbedLoopOnce sync.Once

// jeanEmbedKick signale qu'il y a peut-être de quoi indexer (nouvelle entrée,
// démarrage, réglage activé). Sans effet si la recherche par le sens est coupée.
func jeanEmbedKick() {
	if !jeanEmbedEnabled() {
		return
	}
	// Actif par défaut : le modèle se télécharge tout seul, mais seulement quand
	// Jean a déjà un journal (on ne prend pas 330 Mo à qui ne s'en sert pas), et
	// pas plus d'une tentative par heure après un échec.
	if !isFile(jeanEmbedModelPath()) {
		jeanEmbedDL.Lock()
		retry := jeanEmbedDL.err == "" || time.Since(jeanEmbedDL.tried) > time.Hour
		jeanEmbedDL.Unlock()
		if retry && len(jeanJournalMonths()) > 0 && !testing.Testing() { // jamais depuis les tests
			jeanEmbedDownload()
		}
		return
	}
	jeanEmbedLoopOnce.Do(func() { go jeanEmbedLoop() })
	select {
	case jeanEmbedKickCh <- struct{}{}:
	default:
	}
}

func jeanEmbedLoop() {
	for range jeanEmbedKickCh {
		time.Sleep(3 * time.Second) // laisser la fin de tour se poser
		for jeanEmbedEnabled() {
			if jeanEmbedBusy() {
				time.Sleep(10 * time.Second)
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			n, err := jeanEmbedIndexOnce(ctx, 64)
			cancel()
			// Le journal d'abord (petit, ce dont le rappel a le plus besoin), puis
			// les fichiers de Jean, quelques-uns à la fois (jean_files.go).
			if err == nil && n == 0 {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
				n, err = jeanFilesIndexOnce(ctx, jeanFilesBudget)
				cancel()
			}
			if err != nil || n == 0 {
				break
			}
		}
	}
}

// ---- la recherche ---------------------------------------------------------------

// jeanJournalVecs : les vecteurs de toutes les entrées indexées du journal, par id.
func jeanJournalVecs() map[string][]float32 {
	vecs := map[string][]float32{}
	for _, m := range jeanJournalMonths() {
		if idx := jeanEmbedLoad(jeanEmbedFile(m)); idx != nil {
			jeanEmbedCache.Lock()
			for id, v := range idx {
				vecs[id] = v
			}
			jeanEmbedCache.Unlock()
		}
	}
	return vecs
}

// jeanQueryVec : le vecteur d'une requête, nil si la recherche par le sens est
// coupée ou que le moteur ne répond pas dans les 10 s (démarrage à froid compris).
func jeanQueryVec(query string) []float32 {
	if !jeanEmbedEnabled() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	vs, err := jeanEmbed(ctx, []string{jeanEmbedQueryText(query)})
	if err != nil {
		return nil
	}
	return vs[0]
}

// jeanSemanticRank classe tout le journal par proximité de sens avec le vecteur
// q d'une requête. nil si q manque ou l'index est vide : le classement par mots
// prend alors seul le relais.
func jeanSemanticRank(q []float32, all []jeanEntry) ([]int, map[int]float32) {
	if q == nil || len(all) == 0 {
		return nil, nil
	}
	vecs := jeanJournalVecs()
	if len(vecs) == 0 {
		return nil, nil
	}
	cos := map[int]float32{}
	var ids []int
	for i, e := range all {
		if v, ok := vecs[e.ID]; ok {
			cos[i] = dotVec(q, v)
			ids = append(ids, i)
		}
	}
	sort.SliceStable(ids, func(a, b int) bool { return cos[ids[a]] > cos[ids[b]] })
	jeanEmbedKick() // des entrées sans vecteur ? on rattrape en fond
	return ids, cos
}

// ---- état et réglage (interface) -------------------------------------------------

type jeanEmbedStatus struct {
	Enabled bool   `json:"enabled"`
	Model   bool   `json:"model"`   // modèle téléchargé
	Running bool   `json:"running"` // moteur en marche
	Indexed int    `json:"indexed"`
	Total   int    `json:"total"`
	Error   string `json:"error,omitempty"`
	Loading bool   `json:"loading,omitempty"` // téléchargement en cours
}

var jeanEmbedDL struct {
	sync.Mutex
	running bool
	err     string
	tried   time.Time // dernière tentative (une par heure au plus après un échec)
}

func jeanEmbedState() jeanEmbedStatus {
	st := jeanEmbedStatus{Enabled: jeanEmbedEnabled(), Model: isFile(jeanEmbedModelPath())}
	jeanEmbedEngine.mu.Lock()
	st.Running = jeanEmbedEngine.cmd != nil && jeanEmbedEngine.cmd.ProcessState == nil
	st.Error = jeanEmbedEngine.err
	jeanEmbedEngine.mu.Unlock()
	jeanEmbedDL.Lock()
	st.Loading = jeanEmbedDL.running
	if jeanEmbedDL.err != "" {
		st.Error = jeanEmbedDL.err
	}
	jeanEmbedDL.Unlock()
	jeanMu.Lock()
	all := jeanJournalAll()
	jeanMu.Unlock()
	st.Total = len(all)
	have := map[string]bool{}
	for _, m := range jeanJournalMonths() {
		if idx := jeanEmbedLoad(jeanEmbedFile(m)); idx != nil {
			jeanEmbedCache.Lock()
			for id := range idx {
				have[id] = true
			}
			jeanEmbedCache.Unlock()
		}
	}
	for _, e := range all {
		if have[e.ID] {
			st.Indexed++
		}
	}
	return st
}

// jeanEmbedDownload télécharge le modèle (une fois), puis lance l'indexation.
func jeanEmbedDownload() {
	jeanEmbedDL.Lock()
	if jeanEmbedDL.running {
		jeanEmbedDL.Unlock()
		return
	}
	jeanEmbedDL.running, jeanEmbedDL.err, jeanEmbedDL.tried = true, "", time.Now()
	jeanEmbedDL.Unlock()
	go func() {
		defer func() {
			jeanEmbedDL.Lock()
			jeanEmbedDL.running = false
			jeanEmbedDL.Unlock()
		}()
		dst := jeanEmbedModelPath()
		if fi, err := os.Stat(dst); err == nil && fi.Size() == jeanEmbedModelSize {
			jeanEmbedKick()
			return
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		tmp := dst + ".part"
		err := downloadWithProgress(jeanEmbedModelURL, tmp, jeanEmbedModelSize, func(string) {})
		if err == nil {
			if fi, e := os.Stat(tmp); e != nil || fi.Size() != jeanEmbedModelSize {
				err = fmt.Errorf("téléchargement incomplet")
			}
		}
		if err == nil {
			err = os.Rename(tmp, dst)
		}
		if err != nil {
			_ = os.Remove(tmp)
			jeanEmbedDL.Lock()
			jeanEmbedDL.err = "téléchargement du modèle : " + err.Error()
			jeanEmbedDL.Unlock()
			return
		}
		jeanEmbedKick()
	}()
}

// handleJeanEmbed : GET → état ; POST {on} → active (télécharge au besoin) ou coupe.
func handleJeanEmbed(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			On bool `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": "requête illisible"})
			return
		}
		if err := setJeanEmbedEnabled(req.On); err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if req.On {
			jeanEmbedDownload()
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "status": jeanEmbedState()})
}
