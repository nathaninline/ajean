package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScreensServeThePage(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	h := New()
	for _, p := range []string{"/", "/settings", "/settings/presets", "/memory/fiches", "/machine", "/c/20261118-222048"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<html") {
			t.Errorf("%s : statut %d", p, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/inconnu", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("/inconnu : statut %d", w.Code)
	}
}