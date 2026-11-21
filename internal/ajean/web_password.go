package ajean

// web_password.go — le mot de passe de l'interface et ses sessions.
//
// La clé de pilotage (web_auth.go) protège déjà l'API : un Bearer que les clients
// envoient à chaque appel. Elle convient aux scripts ; pour un navigateur elle
// oblige à garder le secret dans le JavaScript de la page (localStorage), et rien
// ne freine celui qui en essaie mille. Le mot de passe complète la clé, il ne la
// remplace pas :
//
//   - il est DÉRIVÉ (Argon2id, la KDF du coffre de la mémoire), jamais gardé ;
//   - une connexion réussie ouvre une SESSION : un jeton aléatoire remis dans un
//     cookie HttpOnly + SameSite=Strict, que le JavaScript ne peut pas lire. Le
//     serveur n'en garde que l'empreinte : lire la base ne permet pas d'entrer ;
//   - les essais ratés sont freinés, par adresse puis globalement.
//
// Rien ne change tant qu'aucun mot de passe n'est défini (« ajean password », ou
// Paramètres > Sécurité). Avec un mot de passe, l'API exige une session OU, si
// une clé de pilotage existe aussi, son Bearer : les clients déjà réglés
// continuent de marcher.
//
// État (bucket état) : "web_pw" = {salt, hash, t, m, p}, "web_sessions" =
// {empreinte du jeton → session}.

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	bolt "go.etcd.io/bbolt"
	"golang.org/x/term"
)

const (
	pwMinRunes = 10   // caractères
	pwMaxBytes = 1024 // au-delà, ce n'est plus un mot de passe (et Argon2id le paierait)

	stWebPw       = "web_pw"
	stWebSessions = "web_sessions"

	sessionCookieBase = "ajean_session"
	sessionLife       = 30 * 24 * time.Hour // sans visite pendant ce temps, la session s'éteint
	sessionSeenStep   = time.Hour           // on ne réécrit pas la base à chaque requête
	sessionMax        = 50                  // au-delà, les plus anciennes sont fermées

	pwFreeTries    = 5                // essais ratés avant la première attente
	pwFirstWait    = 30 * time.Second // puis elle double à chaque nouvel échec
	pwMaxWait      = time.Hour
	pwGlobalWindow = 10 * time.Minute // fenêtre du frein commun à toutes les adresses
	pwGlobalTries  = 100              // essais ratés tolérés dans cette fenêtre
	pwGlobalWait   = time.Minute
	pwMaxTracked   = 10_000 // adresses suivies au plus (la mémoire reste bornée)
)

// pwKDF : coût de la dérivation d'un NOUVEAU mot de passe. La vérification relit
// toujours les paramètres écrits avec le mot de passe, jamais ceux-ci. Les tests
// l'abaissent.
var pwKDF = kekParams{T: argonTime, M: argonMemory, P: argonThreads}

var (
	errPwWrong    = errors.New("mot de passe incorrect")
	errPwTooShort = fmt.Errorf("mot de passe trop court : %d caractères au moins", pwMinRunes)
	errPwTooLong  = errors.New("mot de passe trop long")
)

// pwLockedError : trop d'essais ratés, il faut attendre.
type pwLockedError struct{ Wait time.Duration }

func (e *pwLockedError) Error() string {
	return fmt.Sprintf("trop d'essais : nouvel essai possible dans %d s", int(e.Wait.Seconds())+1)
}

type pwRecord struct {
	Salt string `json:"salt"`
	Hash string `json:"hash"`
	T    uint32 `json:"t"`
	M    uint32 `json:"m"`
	P    uint8  `json:"p"`
}

type pwSession struct {
	Created int64  `json:"created"` // epoch s
	Seen    int64  `json:"seen"`
	Agent   string `json:"agent,omitempty"`
	IP      string `json:"ip,omitempty"`
}

// pwState : ce que requireWebAuth consulte à chaque requête.
type pwState struct {
	Password *pwRecord
	Sessions map[string]pwSession // clé : empreinte du jeton
}

func (s pwState) configured() bool { return s.Password != nil }

var (
	// pwMu sérialise les écritures de l'état (sessions, mot de passe) dans ce
	// process ; un autre process (« ajean password ») passe par la base.
	pwMu sync.Mutex

	// Dériver un mot de passe coûte du processeur et 64 Mio, exprès. Deux à la
	// fois au plus : une rafale d'essais ne peut pas occuper toute la machine.
	pwHashing = make(chan struct{}, 2)

	pwNow = time.Now // remplacée dans les tests
)

// pwCache : même contrat que webKeyCache — dernière lecture RÉUSSIE, invalidée
// par une écriture de ce process, par la date ou la taille du fichier (autre
// process), une seconde au plus. Une lecture ratée n'est jamais mise en cache.
var pwCache struct {
	sync.Mutex
	ok    bool
	path  string
	st    pwState
	when  time.Time
	mtime time.Time
	size  int64
}

func pwCacheBust() {
	pwCache.Lock()
	pwCache.ok = false
	pwCache.Unlock()
}

// pwStateErr lit le mot de passe et les sessions en distinguant « rien » d'une
// lecture ratée : requireWebAuth FERME l'API sur une lecture ratée. Un contenu
// présent mais illisible est aussi une erreur — le prendre pour « pas de mot de
// passe » ouvrirait l'accès.
func pwStateErr() (pwState, error) {
	path := dbPath()
	mtime, size := dbStamp()
	c := &pwCache
	c.Lock()
	if c.ok && c.path == path && c.size == size && c.mtime.Equal(mtime) && time.Since(c.when) < cacheMaxAge {
		st := c.st
		c.Unlock()
		return st, nil
	}
	c.Unlock()
	st, err := pwReadState()
	if err != nil {
		return pwState{}, err
	}
	c.Lock()
	c.ok, c.path, c.st, c.when, c.mtime, c.size = true, path, st, time.Now(), mtime, size
	c.Unlock()
	return st, nil
}

// pwReadState lit les deux clés en UNE ouverture de la base.
func pwReadState() (pwState, error) {
	var rawPw, rawSess []byte
	err := view(bkState, func(b *bolt.Bucket) error {
		rawPw = append([]byte(nil), b.Get([]byte(stWebPw))...)
		rawSess = append([]byte(nil), b.Get([]byte(stWebSessions))...)
		return nil
	})
	if err != nil {
		return pwState{}, err
	}
	var st pwState
	if len(rawPw) > 0 {
		var rec pwRecord
		if err := json.Unmarshal(rawPw, &rec); err != nil {
			return pwState{}, fmt.Errorf("mot de passe enregistré illisible : %w", err)
		}
		st.Password = &rec
	}
	if len(rawSess) > 0 {
		// Des sessions illisibles ne ferment que les sessions (tout le monde se
		// reconnecte) : elles n'ouvrent rien.
		_ = json.Unmarshal(rawSess, &st.Sessions)
	}
	return st, nil
}

// webPasswordConfigured : un mot de passe protège-t-il l'interface ?
func webPasswordConfigured() bool { st, err := pwStateErr(); return err == nil && st.configured() }

func pwDerive(pw string, salt []byte, kp kekParams) []byte {
	pwHashing <- struct{}{}
	defer func() { <-pwHashing }()
	return deriveKEK(pw, salt, kp.T, kp.M, kp.P)
}

// pwMatches : pw est-il le bon mot de passe ? Aucun verrou tenu (le calcul est long).
func pwMatches(rec *pwRecord, pw string) bool {
	if rec == nil || len(pw) > pwMaxBytes {
		return false
	}
	salt, err1 := base64.StdEncoding.DecodeString(rec.Salt)
	want, err2 := base64.StdEncoding.DecodeString(rec.Hash)
	if err1 != nil || err2 != nil || len(salt) == 0 || len(want) != memDEKLen {
		return false
	}
	got := pwDerive(pw, salt, kekParams{T: rec.T, M: rec.M, P: rec.P})
	return subtle.ConstantTimeCompare(got, want) == 1
}

// setWebPassword enregistre un nouveau mot de passe et ferme toutes les
// sessions : qui était connecté avec l'ancien ne le reste pas.
func setWebPassword(pw string) error {
	if utf8.RuneCountInString(pw) < pwMinRunes {
		return errPwTooShort
	}
	if len(pw) > pwMaxBytes {
		return errPwTooLong
	}
	salt, err := randBytes(memSaltLen)
	if err != nil {
		return err
	}
	key := pwDerive(pw, salt, pwKDF)
	rec := pwRecord{
		Salt: base64.StdEncoding.EncodeToString(salt),
		Hash: base64.StdEncoding.EncodeToString(key),
		T:    pwKDF.T, M: pwKDF.M, P: pwKDF.P,
	}
	// Relecture avant d'écrire : un mot de passe qu'on ne saurait pas vérifier
	// enfermerait son propriétaire dehors.
	if !pwMatches(&rec, pw) {
		return errors.New("dérivation du mot de passe incohérente : rien n'a été enregistré")
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	pwMu.Lock()
	defer pwMu.Unlock()
	defer pwCacheBust()
	return update(bkState, func(b *bolt.Bucket) error {
		if err := b.Put([]byte(stWebPw), raw); err != nil {
			return err
		}
		return b.Delete([]byte(stWebSessions))
	})
}

// removeWebPassword retire le mot de passe et ferme toutes les sessions :
// l'interface revient à son état d'avant (ouverte, ou tenue par la seule clé de
// pilotage).
func removeWebPassword() error {
	pwMu.Lock()
	defer pwMu.Unlock()
	defer pwCacheBust()
	return update(bkState, func(b *bolt.Bucket) error {
		if err := b.Delete([]byte(stWebPw)); err != nil {
			return err
		}
		return b.Delete([]byte(stWebSessions))
	})
}

// ---- frein contre les essais à la chaîne -------------------------------------

type pwAttempts struct {
	fails int
	last  time.Time
	until time.Time
}

var pwTries = struct {
	sync.Mutex
	byAddr      map[string]*pwAttempts
	globalFails []time.Time
	globalUntil time.Time
}{byAddr: map[string]*pwAttempts{}}

// pwBucket : ce qui compte comme « une adresse ». En IPv6, un abonné dispose
// d'un /64 entier : on le compte pour un.
func pwBucket(ip string) string {
	p := net.ParseIP(ip)
	if p == nil {
		return ip
	}
	if p4 := p.To4(); p4 != nil {
		return p4.String()
	}
	return p.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// pwLockedLocked : attente restante pour ce seau (verrou pwTries tenu).
func pwLockedLocked(b string) time.Duration {
	t := pwNow()
	wait := pwTries.globalUntil.Sub(t)
	if a := pwTries.byAddr[b]; a != nil && a.until.Sub(t) > wait {
		wait = a.until.Sub(t)
	}
	if wait < 0 {
		return 0
	}
	return wait
}

func pwFailedLocked(b string) {
	t := pwNow()
	a := pwTries.byAddr[b]
	if a == nil {
		if len(pwTries.byAddr) >= pwMaxTracked {
			for k, v := range pwTries.byAddr {
				if t.Sub(v.last) > pwMaxWait {
					delete(pwTries.byAddr, k)
				}
			}
			if len(pwTries.byAddr) >= pwMaxTracked { // attaque très large : on repart de zéro, le frein commun tient
				pwTries.byAddr = map[string]*pwAttempts{}
			}
		}
		a = &pwAttempts{}
		pwTries.byAddr[b] = a
	}
	if t.Sub(a.last) > pwMaxWait { // l'ardoise s'efface après une heure de calme
		a.fails = 0
	}
	a.fails++
	a.last = t
	if a.fails >= pwFreeTries {
		wait := pwMaxWait
		if shift := a.fails - pwFreeTries; shift < 8 {
			wait = min(pwFirstWait<<shift, pwMaxWait)
		}
		a.until = t.Add(wait)
	}
	// Frein commun : des milliers d'adresses qui essaient chacune quelques fois
	// passent sous le frein par adresse, pas sous celui-ci.
	keep := pwTries.globalFails[:0]
	for _, f := range pwTries.globalFails {
		if t.Sub(f) < pwGlobalWindow {
			keep = append(keep, f)
		}
	}
	pwTries.globalFails = append(keep, t)
	if len(pwTries.globalFails) >= pwGlobalTries {
		pwTries.globalUntil = t.Add(pwGlobalWait)
		pwTries.globalFails = pwTries.globalFails[:0]
	}
}

// pwResetTries remet le frein à zéro (tests).
func pwResetTries() {
	pwTries.Lock()
	pwTries.byAddr = map[string]*pwAttempts{}
	pwTries.globalFails, pwTries.globalUntil = nil, time.Time{}
	pwTries.Unlock()
}

// verifyWebPassword vérifie un mot de passe pour le compte d'une adresse, frein
// compris. Une adresse en attente n'est PAS vérifiée, même avec le bon mot de
// passe : sans cela le frein ne freinerait rien.
func verifyWebPassword(pw, ip string) error {
	st, err := pwStateErr()
	if err != nil {
		return err
	}
	if !st.configured() {
		return errors.New("aucun mot de passe n'est défini")
	}
	b := pwBucket(ip)
	pwTries.Lock()
	wait := pwLockedLocked(b)
	pwTries.Unlock()
	if wait > 0 {
		return &pwLockedError{Wait: wait}
	}
	if pwMatches(st.Password, pw) {
		pwTries.Lock()
		delete(pwTries.byAddr, b)
		pwTries.Unlock()
		return nil
	}
	pwTries.Lock()
	pwFailedLocked(b)
	wait = pwLockedLocked(b)
	pwTries.Unlock()
	if wait > 0 {
		return &pwLockedError{Wait: wait}
	}
	return errPwWrong
}

// ---- sessions ------------------------------------------------------------------

func sessionDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func sessionExpired(s pwSession, t time.Time) bool {
	return t.Sub(time.Unix(s.Seen, 0)) > sessionLife
}

// updateSessions relit les sessions DANS la transaction d'écriture, applique fn
// et réécrit : deux process ne s'écrasent pas.
func updateSessions(fn func(m map[string]pwSession) bool) error {
	pwMu.Lock()
	defer pwMu.Unlock()
	defer pwCacheBust()
	return update(bkState, func(b *bolt.Bucket) error {
		m := map[string]pwSession{}
		if raw := b.Get([]byte(stWebSessions)); len(raw) > 0 {
			_ = json.Unmarshal(raw, &m)
		}
		if !fn(m) {
			return nil
		}
		if len(m) == 0 {
			return b.Delete([]byte(stWebSessions))
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return err
		}
		return b.Put([]byte(stWebSessions), raw)
	})
}

// openWebSession ouvre une session et rend son jeton (à poser dans le cookie).
func openWebSession(ip, agent string) (string, error) {
	raw, err := randBytes(32)
	if err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if len(agent) > 200 {
		agent = agent[:200]
	}
	t := pwNow()
	err = updateSessions(func(m map[string]pwSession) bool {
		for k, s := range m {
			if sessionExpired(s, t) {
				delete(m, k)
			}
		}
		if len(m) >= sessionMax {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool { return m[keys[i]].Seen < m[keys[j]].Seen })
			for _, k := range keys[:len(keys)-sessionMax+1] {
				delete(m, k)
			}
		}
		m[sessionDigest(token)] = pwSession{Created: t.Unix(), Seen: t.Unix(), Agent: agent, IP: ip}
		return true
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// webSessionValid : ce jeton ouvre-t-il une session en cours ? Chaque visite la
// prolonge (au plus une écriture par heure). st est l'état déjà lu par l'appelant.
func webSessionValid(st pwState, token string) bool {
	if token == "" || !st.configured() {
		return false
	}
	key := sessionDigest(token)
	s, ok := st.Sessions[key]
	if !ok {
		return false
	}
	t := pwNow()
	if sessionExpired(s, t) {
		_ = updateSessions(func(m map[string]pwSession) bool {
			if _, ok := m[key]; !ok {
				return false
			}
			delete(m, key)
			return true
		})
		return false
	}
	if t.Sub(time.Unix(s.Seen, 0)) > sessionSeenStep {
		_ = updateSessions(func(m map[string]pwSession) bool {
			cur, ok := m[key]
			if !ok {
				return false
			}
			cur.Seen = t.Unix()
			m[key] = cur
			return true
		})
	}
	return true
}

// closeWebSession ferme une session.
func closeWebSession(token string) {
	if token == "" {
		return
	}
	key := sessionDigest(token)
	_ = updateSessions(func(m map[string]pwSession) bool {
		if _, ok := m[key]; !ok {
			return false
		}
		delete(m, key)
		return true
	})
}

// closeOtherWebSessions ferme toutes les sessions sauf celle de ce jeton, et dit
// combien.
func closeOtherWebSessions(token string) int {
	keep, n := sessionDigest(token), 0
	_ = updateSessions(func(m map[string]pwSession) bool {
		for k := range m {
			if k != keep {
				delete(m, k)
				n++
			}
		}
		return n > 0
	})
	return n
}

// webSessionCount : le nombre de sessions en cours.
func webSessionCount(st pwState) int {
	n, t := 0, pwNow()
	for _, s := range st.Sessions {
		if !sessionExpired(s, t) {
			n++
		}
	}
	return n
}

// ---- côté HTTP -------------------------------------------------------------------

// forwardHeaders : ce qu'un proxy ajoute à une requête qu'il relaie. Une requête
// qui en porte ne vient pas de la machine elle-même, même si elle arrive de
// 127.0.0.1 (le proxy tourne ici, le visiteur est ailleurs).
var forwardHeaders = []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", "Forwarded", "Via", "Cf-Connecting-Ip", "True-Client-Ip"}

func requestRemoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

// fromThisMachine : la requête vient-elle d'un programme de la machine, sans
// intermédiaire ? C'est ce qui autorise à poser le PREMIER mot de passe depuis
// l'interface.
func fromThisMachine(r *http.Request) bool {
	ip := requestRemoteIP(r)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	h := r.Host
	if hh, _, err := net.SplitHostPort(r.Host); err == nil {
		h = hh
	}
	h = strings.ToLower(strings.Trim(h, "[]"))
	if hip := net.ParseIP(h); h != "localhost" && (hip == nil || !hip.IsLoopback()) {
		return false
	}
	for _, name := range forwardHeaders {
		if r.Header.Get(name) != "" {
			return false
		}
	}
	return true
}

// loginClientIP : l'adresse à qui compter les essais de mot de passe. Derrière
// un proxy installé sur la machine, c'est celle qu'il annonce (la dernière de
// X-Forwarded-For : celle qu'il a vue lui-même, les précédentes sont déclarées
// par le visiteur). On ne croit cet en-tête que s'il vient de la machine.
func loginClientIP(r *http.Request) string {
	ip := requestRemoteIP(r)
	if ip == nil {
		return r.RemoteAddr
	}
	if ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if p := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); p != nil {
				return p.String()
			}
		}
		if p := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-Ip"))); p != nil {
			return p.String()
		}
	}
	return ip.String()
}

// requestIsHTTPS : la page est-elle servie en HTTPS, par nous ou par un proxy de
// la machine ?
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	ip := requestRemoteIP(r)
	return ip != nil && ip.IsLoopback() && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// sessionCookieName : un cookie ne connaît pas le port. Deux AJEAN sur la même
// machine (8090 et un second port) se voleraient le leur ; le port de l'adresse
// appelée entre donc dans le nom.
func sessionCookieName(r *http.Request) string {
	if _, port, err := net.SplitHostPort(r.Host); err == nil && port != "" {
		for _, c := range port {
			if c < '0' || c > '9' {
				return sessionCookieBase
			}
		}
		return sessionCookieBase + "_" + port
	}
	return sessionCookieBase
}

func sessionToken(r *http.Request) string {
	if c, err := r.Cookie(sessionCookieName(r)); err == nil {
		return c.Value
	}
	return ""
}

// setSessionCookie pose (ou, jeton vide, retire) le cookie de session : illisible
// par le JavaScript de la page (HttpOnly), jamais envoyé par un autre site
// (SameSite=Strict), réservé à HTTPS dès que la page est servie en HTTPS.
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	c := &http.Cookie{Name: sessionCookieName(r), Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: requestIsHTTPS(r)}
	if token == "" {
		c.MaxAge = -1
	} else {
		c.MaxAge = 400 * 24 * 3600 // le navigateur le garde ; c'est le serveur qui éteint une session inactive
	}
	http.SetCookie(w, c)
}

// webAccess : qui est derrière cette requête, pour les routes /api/auth/* qui
// décident elles-mêmes (requireWebAuth, lui, répond 401).
type webAccess struct {
	Protected bool // une clé de pilotage ou un mot de passe existe
	Key       bool // une clé de pilotage existe
	Password  bool // un mot de passe existe
	Allowed   bool // cette requête peut utiliser l'API
	Session   bool // … grâce à une session (cookie)
}

// webAccessOf évalue une requête avec les règles de requireWebAuth. Une lecture
// ratée remonte en erreur : l'appelant ferme.
func webAccessOf(r *http.Request) (webAccess, pwState, error) {
	hash, err := webKeyHashErr()
	if err != nil {
		return webAccess{}, pwState{}, err
	}
	st, err := pwStateErr()
	if err != nil {
		return webAccess{}, pwState{}, err
	}
	a := webAccess{Protected: hash != "" || st.configured(), Key: hash != "", Password: st.configured()}
	switch {
	case isE2EAuthed(r), !a.Protected:
		a.Allowed = true
	case hash != "" && checkBearer(r, hash):
		a.Allowed = true
	case webSessionValid(st, sessionToken(r)):
		a.Allowed, a.Session = true, true
	}
	return a, st, nil
}

// authGate : les contrôles communs aux routes /api/auth/* (lecture de l'état,
// refus des sites tiers). Renvoie false quand la réponse est déjà partie.
func authGate(w http.ResponseWriter, r *http.Request) (webAccess, pwState, bool) {
	a, st, err := webAccessOf(r)
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "configuration illisible — réessaie dans un instant"})
		return a, st, false
	}
	if !isE2EAuthed(r) {
		if msg := crossSiteReject(r, a.Protected); msg != "" {
			sendJSON(w, http.StatusForbidden, map[string]any{"error": msg})
			return a, st, false
		}
	}
	return a, st, true
}

// sendPwError répond à un mot de passe refusé. Une attente part en 429, avec sa
// durée ; wrong est le statut d'un simple mauvais mot de passe.
func sendPwError(w http.ResponseWriter, err error, wrong int) {
	var locked *pwLockedError
	if errors.As(err, &locked) {
		wait := int(locked.Wait.Seconds()) + 1
		w.Header().Set("Retry-After", fmt.Sprint(wait))
		sendJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "trop d'essais", "wait": wait})
		return
	}
	if errors.Is(err, errPwWrong) {
		sendJSON(w, wrong, map[string]any{"ok": false, "error": errPwWrong.Error()})
		return
	}
	sendJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "configuration illisible — réessaie dans un instant"})
}

// handleAuthState (GET /api/auth) : où en est la porte, pour que la page sache
// quoi montrer. Ouverte à tous : elle ne dit rien de plus que ce qu'un essai de
// connexion apprendrait.
func handleAuthState(w http.ResponseWriter, r *http.Request) {
	a, st, ok := authGate(w, r)
	if !ok {
		return
	}
	out := map[string]any{
		"password": a.Password,                // un mot de passe existe
		"key":      a.Key,                     // une clé de pilotage existe
		"allowed":  a.Allowed,                 // cette requête peut entrer
		"session":  a.Session,                 // … par une session
		"https":    requestIsHTTPS(r),         // la liaison est chiffrée
		"min":      pwMinRunes,                // longueur minimale d'un mot de passe
		"remote":   isE2EAuthed(r),            // par ajean.link (chiffré de bout en bout)
		"can_set":  canSetFirstPassword(r, a), // peut poser le premier mot de passe d'ici
	}
	if a.Allowed && a.Password {
		out["sessions"] = webSessionCount(st)
	}
	sendJSON(w, 200, out)
}

// canSetFirstPassword : d'où peut-on poser le PREMIER mot de passe ?
//
//   - Une clé de pilotage protège déjà l'API : il faut la présenter (ou passer
//     par ajean.link). Être sur la machine ne suffit pas, sinon un programme
//     local sans la clé s'ouvrirait l'API en se créant un mot de passe.
//   - Rien ne protège l'API : elle est ouverte à tout le réseau local, et le
//     premier venu enfermerait le propriétaire dehors. Il faut donc être sur la
//     machine elle-même, ou passer par ajean.link.
func canSetFirstPassword(r *http.Request, a webAccess) bool {
	if a.Password {
		return false
	}
	if a.Key {
		return a.Allowed
	}
	return isE2EAuthed(r) || fromThisMachine(r)
}

// handleAuthLogin (POST /api/auth/login {password}) : ouvre une session.
func handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	a, _, ok := authGate(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST attendu"})
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "requête illisible"})
		return
	}
	if !a.Password {
		sendJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "aucun mot de passe n'est défini"})
		return
	}
	ip := loginClientIP(r)
	if err := verifyWebPassword(in.Password, ip); err != nil {
		sendPwError(w, err, http.StatusUnauthorized)
		return
	}
	token, err := openWebSession(ip, r.UserAgent())
	if err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": "session non enregistrée"})
		return
	}
	setSessionCookie(w, r, token)
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleAuthLogout (POST /api/auth/logout {others}) : ferme sa session, ou
// toutes les autres.
func handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	a, _, ok := authGate(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST attendu"})
		return
	}
	var in struct {
		Others bool `json:"others"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in)
	if in.Others {
		if !a.Session {
			sendJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "connexion requise"})
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true, "closed": closeOtherWebSessions(sessionToken(r))})
		return
	}
	closeWebSession(sessionToken(r))
	setSessionCookie(w, r, "")
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleAuthPassword (POST /api/auth/password {current, password, remove}) :
// définit le mot de passe, le change ou le retire. Le changer ou le retirer
// exige d'être entré ET de redonner l'actuel. Toutes les sessions sont fermées ;
// celui qui vient de le poser en reçoit une neuve.
func handleAuthPassword(w http.ResponseWriter, r *http.Request) {
	a, _, ok := authGate(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST attendu"})
		return
	}
	var in struct {
		Current  string `json:"current"`
		Password string `json:"password"`
		Remove   bool   `json:"remove"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "requête illisible"})
		return
	}
	ip := loginClientIP(r)
	if a.Password {
		if !a.Allowed {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ajean"`)
			sendJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "connexion requise", "login": true})
			return
		}
		if err := verifyWebPassword(in.Current, ip); err != nil {
			// 403 et non 401 : pour la page, 401 veut dire « session perdue ».
			sendPwError(w, err, http.StatusForbidden)
			return
		}
	} else {
		if in.Remove {
			sendJSON(w, 200, map[string]any{"ok": true})
			return
		}
		if !canSetFirstPassword(r, a) {
			sendJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "le premier mot de passe se définit depuis la machine elle-même (commande « ajean password »)"})
			return
		}
	}
	if in.Remove {
		if err := removeWebPassword(); err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": "mot de passe non retiré"})
			return
		}
		setSessionCookie(w, r, "")
		sendJSON(w, 200, map[string]any{"ok": true})
		return
	}
	if err := setWebPassword(in.Password); err != nil {
		code := 500
		if errors.Is(err, errPwTooShort) || errors.Is(err, errPwTooLong) {
			code = 400
		}
		sendJSON(w, code, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !isE2EAuthed(r) {
		token, err := openWebSession(ip, r.UserAgent())
		if err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": "mot de passe enregistré, session non ouverte : reconnecte-toi"})
			return
		}
		setSessionCookie(w, r, token)
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// registerAuthRoutes : les routes de la porte. Elles ne passent PAS par
// requireWebAuth (on ne peut pas exiger d'être connecté pour se connecter) ;
// chacune applique authGate.
func registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth", handleAuthState)
	mux.HandleFunc("/api/auth/login", handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", handleAuthLogout)
	mux.HandleFunc("/api/auth/password", handleAuthPassword)
}

// ---- adresse d'écoute ------------------------------------------------------------

const stWebHost = "web_host"

// webListenHost : l'adresse sur laquelle l'interface écoute. Sans réglage, toutes
// les interfaces (0.0.0.0), comme depuis toujours. AJEAN_HOST (environnement) ou
// « ajean ui listen <adresse> » (enregistré, pour le service) la restreint :
// 127.0.0.1 ferme l'accès réseau, ajean.link continuant de marcher (le tunnel est
// sortant).
//
// Une adresse du réseau demandée EXPLICITEMENT exige une protection (mot de passe
// ou clé de pilotage) : qui écrit AJEAN_HOST=0.0.0.0 sait qu'il expose
// l'interface, on refuse de le faire à découvert. Le défaut, lui, ne change pas.
func webListenHost() (string, error) {
	h := strings.TrimSpace(os.Getenv("AJEAN_HOST"))
	src := "AJEAN_HOST"
	if h == "" {
		h, src = strings.TrimSpace(getStr(bkState, stWebHost)), "ajean ui listen"
	}
	if h == "" {
		return "0.0.0.0", nil
	}
	ip, err := parseListenHost(h)
	if err != nil {
		return "", fmt.Errorf("%s : %w", src, err)
	}
	if !ip.IsLoopback() && !webKeyConfigured() && !webPasswordConfigured() {
		return "", fmt.Errorf("%s=%s ouvre l'interface au réseau : définis d'abord un mot de passe (« ajean password ») ou une clé de pilotage (« ajean set-web-key »)", src, h)
	}
	return ip.String(), nil
}

func parseListenHost(h string) (net.IP, error) {
	if strings.EqualFold(h, "localhost") {
		return net.IPv4(127, 0, 0, 1), nil
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	if ip == nil {
		return nil, fmt.Errorf("adresse d'écoute invalide « %s » (une adresse IP : 127.0.0.1, 0.0.0.0, 192.168.1.10…)", h)
	}
	return ip, nil
}

// webLocalURLHost : le nom à mettre dans l'adresse locale de l'interface.
func webLocalURLHost(listen string) string {
	ip := net.ParseIP(listen)
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
		return "localhost"
	}
	if ip.To4() == nil {
		return "[" + ip.String() + "]"
	}
	return ip.String()
}

// cmdUIListen : « ajean ui listen [adresse|all] » — enregistre l'adresse d'écoute
// du service d'interface (lue à son prochain démarrage).
func cmdUIListen(args []string) error {
	if len(args) == 0 {
		cur := getStr(bkState, stWebHost)
		if cur == "" {
			cur = "0.0.0.0 (toutes les interfaces, défaut)"
		}
		fmt.Printf("%s adresse d'écoute de l'interface : %s\n", cyan("[ui]"), cur)
		if env := strings.TrimSpace(os.Getenv("AJEAN_HOST")); env != "" {
			fmt.Printf("       AJEAN_HOST=%s est défini et l'emporte\n", env)
		}
		return nil
	}
	h := strings.TrimSpace(args[0])
	switch strings.ToLower(h) {
	case "all", "default", "défaut", "":
		if err := putStr(bkState, stWebHost, ""); err != nil {
			return err
		}
		fmt.Printf("%s l'interface écoutera sur toutes les interfaces (défaut)\n", green("[ok]"))
	default:
		ip, err := parseListenHost(h)
		if err != nil {
			return err
		}
		if !ip.IsLoopback() && !webKeyConfigured() && !webPasswordConfigured() {
			return fmt.Errorf("%s ouvre l'interface au réseau : définis d'abord un mot de passe (« ajean password ») ou une clé de pilotage (« ajean set-web-key »)", ip)
		}
		if err := putStr(bkState, stWebHost, ip.String()); err != nil {
			return err
		}
		fmt.Printf("%s l'interface écoutera sur %s\n", green("[ok]"), ip)
	}
	fmt.Println(dim("[info] pris en compte au prochain démarrage de l'interface (ajean ui restart)"))
	return nil
}

// ---- commande « ajean password » ------------------------------------------------

// pwStdin : un seul lecteur pour toute la commande (deux lectures tamponnées
// séparées se voleraient la suite de l'entrée).
var pwStdin *bufio.Reader

// readSecretLine lit un secret au clavier sans l'afficher. Si l'entrée n'est pas
// un terminal (tube, redirection), elle lit une ligne.
func readSecretLine(prompt string) (string, bool, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Print(prompt)
		b, err := term.ReadPassword(fd)
		fmt.Println()
		return string(b), true, err
	}
	if pwStdin == nil {
		pwStdin = bufio.NewReader(os.Stdin)
	}
	line, err := pwStdin.ReadString('\n')
	if err != nil && line == "" {
		return "", false, errors.New("aucun mot de passe reçu sur l'entrée standard")
	}
	return cleanPipedSecret(line), false, nil
}

// cleanPipedSecret retire d'une ligne reçue par un tube ce qui n'appartient pas
// au secret : la fin de ligne, et la marque d'ordre d'octets que Windows
// PowerShell place en tête de ce qu'il envoie à un programme (mesuré : « abc »
// arrive EF BB BF 61 62 63). Sans cela le mot de passe enregistré commençait par
// un caractère invisible, et celui qu'on tape ensuite était refusé.
var utf8BOM = string(rune(0xFEFF))

func cleanPipedSecret(line string) string {
	return strings.TrimRight(strings.TrimPrefix(line, utf8BOM), "\r\n")
}

// cmdPassword définit, remplace ou retire le mot de passe de l'interface.
//
//	ajean password            le définit ou le remplace (saisi au clavier, jamais
//	                          en argument : il resterait dans l'historique du shell)
//	ajean password --remove   le retire
//	ajean password status     dit ce qui protège l'interface
//
// Le serveur web relit l'état à chaud : aucun redémarrage. Toutes les sessions
// ouvertes sont fermées.
func cmdPassword(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch sub {
	case "status":
		st, err := pwStateErr()
		if err != nil {
			return err
		}
		switch {
		case st.configured():
			fmt.Printf("%s mot de passe défini — %d session(s) ouverte(s)\n", green("[ok]"), webSessionCount(st))
		default:
			fmt.Printf("%s aucun mot de passe\n", yellow("[info]"))
		}
		if webKeyConfigured() {
			fmt.Printf("%s clé de pilotage définie (Authorization: Bearer …)\n", green("[ok]"))
		} else if !st.configured() {
			fmt.Printf("%s l'interface est ouverte à qui peut joindre la machine\n", yellow("[!]"))
		}
		return nil
	case "--remove", "remove", "off", "none":
		if err := removeWebPassword(); err != nil {
			return err
		}
		fmt.Printf("%s mot de passe retiré, sessions fermées\n", yellow("[info]"))
		if !webKeyConfigured() {
			fmt.Printf("%s plus rien ne protège l'interface\n", yellow("[!]"))
		}
		return nil
	case "":
	default:
		return fmt.Errorf("usage: ajean password [--remove|status]   (le mot de passe se saisit au clavier, pas en argument)")
	}
	pw, tty, err := readSecretLine(fmt.Sprintf("Nouveau mot de passe (%d caractères au moins) : ", pwMinRunes))
	if err != nil {
		return err
	}
	if tty {
		again, _, err := readSecretLine("Encore une fois : ")
		if err != nil {
			return err
		}
		if again != pw {
			return errors.New("les deux saisies diffèrent : rien n'a été changé")
		}
	}
	if err := setWebPassword(pw); err != nil {
		return err
	}
	fmt.Printf("%s mot de passe enregistré — les sessions ouvertes sont fermées\n", green("[ok]"))
	fmt.Println(dim("       pris en compte tout de suite, sans redémarrer l'interface"))
	return nil
}
