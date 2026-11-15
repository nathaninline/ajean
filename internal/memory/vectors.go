package memory

import (
	"context"
	"encoding/base64"
	"errors"
	"maps"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ajean/internal/embedder"
)

// La recherche par le sens. Par mots, un souvenir ne se retrouve que si la
// requête reprend ses mots ; par le sens, une question reformulée le retrouve
// aussi. Quand une source de vecteurs est réglée (une API d'embeddings ou le
// moteur local : paquet embedder), chaque entrée du journal reçoit un vecteur,
// calculé en fond et rangé par mois à côté de l'état technique. Sans source,
// ou si elle échoue, tout ce fichier se tait : la recherche par mots continue
// seule.

const (
	embedDocChars = 1200 // une entrée plus longue est tronquée : le début porte le sujet
	embedBatch    = 16
	retryMin      = 30 * time.Second
	retryMax      = 30 * time.Minute
)

// vec : un vecteur quantifié en int8. Quatre fois moins de place qu'en float32,
// sur le disque comme en mémoire vive, pour une perte de précision négligeable.
type vec []int8

// unit ramène un vecteur à la norme 1 : le produit scalaire de deux vecteurs
// unitaires est leur cosinus.
func unit(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	norm := float32(math.Sqrt(sum))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x / norm
	}
	return out
}

func quantize(v []float32) vec {
	var top float32
	for _, x := range v {
		top = max(top, float32(math.Abs(float64(x))))
	}
	out := make(vec, len(v))
	if top == 0 {
		return out
	}
	for i, x := range v {
		out[i] = int8(math.Round(float64(x / top * 127)))
	}
	return out
}

// float : le vecteur unitaire que v représente.
func (v vec) float() []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return unit(out)
}

// dot : le produit scalaire (0 si les dimensions diffèrent : vecteurs de deux modèles).
func dot(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var sum float32
	for i, x := range a {
		sum += x * b[i]
	}
	return sum
}

// cos : le cosinus entre v et le vecteur unitaire q (0 si v manque ou vient
// d'un autre modèle, de dimension différente).
func (v vec) cos(q []float32) float32 {
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

func (v vec) encode() string {
	raw := make([]byte, len(v))
	for i, x := range v {
		raw[i] = byte(x)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func decodeVec(s string) vec {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	out := make(vec, len(raw))
	for i, b := range raw {
		out[i] = int8(b)
	}
	return out
}

// monthFile : les vecteurs d'un mois sur le disque. Le modèle est noté (plus
// exactement l'espace de la source, embedder.Source.Space) : un vecteur n'a de
// sens que comparé à ceux du même modèle, en changer invalide le fichier
// (reconstruit tout seul).
type monthFile struct {
	Model   string            `json:"model"`
	Vectors map[string]string `json:"vectors"`
}

// monthIndex : les vecteurs d'un mois en mémoire vive. Jamais modifié une fois
// publié : l'indexeur en publie un nouveau à chaque lot, les lecteurs gardent
// le leur sans verrou.
type monthIndex struct {
	model string
	byID  map[string]vec
}

// vectors : le cache des vecteurs. Son verrou est une feuille : on ne prend
// jamais mu en le tenant.
var vectors = struct {
	sync.Mutex
	months   map[string]monthIndex // par chemin de fichier
	purposes map[string][]float32  // but des fiches déjà vectorisé (modèle + texte)
}{months: map[string]monthIndex{}, purposes: map[string][]float32{}}

func vectorsPath(month string) string { return statePath("vectors", month+".json") }

// monthVectors : les vecteurs d'un mois pour ce modèle (verrou de vectors tenu).
func monthVectors(month, model string) map[string]vec {
	path := vectorsPath(month)
	if idx, ok := vectors.months[path]; ok && idx.model == model {
		return idx.byID
	}
	var file monthFile
	_ = readState(path, &file) // illisibles ou absents : les vecteurs se recalculent
	idx := monthIndex{model: model, byID: map[string]vec{}}
	if file.Model == model {
		for id, s := range file.Vectors {
			if v := decodeVec(s); len(v) > 0 {
				idx.byID[id] = v
			}
		}
	}
	vectors.months[path] = idx
	return idx.byID
}

// vectorsOf : de quoi retrouver le vecteur d'une entrée (nil si elle n'est pas
// encore indexée).
func vectorsOf(model string, entries []entry) func(entry) vec {
	byMonth := map[string]map[string]vec{}
	vectors.Lock()
	for _, e := range entries {
		if _, ok := byMonth[e.month]; !ok {
			byMonth[e.month] = monthVectors(e.month, model)
		}
	}
	vectors.Unlock()
	return func(e entry) vec { return byMonth[e.month][e.id] }
}

// embed : un vecteur unitaire par texte. query : ces textes sont des questions,
// pas des textes à retrouver.
func embed(ctx context.Context, src embedder.Source, texts []string, query bool) ([][]float32, error) {
	vs, err := src.Embed(ctx, texts, query)
	if err != nil {
		return nil, err
	}
	for i, v := range vs {
		if len(v) == 0 {
			return nil, errors.New("embeddings : vecteur vide")
		}
		vs[i] = unit(v)
	}
	return vs, nil
}

// embedQuery : le vecteur d'une requête, si une source de vecteurs est réglée
// et répond à temps. ok=false : on fait sans le sens.
func embedQuery(ctx context.Context, text string, wait time.Duration) (q []float32, model string, ok bool) {
	src, ok := embedder.Active()
	if !ok {
		return nil, "", false
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	vs, err := embed(ctx, src, []string{text}, true)
	if err != nil {
		return nil, "", false
	}
	return vs[0], src.Space, true
}

// purposeVectors : le vecteur du BUT de chaque fiche (nom et « quand »),
// calculé à la demande et gardé en mémoire vive. ok=false si une fiche n'a pu
// être vectorisée : mieux vaut alors juger toutes les fiches par les mots que
// d'en laisser une de côté.
func purposeVectors(ctx context.Context, fs []fiche) (byName map[string][]float32, ok bool) {
	src, ok := embedder.Active()
	if !ok {
		return nil, false
	}
	key := func(f fiche) string { return src.Space + "\x00" + f.Name + ": " + f.When }
	byName = map[string][]float32{}
	var missing []fiche
	vectors.Lock()
	for _, f := range fs {
		if v, ok := vectors.purposes[key(f)]; ok {
			byName[f.Name] = v
		} else {
			missing = append(missing, f)
		}
	}
	vectors.Unlock()
	if len(missing) == 0 {
		return byName, true
	}
	texts := make([]string, len(missing))
	for i, f := range missing {
		texts[i] = strings.ReplaceAll(f.Name, "-", " ") + ": " + f.When
	}
	vs, err := embed(ctx, src, texts, false)
	if err != nil {
		return nil, false
	}
	vectors.Lock()
	for i, f := range missing {
		vectors.purposes[key(f)] = vs[i]
		byName[f.Name] = vs[i]
	}
	vectors.Unlock()
	return byName, true
}

// ---- l'indexation en fond --------------------------------------------------------

var (
	indexerWake    = make(chan struct{}, 1)
	indexerRunning atomic.Bool
	indexing       sync.Mutex // une seule passe d'indexation à la fois
)

// WakeIndexer réveille l'indexeur : une entrée vient d'arriver au journal, ou la
// source des vecteurs vient de changer (il y a tout l'index à construire). Ne
// bloque jamais.
func WakeIndexer() {
	select {
	case indexerWake <- struct{}{}:
	default:
	}
}

// StartIndexer lance l'indexation en fond du journal, jusqu'à la fin de ctx.
// Elle repart à chaque nouvelle entrée. Si l'API échoue, elle se tait et
// réessaie plus tard, de plus en plus rarement. Sans source de vecteurs
// réglée, elle ne fait rien.
func StartIndexer(ctx context.Context) {
	if !indexerRunning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer indexerRunning.Store(false)
		var backoff time.Duration
		for {
			var retry <-chan time.Time
			wake := indexerWake
			if err := indexPending(ctx); err != nil {
				// Pendant le recul, une nouvelle entrée ne relance rien : une API
				// en panne ne doit pas être sollicitée à chaque message.
				backoff = min(max(2*backoff, retryMin), retryMax)
				retry, wake = time.After(backoff), nil
			} else {
				backoff = 0
			}
			select {
			case <-ctx.Done():
				return
			case <-retry:
			case <-wake:
			}
		}
	}()
}

// indexPending donne un vecteur aux entrées du journal qui n'en ont pas, le
// mois le plus récent d'abord (c'est celui qu'on cherche le plus).
func indexPending(ctx context.Context) error {
	src, ok := embedder.Active()
	if !ok {
		return nil
	}
	indexing.Lock()
	defer indexing.Unlock()
	months := journalMonths()
	for i := len(months) - 1; i >= 0; i-- {
		mu.Lock()
		entries := monthLocked(months[i])
		mu.Unlock()
		if err := indexMonth(ctx, src, months[i], entries); err != nil {
			return err
		}
	}
	return nil
}

func indexMonth(ctx context.Context, src embedder.Source, month string, entries []entry) error {
	vectors.Lock()
	current := monthVectors(month, src.Space)
	vectors.Unlock()
	// Une entrée du journal = un vecteur : ceux des entrées supprimées partent.
	next := map[string]vec{}
	var todo []entry
	for _, e := range entries {
		if v, ok := current[e.id]; ok {
			next[e.id] = v
		} else if e.text != "" {
			todo = append(todo, e)
		}
	}
	if len(todo) == 0 && len(next) == len(current) {
		return nil
	}
	var failure error
	for len(todo) > 0 {
		batch := todo[:min(embedBatch, len(todo))]
		texts := make([]string, len(batch))
		for i, e := range batch {
			texts[i] = clip(e.text, embedDocChars)
		}
		vs, err := embed(ctx, src, texts, false)
		if err != nil {
			failure = err
			break
		}
		next = maps.Clone(next)
		for i, e := range batch {
			next[e.id] = quantize(vs[i])
		}
		vectors.Lock()
		vectors.months[vectorsPath(month)] = monthIndex{model: src.Space, byID: next}
		vectors.Unlock()
		todo = todo[len(batch):]
	}
	// Ce qui a été calculé avant un échec est gardé : rien à refaire au prochain essai.
	vectors.Lock()
	vectors.months[vectorsPath(month)] = monthIndex{model: src.Space, byID: next}
	vectors.Unlock()
	file := monthFile{Model: src.Space, Vectors: map[string]string{}}
	for id, v := range next {
		file.Vectors[id] = v.encode()
	}
	if err := writeState(vectorsPath(month), file); err != nil && failure == nil {
		failure = err
	}
	return failure
}
