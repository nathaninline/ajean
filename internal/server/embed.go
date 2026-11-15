package server

import (
	"errors"
	"net/http"
	"strings"

	"ajean/internal/config"
	"ajean/internal/embedder"
	"ajean/internal/llm"
)

// La recherche par le sens tire ses vecteurs d'une API d'embeddings ou du
// moteur local, qu'AJEAN installe lui-même (paquet embedder). L'interface suit
// l'installation par l'événement « embed », puis relit l'état.

// handleEmbedLocal : l'état du moteur local.
func handleEmbedLocal(w http.ResponseWriter, r *http.Request) {
	reply(w, embedder.Status())
}

// handleEmbedInstall (POST {url?}) : lance l'installation en arrière-plan. url
// désigne un modèle GGUF sur Hugging Face ; absente, c'est le modèle proposé
// d'office. À la fin, la recherche par le sens passe par le moteur local.
func handleEmbedInstall(w http.ResponseWriter, r *http.Request) {
	var q struct{ URL string }
	if body(w, r, &q) {
		done(w, embedder.Install(q.URL), http.StatusBadRequest)
	}
}

func handleEmbedCancel(w http.ResponseWriter, r *http.Request) {
	embedder.Cancel()
	ok(w)
}

// handleEmbedRemove : supprime le moteur et son modèle. La recherche par le
// sens est coupée si elle passait par lui.
func handleEmbedRemove(w http.ResponseWriter, r *http.Request) {
	done(w, embedder.Remove(), http.StatusInternalServerError)
}

// handleEmbedTest (POST {provider, model}) : demande un vecteur, pour vérifier
// que le modèle d'embeddings existe chez ce fournisseur avant de s'en servir,
// ou que le moteur local (provider « local ») démarre. Renvoie la taille des
// vecteurs.
func handleEmbedTest(w http.ResponseWriter, r *http.Request) {
	var ref config.ModelRef
	if !body(w, r, &ref) {
		return
	}
	texts := []string{"bonjour"}
	var vectors [][]float32
	var err error
	if ref.Provider == config.LocalEmbed {
		vectors, err = embedder.Embed(r.Context(), texts, true)
	} else {
		p, found := config.Get().Provider(ref.Provider)
		ep := llm.Endpoint{URL: p.URL, Key: p.Key, Model: strings.TrimSpace(ref.Model)}
		if !found || !ep.Ready() {
			fail(w, http.StatusBadRequest, errors.New("il faut un fournisseur et un modèle"))
			return
		}
		vectors, err = llm.Embed(r.Context(), ep, texts)
	}
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	reply(w, map[string]int{"dims": len(vectors[0])})
}
