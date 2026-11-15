package server

import (
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"

	"ajean/internal/auth"
)

// Le cookie de session : illisible par le JavaScript de la page (HttpOnly),
// jamais envoyé par un autre site (SameSite=Strict), et réservé à HTTPS dès que
// la page est servie en HTTPS.
const sessionCookie = "ajean_session"

// forwardHeaders : ce qu'un proxy ajoute à une requête qu'il relaie. Une requête
// qui en porte ne vient pas de la machine elle-même, même si elle arrive de
// 127.0.0.1 (le proxy tourne ici, le visiteur est ailleurs).
var forwardHeaders = []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", "Forwarded", "Via", "Cf-Connecting-Ip", "True-Client-Ip"}

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

// fromThisMachine : la requête vient-elle d'un navigateur de la machine, sans
// intermédiaire ? C'est ce qui donne accès tant qu'aucun mot de passe n'existe.
func fromThisMachine(r *http.Request) bool {
	ip := remoteIP(r)
	if ip == nil || !ip.IsLoopback() || !localHost(r.Host) {
		return false
	}
	for _, h := range forwardHeaders {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	return true
}

// clientIP : l'adresse à qui compter les essais de mot de passe. Derrière un
// proxy installé sur la machine, c'est celle qu'il annonce (la dernière de
// X-Forwarded-For : celle qu'il a vue lui-même, les précédentes sont déclarées
// par le visiteur). On ne croit cet en-tête que s'il vient de la machine.
func clientIP(r *http.Request) string {
	ip := remoteIP(r)
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

// isHTTPS : la page est-elle servie en HTTPS, par nous ou par un proxy de la machine ?
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	ip := remoteIP(r)
	return ip != nil && ip.IsLoopback() && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func sessionToken(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

func setSession(w http.ResponseWriter, r *http.Request, token string) {
	c := &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: isHTTPS(r)}
	if token == "" {
		c.MaxAge = -1
	} else {
		c.MaxAge = 400 * 24 * 3600 // le navigateur le garde ; c'est le serveur qui éteint une session inactive
	}
	http.SetCookie(w, c)
}

// allowed : cette requête a-t-elle le droit d'utiliser l'assistant ? Sinon, le
// statut et l'erreur à rendre.
func allowed(r *http.Request) (int, error) {
	if !auth.Configured() {
		if fromThisMachine(r) {
			return 0, nil
		}
		return http.StatusForbidden, errors.New("aucun mot de passe n'est défini : l'interface n'est ouverte qu'à la machine elle-même")
	}
	if auth.Valid(sessionToken(r)) {
		return 0, nil
	}
	return http.StatusUnauthorized, errors.New("connexion requise")
}

// failAuth répond à un mot de passe refusé. Une attente part en 429, avec sa durée.
func failAuth(w http.ResponseWriter, err error) {
	var locked *auth.LockedError
	if errors.As(err, &locked) {
		wait := int(math.Ceil(locked.Wait.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"trop d'essais","wait":` + strconv.Itoa(wait) + `}`))
		return
	}
	fail(w, http.StatusUnauthorized, err)
}

// handleAuth : où en est la porte, pour que la page sache quoi montrer. Ouverte
// à tous : elle ne dit rien de plus que ce qu'un essai de connexion apprendrait.
func handleAuth(w http.ResponseWriter, r *http.Request) {
	code, _ := allowed(r)
	configured := auth.Configured()
	out := map[string]any{
		"configured": configured,         // un mot de passe existe
		"allowed":    code == 0,          // cette requête peut entrer
		"local":      fromThisMachine(r), // elle vient de la machine elle-même
		"https":      isHTTPS(r),         // la liaison est chiffrée
		"min":        auth.MinPassword,   // longueur minimale d'un mot de passe
	}
	if code == 0 && configured {
		out["sessions"] = auth.Count()
		out["env"] = auth.FromEnv()
	}
	reply(w, out)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !body(w, r, &in) {
		return
	}
	if !auth.Configured() {
		fail(w, http.StatusConflict, errors.New("aucun mot de passe n'est défini"))
		return
	}
	ip := clientIP(r)
	if err := auth.Verify(in.Password, ip); err != nil {
		failAuth(w, err)
		return
	}
	token, err := auth.Open(ip, r.UserAgent())
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	setSession(w, r, token)
	ok(w)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	auth.Close(sessionToken(r))
	setSession(w, r, "")
	ok(w)
}

// handlePassword définit le mot de passe (la première fois : depuis la machine
// seulement) ou le change (il faut être connecté ET redonner l'actuel). Toutes
// les sessions sont fermées ; celui qui vient de le poser en reçoit une neuve.
func handlePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if !body(w, r, &in) {
		return
	}
	if code, err := allowed(r); code != 0 {
		fail(w, code, err)
		return
	}
	ip := clientIP(r)
	if auth.Configured() {
		if err := auth.Verify(in.Current, ip); err != nil {
			if errors.Is(err, auth.ErrWrong) { // 401 voudrait dire « session perdue » pour la page
				fail(w, http.StatusForbidden, errors.New("mot de passe actuel incorrect"))
				return
			}
			failAuth(w, err)
			return
		}
	}
	if err := auth.SetPassword(in.Password); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	token, err := auth.Open(ip, r.UserAgent())
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	setSession(w, r, token)
	ok(w)
}

func handleLogoutOthers(w http.ResponseWriter, r *http.Request) {
	reply(w, map[string]int{"closed": auth.CloseOthers(sessionToken(r))})
}
