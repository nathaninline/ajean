package ajean

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// benchResult captures the timings llama.cpp returns from /completion.
type benchResult struct {
	PromptN         int     `json:"prompt_n"`
	PromptMs        float64 `json:"prompt_ms"`
	PromptPerSecond float64 `json:"prompt_per_second"`
	PredictedN      int     `json:"predicted_n"`
	PredictedMs     float64 `json:"predicted_ms"`
	PredictedPerSec float64 `json:"predicted_per_second"`
	Elapsed         float64 `json:"elapsed_sec"`
}

// benchCorpus is a varied passage used to defeat speculative decoding
// (MTP / n-gram draft) — repetitive text inflates decode tok/s because every
// drafted token gets accepted, which is unlike real chat. We pull a chunk of
// natural-looking content and tile it to reach the target prompt size.
const benchCorpus = `In the early hours of an October morning, Camille walked along the canal, watching the cargo barges slip past the iron bridge that spanned the water. She thought about the meeting she had skipped, the unanswered messages on her phone, the way the city always seemed to forget her name after summer ended. Three streets away, a pâtisserie opened its shutters and the smell of warm butter mixed with diesel exhaust from the waiting bus.
Pendant ce temps, à Marseille, un chercheur en biologie marine prépare son matériel pour une plongée. Il étudie les herbiers de posidonie, ces prairies sous-marines vieilles de plusieurs milliers d'années qui stockent autant de carbone qu'une forêt amazonienne. Le bateau quitte le port à six heures vingt-trois.
Quantum computers, properly engineered, can solve certain classes of problems exponentially faster than classical machines. The catch is that decoherence ruins everything. Engineers use dilution refrigerators to drop superconducting qubits to fifteen millikelvin, colder than deep space. The wires connecting the chip to room-temperature electronics must dissipate almost no heat, or the qubit state collapses before any useful computation finishes.
Le boulanger lève la pâte à quatre heures. Il regarde la balance numérique en plissant les yeux : six cent vingt-trois grammes, presque le compte. Son chien dort sur le tapis de farine près du four. Dehors, deux chats se disputent un poisson abandonné par le pêcheur de nuit.
Consider a recursive descent parser written in Go. The lexer emits tokens; the parser consumes them and produces an abstract syntax tree. Error recovery is hard: after a syntax error, the parser must resynchronize at a known boundary—a semicolon, a closing brace—without losing track of subsequent diagnostics. Tree-sitter solves this with incremental parsing and a glr-like algorithm.
Le philosophe stoïcien disait : "Ce qui nous trouble, ce n'est pas ce qui nous arrive, mais l'opinion que nous nous en faisons." Vingt siècles plus tard, la phrase apparaît dans un livre de poche au rayon développement personnel d'une librairie d'aéroport, à côté d'un roman policier suédois.
Mitochondria descended from ancient bacteria engulfed by archaeal cells roughly two billion years ago. They still keep their own ring of DNA, separate from the nuclear genome. Mutations in mitochondrial DNA accumulate with age and have been implicated in everything from Parkinson's disease to ordinary muscle fatigue. Yet they remain stubbornly difficult to repair therapeutically because each cell contains hundreds.
La marée descend lentement, exposant des rochers couverts d'huîtres et d'algues vertes. Un héron immobile surveille les flaques laissées par l'eau. Plus loin, deux enfants courent avec un cerf-volant rouge qui refuse de monter à cause de l'humidité dans la voile.
Compilers translate high-level languages into machine code through several intermediate representations. LLVM IR sits in the middle: typed, mostly static-single-assignment, suitable for both aggressive optimization and direct lowering to x86 or ARM. The optimizer runs dozens of passes—dead code elimination, loop-invariant code motion, induction variable simplification—each touching the IR in carefully ordered ways.
Le cuisinier ferme les yeux pour goûter la sauce. Trop salée. Il ajoute une pomme de terre crue coupée en quartiers, sachant qu'elle absorbera l'excès en mijotant vingt minutes. Sa grand-mère lui a appris ce geste un dimanche de novembre il y a très longtemps.
`

// runBench fires a prompt of roughly `nPrompt` tokens at /completion with
// cache_prompt:false (so prefill is actually measured, not cached). The prompt
// is a varied corpus to keep speculative decoding (MTP / n-gram draft) from
// inflating decode numbers — what you measure here is close to what you'll
// see in real chat at the same context length.
func runBench(nPrompt, nPredict int) (*benchResult, error) {
	if s := explicitChatService(); s != nil {
		if err := probeReady(s, 5*time.Second); err != nil {
			return nil, fmt.Errorf("le modèle ne répond pas : %v", err)
		}
		return runBenchStream(nPrompt, nPredict)
	}
	port := LLMPort()
	if !healthCheck() {
		return nil, fmt.Errorf("serveur injoignable sur :%d", port)
	}
	if nPrompt <= 0 {
		nPrompt = 2000
	}
	prompt := benchPrompt(nPrompt)
	// Use the same endpoint your real chat hits, so the comparison is honest
	// (chat template, reasoning, OpenAI-compat layer all included).
	payload := map[string]any{
		"model":        "ajean",
		"messages":     []Message{{Role: "user", Content: prompt + "\n\nContinue this passage with another 1000+ words of original varied prose, mixing French and English narrative paragraphs on different topics."}},
		"max_tokens":   nPredict,
		"stream":       false,
		"temperature":  0.7,
		"cache_prompt": false,
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://localhost:%d/v1/chat/completions", port)
	t0 := time.Now()
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	authHeader(req)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var parsed struct {
		Timings struct {
			PromptN         int     `json:"prompt_n"`
			PromptMs        float64 `json:"prompt_ms"`
			PromptPerSecond float64 `json:"prompt_per_second"`
			PredictedN      int     `json:"predicted_n"`
			PredictedMs     float64 `json:"predicted_ms"`
			PredictedPerSec float64 `json:"predicted_per_second"`
		} `json:"timings"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	// /v1/chat/completions may report timings at top level or omit them; if
	// missing, fall back to wall-clock derived from `usage` so we always show
	// numbers comparable to what chat displays in its label.
	if parsed.Timings.PredictedN == 0 && parsed.Usage.CompletionTokens > 0 {
		elapsed := time.Since(t0).Seconds()
		parsed.Timings.PromptN = parsed.Usage.PromptTokens
		parsed.Timings.PredictedN = parsed.Usage.CompletionTokens
		// Distribute elapsed time using a rough split (prefill is usually <20% at this size).
		parsed.Timings.PromptMs = elapsed * 1000 * 0.15
		parsed.Timings.PredictedMs = elapsed * 1000 * 0.85
		if parsed.Timings.PromptMs > 0 {
			parsed.Timings.PromptPerSecond = float64(parsed.Timings.PromptN) / (parsed.Timings.PromptMs / 1000)
		}
		if parsed.Timings.PredictedMs > 0 {
			parsed.Timings.PredictedPerSec = float64(parsed.Timings.PredictedN) / (parsed.Timings.PredictedMs / 1000)
		}
	}
	elapsed := time.Since(t0).Seconds()
	t := parsed.Timings
	res := &benchResult{
		PromptN: t.PromptN, PromptMs: t.PromptMs, PromptPerSecond: t.PromptPerSecond,
		PredictedN: t.PredictedN, PredictedMs: t.PredictedMs, PredictedPerSec: t.PredictedPerSec,
		Elapsed: elapsed,
	}
	// Un bench ne mesure que le moteur LOCAL : pour un preset distant (API
	// Externe, GPU Cloud), l'enregistrer l'attribuerait à tort à ce preset.
	if !usesRemoteEndpoint(ReadConfig()) {
		saveBenchForActivePreset(res)
	}
	return res, nil
}

// benchPrompt : un passage varié d'environ nPrompt tokens (voir runBench).
func benchPrompt(nPrompt int) string {
	// 1 word ≈ 1.3 tokens roughly. Tile the varied corpus until we exceed
	// nPrompt, then truncate to characters so the server tokenises a passage
	// close to the requested size.
	corpusWords := strings.Fields(benchCorpus)
	target := nPrompt * 5 // ~5 chars/token gives a generous over-estimate
	var b strings.Builder
	for b.Len() < target {
		for _, w := range corpusWords {
			b.WriteString(w)
			b.WriteByte(' ')
			if b.Len() >= target {
				break
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// savedBench is a benchResult plus the model it was run against and a timestamp.
type savedBench struct {
	Result benchResult `json:"result"`
	Model  string      `json:"model"`
	At     int64       `json:"at"`
}

// benchModelKey : sous quel modèle ranger le bench de cette config. Un moteur
// local : son GGUF. Un moteur tiers lancé par ajean (EXTERNAL_SERVICE) : son
// modèle déclaré, préfixé pour ne jamais coïncider avec un GGUF. Une API
// distante ou le GPU Cloud : "" (pas de bench attribuable).
func benchModelKey(cfg map[string]string) string {
	if externalServiceOf(cfg) != "" {
		return "ext:" + strings.TrimSpace(cfg[extKeyModel])
	}
	// moteur MoE intégré : sa config d'installation désigne le modèle
	if isMoeConfig(cfg) {
		if c := strings.TrimSpace(cfg["MOE_CONFIG"]); c != "" {
			return "moe:" + filepath.Base(c)
		}
		return ""
	}
	if isExl3Config(cfg) {
		if m := strings.TrimSpace(cfg["EXL3_MODEL"]); m != "" {
			return "exl3:" + filepath.Base(m)
		}
		return ""
	}
	if usesRemoteEndpoint(cfg) {
		return ""
	}
	if m := strings.TrimSpace(cfg["MODEL"]); m != "" {
		return filepath.Base(m)
	}
	return ""
}

// deletePresetBench oublie le bench d'un preset supprimé.
func deletePresetBench(id string) {
	m := loadBenchStore()
	if _, ok := m[id]; ok {
		delete(m, id)
		_ = putJSON(bkState, "bench_presets", m)
	}
}

// loadBenchStore renvoie les benchmarks par preset (vide s'il n'y en a pas).
func loadBenchStore() map[string]savedBench {
	m := map[string]savedBench{}
	getJSON(bkState, "bench_presets", &m)
	if m == nil {
		m = map[string]savedBench{}
	}
	return m
}

// saveBenchForActivePreset records res under the name of the currently active
// preset (celui qui correspond à la configuration active). Sans effet si aucun
// preset ne correspond.
func saveBenchForActivePreset(res *benchResult) {
	if s := explicitChatService(); s != nil {
		saveServiceBench(s, res)
		return
	}
	list, err := ListPresets()
	if err != nil {
		return
	}
	id := ""
	for _, p := range list {
		if p.Active {
			id = p.ID
			break
		}
	}
	if id == "" {
		return
	}
	m := loadBenchStore()
	m[id] = savedBench{Result: *res, Model: benchModelKey(ReadConfig()), At: time.Now().Unix()}
	_ = putJSON(bkState, "bench_presets", m)
}

func cmdBench(args []string) error {
	nPredict, nPrompt := 300, 2000
	if len(args) >= 1 && args[0] != "" {
		if n, err := strconv.Atoi(args[0]); err == nil {
			nPredict = n
		} else {
			return fmt.Errorf("argument invalide: %s", args[0])
		}
	}
	if len(args) >= 2 && args[1] != "" {
		if n, err := strconv.Atoi(args[1]); err == nil {
			nPrompt = n
		} else {
			return fmt.Errorf("argument invalide: %s", args[1])
		}
	}
	fmt.Printf("[bench] prompt ~%d tokens, n_predict=%d…\n", nPrompt, nPredict)
	r, err := runBench(nPrompt, nPredict)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Printf("  %s  %7.1f tok/s   (%d tokens en %.2fs)\n", cyan("Prefill"), r.PromptPerSecond, r.PromptN, r.PromptMs/1000)
	fmt.Printf("  %s  %7.1f tok/s   (%d tokens en %.2fs)\n", cyan("Decode "), r.PredictedPerSec, r.PredictedN, r.PredictedMs/1000)
	fmt.Printf("  Total                     %.2fs\n", r.Elapsed)
	fmt.Println()
	return nil
}

// benchNonce : marque unique EN TÊTE du prompt. Un moteur tiers peut ignorer
// cache_prompt:false et réutiliser sa conversation en cache (certains moteurs le font) :
// un 2e bench identique affichait alors 2 166 tokens « lus » en 0,1 s. Un début
// différent à chaque fois interdit toute réutilisation de préfixe.
func benchNonce() string {
	// l'horloge seule ne suffit pas : sous Windows deux appels rapprochés
	// renvoient la même valeur, d'où le compteur.
	return fmt.Sprintf("[bench %d-%d]\n", time.Now().UnixNano(), benchSeq.Add(1))
}

var benchSeq atomic.Int64

// runBenchStream mesure un moteur tiers lancé par ajean (EXTERNAL_SERVICE) qui
// ne renvoie pas les `timings` de llama.cpp : on stream la réponse, le délai
// jusqu'au premier token donne la lecture du prompt, le reste la génération.
// Les comptes de tokens viennent de `usage` (dernier chunk du flux).
func runBenchStream(nPrompt, nPredict int) (*benchResult, error) {
	if nPrompt <= 0 {
		nPrompt = 2000
	}
	ep := resolveChatEndpoint()
	payload := map[string]any{
		"model":          ep.Model,
		"messages":       []Message{{Role: "user", Content: benchNonce() + benchPrompt(nPrompt) + "\n\nContinue this passage with another 1000+ words of original varied prose, mixing French and English narrative paragraphs on different topics."}},
		"max_tokens":     nPredict,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"temperature":    0.7,
		"cache_prompt":   false,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", ep.URL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if ep.Key != "" {
		req.Header.Set("Authorization", "Bearer "+ep.Key)
	}
	benchProgressStart(nPrompt, nPredict)
	defer benchProgressSet(func(p *benchProgress) { p.Running = false })
	stopSlots := benchWatchSlots(ep.URL, ep.Key)
	defer stopSlots()
	t0 := time.Now()
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return nil, fmt.Errorf("HTTP %d : %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var first, last time.Time
	var usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	}
	chunks := 0
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ch struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &ch) != nil {
			continue
		}
		if ch.Usage != nil && ch.Usage.CompletionTokens > 0 {
			usage.PromptTokens, usage.CompletionTokens = ch.Usage.PromptTokens, ch.Usage.CompletionTokens
		}
		for _, c := range ch.Choices {
			if c.Delta.Content != "" || c.Delta.ReasoningContent != "" {
				now := time.Now()
				if first.IsZero() {
					first = now
				}
				last = now
				chunks++
				n := chunks
				benchProgressSet(func(p *benchProgress) { p.Generated = n; p.PrefillDone = p.Prompt })
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if first.IsZero() {
		return nil, fmt.Errorf("aucun token reçu")
	}
	if usage.CompletionTokens == 0 { // serveur sans `usage` : un chunk ≈ un token
		usage.CompletionTokens = chunks
	}
	prefill := first.Sub(t0).Seconds()
	gen := last.Sub(first).Seconds()
	res := &benchResult{
		PromptN:     usage.PromptTokens,
		PromptMs:    prefill * 1000,
		PredictedN:  usage.CompletionTokens,
		PredictedMs: gen * 1000,
		Elapsed:     time.Since(t0).Seconds(),
	}
	if prefill > 0 && usage.PromptTokens > 0 {
		res.PromptPerSecond = float64(usage.PromptTokens) / prefill
	}
	if gen > 0 && usage.CompletionTokens > 1 {
		res.PredictedPerSec = float64(usage.CompletionTokens-1) / gen // le 1er token est compté dans la lecture
	}
	saveBenchForActivePreset(res)
	return res, nil
}

// benchProgress : où en est le test en cours (fenêtre Benchmark).
// Generated vient du flux ; PrefillDone du /slots du moteur s'il en a un.
type benchProgress struct {
	Running     bool  `json:"running"`
	Prompt      int   `json:"prompt"`
	PrefillDone int   `json:"prefill_done"`
	Generated   int   `json:"generated"`
	Output      int   `json:"output"`
	Started     int64 `json:"started"`
}

var (
	benchProgMu sync.Mutex
	benchProg   benchProgress
)

func benchProgressStart(prompt, output int) {
	benchProgMu.Lock()
	benchProg = benchProgress{Running: true, Prompt: prompt, Output: output, Started: time.Now().UnixMilli()}
	benchProgMu.Unlock()
}

func benchProgressSet(f func(*benchProgress)) {
	benchProgMu.Lock()
	f(&benchProg)
	benchProgMu.Unlock()
}

func benchProgressGet() benchProgress {
	benchProgMu.Lock()
	defer benchProgMu.Unlock()
	return benchProg
}

// benchWatchSlots relève l'avancement du prefill sur /slots (Basalt, llama.cpp)
// tant que le premier token n'est pas arrivé. Un moteur sans /slots : rien.
func benchWatchSlots(chatURL, key string) func() {
	root := chatURL
	if i := strings.Index(root, "/v1"); i > 0 {
		root = root[:i]
	}
	done := make(chan struct{})
	go func() {
		cl := &http.Client{Timeout: 800 * time.Millisecond}
		for {
			select {
			case <-done:
				return
			case <-time.After(500 * time.Millisecond):
			}
			if benchProgressGet().Generated > 0 {
				return
			}
			req, err := http.NewRequest("GET", root+"/slots", nil)
			if err != nil {
				return
			}
			if key != "" {
				req.Header.Set("Authorization", "Bearer "+key)
			}
			resp, err := cl.Do(req)
			if err != nil {
				continue
			}
			var slots []struct {
				Busy        bool `json:"is_processing"`
				Prompt      int  `json:"prompt_tokens"`
				PrefillDone int  `json:"prefill_done"`
			}
			ok := resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&slots) == nil
			resp.Body.Close()
			if !ok {
				return // pas de /slots : on n'insiste pas
			}
			for _, sl := range slots {
				if sl.Busy && sl.Prompt > 0 {
					benchProgressSet(func(p *benchProgress) {
						if p.Generated == 0 {
							p.Prompt, p.PrefillDone = sl.Prompt, sl.PrefillDone
						}
					})
					break
				}
			}
		}
	}()
	return func() { close(done) }
}
