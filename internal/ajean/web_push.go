// web_push.go — endpoints d'inscription aux notifications Web Push (voir push.go).
// Tous passent par /api/* (donc par jfetch/E2E sur app.ajean.link, comme le reste)
// en simple req/resp : le service worker et le manifest, eux, sont servis en clair
// à la racine (web_server.go), car un service worker doit venir de l'origine même.
package ajean

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// pushEndpointHost extrait l'hôte d'un endpoint de push pour les logs (ex.
// web.push.apple.com) sans divulguer le jeton complet.
func pushEndpointHost(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		return u.Host
	}
	return "?"
}

// handlePushKey (GET) : remet la clé publique VAPID dont l'UI a besoin pour
// s'abonner (PushManager.subscribe applicationServerKey). Générée à la volée au
// premier appel, puis stable.
func handlePushKey(w http.ResponseWriter, r *http.Request) {
	_, pub, err := vapidKeys()
	if err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "key": pub})
}

// handlePushSubscribe (POST) : enregistre l'abonnement PushSubscription du
// navigateur. Corps = l'objet renvoyé par subscription.toJSON() côté JS.
func handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	var s pushSub
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil || s.Endpoint == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "abonnement invalide"})
		return
	}
	if err := addSub(s); err != nil {
		fmt.Printf("[push] abonnement REFUSÉ (stockage) : %v\n", err)
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	fmt.Printf("[push] abonnement enregistré (%s) — %d au total\n", pushEndpointHost(s.Endpoint), len(loadSubs()))
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handlePushUnsubscribe (POST {endpoint}) : retire un abonnement (l'utilisateur a
// coupé les notifs dans les réglages).
func handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	removeSub(body.Endpoint)
	sendJSON(w, 200, map[string]any{"ok": true})
}
