// Package netguard empêche l'assistant de joindre la machine sur laquelle il
// tourne. Son interface web écoute sur 127.0.0.1 : un script ou une page qui
// pourrait l'appeler piloterait l'assistant à la place de l'utilisateur. Le
// réseau local (box, imprimante, NAS) est fermé lui aussi, sauf si
// AJEAN_TOOLS_LAN=1. Rien n'est rangé sur le disque.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"syscall"
)

// Control se branche sur net.Dialer.Control. Il voit l'adresse réellement
// jointe, après résolution du nom : un nom public qui pointe vers 127.0.0.1 ou
// une redirection vers la machine locale sont donc refusés aussi.
func Control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("adresse refusée : %s", host)
	}
	return checkIP(ip)
}

// CheckURL valide une URL donnée par le modèle avant qu'un navigateur (qui
// n'ouvre pas ses connexions par notre Dialer) ne s'y rende : http ou https
// seulement, et aucune des adresses de l'hôte ne doit être locale.
func CheckURL(ctx context.Context, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("URL illisible : %s", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("URL refusée : http:// ou https:// uniquement")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return errors.New("URL refusée : hôte manquant")
	}
	// Certains résolveurs ne connaissent pas « *.localhost » alors que les
	// navigateurs l'envoient toujours sur la boucle locale.
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errLocal
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return checkIP(ip)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("hôte introuvable : %s", host)
	}
	for _, ip := range ips {
		if err := checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}

// sharedRange : la plage des adresses partagées (CGNAT), où vivent aussi les
// réseaux privés virtuels du type Tailscale. Privée dans les faits.
var sharedRange = netip.MustParsePrefix("100.64.0.0/10")

var (
	errLocal   = errors.New("adresse locale refusée")
	errPrivate = errors.New("réseau local refusé (AJEAN_TOOLS_LAN=1 pour l'autoriser)")
)

func checkIP(ip netip.Addr) error {
	// « ::ffff:127.0.0.1 » est la boucle locale écrite en IPv6.
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		return errLocal
	}
	if (ip.IsPrivate() || sharedRange.Contains(ip)) && os.Getenv("AJEAN_TOOLS_LAN") == "" {
		return errPrivate
	}
	return nil
}
