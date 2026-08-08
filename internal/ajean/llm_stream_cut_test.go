package ajean

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// sseServer sert un flux de complétion puis le COUPE brutalement (fermeture de
// la connexion sans terminer la réponse), comme le ferait un moteur qui perd sa
// connexion en plein milieu. Renvoie le port à mettre dans PORT.
func sseCuttingServer(t *testing.T, body string, cut bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
		w.(http.Flusher).Flush()
		if !cut {
			return
		}
		// Coupe la connexion sans fin de réponse propre : côté client, la lecture
		// du corps échoue (« unexpected EOF ») au lieu de se terminer.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("serveur de test non détournable")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

// chunk fabrique une ligne SSE de contenu.
func sseChunk(text string) string {
	return `data: {"choices":[{"delta":{"content":"` + text + `"}}]}` + "\n\n"
}

// Le cœur de l'issue #19 : llama-server termine normalement de son côté (« stop
// processing » dans son journal), mais la lecture du flux casse. Avant, le tour
// s'arrêtait EN SILENCE — l'agent « rendait la main sans avoir terminé, ni même
// commenté ». Il doit maintenant remonter une erreur explicite.
func TestFluxCoupeRemonteUneErreur(t *testing.T) {
	testHome(t)
	// Réponse annoncée sur 3 chunks mais coupée après le premier, sans [DONE].
	port := sseCuttingServer(t, sseChunk("début de r"), true)
	if err := SetConfigKey("PORT", port); err != nil {
		t.Fatal(err)
	}

	var gotErr error
	var content strings.Builder
	_, err := runChat(context.Background(), []Message{{Role: "user", Content: "bonjour"}}, 0.7, Caps{}, func(ev StreamEvent) bool {
		switch {
		case ev.Err != nil:
			gotErr = ev.Err
		case ev.Content != "":
			content.WriteString(ev.Content)
		}
		return true
	})
	if err == nil {
		t.Fatal("flux coupé : runChat a rendu la main SANS erreur (le tour s'arrêtait en silence)")
	}
	if gotErr == nil {
		t.Fatal("flux coupé : aucune erreur poussée vers l'interface")
	}
	if !strings.Contains(err.Error(), "coupé") {
		t.Errorf("message peu clair pour l'utilisateur : %v", err)
	}
	// Le texte déjà reçu n'est pas jeté : on a bien affiché ce qui était arrivé.
	if !strings.Contains(content.String(), "début de r") {
		t.Errorf("le texte reçu avant la coupure a été perdu : %q", content.String())
	}
}

// Contre-épreuve : un flux qui se termine PROPREMENT ne doit évidemment pas
// déclencher l'erreur, sans quoi chaque réponse normale se plaindrait.
func TestFluxCompletNeRemonteRien(t *testing.T) {
	testHome(t)
	port := sseCuttingServer(t, sseChunk("réponse complète")+"data: [DONE]\n\n", false)
	if err := SetConfigKey("PORT", port); err != nil {
		t.Fatal(err)
	}
	var gotErr error
	var content strings.Builder
	_, err := runChat(context.Background(), []Message{{Role: "user", Content: "bonjour"}}, 0.7, Caps{}, func(ev StreamEvent) bool {
		if ev.Err != nil {
			gotErr = ev.Err
		}
		if ev.Content != "" {
			content.WriteString(ev.Content)
		}
		return true
	})
	if err != nil || gotErr != nil {
		t.Fatalf("flux normal signalé en erreur : %v / %v", err, gotErr)
	}
	if content.String() != "réponse complète" {
		t.Fatalf("contenu = %q", content.String())
	}
}

// Un arrêt demandé par l'utilisateur (/stop) coupe aussi la lecture : il ne doit
// PAS se déguiser en panne du moteur.
func TestFluxAnnuleResteSilencieux(t *testing.T) {
	testHome(t)
	port := sseCuttingServer(t, sseChunk("a"), true)
	if err := SetConfigKey("PORT", port); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var gotErr error
	_, _ = runChat(ctx, []Message{{Role: "user", Content: "bonjour"}}, 0.7, Caps{}, func(ev StreamEvent) bool {
		if ev.Content != "" {
			cancel() // l'utilisateur clique sur stop dès le premier morceau
		}
		if ev.Err != nil {
			gotErr = ev.Err
		}
		return true
	})
	if gotErr != nil && strings.Contains(gotErr.Error(), "coupé") {
		t.Fatalf("un stop volontaire a été présenté comme une panne : %v", gotErr)
	}
}
