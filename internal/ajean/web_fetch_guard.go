package ajean

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"
)

// Garde-fou SSRF du moteur web. web_open et la recherche lisent des adresses
// choisies par le modèle, lui-même influençable par une page piégée : sans
// filtre, il pouvait lui faire lire la boucle locale (dont l'API d'AJEAN), le
// réseau privé ou les métadonnées d'un hébergeur cloud.
//
// Deux barrières :
//   - webDestCheck, avant la requête et à chaque redirection, sur le nom d'hôte ;
//   - webDialControl, au moment de la connexion, sur l'adresse IP réellement
//     jointe — la seule qui tienne face à un DNS qui change de réponse entre la
//     vérification et la connexion.
//
// AJEAN_WEB_ALLOW_LOCAL=1 lève le filtre pour qui veut faire lire à l'IA une
// page de son propre réseau.

var errWebLocalDest = fmt.Errorf("adresse locale ou privée refusée : le moteur web ne lit que l'internet public")

func webFetchAllowLocal() bool { return os.Getenv("AJEAN_WEB_ALLOW_LOCAL") == "1" }

// webIPBlocked : boucle locale, réseaux privés, lien-local (dont 169.254.169.254,
// les métadonnées cloud), adresses non spécifiées, multidiffusion, et la plage
// CGNAT 100.64.0.0/10 (Tailscale et consorts).
func webIPBlocked(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return true
	}
	return false
}

// webDestCheck refuse une URL dont l'hôte est, ou se résout en, une adresse
// bloquée. Un nom qui ne se résout pas ici passe : derrière un proxy
// d'entreprise c'est le proxy qui résout, et sans proxy la connexion échouera
// d'elle-même (webDialControl veille de toute façon).
func webDestCheck(ctx context.Context, u *url.URL) error {
	if webFetchAllowLocal() || u == nil {
		return nil
	}
	host := u.Hostname()
	if host == "" {
		return errWebLocalDest
	}
	if ip := net.ParseIP(host); ip != nil {
		if webIPBlocked(ip) {
			return errWebLocalDest
		}
		return nil
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lctx, host)
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if webIPBlocked(a.IP) {
			return errWebLocalDest
		}
	}
	return nil
}

// webDialControl s'exécute juste avant chaque connexion, avec l'adresse IP
// résolue : c'est elle qui est jointe, pas le nom.
func webDialControl(_, address string, _ syscall.RawConn) error {
	if webFetchAllowLocal() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if webIPBlocked(net.ParseIP(host)) {
		return errWebLocalDest
	}
	return nil
}

// webProxyConfigured : un proxy est déclaré dans l'environnement. La connexion
// part alors vers lui (souvent une adresse privée) : le contrôle à la connexion
// n'a plus de sens, seul webDestCheck s'applique.
func webProxyConfigured() bool {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

// newWebTransport : le transport du moteur web, copie du transport par défaut
// avec le contrôle à la connexion.
func newWebTransport() http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	tr := base.Clone()
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if !webProxyConfigured() {
		d.Control = webDialControl
	}
	tr.DialContext = d.DialContext
	return tr
}
