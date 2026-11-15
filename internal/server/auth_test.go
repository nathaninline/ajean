package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ajean/internal/auth"
)

const testPassword = "un bon mot de passe"

// call envoie une requête telle qu'un visiteur l'enverrait : remote est l'adresse
// d'où elle arrive, cookie la session qu'il porte, headers ce qu'un proxy ajoute.
func call(t *testing.T, method, path, remote, host, cookie, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr, req.Host = remote, host
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	return rec
}

func cookieOf(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	return nil
}

const (
	here    = "127.0.0.1:50000"   // un navigateur de la machine
	outside = "203.0.113.7:50000" // un visiteur d'Internet
	public  = "ajean.example"
)

// Sans mot de passe, seule la machine elle-même entre : ni un visiteur direct,
// ni un visiteur relayé par un proxy installé sur la machine.
func TestWithoutPasswordOnlyThisMachineGetsIn(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if got := call(t, "GET", "/api/state", here, local, "", "").Code; got != http.StatusOK {
		t.Errorf("depuis la machine : %d", got)
	}
	if got := call(t, "GET", "/api/state", outside, "203.0.113.1:8090", "", "").Code; got != http.StatusForbidden {
		t.Errorf("visiteur direct : %d, attendu 403", got)
	}
	// Un proxy mal réglé réécrit Host en 127.0.0.1 : l'adresse d'arrivée du visiteur le trahit.
	if got := call(t, "GET", "/api/state", outside, local, "", "").Code; got != http.StatusForbidden {
		t.Errorf("visiteur direct se disant local : %d, attendu 403", got)
	}
	// Proxy sur la machine : la requête arrive de 127.0.0.1, mais relayée.
	if got := call(t, "GET", "/api/state", here, local, "", "", "X-Forwarded-For", "203.0.113.7").Code; got != http.StatusForbidden {
		t.Errorf("visiteur relayé par un proxy : %d, attendu 403", got)
	}
	if got := call(t, "GET", "/api/state", here, public, "", "").Code; got != http.StatusForbidden {
		t.Errorf("visiteur relayé sous un nom public : %d, attendu 403", got)
	}
	// Et il ne peut pas poser le premier mot de passe à la place du propriétaire.
	if got := call(t, "POST", "/api/auth/password", outside, public, "", `{"password":"`+testPassword+`"}`).Code; got != http.StatusForbidden {
		t.Errorf("premier mot de passe posé de l'extérieur : %d, attendu 403", got)
	}
	if auth.Configured() {
		t.Fatal("un visiteur a posé le mot de passe")
	}
}

func TestLoginOpensASession(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	// Le propriétaire pose le mot de passe depuis la machine, et reste connecté.
	set := call(t, "POST", "/api/auth/password", here, local, "", `{"password":"`+testPassword+`"}`)
	if set.Code != http.StatusOK || cookieOf(set) == nil {
		t.Fatalf("pose du mot de passe : %d", set.Code)
	}
	// Désormais tout le monde se connecte, la machine comprise.
	if got := call(t, "GET", "/api/state", here, local, "", "").Code; got != http.StatusUnauthorized {
		t.Errorf("machine sans session : %d, attendu 401", got)
	}
	if got := call(t, "GET", "/api/state", outside, public, "", "").Code; got != http.StatusUnauthorized {
		t.Errorf("visiteur sans session : %d, attendu 401", got)
	}
	if got := call(t, "GET", "/api/state", outside, public, "jeton-invente", "").Code; got != http.StatusUnauthorized {
		t.Errorf("jeton inventé : %d, attendu 401", got)
	}
	if got := call(t, "POST", "/api/auth/login", outside, public, "", `{"password":"mauvais mot de passe"}`).Code; got != http.StatusUnauthorized {
		t.Errorf("mauvais mot de passe : %d, attendu 401", got)
	}
	in := call(t, "POST", "/api/auth/login", outside, public, "", `{"password":"`+testPassword+`"}`)
	c := cookieOf(in)
	if in.Code != http.StatusOK || c == nil {
		t.Fatalf("connexion : %d", in.Code)
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie mal protégé : %+v", c)
	}
	if strings.Contains(in.Body.String(), c.Value) {
		t.Error("le jeton de session ne doit pas apparaître dans la réponse")
	}
	if got := call(t, "GET", "/api/state", outside, public, c.Value, "").Code; got != http.StatusOK {
		t.Errorf("avec session : %d", got)
	}
	// La clé dans l'adresse (ancienne façon) ne donne plus rien.
	if got := call(t, "GET", "/api/state?key="+strings.ReplaceAll(testPassword, " ", "%20"), outside, public, "", "").Code; got != http.StatusUnauthorized {
		t.Errorf("mot de passe dans l'adresse : %d, attendu 401", got)
	}
	// Déconnexion : la session ne vaut plus rien, même rejouée.
	if got := call(t, "POST", "/api/auth/logout", outside, public, c.Value, `{}`).Code; got != http.StatusOK {
		t.Errorf("déconnexion : %d", got)
	}
	if got := call(t, "GET", "/api/state", outside, public, c.Value, "").Code; got != http.StatusUnauthorized {
		t.Errorf("session fermée rejouée : %d, attendu 401", got)
	}
}

func TestCookieIsSecureOverHTTPS(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := auth.SetPassword(testPassword); err != nil {
		t.Fatal(err)
	}
	// Derrière un proxy HTTPS installé sur la machine.
	in := call(t, "POST", "/api/auth/login", here, public, "", `{"password":"`+testPassword+`"}`,
		"X-Forwarded-For", "203.0.113.7", "X-Forwarded-Proto", "https")
	if c := cookieOf(in); c == nil || !c.Secure {
		t.Errorf("cookie non réservé à HTTPS : %+v", c)
	}
	if in.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS absent en HTTPS")
	}
	// Un visiteur direct ne peut pas se dire en HTTPS par un en-tête.
	in = call(t, "POST", "/api/auth/login", outside, public, "", `{"password":"`+testPassword+`"}`, "X-Forwarded-Proto", "https")
	if c := cookieOf(in); c == nil || c.Secure {
		t.Errorf("en-tête de proxy cru venant d'un visiteur direct : %+v", c)
	}
}

func TestLoginIsThrottled(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := auth.SetPassword(testPassword); err != nil {
		t.Fatal(err)
	}
	const attacker = "198.51.100.23:40000"
	var last *httptest.ResponseRecorder
	for i := 0; i < 5; i++ {
		last = call(t, "POST", "/api/auth/login", attacker, public, "", `{"password":"essai numéro `+string(rune('a'+i))+`"}`)
	}
	if last.Code != http.StatusTooManyRequests || last.Header().Get("Retry-After") == "" {
		t.Fatalf("après 5 échecs : %d, attendu 429 avec Retry-After", last.Code)
	}
	if got := call(t, "POST", "/api/auth/login", attacker, public, "", `{"password":"`+testPassword+`"}`).Code; got != http.StatusTooManyRequests {
		t.Errorf("adresse bloquée, bon mot de passe : %d, attendu 429", got)
	}
	// Le propriétaire, depuis une autre adresse, n'est pas gêné.
	if got := call(t, "POST", "/api/auth/login", outside, public, "", `{"password":"`+testPassword+`"}`).Code; got != http.StatusOK {
		t.Errorf("autre adresse : %d", got)
	}
	// Derrière un proxy de la machine, c'est l'adresse du visiteur qui est comptée,
	// et celle qu'il déclare lui-même en tête de X-Forwarded-For est ignorée.
	for i := 0; i < 5; i++ {
		last = call(t, "POST", "/api/auth/login", here, public, "", `{"password":"encore un essai"}`, "X-Forwarded-For", "10.0.0."+string(rune('1'+i))+", 198.51.100.99")
	}
	if last.Code != http.StatusTooManyRequests {
		t.Errorf("visiteur relayé changeant d'adresse déclarée : %d, attendu 429", last.Code)
	}
}

func TestChangingThePassword(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := auth.SetPassword(testPassword); err != nil {
		t.Fatal(err)
	}
	mine, _ := auth.Open("203.0.113.7", "")
	other, _ := auth.Open("203.0.113.8", "")
	const next = "un nouveau mot de passe"
	// Sans session : refusé, même avec le bon mot de passe actuel.
	if got := call(t, "POST", "/api/auth/password", outside, public, "", `{"current":"`+testPassword+`","password":"`+next+`"}`).Code; got != http.StatusUnauthorized {
		t.Errorf("sans session : %d, attendu 401", got)
	}
	// Avec session mais sans le mot de passe actuel : refusé (session volée).
	if got := call(t, "POST", "/api/auth/password", outside, public, mine, `{"current":"je ne sais pas","password":"`+next+`"}`).Code; got != http.StatusForbidden {
		t.Errorf("mot de passe actuel faux : %d, attendu 403", got)
	}
	if got := call(t, "POST", "/api/auth/password", outside, public, mine, `{"current":"`+testPassword+`","password":"court"}`).Code; got != http.StatusBadRequest {
		t.Errorf("nouveau mot de passe trop court : %d, attendu 400", got)
	}
	done := call(t, "POST", "/api/auth/password", outside, public, mine, `{"current":"`+testPassword+`","password":"`+next+`"}`)
	c := cookieOf(done)
	if done.Code != http.StatusOK || c == nil {
		t.Fatalf("changement : %d", done.Code)
	}
	if auth.Valid(mine) || auth.Valid(other) || !auth.Valid(c.Value) {
		t.Error("après le changement, seules les anciennes sessions doivent être fermées")
	}
}

func TestLogoutOthers(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := auth.SetPassword(testPassword); err != nil {
		t.Fatal(err)
	}
	mine, _ := auth.Open("203.0.113.7", "")
	other, _ := auth.Open("203.0.113.8", "")
	if got := call(t, "POST", "/api/auth/logout-others", outside, public, mine, `{}`).Code; got != http.StatusOK {
		t.Fatalf("statut %d", got)
	}
	if !auth.Valid(mine) || auth.Valid(other) {
		t.Error("seules les autres sessions doivent être fermées")
	}
}

// Une page d'un autre site ne peut ni se connecter à notre place, ni lire où en est la porte.
func TestDoorRefusesOtherSites(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := auth.SetPassword(testPassword); err != nil {
		t.Fatal(err)
	}
	if got := call(t, "POST", "/api/auth/login", outside, public, "", `{"password":"`+testPassword+`"}`, "Origin", "https://evil.example").Code; got != http.StatusForbidden {
		t.Errorf("connexion depuis un autre site : %d, attendu 403", got)
	}
	if got := call(t, "GET", "/api/auth", outside, public, "", "", "Origin", "https://evil.example").Code; got != http.StatusForbidden {
		t.Errorf("état lu depuis un autre site : %d, attendu 403", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	for _, path := range []string{"/", "/api/auth"} {
		h := call(t, "GET", path, here, local, "", "").Header()
		for name, want := range map[string]string{
			"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer",
		} {
			if h.Get(name) != want {
				t.Errorf("%s : %s = %q, attendu %q", path, name, h.Get(name), want)
			}
		}
	}
}
