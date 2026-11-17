package agent

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"ajean/internal/embedder"
	"ajean/internal/home"
)

// Recherche dans les conversations, pour la fenêtre de recherche de
// l'interface. Par le sens quand une source de vecteurs est réglée (le même
// modèle d'embeddings que la mémoire) : chaque message reçoit un vecteur,
// calculé en fond et rangé par conversation. Toujours aussi par les mots, ce
// qui suffit quand aucune source n'est réglée.

const (
	searchDocChars = 1000 // un message plus long est tronqué : le début porte le sujet
	searchBatch    = 16
	semMin         = 0.32 // en dessous, un rapprochement par le sens n'est que du bruit
)

// Hit : une conversation trouvée, avec le message qui correspond le mieux.
type Hit struct {
	Conv     string  `json:"conv"`
	Title    string  `json:"title"`
	TS       int64   `json:"ts"`   // date du message trouvé
	Role     string  `json:"role"` // user | assistant
	Index    int     `json:"index"`
	Snippet  string  `json:"snippet"`
	Score    float32 `json:"score"`
	Semantic bool    `json:"semantic"` // trouvé par le sens (pas seulement par les mots)
}

// ---- index des vecteurs ---------------------------------------------------------

type convVecFile struct {
	Model   string            `json:"model"`
	Vectors map[string]string `json:"vectors"` // empreinte du texte → vecteur int8 en base64
}

var searchIdx = struct {
	sync.Mutex
	byConv map[string]convVecFile
	wake   chan struct{}
}{byConv: map[string]convVecFile{}, wake: make(chan struct{}, 1)}

func searchPath(conv string) string { return home.State("convsearch", conv+".json") }

func docText(it Item) string {
	r := []rune(strings.Join(strings.Fields(it.Text), " "))
	return string(r[:min(len(r), searchDocChars)])
}

func docKey(text string) string {
	h := sha1.Sum([]byte(text))
	return hex.EncodeToString(h[:8])
}

// vectorsFor : les vecteurs d'une conversation pour ce modèle (verrou tenu).
func vectorsFor(conv, model string) map[string]string {
	if f, ok := searchIdx.byConv[conv]; ok && f.Model == model {
		return f.Vectors
	}
	var f convVecFile
	if b, err := os.ReadFile(searchPath(conv)); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	if f.Model != model || f.Vectors == nil {
		f = convVecFile{Model: model, Vectors: map[string]string{}}
	}
	searchIdx.byConv[conv] = f
	return f.Vectors
}

// WakeSearchIndex demande d'indexer ce qui manque (après une réponse, ou quand
// la source de vecteurs change).
func WakeSearchIndex() {
	select {
	case searchIdx.wake <- struct{}{}:
	default:
	}
}

// StartSearchIndex lance l'indexeur en fond.
func StartSearchIndex(ctx context.Context) {
	go func() {
		WakeSearchIndex()
		wait := time.Duration(0)
		for {
			select {
			case <-ctx.Done():
				return
			case <-searchIdx.wake:
			case <-time.After(max(wait, time.Minute)):
				if wait == 0 {
					continue
				}
			}
			if err := indexAll(ctx); err != nil {
				wait = min(max(wait*2, 30*time.Second), 30*time.Minute)
				log.Printf("recherche : index incomplet (%v), nouvel essai dans %s", err, wait)
			} else {
				wait = 0
			}
		}
	}()
}

// indexAll calcule les vecteurs manquants de toutes les conversations.
func indexAll(ctx context.Context) error {
	src, ok := embedder.Active()
	if !ok {
		return nil
	}
	for _, in := range List() {
		_, items, ok := Thread(in.ID)
		if !ok {
			continue
		}
		searchIdx.Lock()
		have := vectorsFor(in.ID, src.Space)
		var todo []string
		seen := map[string]bool{}
		for _, it := range items {
			t := docText(it)
			k := docKey(t)
			if t == "" || seen[k] || have[k] != "" {
				continue
			}
			seen[k] = true
			todo = append(todo, t)
		}
		searchIdx.Unlock()
		for len(todo) > 0 {
			batch := todo[:min(searchBatch, len(todo))]
			todo = todo[len(batch):]
			vs, err := src.Embed(ctx, batch, false)
			if err != nil {
				return err
			}
			if len(vs) != len(batch) {
				return errors.New("embeddings : nombre de vecteurs inattendu")
			}
			searchIdx.Lock()
			f := searchIdx.byConv[in.ID]
			for i, v := range vs {
				f.Vectors[docKey(batch[i])] = encodeVec(quantize(unitVec(v)))
			}
			b, _ := json.Marshal(f)
			searchIdx.Unlock()
			if err := home.WriteFile(searchPath(in.ID), b); err != nil {
				return err
			}
		}
	}
	return nil
}

// forgetSearch efface l'index d'une conversation supprimée.
func forgetSearch(conv string) {
	searchIdx.Lock()
	delete(searchIdx.byConv, conv)
	searchIdx.Unlock()
	_ = os.Remove(searchPath(conv))
}

// ---- recherche ------------------------------------------------------------------

// Search cherche dans toutes les conversations. semantic dit si le sens a été
// utilisé (source réglée et requête vectorisée à temps).
func Search(ctx context.Context, query string, limit int) (hits []Hit, semantic bool) {
	query = strings.TrimSpace(query)
	hits = []Hit{}
	if query == "" {
		return hits, false
	}
	terms := strings.Fields(foldText(query))
	var q []float32
	model := ""
	if src, ok := embedder.Active(); ok {
		qctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		vs, err := src.Embed(qctx, []string{query}, true)
		cancel()
		if err == nil && len(vs) == 1 && len(vs[0]) > 0 {
			q, model, semantic = unitVec(vs[0]), src.Space, true
		}
	}
	for _, in := range List() {
		info, items, ok := Thread(in.ID)
		if !ok {
			continue
		}
		var vecs map[string]string
		if semantic {
			searchIdx.Lock()
			vecs = vectorsFor(in.ID, model)
			searchIdx.Unlock()
		}
		titleLex := lexScore(terms, foldText(info.Title))
		best := Hit{Index: -1}
		for i, it := range items {
			t := docText(it)
			if t == "" {
				continue
			}
			lex := lexScore(terms, foldText(it.Text))
			var sem float32
			if semantic {
				if s := vecs[docKey(t)]; s != "" {
					sem = cosVec(decodeVec(s), q)
				}
			}
			found := lex > 0 || sem >= semMin
			if !found {
				continue
			}
			score := sem + 0.35*lex
			if !semantic {
				score = lex
			}
			if score > best.Score || best.Index < 0 {
				best = Hit{Conv: info.ID, Title: info.Title, TS: it.TS, Role: it.Role, Index: i,
					Snippet: snippet(it.Text, terms), Score: score, Semantic: sem >= semMin}
			}
		}
		if best.Index < 0 && titleLex > 0 && len(items) > 0 {
			best = Hit{Conv: info.ID, Title: info.Title, TS: items[0].TS, Role: items[0].Role, Index: 0, Snippet: snippet(items[0].Text, terms)}
		}
		if best.Index >= 0 {
			best.Score += 0.2 * titleLex
			hits = append(hits, best)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, semantic
}

// lexScore : la part des mots de la requête présents dans le texte (0 à 1).
func lexScore(terms []string, folded string) float32 {
	if len(terms) == 0 {
		return 0
	}
	n := 0
	for _, t := range terms {
		if strings.Contains(folded, t) {
			n++
		}
	}
	return float32(n) / float32(len(terms))
}

// mdNoise : la syntaxe Markdown qui n'a rien à faire dans un extrait (tableaux,
// titres, gras, code, citations).
var mdNoise = regexp.MustCompile("[|*#`>]+|-{3,}|:?-{3,}:?")

// snippet : un extrait autour du premier mot trouvé (ou le début du message).
func snippet(text string, terms []string) string {
	text = mdNoise.ReplaceAllString(text, " ")
	flat := []rune(strings.Join(strings.Fields(text), " "))
	folded := []rune(foldText(string(flat)))
	at := -1
	for _, t := range terms {
		if i := strings.Index(string(folded), t); i >= 0 {
			at = len([]rune(string(folded)[:i]))
			break
		}
	}
	const width = 170
	start := 0
	if at > 60 {
		start = at - 60
	}
	end := min(len(flat), start+width)
	out := string(flat[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(flat) {
		out += "…"
	}
	return out
}

// foldText : minuscules sans accents, pour comparer des mots.
func foldText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case strings.ContainsRune("àâäáãå", r):
			r = 'a'
		case strings.ContainsRune("éèêë", r):
			r = 'e'
		case strings.ContainsRune("îïíì", r):
			r = 'i'
		case strings.ContainsRune("ôöóõò", r):
			r = 'o'
		case strings.ContainsRune("ùûüú", r):
			r = 'u'
		case r == 'ç':
			r = 'c'
		case r == 'ÿ':
			r = 'y'
		case r == 'ñ':
			r = 'n'
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ---- vecteurs (quantifiés en int8, comme ceux de la mémoire) ---------------------

func unitVec(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	n := float32(math.Sqrt(sum))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x / n
	}
	return out
}

func quantize(v []float32) []int8 {
	var top float32
	for _, x := range v {
		top = max(top, float32(math.Abs(float64(x))))
	}
	out := make([]int8, len(v))
	if top == 0 {
		return out
	}
	for i, x := range v {
		out[i] = int8(math.Round(float64(x / top * 127)))
	}
	return out
}

func encodeVec(v []int8) string {
	raw := make([]byte, len(v))
	for i, x := range v {
		raw[i] = byte(x)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func decodeVec(s string) []int8 {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	out := make([]int8, len(raw))
	for i, b := range raw {
		out[i] = int8(b)
	}
	return out
}

// cosVec : le cosinus entre v (int8) et le vecteur unitaire q.
func cosVec(v []int8, q []float32) float32 {
	if len(v) == 0 || len(v) != len(q) {
		return 0
	}
	var dot, norm float32
	for i, x := range v {
		f := float32(x)
		dot += f * q[i]
		norm += f * f
	}
	if norm == 0 {
		return 0
	}
	return dot / float32(math.Sqrt(float64(norm)))
}
