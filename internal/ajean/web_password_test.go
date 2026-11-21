package ajean

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPw = "correct-cheval-pile"

// pwTestSetup : dossier neuf, dérivation légère (Argon2id à 64 Mio par essai
// rendrait la suite interminable), frein et horloge remis à zéro.
func pwTestSetup(t *testing.T) {
	t.Helper()
	testHome(t)
	old := pwKDF
	pwKDF = kekParams{T: 1, M: 64, P: 1}
	pwResetTries()
	pwCacheBust()
	webKeyCacheBust()
	t.Cleanup(func() {
		pwKDF = old
		pwNow = time.Now
		pwResetTries()
		pwCacheBust()
		webKeyCacheBust()
	})
}

func pwProtected() http.HandlerFunc {
	return requireWebAuth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
}

// pwReq fabrique une requête comme en envoie un navigateur du réseau local.
func pwReq(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "192.168.1.10:8090"
	r.RemoteAddr = "192.168.1.50:51000"
	r.Header.Set("X-Ajean-Request", "1") // la marque que jfetch pose sur chaque appel de la page
	return r
}

// Le frein tient aussi face à une rafale SIMULTANÉE : l'essai est compté avant
// la dérivation, pas après. (Avant : 40 envois d'un coup, 31 essais vérifiés.)
func TestRateLimitHoldsUnderConcurrency(t *testing.T) {
	pwTestSetup(t)
	// Dérivation assez lente pour que la rafale soit réellement simultanée.
	pwKDF = kekParams{T: 1, M: 8 * 1024, P: 1}
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	burst := func(n int, ip func(i int) string) int64 {
		before := pwDerivations.Load()
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				_ = verifyWebPassword("faux-mot-de-passe", ip(i))
			}(i)
		}
		close(start)
		wg.Wait()
		return pwDerivations.Load() - before
	}
	// Une seule adresse : pas plus d'essais vérifiés que d'essais libres.
	if got := burst(40, func(int) string { return "192.168.1.50" }); got > pwFreeTries {
		t.Fatalf("%d essais vérifiés pour une adresse, %d au plus attendus", got, pwFreeTries)
	}
	if _, ok := verifyWebPassword(testPw, "192.168.1.50").(*pwLockedError); !ok {
		t.Fatal("après la rafale, l'adresse doit être en attente")
	}
	// Frein commun : beaucoup d'adresses d'un coup, pas plus que ce qu'il tolère.
	pwResetTries()
	if got := burst(3*pwGlobalTries, func(i int) string { return "10.1." + itoa(i/250) + "." + itoa(1+i%250) }); got > pwGlobalTries {
		t.Fatalf("%d essais vérifiés toutes adresses confondues, %d au plus attendus", got, pwGlobalTries)
	}
	// Les réservations sont toutes rendues, et un succès n'est pas gêné ensuite.
	pwTries.Lock()
	inflight := pwTries.globalInflight
	pwTries.Unlock()
	if inflight != 0 {
		t.Fatalf("%d réservation(s) jamais rendue(s)", inflight)
	}
	pwResetTries()
	if err := verifyWebPassword(testPw, "192.168.1.60"); err != nil {
		t.Fatalf("bon mot de passe après remise à zéro : %v", err)
	}
	pwTries.Lock()
	left := len(pwTries.byAddr)
	pwTries.Unlock()
	if left != 0 {
		t.Fatal("un succès doit effacer l'ardoise de l'adresse")
	}
}

// Le cookie de session ne vaut que pour NOTRE page : une page d'un autre port de
// la machine (même « site » pour le navigateur) ne peut pas s'en servir, même par
// un simple GET d'image. Le chemin Bearer, lui, ne change pas.
func TestSessionCookieNeedsOurPage(t *testing.T) {
	pwTestSetup(t)
	if err := storeWebKey("cle-de-pilotage"); err != nil {
		t.Fatal(err)
	}
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	c := pwCookie(t, pwLogin(t, testPw, ""))
	call := func(h map[string]string, withCookie bool) int {
		r := httptest.NewRequest("GET", "/api/restart", nil)
		r.Host = "192.168.1.10:8090"
		r.RemoteAddr = "192.168.1.50:51000"
		for k, v := range h {
			r.Header.Set(k, v)
		}
		if withCookie {
			r.AddCookie(c)
		}
		rr := httptest.NewRecorder()
		pwProtected()(rr, r)
		return rr.Code
	}
	for _, tc := range []struct {
		why  string
		h    map[string]string
		want int
	}{
		{"<img> d'une page d'un autre port, sans aucun en-tête", nil, 403},
		{"même site mais autre origine", map[string]string{"Sec-Fetch-Site": "same-site"}, 403},
		{"même site, avec la marque (impossible depuis un navigateur, refusé quand même)", map[string]string{"Sec-Fetch-Site": "same-site", "X-Ajean-Request": "1"}, 403},
		{"Referer d'un autre port", map[string]string{"Referer": "http://192.168.1.10:3000/page"}, 403},
		{"Referer d'un sous-domaine frère", map[string]string{"Referer": "http://autre.192.168.1.10:8090/"}, 403},
		{"Referer illisible", map[string]string{"Referer": "::pas une adresse"}, 403},
		{"notre page (HTTPS ou localhost)", map[string]string{"Sec-Fetch-Site": "same-origin"}, 200},
		{"adresse tapée par l'utilisateur", map[string]string{"Sec-Fetch-Site": "none"}, 200},
		{"notre page en HTTP sur le réseau : marque de jfetch", map[string]string{"X-Ajean-Request": "1"}, 200},
		{"notre page en HTTP sur le réseau : Referer du même hôte", map[string]string{"Referer": "http://192.168.1.10:8090/"}, 200},
		{"notre page : Origin du même hôte", map[string]string{"Origin": "http://192.168.1.10:8090"}, 200},
	} {
		if got := call(tc.h, true); got != tc.want {
			t.Errorf("%s : %d, attendu %d", tc.why, got, tc.want)
		}
	}
	// Chemin Bearer : inchangé, aucune preuve d'origine demandée.
	r := httptest.NewRequest("GET", "/api/restart", nil)
	r.Host, r.RemoteAddr = "192.168.1.10:8090", "192.168.1.50:51000"
	r.Header.Set("Authorization", "Bearer cle-de-pilotage")
	rr := httptest.NewRecorder()
	pwProtected()(rr, r)
	if rr.Code != 200 {
		t.Fatalf("le Bearer ne doit dépendre d'aucun en-tête d'origine : %d", rr.Code)
	}
	// Sans cookie ni Bearer : toujours 401 (et non 403), la page sait qu'elle doit se connecter.
	if got := call(map[string]string{"X-Ajean-Request": "1"}, false); got != 401 {
		t.Fatalf("sans session : %d", got)
	}
	// Les routes de la porte suivent la même règle : fermer les autres sessions
	// depuis une page voisine est refusé.
	r = httptest.NewRequest("GET", "/api/auth", nil)
	r.Host, r.RemoteAddr = "192.168.1.10:8090", "192.168.1.50:51000"
	r.AddCookie(c)
	rr = httptest.NewRecorder()
	handleAuthState(rr, r)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["session"] != false || out["allowed"] != false {
		t.Fatalf("une requête sans preuve d'origine ne doit pas compter comme entrée : %v", out)
	}
}

func pwLogin(t *testing.T, pw, remote string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": pw})
	r := pwReq("POST", "/api/auth/login", string(body))
	if remote != "" {
		r.RemoteAddr = remote
	}
	rr := httptest.NewRecorder()
	handleAuthLogin(rr, r)
	return rr
}

func pwCookie(t *testing.T, rr *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rr.Result().Cookies() {
		if strings.HasPrefix(c.Name, sessionCookieBase) {
			return c
		}
	}
	t.Fatalf("aucun cookie de session dans la réponse (%d %s)", rr.Code, rr.Body.String())
	return nil
}

// Sans mot de passe ni clé, rien ne change : l'API reste ouverte comme en 0.18.
func TestPasswordAbsentKeepsOldBehaviour(t *testing.T) {
	pwTestSetup(t)
	rr := httptest.NewRecorder()
	pwProtected()(rr, pwReq("GET", "/api/status", ""))
	if rr.Code != 200 {
		t.Fatalf("sans mot de passe ni clé, l'API doit rester ouverte : %d", rr.Code)
	}
	// Avec la seule clé de pilotage : Bearer exigé, et le 401 ne propose pas de mot de passe.
	if err := storeWebKey("cle-de-pilotage"); err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	pwProtected()(rr, pwReq("GET", "/api/status", ""))
	if rr.Code != 401 || !strings.Contains(rr.Header().Get("WWW-Authenticate"), `realm="ajean"`) {
		t.Fatalf("clé seule, sans Bearer : %d %q", rr.Code, rr.Header().Get("WWW-Authenticate"))
	}
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["login"] != false {
		t.Fatalf("clé seule : la page ne doit pas demander un mot de passe (%v)", out)
	}
	r := pwReq("GET", "/api/status", "")
	r.Header.Set("Authorization", "Bearer cle-de-pilotage")
	rr = httptest.NewRecorder()
	pwProtected()(rr, r)
	if rr.Code != 200 {
		t.Fatalf("le Bearer doit continuer de passer : %d", rr.Code)
	}
}

// Le mot de passe n'est jamais gardé : seule sa dérivation l'est.
func TestPasswordStoredDerivedOnly(t *testing.T) {
	pwTestSetup(t)
	if err := setWebPassword("court"); err != errPwTooShort {
		t.Fatalf("mot de passe court : %v", err)
	}
	if err := setWebPassword(strings.Repeat("x", pwMaxBytes+1)); err != errPwTooLong {
		t.Fatalf("mot de passe démesuré : %v", err)
	}
	if webPasswordConfigured() {
		t.Fatal("un mot de passe refusé ne doit rien enregistrer")
	}
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	for k, v := range allKV(bkState) {
		if strings.Contains(v, testPw) {
			t.Fatalf("le mot de passe apparaît en clair dans l'état (%s)", k)
		}
	}
	st, err := pwStateErr()
	if err != nil || !st.configured() {
		t.Fatalf("état relu : %v %v", st, err)
	}
	if !pwMatches(st.Password, testPw) || pwMatches(st.Password, testPw+"x") || pwMatches(st.Password, "") {
		t.Fatal("vérification du mot de passe incorrecte")
	}
	// Deux enregistrements du même mot de passe ne donnent pas la même empreinte (sel).
	h1 := st.Password.Hash
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	st, _ = pwStateErr()
	if st.Password.Hash == h1 {
		t.Fatal("même empreinte deux fois : le sel n'est pas tiré au hasard")
	}
}

// Parcours complet : refus sans session, connexion, cookie durci, accès, déconnexion.
func TestPasswordLoginSession(t *testing.T) {
	pwTestSetup(t)
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	pwProtected()(rr, pwReq("GET", "/api/status", ""))
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if rr.Code != 401 || out["login"] != true {
		t.Fatalf("sans session : attendu 401 + login, obtenu %d %v", rr.Code, out)
	}

	if rr := pwLogin(t, "mauvais-mot-de-passe", ""); rr.Code != 401 || len(rr.Result().Cookies()) != 0 {
		t.Fatalf("mauvais mot de passe : %d, cookies %v", rr.Code, rr.Result().Cookies())
	}
	rr = pwLogin(t, testPw, "")
	if rr.Code != 200 {
		t.Fatalf("bon mot de passe refusé : %d %s", rr.Code, rr.Body.String())
	}
	c := pwCookie(t, rr)
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Secure {
		t.Fatalf("cookie mal durci : %+v", c)
	}
	if c.Name != sessionCookieBase+"_8090" {
		t.Fatalf("nom du cookie : %s", c.Name)
	}
	if strings.Contains(rr.Body.String(), c.Value) {
		t.Fatal("le jeton de session ne doit pas apparaître dans le corps de la réponse")
	}
	// Le serveur ne garde que l'empreinte du jeton.
	if strings.Contains(getStr(bkState, stWebSessions), c.Value) {
		t.Fatal("le jeton de session est gardé en clair")
	}

	r := pwReq("GET", "/api/status", "")
	r.AddCookie(c)
	rr = httptest.NewRecorder()
	pwProtected()(rr, r)
	if rr.Code != 200 {
		t.Fatalf("la session doit ouvrir l'API : %d", rr.Code)
	}
	// Un jeton inventé n'ouvre rien.
	r = pwReq("GET", "/api/status", "")
	r.AddCookie(&http.Cookie{Name: c.Name, Value: "jeton-invente"})
	rr = httptest.NewRecorder()
	pwProtected()(rr, r)
	if rr.Code != 401 {
		t.Fatalf("jeton inventé accepté : %d", rr.Code)
	}

	// Déconnexion : le cookie est retiré et la session fermée côté serveur.
	r = pwReq("POST", "/api/auth/logout", "{}")
	r.AddCookie(c)
	rr = httptest.NewRecorder()
	handleAuthLogout(rr, r)
	if rr.Code != 200 || pwCookie(t, rr).MaxAge >= 0 {
		t.Fatalf("déconnexion : %d", rr.Code)
	}
	r = pwReq("GET", "/api/status", "")
	r.AddCookie(c)
	rr = httptest.NewRecorder()
	pwProtected()(rr, r)
	if rr.Code != 401 {
		t.Fatalf("la session fermée ouvre encore l'API : %d", rr.Code)
	}
}

// Cookie Secure quand la page est servie en HTTPS par un proxy de la machine.
func TestSessionCookieSecureBehindLocalProxy(t *testing.T) {
	pwTestSetup(t)
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"password": testPw})
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(string(body)))
	r.Host = "ajean.exemple.fr"
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	rr := httptest.NewRecorder()
	handleAuthLogin(rr, r)
	c := pwCookie(t, rr)
	if !c.Secure || c.Name != sessionCookieBase {
		t.Fatalf("derrière un proxy HTTPS : %+v", c)
	}
	// Le même en-tête venu du réseau n'est pas cru.
	r = pwReq("GET", "/", "")
	r.Header.Set("X-Forwarded-Proto", "https")
	if requestIsHTTPS(r) {
		t.Fatal("X-Forwarded-Proto cru alors qu'il ne vient pas de la machine")
	}
}

// Frein : cinq essais ratés, puis attente — même pour le bon mot de passe — et
// l'attente double.
func TestPasswordRateLimit(t *testing.T) {
	pwTestSetup(t)
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pwNow = func() time.Time { return now }
	for i := 1; i < pwFreeTries; i++ {
		if rr := pwLogin(t, "faux-mot-de-passe", ""); rr.Code != 401 {
			t.Fatalf("essai %d : attendu 401, obtenu %d", i, rr.Code)
		}
	}
	rr := pwLogin(t, "faux-mot-de-passe", "")
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("cinquième échec : attendu 429 + Retry-After, obtenu %d", rr.Code)
	}
	// Pendant l'attente, le BON mot de passe est refusé aussi.
	if rr := pwLogin(t, testPw, ""); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("bon mot de passe pendant l'attente : %d", rr.Code)
	}
	// Une autre adresse n'est pas pénalisée.
	if rr := pwLogin(t, testPw, "192.168.1.77:4000"); rr.Code != 200 {
		t.Fatalf("autre adresse : %d", rr.Code)
	}
	// Après l'attente : un nouvel échec double la suivante.
	now = now.Add(pwFirstWait + time.Second)
	if rr := pwLogin(t, "faux-mot-de-passe", ""); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("échec après l'attente : %d", rr.Code)
	}
	now = now.Add(pwFirstWait + time.Second)
	if rr := pwLogin(t, testPw, ""); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("l'attente n'a pas doublé : %d", rr.Code)
	}
	now = now.Add(pwFirstWait + time.Second)
	if rr := pwLogin(t, testPw, ""); rr.Code != 200 {
		t.Fatalf("après l'attente doublée, le bon mot de passe doit passer : %d", rr.Code)
	}
	// Un succès efface l'ardoise de cette adresse.
	if rr := pwLogin(t, "faux-mot-de-passe", ""); rr.Code != 401 {
		t.Fatalf("après un succès, l'ardoise devait être effacée : %d", rr.Code)
	}
}

// Frein commun : beaucoup d'adresses qui essaient chacune peu.
func TestPasswordGlobalRateLimit(t *testing.T) {
	pwTestSetup(t)
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pwNow = func() time.Time { return now }
	for i := 0; i < pwGlobalTries; i++ {
		ip := "10.0." + itoa(i/250) + "." + itoa(1+i%250)
		err := verifyWebPassword("faux-mot-de-passe", ip)
		if i < pwGlobalTries-1 && err != errPwWrong {
			t.Fatalf("essai %d depuis %s : %v", i, ip, err)
		}
	}
	if _, ok := verifyWebPassword(testPw, "172.16.0.9").(*pwLockedError); !ok {
		t.Fatal("le frein commun devait retenir une adresse jamais vue")
	}
	now = now.Add(pwGlobalWait + time.Second)
	if err := verifyWebPassword(testPw, "172.16.0.9"); err != nil {
		t.Fatalf("après l'attente commune : %v", err)
	}
}

// Une session inactive s'éteint ; une visite la prolonge.
func TestSessionExpiry(t *testing.T) {
	pwTestSetup(t)
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pwNow = func() time.Time { return now }
	token, err := openWebSession("192.168.1.50", "test")
	if err != nil {
		t.Fatal(err)
	}
	valid := func() bool {
		pwCacheBust()
		st, err := pwStateErr()
		return err == nil && webSessionValid(st, token)
	}
	if !valid() {
		t.Fatal("session neuve refusée")
	}
	now = now.Add(sessionLife - time.Hour)
	if !valid() {
		t.Fatal("session refusée avant son terme")
	}
	// Cette visite l'a prolongée : 29 jours plus tard elle vit encore.
	now = now.Add(sessionLife - time.Hour)
	if !valid() {
		t.Fatal("une visite devait prolonger la session")
	}
	now = now.Add(sessionLife + time.Hour)
	if valid() {
		t.Fatal("session inactive depuis plus de 30 jours acceptée")
	}
	if st, _ := pwStateErr(); len(st.Sessions) != 0 {
		t.Fatalf("la session expirée devait être retirée : %v", st.Sessions)
	}
}

// Changer le mot de passe ferme toutes les sessions ; « autres sessions » aussi.
func TestPasswordChangeClosesSessions(t *testing.T) {
	pwTestSetup(t)
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	c1 := pwCookie(t, pwLogin(t, testPw, ""))
	c2 := pwCookie(t, pwLogin(t, testPw, "192.168.1.60:1"))
	st, _ := pwStateErr()
	if webSessionCount(st) != 2 {
		t.Fatalf("deux sessions attendues : %d", webSessionCount(st))
	}
	// Fermer les autres : exige une session.
	rr := httptest.NewRecorder()
	handleAuthLogout(rr, pwReq("POST", "/api/auth/logout", `{"others":true}`))
	if rr.Code != 401 {
		t.Fatalf("fermer les autres sessions sans être connecté : %d", rr.Code)
	}
	r := pwReq("POST", "/api/auth/logout", `{"others":true}`)
	r.AddCookie(c1)
	rr = httptest.NewRecorder()
	handleAuthLogout(rr, r)
	st, _ = pwStateErr()
	if rr.Code != 200 || webSessionCount(st) != 1 || !webSessionValid(st, c1.Value) || webSessionValid(st, c2.Value) {
		t.Fatalf("fermeture des autres sessions : %d, %d session(s)", rr.Code, webSessionCount(st))
	}

	// Changement : sans l'actuel → 403 ; avec → toutes fermées, une neuve pour l'auteur.
	change := func(cur, next string, c *http.Cookie) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"current": cur, "password": next})
		r := pwReq("POST", "/api/auth/password", string(body))
		if c != nil {
			r.AddCookie(c)
		}
		rr := httptest.NewRecorder()
		handleAuthPassword(rr, r)
		return rr
	}
	if rr := change(testPw, "un-autre-mot-de-passe", nil); rr.Code != 401 {
		t.Fatalf("changer sans session : %d", rr.Code)
	}
	if rr := change("pas-le-bon-actuel", "un-autre-mot-de-passe", c1); rr.Code != 403 {
		t.Fatalf("changer avec un mauvais mot de passe actuel : %d", rr.Code)
	}
	if rr := change(testPw, "court", c1); rr.Code != 400 {
		t.Fatalf("nouveau mot de passe trop court : %d", rr.Code)
	}
	rr = change(testPw, "un-autre-mot-de-passe", c1)
	if rr.Code != 200 {
		t.Fatalf("changement refusé : %d %s", rr.Code, rr.Body.String())
	}
	fresh := pwCookie(t, rr)
	st, _ = pwStateErr()
	if webSessionValid(st, c1.Value) || !webSessionValid(st, fresh.Value) || webSessionCount(st) != 1 {
		t.Fatal("après un changement : l'ancienne session doit être fermée, une neuve ouverte")
	}
	if pwLogin(t, testPw, "192.168.1.61:1").Code != 401 || pwLogin(t, "un-autre-mot-de-passe", "192.168.1.62:1").Code != 200 {
		t.Fatal("l'ancien mot de passe doit être refusé, le nouveau accepté")
	}

	// Retrait : l'API redevient ouverte (aucune clé ici), les sessions disparaissent.
	body, _ := json.Marshal(map[string]any{"current": "un-autre-mot-de-passe", "remove": true})
	r = pwReq("POST", "/api/auth/password", string(body))
	r.AddCookie(fresh)
	rr = httptest.NewRecorder()
	handleAuthPassword(rr, r)
	if rr.Code != 200 || webPasswordConfigured() || getStr(bkState, stWebSessions) != "" {
		t.Fatalf("retrait du mot de passe : %d", rr.Code)
	}
}

// Le PREMIER mot de passe ne se pose pas de n'importe où.
func TestFirstPasswordOnlyFromTrustedPlace(t *testing.T) {
	pwTestSetup(t)
	set := func(r *http.Request) int {
		rr := httptest.NewRecorder()
		handleAuthPassword(rr, r)
		return rr.Code
	}
	body := `{"password":"` + testPw + `"}`
	// Depuis le réseau, API ouverte : refusé.
	if code := set(pwReq("POST", "/api/auth/password", body)); code != 403 || webPasswordConfigured() {
		t.Fatalf("premier mot de passe posé depuis le réseau : %d", code)
	}
	// Depuis la machine, mais relayé par un proxy : refusé.
	r := httptest.NewRequest("POST", "/api/auth/password", strings.NewReader(body))
	r.Host, r.RemoteAddr = "localhost:8090", "127.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	if code := set(r); code != 403 {
		t.Fatalf("requête relayée prise pour locale : %d", code)
	}
	// Une clé de pilotage existe : être sur la machine ne suffit pas, il faut la présenter.
	if err := storeWebKey("cle-de-pilotage"); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("POST", "/api/auth/password", strings.NewReader(body))
	r.Host, r.RemoteAddr = "localhost:8090", "127.0.0.1:5000"
	if code := set(r); code != 403 || webPasswordConfigured() {
		t.Fatalf("mot de passe posé sans la clé de pilotage : %d", code)
	}
	r = pwReq("POST", "/api/auth/password", body)
	r.Header.Set("Authorization", "Bearer cle-de-pilotage")
	if code := set(r); code != 200 || !webPasswordConfigured() {
		t.Fatalf("avec la clé de pilotage : %d", code)
	}
	// Clé ET mot de passe : chacun des deux ouvre l'API.
	r = pwReq("GET", "/api/status", "")
	r.Header.Set("Authorization", "Bearer cle-de-pilotage")
	rr := httptest.NewRecorder()
	pwProtected()(rr, r)
	if rr.Code != 200 {
		t.Fatalf("le Bearer doit rester valable à côté du mot de passe : %d", rr.Code)
	}
	r = pwReq("GET", "/api/status", "")
	r.AddCookie(pwCookie(t, pwLogin(t, testPw, "")))
	rr = httptest.NewRecorder()
	pwProtected()(rr, r)
	if rr.Code != 200 {
		t.Fatalf("la session doit valoir le Bearer : %d", rr.Code)
	}
}

// Sans protection, depuis la machine elle-même : le premier mot de passe passe et
// son auteur reçoit une session.
func TestFirstPasswordFromThisMachine(t *testing.T) {
	pwTestSetup(t)
	r := httptest.NewRequest("POST", "/api/auth/password", strings.NewReader(`{"password":"`+testPw+`"}`))
	r.Host, r.RemoteAddr = "localhost:8090", "127.0.0.1:5000"
	rr := httptest.NewRecorder()
	handleAuthPassword(rr, r)
	if rr.Code != 200 || !webPasswordConfigured() {
		t.Fatalf("depuis la machine : %d %s", rr.Code, rr.Body.String())
	}
	st, _ := pwStateErr()
	if !webSessionValid(st, pwCookie(t, rr).Value) {
		t.Fatal("l'auteur du mot de passe devait recevoir une session")
	}
	// Par ajean.link (identité E2E) : pas de cookie, la requête est déjà authentifiée.
	if err := removeWebPassword(); err != nil {
		t.Fatal(err)
	}
	r = markE2EAuthed(pwReq("POST", "/api/auth/password", `{"password":"`+testPw+`"}`))
	rr = httptest.NewRecorder()
	handleAuthPassword(rr, r)
	if rr.Code != 200 || len(rr.Result().Cookies()) != 0 || !webPasswordConfigured() {
		t.Fatalf("par ajean.link : %d", rr.Code)
	}
	// Et une requête E2E continue de passer sans session une fois le mot de passe posé.
	rr = httptest.NewRecorder()
	pwProtected()(rr, markE2EAuthed(pwReq("GET", "/api/status", "")))
	if rr.Code != 200 {
		t.Fatalf("ajean.link bloqué par le mot de passe : %d", rr.Code)
	}
}

// Un site tiers ne peut ni se connecter ni interroger la porte à la place de
// l'utilisateur.
func TestAuthRoutesRejectCrossSite(t *testing.T) {
	pwTestSetup(t)
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"password": testPw})
	r := pwReq("POST", "/api/auth/login", string(body))
	r.Header.Set("Origin", "https://site-tiers.example")
	rr := httptest.NewRecorder()
	handleAuthLogin(rr, r)
	if rr.Code != 403 || len(rr.Result().Cookies()) != 0 {
		t.Fatalf("connexion depuis un site tiers : %d", rr.Code)
	}
	r = pwReq("GET", "/api/auth", "")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	rr = httptest.NewRecorder()
	handleAuthState(rr, r)
	if rr.Code != 403 {
		t.Fatalf("état lu depuis un site tiers : %d", rr.Code)
	}
	// La connexion n'accepte que POST.
	rr = httptest.NewRecorder()
	handleAuthLogin(rr, pwReq("GET", "/api/auth/login", ""))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET sur la connexion : %d", rr.Code)
	}
}

// L'état public ne dit rien de sensible et suit la requête.
func TestAuthState(t *testing.T) {
	pwTestSetup(t)
	state := func(r *http.Request) map[string]any {
		rr := httptest.NewRecorder()
		handleAuthState(rr, r)
		var out map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("état illisible : %d %s", rr.Code, rr.Body.String())
		}
		return out
	}
	out := state(pwReq("GET", "/api/auth", ""))
	if out["password"] != false || out["allowed"] != true || out["can_set"] != false {
		t.Fatalf("sans protection, depuis le réseau : %v", out)
	}
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	out = state(pwReq("GET", "/api/auth", ""))
	if out["password"] != true || out["allowed"] != false || out["sessions"] != nil {
		t.Fatalf("protégé, sans session : %v", out)
	}
	r := pwReq("GET", "/api/auth", "")
	r.AddCookie(pwCookie(t, pwLogin(t, testPw, "")))
	out = state(r)
	if out["allowed"] != true || out["session"] != true || out["sessions"] != float64(1) {
		t.Fatalf("avec session : %v", out)
	}
	raw, _ := json.Marshal(out)
	st, _ := pwStateErr()
	if strings.Contains(string(raw), st.Password.Hash) || strings.Contains(string(raw), st.Password.Salt) {
		t.Fatal("l'état public expose la dérivation du mot de passe")
	}
}

// Une lecture ratée ferme l'accès, elle ne l'ouvre jamais.
func TestPasswordUnreadableStateCloses(t *testing.T) {
	pwTestSetup(t)
	// Mot de passe enregistré mais illisible : pas « aucun mot de passe ».
	if err := putStr(bkState, stWebPw, "{pas du json"); err != nil {
		t.Fatal(err)
	}
	pwCacheBust()
	rr := httptest.NewRecorder()
	pwProtected()(rr, pwReq("GET", "/api/status", ""))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("mot de passe illisible : attendu 503, obtenu %d", rr.Code)
	}
	if rr := pwLogin(t, testPw, ""); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("connexion avec un état illisible : %d", rr.Code)
	}
	// Base elle-même illisible.
	home := testHome(t)
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home+"/ajean.db", []byte("ceci n'est pas une base"), 0o600); err != nil {
		t.Fatal(err)
	}
	pwCacheBust()
	webKeyCacheBust()
	rr = httptest.NewRecorder()
	pwProtected()(rr, pwReq("GET", "/api/status", ""))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("base illisible : attendu 503, obtenu %d", rr.Code)
	}
}

// Adresse d'écoute : le défaut ne bouge pas ; une adresse du réseau demandée
// explicitement exige une protection.
func TestWebListenHost(t *testing.T) {
	pwTestSetup(t)
	t.Setenv("AJEAN_HOST", "")
	if h, err := webListenHost(); err != nil || h != "0.0.0.0" {
		t.Fatalf("défaut : %q %v", h, err)
	}
	t.Setenv("AJEAN_HOST", "127.0.0.1")
	if h, err := webListenHost(); err != nil || h != "127.0.0.1" {
		t.Fatalf("boucle locale : %q %v", h, err)
	}
	t.Setenv("AJEAN_HOST", "localhost")
	if h, err := webListenHost(); err != nil || h != "127.0.0.1" {
		t.Fatalf("localhost : %q %v", h, err)
	}
	t.Setenv("AJEAN_HOST", "pas-une-adresse")
	if _, err := webListenHost(); err == nil {
		t.Fatal("adresse invalide acceptée")
	}
	t.Setenv("AJEAN_HOST", "0.0.0.0")
	if _, err := webListenHost(); err == nil {
		t.Fatal("AJEAN_HOST=0.0.0.0 sans protection devait être refusé")
	}
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	if h, err := webListenHost(); err != nil || h != "0.0.0.0" {
		t.Fatalf("avec un mot de passe : %q %v", h, err)
	}
	// Réglage enregistré (service) ; l'environnement l'emporte.
	t.Setenv("AJEAN_HOST", "")
	if err := cmdUIListen([]string{"192.168.1.10"}); err != nil {
		t.Fatal(err)
	}
	if h, err := webListenHost(); err != nil || h != "192.168.1.10" {
		t.Fatalf("réglage enregistré : %q %v", h, err)
	}
	t.Setenv("AJEAN_HOST", "127.0.0.1")
	if h, _ := webListenHost(); h != "127.0.0.1" {
		t.Fatalf("l'environnement devait l'emporter : %q", h)
	}
	t.Setenv("AJEAN_HOST", "")
	if err := cmdUIListen([]string{"all"}); err != nil {
		t.Fatal(err)
	}
	if h, _ := webListenHost(); h != "0.0.0.0" {
		t.Fatalf("retour au défaut : %q", h)
	}
	// Sans protection, « ajean ui listen » refuse une adresse du réseau.
	if err := removeWebPassword(); err != nil {
		t.Fatal(err)
	}
	if err := cmdUIListen([]string{"192.168.1.10"}); err == nil || getStr(bkState, stWebHost) != "" {
		t.Fatalf("adresse du réseau enregistrée sans protection : %v", err)
	}
	if webLocalURLHost("0.0.0.0") != "localhost" || webLocalURLHost("192.168.1.10") != "192.168.1.10" || webLocalURLHost("::1") != "localhost" {
		t.Fatal("nom d'hôte de l'adresse locale")
	}
}

// Réglage enregistré : mot de passe retiré ensuite, ou base illisible — le
// service démarre quand même, sur cette machine seulement (jamais sur 0.0.0.0,
// et sans s'arrêter : l'interface et le tunnel doivent rester joignables).
func TestWebListenHostFallsBackToLoopback(t *testing.T) {
	pwTestSetup(t)
	t.Setenv("AJEAN_HOST", "")
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	if err := cmdUIListen([]string{"192.168.1.10"}); err != nil {
		t.Fatal(err)
	}
	if h, err := webListenHost(); err != nil || h != "192.168.1.10" {
		t.Fatalf("protégé : %q %v", h, err)
	}
	// Mot de passe retiré depuis l'interface : plus de protection.
	if err := removeWebPassword(); err != nil {
		t.Fatal(err)
	}
	if h, err := webListenHost(); err != nil || h != "127.0.0.1" {
		t.Fatalf("sans protection, le service doit démarrer sur 127.0.0.1 : %q %v", h, err)
	}
	// Adresse enregistrée invalide : idem.
	if err := putStr(bkState, stWebHost, "pas-une-adresse"); err != nil {
		t.Fatal(err)
	}
	if h, err := webListenHost(); err != nil || h != "127.0.0.1" {
		t.Fatalf("adresse enregistrée invalide : %q %v", h, err)
	}
	// Base illisible : on ne sait pas si l'accès réseau avait été fermé. Surtout
	// pas 0.0.0.0.
	home := testHome(t)
	if err := putStr(bkState, stWebHost, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home+"/ajean.db", []byte("ceci n'est pas une base"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h, err := webListenHost(); err != nil || h != "127.0.0.1" {
		t.Fatalf("base illisible : %q %v", h, err)
	}
	// Demandé explicitement dans l'environnement, sans protection : toujours un refus.
	t.Setenv("AJEAN_HOST", "0.0.0.0")
	if _, err := webListenHost(); err == nil {
		t.Fatal("AJEAN_HOST=0.0.0.0 sans protection devait rester refusé")
	}
}

func TestLoginClientIPAndBucket(t *testing.T) {
	r := pwReq("POST", "/api/auth/login", "")
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if ip := loginClientIP(r); ip != "192.168.1.50" {
		t.Fatalf("X-Forwarded-For cru depuis le réseau : %s", ip)
	}
	r.RemoteAddr = "127.0.0.1:9"
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 203.0.113.7")
	if ip := loginClientIP(r); ip != "203.0.113.7" {
		t.Fatalf("derrière un proxy local, la dernière adresse fait foi : %s", ip)
	}
	if pwBucket("2001:db8:1:2:aaaa::1") != pwBucket("2001:db8:1:2:bbbb::2") {
		t.Fatal("deux adresses du même /64 doivent partager le frein")
	}
	if pwBucket("2001:db8:1:2::1") == pwBucket("2001:db8:1:3::1") || pwBucket("10.0.0.1") == pwBucket("10.0.0.2") {
		t.Fatal("seaux confondus")
	}
	if !fromThisMachine(func() *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host, r.RemoteAddr = "127.0.0.1:8090", "[::1]:1"
		return r
	}()) {
		t.Fatal("requête locale non reconnue")
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.Host, r.RemoteAddr = "ajean.exemple.fr", "127.0.0.1:1"
	if fromThisMachine(r) {
		t.Fatal("un nom public n'est pas la machine elle-même")
	}
}

// Un mot de passe reçu par un tube : ni fin de ligne, ni marque d'ordre d'octets
// (Windows PowerShell en place une en tête).
func TestCleanPipedSecret(t *testing.T) {
	for in, want := range map[string]string{
		utf8BOM + "mot-de-passe\r\n": "mot-de-passe",
		"mot-de-passe\n":             "mot-de-passe",
		"mot de passe":               "mot de passe",
		" espaces gardés \n":         " espaces gardés ",
	} {
		if got := cleanPipedSecret(in); got != want {
			t.Errorf("%q : obtenu %q, attendu %q", in, got, want)
		}
	}
}

// Les sessions sont bornées : au-delà, les plus anciennes ferment.
func TestSessionCap(t *testing.T) {
	pwTestSetup(t)
	if err := setWebPassword(testPw); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pwNow = func() time.Time { return now }
	first, err := openWebSession("10.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < sessionMax; i++ {
		now = now.Add(time.Second)
		if _, err := openWebSession("10.0.0.1", ""); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := pwStateErr()
	if len(st.Sessions) != sessionMax || webSessionValid(st, first) {
		t.Fatalf("%d sessions gardées, la plus ancienne devait fermer", len(st.Sessions))
	}
}
