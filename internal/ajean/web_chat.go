// web_chat.go — endpoints de chat du serveur web local : envoi/stop/reset,
// flux SSE d'abonnement à la conversation serveur (voir chat_conversation.go).
package ajean

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// capsFromBody dérive les capacités d'un tour à partir de la configuration
// machine et des surcharges portées par la requête.
//
// ⚠️ Une surcharge ne peut que RESTREINDRE. Elle pouvait auparavant rallumer le
// mode agent : un simple {"agent":true} redonnait bash, write, edit et les
// outils MCP alors que l'interrupteur de la machine était sur OFF. Comme l'API
// n'est pas protégée par défaut et écoute sur 0.0.0.0, l'interrupteur ne
// garantissait donc rien. Il redevient une vraie fermeture : ce qui est éteint
// sur la machine ne peut pas être rallumé par un client.
func capsFromBody(body chatReq) Caps {
	caps := globalCaps()
	switch {
	case body.Agent != nil:
		caps.Agent = caps.Agent && *body.Agent
	case body.Tools != nil || body.Skills != nil:
		want := (body.Tools != nil && *body.Tools) || (body.Skills != nil && *body.Skills)
		caps.Agent = caps.Agent && want
	}
	if body.Internet != nil {
		caps.Internet = caps.Internet && *body.Internet
	}
	// Les outils dépendent du mode agent : agent coupé, tout est coupé.
	if !caps.Agent {
		caps.Internet = false
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
	// Un envoi SANS texte mais AVEC pièce jointe est légitime (« tiens, regarde »).
	files := attachFiles(body.Files)
	if strings.TrimSpace(body.Message) == "" && len(files) == 0 {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "message vide"})
		return
	}
	if err := conv.StartTurn(body.Message, files, capsFromBody(body), body.Temperature); err != nil {
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

// handleChatReset : « clear chat ». La conversation courante n'est pas jetée mais
// ARCHIVÉE dans l'historique (récupérable dans le modal Historique), puis une
// conversation vierge démarre.
func handleChatReset(w http.ResponseWriter, r *http.Request) {
	id := conv.NewSession()
	sendJSON(w, 200, map[string]any{"ok": true, "active": id})
}

// handleChatHistory (GET) : liste des sessions + id de la session active (pour que
// l'UI marque « en cours ») + drapeau `generating`. Avec ?project=<slug>, liste EN
// LECTURE SEULE les sessions d'un AUTRE projet sans basculer le projet actif : c'est
// ce qui permet de parcourir un autre projet pendant qu'une génération tourne.
func handleChatHistory(w http.ResponseWriter, r *http.Request) {
	if p := strings.TrimSpace(r.URL.Query().Get("project")); p != "" {
		sendJSON(w, 200, map[string]any{"ok": true, "conversations": listArchivesForProject(p),
			"active": conv.currentID(), "generating": conv.isGenerating()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "conversations": listArchives(),
		"active": conv.currentID(), "generating": conv.isGenerating()})
}

// handleChatPeek (GET ?id=) : renvoie le contenu d'une conversation archivée EN
// LECTURE SEULE (titre + projet + journal rejouable), sans la restaurer ni toucher
// la conversation vive — pour la lire pendant qu'une génération tourne ailleurs.
// Le client rejoue ce `log` avec le même pipeline que le fil normal (rendu natif).
func handleChatPeek(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "id manquant"})
		return
	}
	a, ok := loadArchive(id)
	if !ok {
		sendJSON(w, 404, map[string]any{"ok": false, "error": "conversation introuvable"})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "id": a.ID, "title": a.Title,
		"project": a.Project, "log": a.Log})
}

// handleChatHistoryRestore (POST {id}) : ouvre une session comme conversation
// active (la courante est d'abord sauvegardée dans SA session).
func handleChatHistoryRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.ID) == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "id manquant"})
		return
	}
	if err := conv.OpenSession(body.ID); err != nil {
		sendJSON(w, 404, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleChatHistoryDelete (POST {id}) : supprime DÉFINITIVEMENT une conversation
// archivée.
func handleChatHistoryDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.ID) == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "id manquant"})
		return
	}
	if err := conv.DeleteSession(body.ID); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleChatHistoryRename (POST {id, title}) : renomme une conversation
// archivée. Titre vide = re-dérive le titre automatique.
func handleChatHistoryRename(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.ID) == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "id manquant"})
		return
	}
	if err := renameArchive(body.ID, body.Title); err != nil {
		sendJSON(w, 404, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Si c'est la session active, garder son nom en phase (sinon le prochain
	// upsertSession réécraserait l'archive avec l'ancien nom).
	conv.setActiveTitleIfMatch(body.ID, body.Title)
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleChatHistoryFav (POST {id, fav}) : épingle/dépingle une conversation
// archivée en favori.
func handleChatHistoryFav(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID  string `json:"id"`
		Fav bool   `json:"fav"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.ID) == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "id manquant"})
		return
	}
	if err := setArchiveFav(body.ID, body.Fav); err != nil {
		sendJSON(w, 404, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	conv.setActiveFavIfMatch(body.ID, body.Fav)
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleChatHistoryClear (POST) : supprime toutes les conversations archivées
// SAUF les favoris.
func handleChatHistoryClear(w http.ResponseWriter, r *http.Request) {
	n := deleteNonFavArchives(conv.currentID())
	sendJSON(w, 200, map[string]any{"ok": true, "deleted": n})
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
