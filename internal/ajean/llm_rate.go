package ajean

import (
	"net"
	"net/url"
	"time"
)

// llm_rate.go — débit de génération quand le serveur n'envoie pas de `timings`
// (API tierces, services). AJEAN le mesure alors lui-même.

// remoteEndpoint : l'URL désigne-t-elle une autre machine ? (faux pour
// localhost, 127.0.0.1, ::1 et une URL illisible.)
func remoteEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return !ip.IsLoopback() && !ip.IsUnspecified()
	}
	return true
}

// measuredGenRate renvoie les tokens par seconde d'une complétion de `gen`
// tokens, envoyée à tReq, dont le premier morceau est arrivé à tFirst et le
// dernier à tLast.
//
// Serveur sur cette machine : débit de décodage, entre le premier et le dernier
// token. Le délai avant le premier token y est la lecture du prompt, qu'on ne
// veut pas compter.
//
// API distante : tokens sur le temps total de la requête. Le compte `gen` vient
// de `usage` et inclut ce que le fournisseur a produit SANS le diffuser (réflexion
// masquée, texte mis en tampon) avant le premier morceau : le rapporter à la
// seule fenêtre visible gonflait le débit (247 tokens en 15 s affichés 23,5 t/s).
func measuredGenRate(endpoint string, gen int, tReq, tFirst, tLast time.Time) float64 {
	if gen <= 1 || tFirst.IsZero() {
		return 0
	}
	if remoteEndpoint(endpoint) {
		if sec := tLast.Sub(tReq).Seconds(); sec > 0 {
			return float64(gen) / sec
		}
		return 0
	}
	if sec := tLast.Sub(tFirst).Seconds(); sec > 0 {
		return float64(gen-1) / sec
	}
	return 0
}
