// web_chat.go — endpoints de chat du serveur web local : envoi/stop/reset,
// flux SSE d'abonnement à la conversation serveur (voir chat_conversation.go).
package ajean

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
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
	if body.Computer != nil {
		caps.ComputerUse = caps.ComputerUse && *body.Computer
	}
	// Mode rapide : même moteur que « ajean chat ». Il ne fait que retirer
	// (mémoire, web, computer use), donc reste une restriction.
	if body.Fast {
		caps.Terminal, caps.Web = true, true
		caps.Mem = MemOff
		caps.Internet = false
		caps.ComputerUse = false
	}
	// Modèle de base : tout coupé, y compris la mémoire (qui, seule, suffisait à
	// réinjecter le préambule « Jean + mémoire » et l'index des pages).
	if body.Raw {
		caps = Caps{Mem: MemOff}
	}
	// Mode Jean : pas de projet ni de mémoire de projet, sa propre mémoire à la place.
	if body.Mode == "jean" {
		caps.Mem = MemOff
		caps.Jean = true
	}
	// Les outils dépendent du mode agent : agent coupé, tout est coupé.
	if !caps.Agent {
		caps.Internet = false
		caps.ComputerUse = false
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
	exited := make(chan struct{})
	go func() {
		defer close(exited)
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
				// Arrêt demandé pendant l'attente du verrou : la réponse HTTP est
				// peut-être déjà rendue, on n'y écrit plus.
				select {
				case <-done:
					mu.Unlock()
					return
				default:
				}
				_, err := w.Write([]byte(": ping\n\n"))
				if err == nil && flusher != nil {
					flusher.Flush()
				}
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()
	// L'arrêt ATTEND la fin de la goroutine (issue #105) : sinon un battement en
	// attente du verrou écrivait après le retour du handler, et l'écriture sur
	// une réponse terminée faisait tomber le process de l'interface.
	var once sync.Once
	return mu, func() {
		once.Do(func() { close(done) })
		<-exited
	}
}

// runChatStream est désormais un pur ABONNÉ au journal de la conversation serveur :
// il rejoue Log[body.From:] puis suit le direct, jusqu'à ce que la connexion (ctx)
// se ferme. La GÉNÉRATION est lancée séparément par /api/chat/send dans une
// goroutine détachée — fermer le navigateur n'arrête donc plus rien. Partagé par
// handleChat (clair) et handleE2EChat (chiffré).
func runChatStream(ctx context.Context, body chatReq, emit func(map[string]any) bool) {
	tail := -1
	if body.Tail != nil {
		tail = *body.Tail
	}
	conv.SubscribeTail(ctx, body.From, body.ConvID, tail, emit)
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
	// Génération en cours : au lieu de refuser (409), on MET EN FILE (issue #74). Le
	// message sera injecté dans la réponse en cours à la prochaine frontière d'étape,
	// ou traité comme tour suivant si le tour se termine avant. queued=true le signale
	// au client (qui affiche une bulle « en attente »).
	rememberUserTZ(body.TZ)
	// Bonne conversation d'abord (plusieurs appareils : voir alignConvForMode).
	if err := conv.alignConvForMode(requestedMode(body)); err != nil {
		sendJSON(w, 409, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Mode : celui de la conversation s'il est déjà fixé, sinon celui demandé.
	mode, fresh := conv.lockMode(requestedMode(body))
	applyChatMode(&body, mode)
	if mode == "jean" {
		files = moveUploadsToJean(files)
	}
	queued, err := conv.EnqueueOrStart(body.Message, files, capsFromBody(body), body.Temperature)
	if err != nil {
		// Message refusé (modèle pas prêt) : il n'a rien démarré, le mode ne doit
		// pas rester figé sur une conversation encore vide.
		if fresh {
			conv.unlockMode(mode)
		}
		// 503 = modèle pas prêt (ErrBusy ne remonte plus : on met en file à la place).
		sendJSON(w, 503, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "queued": queued})
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
	q := r.URL.Query()
	var list []convArchiveMeta
	if p := strings.TrimSpace(q.Get("project")); p != "" {
		list = listArchivesForProject(p)
	} else if q.Get("all") == "1" {
		// Historique général (menu latéral) : toutes les conversations, tous projets.
		list = listAllArchives()
	} else {
		list = historyList(q.Get("scope"))
	}
	// Recherche (?q=) : titre et nom du projet (sous-chaîne, sans casse ni
	// accents) ET texte des conversations (index plein-texte, #98). Pertinence
	// d'abord (titre = 2, chaque terme trouvé dans le texte = 1), puis l'ordre
	// de la liste (favoris, date). Faite avant la pagination.
	state, terms := "ok", []string{}
	if needle := foldSearch(q.Get("q")); needle != "" {
		var body map[string]int
		body, terms, state = histBodyScores(q.Get("q"))
		names := map[string]string{}
		for _, p := range listProjects() {
			names[p.Slug] = p.Name
		}
		score := map[string]int{}
		kept := list[:0:0]
		for _, m := range list {
			sc := body[m.ID]
			if strings.Contains(foldSearch(m.Title+" "+names[archiveProject(m)]), needle) {
				sc += 2
			}
			if sc > 0 {
				score[m.ID] = sc
				kept = append(kept, m)
			}
		}
		sort.SliceStable(kept, func(i, j int) bool { return score[kept[i].ID] > score[kept[j].ID] })
		list = kept
	}
	// Pagination optionnelle (?offset=&limit=) : la liste des sessions grandit au
	// défilement au lieu de tout rendre d'un coup (537 sessions sur une machine de test). Sans
	// limit, tout est renvoyé comme avant (compat des autres appelants). Les favoris
	// étant triés en tête, ils sont toujours dans la première page.
	total := len(list)
	if limit, _ := strconv.Atoi(q.Get("limit")); limit > 0 {
		off, _ := strconv.Atoi(q.Get("offset"))
		off = max(0, min(off, total))
		list = list[off:min(off+limit, total)]
	}
	// Noms des projets (slug → nom) : l'historique général affiche le projet de
	// chaque conversation.
	names := map[string]string{}
	for _, p := range listProjects() {
		names[p.Slug] = p.Name
	}
	if list == nil {
		list = []convArchiveMeta{}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "conversations": list, "total": total,
		"state": state, "terms": terms, "indexed": 0, "indexed_total": 0,
		"active": conv.currentID(), "generating": conv.isGenerating(),
		"projects": names, "default_project": defaultProjectSlug})
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
	syncActiveArchive(id)
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
	// Conversation d'un autre projet (historique général) : on bascule d'abord sur
	// son projet, pour que mémoire et contexte suivent la conversation ouverte.
	// Sauf une conversation rapide ou de modèle de base : elle n'a pas de projet.
	if a, ok := loadArchive(body.ID); ok && !quickMode(a.Mode) {
		if p := archiveProject(convArchiveMeta{Project: a.Project}); p != activeProjectSlug() && projectExists(p) {
			if err := setActiveProject(p); err != nil {
				sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		}
	}
	if err := conv.OpenSession(body.ID); err != nil {
		sendJSON(w, 404, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "project": activeProjectSlug()})
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
	syncActiveArchive(body.ID)
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
	syncActiveArchive(body.ID)
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
	var body struct{ Scope string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	n := deleteNonFavIn(historyList(body.Scope), conv.currentID())
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

// handleToolResult renvoie le résultat COMPLET d'un appel d'outil, chargé à la
// demande par le bouton « voir plus » de la bulle (le flux ne transporte qu'un
// aperçu). Le résultat complet vit dans la vue « modèle » (conv.Messages), repéré
// par son tool_call_id. Absent (compacté / archivé) → 404, l'UI garde l'aperçu.
func handleToolResult(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		sendJSON(w, 400, map[string]any{"error": "id manquant"})
		return
	}
	// Stockage dédié d'abord (tool_results.go) ; conv.Messages ne sert plus
	// qu'aux anciens résultats, dont l'id était le tool_call_id.
	if s, ok := loadToolResult(id); ok {
		sendJSON(w, 200, map[string]any{"result": s})
		return
	}
	// Référence vers un message tool d'une conversation ARCHIVÉE (lecture seule,
	// ou journal allégé par slimArchives) : le client passe l'id de la session.
	if sid := strings.TrimSpace(r.URL.Query().Get("sid")); sid != "" && sid != conv.currentID() {
		if a, ok := loadArchive(sid); ok {
			for _, m := range a.Messages {
				if s, isStr := m.Content.(string); isStr && m.Role == "tool" && m.ToolCallID == id {
					sendJSON(w, 200, map[string]any{"result": s})
					return
				}
			}
		}
	}
	conv.mu.Lock()
	var found string
	ok := false
	for _, m := range conv.Messages {
		if m.Role == "tool" && m.ToolCallID == id {
			if s, isStr := m.Content.(string); isStr {
				found = s
				ok = true
			}
			break
		}
	}
	conv.mu.Unlock()
	if !ok {
		sendJSON(w, 404, map[string]any{"error": "résultat non disponible"})
		return
	}
	sendJSON(w, 200, map[string]any{"result": found})
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

// foldSearch normalise un texte pour la recherche : minuscules, accents retirés
// (é → e), espaces de bord ôtés.
func foldSearch(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if f, ok := accentFold[r]; ok {
			r = f
		}
		b.WriteRune(r)
	}
	return b.String()
}

var accentFold = map[rune]rune{
	'à': 'a', 'â': 'a', 'ä': 'a', 'á': 'a', 'ã': 'a', 'ç': 'c',
	'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e', 'î': 'i', 'ï': 'i', 'í': 'i',
	'ô': 'o', 'ö': 'o', 'ó': 'o', 'õ': 'o', 'ù': 'u', 'û': 'u', 'ü': 'u', 'ú': 'u', 'ÿ': 'y', 'ñ': 'n',
}

// requestedMode : mode demandé par le client (champ mode, ou anciens drapeaux).
func requestedMode(b chatReq) string {
	switch {
	case b.Mode == "fast" || b.Mode == "project" || b.Mode == "base" || b.Mode == "jean":
		return b.Mode
	case b.Raw:
		return "base"
	case b.Fast:
		return "fast"
	}
	return "project"
}

// applyChatMode aligne les drapeaux de la requête sur le mode effectif.
func applyChatMode(b *chatReq, mode string) {
	b.Mode = mode
	b.Fast = mode == "fast"
	b.Raw = mode == "base"
}
