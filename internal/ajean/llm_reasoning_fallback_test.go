package ajean

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Un fournisseur qui refuse le niveau de réflexion du preset ne doit pas faire
// échouer le tour : la requête repart sans lui, la réponse arrive, l'interface
// est prévenue, et le tour suivant ne renvoie plus le champ refusé.
func TestReflexionRefuseeParLeFournisseur(t *testing.T) {
	testHome(t)
	var withField, total int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		total++
		if strings.Contains(string(body), "chat_template_kwargs") {
			withField++
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown field chat_template_kwargs"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseChunk("bon") + sseChunk("jour") + "data: [DONE]\n\n"))
	}))
	defer srv.Close()
	for k, v := range map[string]string{"EXTERNAL": "1", "EXTERNAL_URL": srv.URL + "/v1", "EXTERNAL_MODEL": "m-refus", "REASONING_EFFORT": "high"} {
		if err := SetConfigKey(k, v); err != nil {
			t.Fatal(err)
		}
	}
	turn := func() (string, bool) {
		var content strings.Builder
		ignored := false
		_, err := runChat(context.Background(), []Message{{Role: "user", Content: "bonjour"}}, 0.7, Caps{}, func(ev StreamEvent) bool {
			if ev.Content != "" {
				content.WriteString(ev.Content)
			}
			if ev.Stats != nil && ev.Stats.ReasoningIgnored {
				ignored = true
			}
			return true
		}, nil)
		if err != nil {
			t.Fatalf("le tour a échoué : %v", err)
		}
		return content.String(), ignored
	}

	got, ignored := turn()
	if got != "bonjour" {
		t.Errorf("réponse = %q, attendu « bonjour »", got)
	}
	if !ignored {
		t.Error("l'interface n'est pas prévenue que le niveau de réflexion est ignoré")
	}
	if withField != 1 || total != 2 {
		t.Errorf("1er tour : %d requête(s) dont %d avec le champ, attendu 2 dont 1", total, withField)
	}

	// Tour suivant : le refus est retenu, une seule requête, sans le champ.
	if got, ignored := turn(); got != "bonjour" || !ignored {
		t.Errorf("2e tour : réponse %q, prévenu %v", got, ignored)
	}
	if withField != 1 || total != 3 {
		t.Errorf("2e tour : %d requête(s) dont %d avec le champ, attendu 3 dont 1", total, withField)
	}

	// Niveau changé dans le preset : on retente avec le nouveau.
	if err := SetConfigKey("REASONING_EFFORT", "low"); err != nil {
		t.Fatal(err)
	}
	turn()
	if withField != 2 {
		t.Errorf("niveau changé : le champ n'a pas été retenté (%d envois)", withField)
	}
}
