package ajean

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// La clé de pilotage n'est plus stockée en CLAIR : seule son EMPREINTE (SHA-256)
// est persistée, sous bkState "web_key_hash". Ainsi le serveur peut VALIDER un
// Bearer présenté (il compare les empreintes) mais ne détient jamais la clé
// elle-même — condition pour que cette même clé serve à ouvrir le coffre du
// chiffrement sans que le serveur puisse l'ouvrir seul.

func hashWebKey(k string) string {
	sum := sha256.Sum256([]byte(k))
	return hex.EncodeToString(sum[:])
}

// webKeyHashErr renvoie l'empreinte stockée en distinguant « aucune clé » d'une
// lecture ratée (voir requireWebAuth : une lecture ratée FERME l'API).
func webKeyHashErr() (string, error) {
	b, err := getBytesErr(bkState, "web_key_hash")
	return string(b), err
}

// webKeyConfigured indique qu'une clé de pilotage est définie (empreinte présente).
func webKeyConfigured() bool { h, _ := webKeyHashErr(); return h != "" }

// storeWebKey enregistre la clé de pilotage sous forme d'EMPREINTE uniquement
// (jamais le clair), et efface tout ancien clair résiduel. key vide = protection
// retirée.
func storeWebKey(key string) error {
	_ = putStr(bkState, "web_key", "") // le clair ne doit plus jamais traîner
	hash := ""
	if key != "" {
		hash = hashWebKey(key)
	}
	return putStr(bkState, "web_key_hash", hash)
}

// migrateWebKeyToHash convertit une ancienne clé stockée en clair vers son
// empreinte (une fois), puis efface le clair. Appelé au démarrage.
func migrateWebKeyToHash() {
	plain := getStr(bkState, "web_key")
	if plain == "" {
		return
	}
	if !webKeyConfigured() {
		_ = putStr(bkState, "web_key_hash", hashWebKey(plain))
	}
	_ = putStr(bkState, "web_key", "") // le clair ne doit plus jamais traîner
}

// --- Authentification par identité E2E (contexte, non falsifiable) ------------
//
// Une requête arrivée par le tunnel ajean.link est DÉJÀ authentifiée par
// l'identité E2E de l'utilisateur (voir handleE2EReq → e2eAuthOpenReq). On la
// marque alors via le CONTEXTE de la requête — impossible à forger par un client
// HTTP externe, contrairement à un en-tête. requireWebAuth l'accepte donc sans
// exiger la clé de pilotage : plus besoin que le serveur détienne cette clé.

type ctxKey int

const e2eAuthedKey ctxKey = 1

func markE2EAuthed(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), e2eAuthedKey, true))
}

func isE2EAuthed(r *http.Request) bool {
	v, _ := r.Context().Value(e2eAuthedKey).(bool)
	return v
}

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
		// Requête arrivée par le tunnel ajean.link : déjà authentifiée par l'identité
		// E2E de l'utilisateur (contexte non falsifiable). On n'exige pas la clé.
		if isE2EAuthed(r) {
			next(w, r)
			return
		}
		hash, err := webKeyHashErr()
		if err != nil {
			// On ne sait pas si une clé protège cette API : on ferme.
			sendJSON(w, http.StatusServiceUnavailable,
				map[string]any{"error": "configuration illisible — réessaie dans un instant"})
			return
		}
		if hash == "" {
			next(w, r)
			return
		}
		if !checkBearer(r, hash) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ajean"`)
			sendJSON(w, http.StatusUnauthorized, map[string]any{"error": "non autorisé"})
			return
		}
		next(w, r)
	}
}

// checkBearer reports whether the request carries a Bearer whose EMPREINTE égale
// wantHash. On ne compare jamais la clé en clair (le serveur ne la détient pas) :
// on hache le Bearer présenté et on compare à temps constant. PAS de repli
// ?key=<clé> en query string (fuite dans les logs proxy / l'historique).
func checkBearer(r *http.Request, wantHash string) bool {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got := hashWebKey(strings.TrimSpace(h[len("Bearer "):]))
		if subtle.ConstantTimeCompare([]byte(got), []byte(wantHash)) == 1 {
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
	if err := storeWebKey(key); err != nil {
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
