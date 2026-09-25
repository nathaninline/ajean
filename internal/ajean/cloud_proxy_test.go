package ajean

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Le relais GPU Cloud se comporte comme llama-server : clé de la machine exigée,
// clé du GPU Cloud substituée, réveil (503) attendu, /health sans réveiller.
func TestCloudProxy(t *testing.T) {
	calls, sawKey := 0, ""
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		sawKey = r.Header.Get("Authorization")
		if calls == 1 {
			http.Error(w, `{"error":{"message":"Loading model"}}`, http.StatusServiceUnavailable)
			return
		}
		b, _ := io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"path":"` + r.URL.Path + `","body":` + string(b) + `}`))
	}))
	defer up.Close()

	cloudTarget.Lock()
	cloudTarget.url, cloudTarget.at = up.URL, time.Now().Add(time.Hour)
	cloudTarget.Unlock()
	defer func() {
		cloudTarget.Lock()
		cloudTarget.url, cloudTarget.at = "", time.Time{}
		cloudTarget.Unlock()
	}()

	h := cloudProxyHandler("cle-machine")
	do := func(path, key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	if rr := do("/v1/chat/completions", "mauvaise", `{}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("mauvaise clé : %d, attendu 401", rr.Code)
	}
	if calls != 0 {
		t.Fatal("une requête non autorisée ne doit pas atteindre le GPU")
	}
	if rr := do("/health", "", ""); rr.Code != 200 || calls != 0 {
		t.Fatalf("/health : %d (appels GPU %d), attendu 200 sans réveil", rr.Code, calls)
	}
	// 1er appel : 503 (modèle en chargement), le relais attend puis rejoue.
	start := time.Now()
	rr := do("/v1/chat/completions", "cle-machine", `{"model":"x"}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"path":"/v1/chat/completions"`) || !strings.Contains(rr.Body.String(), `"model":"x"`) {
		t.Fatalf("réponse : %d %s", rr.Code, rr.Body.String())
	}
	if calls != 2 || time.Since(start) < 4*time.Second {
		t.Fatalf("le 503 doit être rejoué (appels %d)", calls)
	}
	if sawKey != "Bearer "+cloudAPIKey() || strings.Contains(sawKey, "cle-machine") {
		t.Fatalf("clé transmise au GPU : %q", sawKey)
	}
}
