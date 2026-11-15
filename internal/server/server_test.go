package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// do envoie une requête au gestionnaire, comme le ferait un navigateur visant host.
func do(t *testing.T, method, path, host, origin, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	return rec
}

func TestGuardOnlyServesTheLocalMachine(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	cases := []struct {
		name, host, origin string
		want               int
	}{
		{"adresse locale", "127.0.0.1:8090", "", http.StatusOK},
		{"localhost", "localhost:8090", "http://localhost:8090", http.StatusOK},
		{"IPv6 locale", "[::1]:8090", "", http.StatusOK},
		// DNS rebinding : le site pointe son propre nom vers 127.0.0.1.
		{"nom d'un autre site", "evil.example:8090", "http://evil.example:8090", http.StatusForbidden},
		{"origine d'un autre site", "127.0.0.1:8090", "http://evil.example", http.StatusForbidden},
	}
	for _, c := range cases {
		if got := do(t, http.MethodGet, "/api/state", c.host, c.origin, "").Code; got != c.want {
			t.Errorf("%s : statut %d, attendu %d", c.name, got, c.want)
		}
	}
}

func TestGuardRejectsCrossSiteForms(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	req := httptest.NewRequest(http.MethodPost, "/api/convs/new", strings.NewReader("x=1"))
	req.Host = "127.0.0.1:8090"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded") // ce qu'envoie un formulaire d'un autre site
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusUnsupportedMediaType)
	}
}

func TestGuardRequiresTheAccessKey(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	t.Setenv("AJEAN_WEB_KEY", "secret")
	if got := do(t, http.MethodGet, "/api/state", "192.168.1.10:8090", "", "").Code; got != http.StatusUnauthorized {
		t.Errorf("sans clé : statut %d, attendu 401", got)
	}
	if got := do(t, http.MethodGet, "/api/state?key=secret", "192.168.1.10:8090", "", "").Code; got != http.StatusOK {
		t.Errorf("avec clé : statut %d, attendu 200", got)
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"rapport.pdf":            "rapport.pdf",
		`C:\Users\moi\notes.txt`: "notes.txt",
		"../../etc/passwd":       "passwd",
		"a|b?.txt":               "a_b_.txt",
		"...":                    "fichier",
		"":                       "fichier",
	}
	for in, want := range cases {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, attendu %q", in, got, want)
		}
	}
}

// La page interdit à ses cadres de charger une adresse : une app ne peut pas
// faire naviguer le sien vers un site pour y faire sortir des données. Et le
// cadre d'une app, sans origine, n'a accès à aucune route.
func TestAppsCannotReachOut(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	page := do(t, http.MethodGet, "/", "127.0.0.1:8090", "", "")
	if got := page.Header().Get("Content-Security-Policy"); got != "frame-src 'none'" {
		t.Errorf("politique de la page = %q", got)
	}
	if got := do(t, http.MethodGet, "/api/memory", "127.0.0.1:8090", "null", "").Code; got != http.StatusForbidden {
		t.Errorf("requête d'un cadre sans origine : statut %d, attendu 403", got)
	}
}
