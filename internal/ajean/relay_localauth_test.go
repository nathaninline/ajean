package ajean

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Changer la clé de pilotage ne doit PAS couper l'accès distant jusqu'au
// prochain redémarrage. La clé était capturée à l'ouverture du tunnel : après un
// `ajean set-web-key`, le tunnel injectait l'ancienne, tout passait en 401, et
// le portail restait bloqué sur « chargement de la conversation » sans jamais
// dire pourquoi.
func TestWithLocalAuthSuitLaCleCourante(t *testing.T) {
	testHome(t)
	if err := putStr(bkState, "web_key", "cle-numero-1"); err != nil {
		t.Fatal(err)
	}
	// Handler protégé, comme l'API web réelle.
	protege := withLocalAuth(requireWebAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	appel := func() int {
		rr := httptest.NewRecorder()
		protege.ServeHTTP(rr, httptest.NewRequest("GET", "/api/status", nil))
		return rr.Code
	}
	if code := appel(); code != 200 {
		t.Fatalf("clé initiale : HTTP %d", code)
	}
	// La clé change pendant que le tunnel tourne (bouton de l'interface, CLI).
	if err := putStr(bkState, "web_key", "cle-numero-2"); err != nil {
		t.Fatal(err)
	}
	if code := appel(); code != 200 {
		t.Fatalf("après changement de clé : HTTP %d — le tunnel injecte encore l'ancienne", code)
	}
	// Clé retirée : l'API est ouverte, le tunnel ne doit rien casser non plus.
	if err := putStr(bkState, "web_key", ""); err != nil {
		t.Fatal(err)
	}
	if code := appel(); code != 200 {
		t.Fatalf("sans clé : HTTP %d", code)
	}
}

// Une requête qui porte DÉJÀ une autorisation n'est pas réécrite : c'est le
// client distant qui décide dans ce cas.
func TestWithLocalAuthNecraseAucunEnTete(t *testing.T) {
	testHome(t)
	if err := putStr(bkState, "web_key", "cle-locale"); err != nil {
		t.Fatal(err)
	}
	var vu string
	protege := withLocalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vu = r.Header.Get("Authorization")
	}))
	req := httptest.NewRequest("GET", "/api/status", nil)
	req.Header.Set("Authorization", "Bearer venue-du-client")
	protege.ServeHTTP(httptest.NewRecorder(), req)
	if vu != "Bearer venue-du-client" {
		t.Fatalf("en-tête client écrasé : %q", vu)
	}
}
