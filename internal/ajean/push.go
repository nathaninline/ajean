// push.go — notifications Web Push : quand un tour utilisateur se termine
// (chat_conversation.go, turn_done), le serveur AJEAN pousse une notification
// vers les navigateurs abonnés (iPhone verrouillé, Android, desktop) via leur
// service de push (Apple/Google). C'est le SERVEUR qui pousse, directement vers
// le endpoint du navigateur — ça marche donc app fermée, contrairement à une
// notification purement client (l'onglet caché relâche son flux SSE).
//
// Ce que ça ne fait PAS transiter par le tunnel E2E : la charge utile est
// chiffrée de bout en bout avec les clés PROPRES de l'abonnement (RFC 8291),
// puis remise au service de push public. L'inscription, elle, passe par /api
// (donc jfetch/E2E) comme le reste.
package ajean

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// fmtDurFR : durée → « 42s », « 3 mn 05s », « 1 h 12 mn » pour le corps de la
// notification. Miroir serveur de fmtElapsed() côté UI (09-stream.js).
func fmtDurFR(d time.Duration) string {
	secs := int(d.Round(time.Second).Seconds())
	if secs < 0 {
		secs = 0
	}
	h, m, s := secs/3600, (secs%3600)/60, secs%60
	switch {
	case h > 0:
		return fmt.Sprintf("%d h %02d mn", h, m)
	case m > 0:
		return fmt.Sprintf("%d mn %02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// Stockées dans bkState (une seule base bbolt, voir store.go).
const (
	stPushVAPIDPriv = "push_vapid_priv" // clé privée VAPID (base64url)
	stPushVAPIDPub  = "push_vapid_pub"  // clé publique VAPID (base64url), servie à l'UI
	stPushSubs      = "push_subs"       // liste JSON des abonnements
)

// pushSub : un abonnement PushSubscription du navigateur. Même forme que l'objet
// JS, on le reçoit tel quel depuis l'UI et on le rejoue tel quel à l'envoi.
type pushSub struct {
	Endpoint string       `json:"endpoint"`
	Keys     webpush.Keys `json:"keys"`
}

// vapidMu sérialise la génération paresseuse des clés : deux requêtes /api/push/key
// concurrentes sur une base neuve ne doivent pas produire deux paires différentes
// (la seconde écraserait la première, invalidant les abonnements de la première).
var vapidMu sync.Mutex

// vapidKeys renvoie la paire VAPID, la générant et la persistant au premier appel.
// La paire est STABLE pour la vie de l'installation : la changer invaliderait tous
// les abonnements déjà pris (le navigateur signe l'abonnement avec la clé publique).
func vapidKeys() (priv, pub string, err error) {
	vapidMu.Lock()
	defer vapidMu.Unlock()
	priv = getStr(bkState, stPushVAPIDPriv)
	pub = getStr(bkState, stPushVAPIDPub)
	if priv != "" && pub != "" {
		return priv, pub, nil
	}
	priv, pub, err = webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", "", err
	}
	if err = putStr(bkState, stPushVAPIDPriv, priv); err != nil {
		return "", "", err
	}
	if err = putStr(bkState, stPushVAPIDPub, pub); err != nil {
		return "", "", err
	}
	return priv, pub, nil
}

// subsMu sérialise les mutations de la liste d'abonnements (lecture-modif-écriture) :
// une inscription et une purge concurrentes ne doivent pas s'écraser l'une l'autre.
var subsMu sync.Mutex

func loadSubs() []pushSub {
	var subs []pushSub
	getJSON(bkState, stPushSubs, &subs)
	return subs
}

func saveSubs(subs []pushSub) error { return putJSON(bkState, stPushSubs, subs) }

// addSub enregistre un abonnement (idempotent : ré-inscrire le même endpoint met
// simplement à jour ses clés au lieu de le dupliquer — un navigateur ré-abonné
// garde le même endpoint mais peut renouveler ses clés).
func addSub(s pushSub) error {
	if s.Endpoint == "" {
		return nil
	}
	subsMu.Lock()
	defer subsMu.Unlock()
	subs := loadSubs()
	for i, x := range subs {
		if x.Endpoint == s.Endpoint {
			subs[i] = s
			return saveSubs(subs)
		}
	}
	subs = append(subs, s)
	return saveSubs(subs)
}

// removeSub retire un abonnement par son endpoint (désinscription explicite, ou
// purge après un 404/410 « gone » renvoyé par le service de push).
func removeSub(endpoint string) {
	if endpoint == "" {
		return
	}
	subsMu.Lock()
	defer subsMu.Unlock()
	subs := loadSubs()
	out := subs[:0]
	for _, x := range subs {
		if x.Endpoint != endpoint {
			out = append(out, x)
		}
	}
	_ = saveSubs(out)
}

// pushPayload : ce que le service worker reçoit dans l'événement `push`.
type pushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag"`
}

// hasPushSubs : y a-t-il au moins un abonnement ? Évite de sérialiser une charge
// utile pour rien à chaque fin de tour quand personne n'a activé les notifs.
func hasPushSubs() bool { return len(loadSubs()) > 0 }

// sendPushToAll pousse une notification à tous les abonnés. Best-effort : les
// échecs réseau sont ignorés (le service de push réessaiera selon le TTL), mais un
// abonnement rejeté définitivement (404/410) est purgé pour ne pas s'accumuler.
// Appelé depuis la goroutine de génération, à turn_done — jamais bloquant pour l'UI.
func sendPushToAll(title, body string) {
	subs := loadSubs()
	if len(subs) == 0 {
		fmt.Printf("[push] fin de tour : aucun abonné enregistré (rien à envoyer)\n")
		return
	}
	priv, pub, err := vapidKeys()
	if err != nil {
		fmt.Printf("[push] clés VAPID indisponibles : %v\n", err)
		return
	}
	msg, _ := json.Marshal(pushPayload{Title: title, Body: body, Tag: "ajean-turn"})
	opts := &webpush.Options{
		// ⚠️ SANS préfixe « mailto: » : webpush-go l'ajoute lui-même (sauf si la
		// chaîne commence par « https: »). Passer « mailto:… » ici donnait
		// « mailto:mailto:… », un sujet VAPID malformé → Apple répond 403
		// BadJwtToken et rien n'arrive. On donne donc l'email nu.
		Subscriber:      "mail@nathaninline.com",
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		TTL:             120, // périmé après 2 min : une notif « réponse prête » n'a pas de sens tardive
		Urgency:         webpush.UrgencyHigh,
	}
	fmt.Printf("[push] envoi à %d abonné(s)…\n", len(subs))
	for _, s := range subs {
		sub := &webpush.Subscription{Endpoint: s.Endpoint, Keys: s.Keys}
		resp, err := webpush.SendNotification(msg, sub, opts)
		if err != nil {
			fmt.Printf("[push] échec envoi (%s) : %v\n", endpointHost(s.Endpoint), err)
			continue
		}
		// 201 Created = accepté par le service de push. 4xx = problème (VAPID,
		// chiffrement, abonnement mort) — on TRACE le corps de la réponse, qui porte
		// le motif exact d'Apple/Google (ex. « BadJwtToken »).
		if resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			fmt.Printf("[push] refus %d (%s) : %s\n", resp.StatusCode, endpointHost(s.Endpoint), strings.TrimSpace(string(body)))
		} else {
			fmt.Printf("[push] accepté %d (%s)\n", resp.StatusCode, endpointHost(s.Endpoint))
		}
		// 404/410 = abonnement expiré côté service de push : on le retire.
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			removeSub(s.Endpoint)
		}
		resp.Body.Close()
	}
}

// endpointHost : hôte de l'endpoint pour les logs (miroir de pushEndpointHost,
// gardé dans ce fichier pour éviter un couplage de compilation entre les deux).
func endpointHost(endpoint string) string { return pushEndpointHost(endpoint) }
