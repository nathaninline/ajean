// web_chat.go — endpoints de chat du serveur web local : envoi/stop/reset,
// flux SSE d'abonnement à la conversation serveur (voir chat_conversation.go).
package jean

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

func capsFromBody(body chatReq) Caps {
	caps := globalCaps()
	if body.Agent != nil {
		caps.Agent = *body.Agent
	} else if body.Tools != nil || body.Skills != nil {
		caps.Agent = (body.Tools != nil && *body.Tools) || (body.Skills != nil && *body.Skills)
	}
	if body.Internet != nil {
		caps.Internet = *body.Internet && crawlReachable()
	}
	return caps
}

// sseHeartbeat garde la réponse SSE active en écrivant un commentaire (`: ping`,
// ignoré par le parseur côté navigateur, aucun contenu donc rien à chiffrer)
// toutes les ~15 s. Sans ça, un long silence (exécution d'outil en mode agent,
// gros prefill) laisse la réponse inactive et un proxy intermédiaire (Cloudflare,
// ~100 s) la coupe → le fetch navigateur échoue (« Load failed »). Retourne un
// mutex à partager avec l'émetteur (writes concurrents sur le même w) et une
// fonction d'arrêt à différer.
func sseHeartbeat(w http.ResponseWriter, flusher http.Flusher) (*sync.Mutex, func()) {
	mu := &sync.Mutex{}
	done := make(chan struct{})
	go func() {
		// 4 s (et non 15) : borne le temps qu'un dernier bout de flux peut rester
		// coincé dans un buffer proxy (Cloudflare) faute d'octets pour le pousser.
		t := time.NewTicker(4 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				mu.Lock()
				_, err := w.Write([]byte(": ping\n\n"))
				if flusher != nil {
					flusher.Flush()
				}
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()
	return mu, func() { close(done) }
}

// runChatStream est désormais un pur ABONNÉ au journal de la conversation serveur :
// il rejoue Log[body.From:] puis suit le direct, jusqu'à ce que la connexion (ctx)
// se ferme. La GÉNÉRATION est lancée séparément par /api/chat/send dans une
// goroutine détachée — fermer le navigateur n'arrête donc plus rien. Partagé par
// handleChat (clair) et handleE2EChat (chiffré).
func runChatStream(ctx context.Context, body chatReq, emit func(map[string]any) bool) {
	conv.Subscribe(ctx, body.From, emit)
}

// handleChatSend ajoute un message et lance la génération en arrière-plan. Réponse
// req/resp (les événements arrivent par le flux d'abonnement). Passe par le proxy
// tunnel /api/e2e/req pour app.ajean.link — aucun code E2E spécifique requis.
func handleChatSend(w http.ResponseWriter, r *http.Request) {
	var body chatReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if strings.TrimSpace(body.Message) == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "message vide"})
		return
	}
	if err := conv.StartTurn(body.Message, capsFromBody(body), body.Temperature); err != nil {
		// 409 = occupé (génération en cours) ; 503 = modèle pas prêt.
		code := 503
		if err == ErrBusy {
			code = 409
		}
		sendJSON(w, code, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

func handleChatStop(w http.ResponseWriter, r *http.Request) {
	conv.Stop()
	sendJSON(w, 200, map[string]any{"ok": true})
}

func handleChatReset(w http.ResponseWriter, r *http.Request) {
	conv.Reset()
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleChatCompact lance une compaction manuelle du contexte (bouton UI). La
// progression est diffusée via le flux d'abonnement (compacting/compacted).
func handleChatCompact(w http.ResponseWriter, r *http.Request) {
	if err := conv.CompactNow(); err != nil {
		code := 503
		if err == ErrBusy {
			code = 409
		}
		sendJSON(w, code, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

func handleChatState(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, conv.state())
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	var body chatReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	mu, stop := sseHeartbeat(w, flusher)
	defer stop()
	emit := func(obj map[string]any) bool {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": obj}}})
		mu.Lock()
		defer mu.Unlock()
		if _, err := w.Write([]byte("data: " + string(b) + "\n\n")); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	runChatStream(r.Context(), body, emit)
}
