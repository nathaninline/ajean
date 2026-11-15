package memory

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ajean/internal/config"
)

// Un faux modèle d'embeddings : les mots d'un même sujet tombent sur le même
// axe, si bien que « véhicule » et « voiture » se ressemblent sans partager de
// lettres. Un texte sans sujet connu reçoit un axe à lui.
var fakeTopics = map[string]int{
	"voiture": 0, "vehicule": 0, "garage": 0, "auto": 0,
	"chat": 1, "felin": 1, "croquettes": 1,
	"gateau": 2, "dessert": 2, "patisserie": 2,
}

func fakeVector(text string) []float32 {
	v := make([]float32, 64)
	known := false
	for w := range words(text) {
		if axis, ok := fakeTopics[w]; ok {
			v[axis]++
			known = true
		}
	}
	if !known {
		h := fnv.New32a()
		h.Write([]byte(text))
		v[8+h.Sum32()%56] = 1
	}
	return v
}

// fakeEmbeddings démarre le faux serveur et branche la configuration dessus.
// down coupe l'API ; calls compte les textes vectorisés.
func fakeEmbeddings(t *testing.T) (down *atomic.Bool, calls *atomic.Int64) {
	t.Helper()
	down, calls = new(atomic.Bool), new(atomic.Int64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if down.Load() || r.URL.Path != "/v1/embeddings" || json.NewDecoder(r.Body).Decode(&req) != nil || req.Model != "faux-embed" {
			http.Error(w, `{"error":{"message":"indisponible"}}`, http.StatusServiceUnavailable)
			return
		}
		type item struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		var resp struct {
			Data []item `json:"data"`
		}
		for i, text := range req.Input {
			resp.Data = append(resp.Data, item{i, fakeVector(text)})
		}
		calls.Add(int64(len(req.Input)))
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	if err := config.Update(func(c *config.Config) error {
		c.Providers = []config.Provider{{ID: "p", Name: "Faux", URL: srv.URL + "/v1"}}
		c.Embed = config.ModelRef{Provider: "p", Model: "faux-embed"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return down, calls
}

func TestQuantize(t *testing.T) {
	a := unit([]float32{0.3, -0.8, 0.1, 0.5})
	b := unit([]float32{0.25, -0.7, 0.2, 0.6})
	want := dot(a, b)
	qa := quantize(a)
	if got := qa.cos(b); math.Abs(float64(got-want)) > 0.01 {
		t.Errorf("cosinus après quantification : %v, attendu %v", got, want)
	}
	if back := decodeVec(qa.encode()); len(back) != len(qa) || back.cos(a) < 0.999 {
		t.Errorf("aller-retour base64 : %v → %v", qa, back)
	}
	if got := qa.cos([]float32{1, 0}); got != 0 {
		t.Errorf("dimensions différentes : %v", got)
	}
	if got := dot(qa.float(), a); got < 0.999 {
		t.Errorf("vecteur unitaire restitué : %v", got)
	}
}

func TestSemanticSearchAndRecall(t *testing.T) {
	setup(t)
	LogExchange("La voiture est passée au garage Dupont pour la révision", "C'est noté.")
	LogExchange("Le chat a fini ses croquettes", "Il faudra en racheter.")
	LogExchange("Rendez-vous chez le dentiste mardi", "Ajouté.")
	query := "mon véhicule fait un bruit étrange"

	// Sans modèle d'embeddings : les mots seuls, qui ne trouvent rien ici.
	if err := indexPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if out := run(t, "memory_search", map[string]any{"query": query}); !strings.HasPrefix(out, "[aucun résultat]") {
		t.Fatalf("par mots, rien ne devait correspondre : %s", out)
	}

	down, calls := fakeEmbeddings(t)
	if err := indexPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	all, _ := Journal("", 0)
	var file monthFile
	_ = readState(vectorsPath(journalMonths()[0]), &file)
	if file.Model != "faux-embed" || len(file.Vectors) != len(all) {
		t.Fatalf("une entrée du journal = un vecteur : %d vecteurs pour %d entrées", len(file.Vectors), len(all))
	}
	indexed := calls.Load()
	if err := indexPending(t.Context()); err != nil || calls.Load() != indexed {
		t.Errorf("rien à réindexer : %v, %d appels de plus", err, calls.Load()-indexed)
	}

	out := run(t, "memory_search", map[string]any{"query": query})
	if !strings.Contains(strings.Split(out, "\n")[0], "garage Dupont") || strings.Contains(out, "croquettes") || strings.Contains(out, "dentiste") {
		t.Errorf("par le sens, la voiture devait sortir seule :\n%s", out)
	}

	// Le rappel automatique : l'échange proche par le sens, et la fiche dont le
	// but correspond, en entier.
	if err := UpdateFiche("", "entretien-auto", "Quand la voiture doit passer au garage", "Appeler le garage Dupont le matin"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateFiche("", "recette-fondant", "Pour un gateau au chocolat en dessert", "Deux cents grammes de chocolat"); err != nil {
		t.Fatal(err)
	}
	got := Recall(t.Context(), query, nil)
	if !strings.Contains(got, "Fiche entretien-auto") || !strings.Contains(got, "Appeler le garage Dupont le matin") || !strings.Contains(got, "révision") {
		t.Errorf("rappel par le sens :\n%s", got)
	}
	if strings.Contains(got, "croquettes") || strings.Contains(got, "fondant") {
		t.Errorf("rappel sans rapport :\n%s", got)
	}
	if got := Recall(t.Context(), "quel temps fera-t-il demain matin", nil); got != "" {
		t.Errorf("rien de proche par le sens : %q", got)
	}

	// Une entrée supprimée perd son vecteur à la passe suivante.
	if err := DeleteEntry(all[len(all)-1].ID); err != nil {
		t.Fatal(err)
	}
	LogExchange("Un dessert pour dimanche ?", "Une patisserie de chez Marcel.")
	if err := indexPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	file = monthFile{}
	_ = readState(vectorsPath(journalMonths()[0]), &file)
	now, _ := Journal("", 0)
	if _, kept := file.Vectors[all[len(all)-1].ID]; kept || len(file.Vectors) != len(now) {
		t.Errorf("vecteurs après suppression : %d pour %d entrées", len(file.Vectors), len(now))
	}

	// L'API tombe : tout continue par les mots, sans bruit.
	down.Store(true)
	LogExchange("La tondeuse est rangée dans le cabanon", "Entendu.")
	if err := indexPending(t.Context()); err == nil {
		t.Error("l'indexation devait signaler l'échec à sa boucle")
	}
	if out := run(t, "memory_search", map[string]any{"query": "où est la tondeuse"}); !strings.Contains(out, "cabanon") {
		t.Errorf("recherche par mots pendant la panne :\n%s", out)
	}
	if got := Recall(t.Context(), "où est rangée la tondeuse", nil); !strings.Contains(got, "cabanon") {
		t.Errorf("rappel par mots pendant la panne :\n%s", got)
	}
	// Au retour de l'API, l'entrée fraîche est jugée par les mots tant qu'elle
	// n'a pas de vecteur.
	down.Store(false)
	if got := Recall(t.Context(), "où est rangée la tondeuse", nil); !strings.Contains(got, "cabanon") {
		t.Errorf("entrée pas encore indexée invisible :\n%s", got)
	}

	// Changer de modèle invalide les vecteurs : ils sont reconstruits.
	if err := config.Update(func(c *config.Config) error { c.Embed.Model = "autre-modele"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := indexPending(t.Context()); err == nil {
		t.Error("le faux serveur ne connaît pas ce modèle")
	}
	if out := run(t, "memory_search", map[string]any{"query": "où est la tondeuse"}); !strings.Contains(out, "cabanon") {
		t.Errorf("recherche après changement de modèle :\n%s", out)
	}
}

func TestIndexerRunsInBackground(t *testing.T) {
	setup(t)
	fakeEmbeddings(t)
	ctx, cancel := context.WithCancel(t.Context())
	StartIndexer(ctx)
	StartIndexer(ctx) // un second appel ne lance rien de plus
	LogExchange("Le gateau de mamie était un dessert parfait", "Je garde la recette.")
	entries, _ := Journal("", 0)
	deadline := time.Now().Add(10 * time.Second)
	for {
		var file monthFile
		_ = readState(vectorsPath(journalMonths()[0]), &file)
		if _, ok := file.Vectors[entries[0].ID]; ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("l'entrée n'a pas été indexée en fond")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	for indexerRunning.Load() {
		if time.Now().After(deadline) {
			t.Fatal("l'indexeur ne s'est pas arrêté")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(vectorsPath(journalMonths()[0])); err != nil {
		t.Error(err)
	}
}

func TestIndexerQuietWithoutEmbeddings(t *testing.T) {
	setup(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	StartIndexer(ctx)
	LogExchange("Rien à vectoriser ici aujourd'hui", "En effet.")
	if got := Recall(ctx, "rien à vectoriser ici", nil); !strings.Contains(got, "vectoriser") {
		t.Errorf("rappel sans modèle d'embeddings : %q", got)
	}
	if _, err := os.Stat(statePath("vectors")); err == nil {
		t.Error("aucun vecteur ne devait être écrit")
	}
	cancel()
	for deadline := time.Now().Add(5 * time.Second); indexerRunning.Load(); {
		if time.Now().After(deadline) {
			t.Fatal("l'indexeur ne s'est pas arrêté")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
