package jean

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// web_auth.go protège l'API de pilotage (jean web) quand elle est exposée sur
// internet — c.-à-d. l'API que tout client (navigateur, app mobile, script…)
// utilise pour switcher de preset, redémarrer le service, lire le status, etc.
//
// La clé de pilotage est volontairement DISTINCTE de .api_key (qui, elle,
// protège llama-server / les complétions). On veut pouvoir donner à un client
// un accès aux complétions sans lui donner le droit de redémarrer la machine,
// et inversement. Elle est stockée dans $JEAN_HOME/.web_key et lue à chaque
// requête (pas de cache) pour qu'un changement de clé prenne effet sans
// redémarrer le serveur web.

func webKeyPath() string { return filepath.Join(JeanHome(), ".web_key") }

// readWebKey returns the trimmed contents of $JEAN_HOME/.web_key, or "" if the
// file is absent/empty.
func readWebKey() string {
	b, err := os.ReadFile(webKeyPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// requireWebAuth wraps an HTTP handler, rejecting requests that don't present
// the configured Bearer token. When no key is configured the handler is left
// open (pratique en local) — cmdWeb avertit alors bruyamment au démarrage.
func requireWebAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := readWebKey()
		if key == "" {
			next(w, r)
			return
		}
		if !checkBearer(r, key) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="jean"`)
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

// cmdSetWebKey sets (or clears) the control-API key in $JEAN_HOME/.web_key.
//
//	jean set-web-key <clé>     définit la clé
//	jean set-web-key           génère une clé aléatoire
//	jean set-web-key ""        supprime la protection (API ouverte)
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
		key = "jean-web-" + hex.EncodeToString(buf)
		fmt.Printf("%s clé générée : %s\n", green("[ok]"), bold(key))
	case args[0] == "" || args[0] == "off" || args[0] == "none":
		key = ""
	default:
		key = strings.TrimSpace(args[0])
	}
	if key == "" {
		if err := os.Remove(webKeyPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Printf("%s clé de pilotage supprimée — l'API web n'est plus protégée\n", yellow("[info]"))
		return nil
	}
	if err := os.MkdirAll(JeanHome(), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(webKeyPath(), []byte(key+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Printf("%s clé de pilotage enregistrée dans %s\n", green("[ok]"), webKeyPath())
	fmt.Printf("       les clients doivent envoyer : %s\n", dim("Authorization: Bearer "+key))
	fmt.Printf("       (relance 'jean web' si le serveur web tourne déjà — non requis, lu à chaud)\n")
	return nil
}
