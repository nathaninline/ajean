package jean

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	port := LLMPort()
	if !healthCheck() {
		return nil, fmt.Errorf("serveur injoignable sur :%d", port)
	}
	if nPrompt <= 0 {
		nPrompt = 2000
	}
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
	prompt := strings.TrimSpace(b.String())
	// Use the same endpoint your real chat hits, so the comparison is honest
	// (chat template, reasoning, OpenAI-compat layer all included).
	payload := map[string]any{
		"model":        "jean",
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
	saveLastBench(res)
	saveBenchForActivePreset(res)
	return res, nil
}

// lastBenchPath stores the most recent benchmark result so the web UI can show
// it without re-running. Lives in JEAN_HOME so it survives restarts.
func lastBenchPath() string { return filepath.Join(JeanHome(), ".last_bench.json") }

// savedBench is a benchResult plus the model it was run against and a timestamp.
type savedBench struct {
	Result benchResult `json:"result"`
	Model  string      `json:"model"`
	At     int64       `json:"at"`
}

// saveLastBench persists res to JEAN_HOME/.last_bench.json (best-effort).
func saveLastBench(res *benchResult) {
	sb := savedBench{Result: *res, Model: filepath.Base(ReadConfig()["MODEL"]), At: time.Now().Unix()}
	if b, err := json.Marshal(sb); err == nil {
		_ = os.WriteFile(lastBenchPath(), b, 0o644)
	}
}

// loadLastBench reads the persisted benchmark, or nil if none/unreadable.
func loadLastBench() *savedBench {
	b, err := os.ReadFile(lastBenchPath())
	if err != nil {
		return nil
	}
	var sb savedBench
	if json.Unmarshal(b, &sb) != nil {
		return nil
	}
	return &sb
}

// benchStorePath holds per-preset benchmark results so the UI can show each
// preset's measured performance. Keyed by preset name.
func benchStorePath() string { return filepath.Join(JeanHome(), ".bench_presets.json") }

// loadBenchStore returns the per-preset bench map (empty if none/unreadable).
func loadBenchStore() map[string]savedBench {
	m := map[string]savedBench{}
	b, err := os.ReadFile(benchStorePath())
	if err != nil {
		return m
	}
	_ = json.Unmarshal(b, &m)
	return m
}

// saveBenchForActivePreset records res under the name of the currently active
// preset (the one whose config.env matches the live config). No-op if no preset
// matches — the bench still lives in .last_bench.json via saveLastBench.
func saveBenchForActivePreset(res *benchResult) {
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
	m[id] = savedBench{Result: *res, Model: filepath.Base(ReadConfig()["MODEL"]), At: time.Now().Unix()}
	if b, err := json.Marshal(m); err == nil {
		_ = os.WriteFile(benchStorePath(), b, 0o644)
	}
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
