package ajean

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// web_auth.go protège l'API de pilotage (ajean web) quand elle est exposée sur
// internet — c.-à-d. l'API que tout client (navigateur, app mobile, script…)
// utilise pour switcher de preset, redémarrer le service, lire le status, etc.
//
// La clé de pilotage est volontairement DISTINCTE de .api_key (qui, elle,
// protège llama-server / les complétions). On veut pouvoir donner à un client
// un accès aux complétions sans lui donner le droit de redémarrer la machine,
// et inversement. Elle est relue à chaque requête (pas de cache) pour qu'un
// changement de clé prenne effet sans redémarrer le serveur web.

// readWebKeyErr renvoie la clé de pilotage en distinguant « aucune clé » d'une
// LECTURE RATÉE. La nuance est tout sauf cosmétique : sans clé, l'API est
// ouverte. Confondre les deux, c'est ouvrir l'API parce que la base était
// momentanément verrouillée par une commande CLI — une panne d'E/S qui désarme
// l'authentification. requireWebAuth refuse donc plutôt que d'ouvrir.
func readWebKeyErr() (string, error) {
	b, err := getBytesErr(bkState, "web_key")
	return string(b), err
}

// readWebKey renvoie la clé de pilotage, ou "" si aucune n'est définie (ou
// illisible). Réservé à l'affichage ; toute décision d'accès passe par
// readWebKeyErr.
func readWebKey() string { k, _ := readWebKeyErr(); return k }

// requireWebAuth wraps an HTTP handler, rejecting requests that don't present
// the configured Bearer token. When no key is configured the handler is left
// open (pratique en local) — cmdWeb avertit alors bruyamment au démarrage.
func requireWebAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, err := readWebKeyErr()
		if err != nil {
			// On ne sait pas si une clé protège cette API : on ferme.
			sendJSON(w, http.StatusServiceUnavailable,
				map[string]any{"error": "configuration illisible — réessaie dans un instant"})
			return
		}
		if key == "" {
			next(w, r)
			return
		}
		if !checkBearer(r, key) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ajean"`)
			sendJSON(w, http.StatusUnauthorized, map[string]any{"error": "non autorisé"})
			return
		}
		next(w, r)
	}
}

// checkBearer reports whether the request carries the expected key as an
// "Authorization: Bearer <clé>" header. La comparaison est à temps constant.
// PAS de repli ?key=<clé> en query string : une clé dans l'URL finit dans les
// logs des proxys (Caddy, Cloudflare), l'historique navigateur et les Referer.
func checkBearer(r *http.Request, key string) bool {
	want := []byte(key)
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got := []byte(strings.TrimSpace(h[len("Bearer "):]))
		if subtle.ConstantTimeCompare(got, want) == 1 {
			return true
		}
	}
	return false
}

// cmdSetWebKey sets (or clears) the control-API key in $AJEAN_HOME/.web_key.
//
//	ajean set-web-key <clé>     définit la clé
//	ajean set-web-key           génère une clé aléatoire
//	ajean set-web-key ""        supprime la protection (API ouverte)
//
// Contrairement à set-api-key, aucun redémarrage n'est nécessaire : le serveur
// web relit la clé à chaque requête.
func cmdSetWebKey(args []string) error {
	var key string
	switch {
	case len(args) == 0:
		buf := make([]byte, 24)
		if _, err := rand.Read(buf); err != nil {
			return err
		}
		key = "ajean-web-" + hex.EncodeToString(buf)
		fmt.Printf("%s clé générée : %s\n", green("[ok]"), bold(key))
	case args[0] == "" || args[0] == "off" || args[0] == "none":
		key = ""
	default:
		key = strings.TrimSpace(args[0])
	}
	if err := putStr(bkState, "web_key", key); err != nil {
		return err
	}
	if key == "" {
		fmt.Printf("%s clé de pilotage supprimée — l'API web n'est plus protégée\n", yellow("[info]"))
		return nil
	}
	fmt.Printf("%s clé de pilotage enregistrée\n", green("[ok]"))
	fmt.Printf("       les clients doivent envoyer : %s\n", dim("Authorization: Bearer "+key))
	fmt.Printf("       (relance 'ajean web' si le serveur web tourne déjà — non requis, lu à chaud)\n")
	return nil
}
