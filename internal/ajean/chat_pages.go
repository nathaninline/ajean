package ajean

// chat_pages.go — conversation SANS FIN (mode Jean) : le journal d'affichage ne
// grossit pas indéfiniment.
//
// Le journal (Log) d'une conversation est réécrit en entier à chaque sauvegarde et
// recopié à chaque abonnement. Pour une conversation qui ne se termine jamais, on
// le borne : au-delà de pageKeepTurns échanges, la partie ancienne part dans des
// PAGES (bucket bkChatPages, clé « <conv>/<premier seq>/<échanges> »), écrites une seule fois
// puis plus jamais relues sauf quand l'utilisateur remonte le fil. Le fil vivant
// reste petit : sauvegarde, chargement et rejeu gardent la même vitesse au bout
// d'un an qu'au premier jour.
//
// Côté interface, remonter le fil charge les échanges précédents par lots
// (handleChatOlder), d'abord depuis le journal vivant, puis depuis les pages.

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"
)

const (
	bkChatPages   = "chatpages"
	pageKeepTurns = 120 // échanges gardés dans le journal vivant
	pageMinTurns  = 40  // on ne découpe que par lots d'au moins autant d'échanges
	olderMaxTurns = 50  // plafond d'un lot demandé par l'interface
)

// paged : conversations dont le journal est découpé en pages (celles sans fin).
func paged(convID string) bool { return convID == jeanConvID }

// pageKey : le premier seq (sur 10 chiffres, pour que l'ordre des clés soit
// chronologique) et le nombre d'échanges, lisible sans ouvrir la page.
func pageKey(convID string, firstSeq, turns int) string {
	return fmt.Sprintf("%s/%010d/%d", convID, firstSeq, turns)
}

// pageTurns lit le nombre d'échanges inscrit dans une clé de page.
func pageTurns(key string) int {
	n, _ := strconv.Atoi(key[strings.LastIndexByte(key, '/')+1:])
	return n
}

// userTurnStarts renvoie les index des événements qui ouvrent un échange.
func userTurnStarts(log []LogEvent) []int {
	var out []int
	for i, ev := range log {
		if _, ok := ev.Delta["user"]; ok {
			out = append(out, i)
		}
	}
	return out
}

// pageOutLocked déplace la partie ancienne du journal vers une page quand il dépasse
// pageKeepTurns + pageMinTurns échanges (on découpe par lots, pas à chaque tour).
// Verrou de la conversation détenu par l'appelant. Sans effet si la page ne peut
// pas être écrite (mémoire chiffrée verrouillée) : le journal reste entier.
func (c *Conversation) pageOutLocked() {
	if !paged(c.ID) {
		return
	}
	starts := userTurnStarts(c.Log)
	if len(starts) <= pageKeepTurns+pageMinTurns {
		return
	}
	cut := starts[len(starts)-pageKeepTurns]
	old := c.Log[:cut]
	if len(old) == 0 {
		return
	}
	if err := putStoreJSON(bkChatPages, pageKey(c.ID, old[0].Seq, len(userTurnStarts(old))), old); err != nil {
		return
	}
	c.Log = append([]LogEvent(nil), c.Log[cut:]...)
}

// pageKeys : clés des pages d'une conversation, de la plus ancienne à la plus récente.
func pageKeys(convID string) []string {
	var keys []string
	prefix := []byte(convID + "/")
	_ = view(bkChatPages, func(b *bolt.Bucket) error {
		cur := b.Cursor()
		for k, _ := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cur.Next() {
			keys = append(keys, string(k))
		}
		return nil
	})
	return keys
}

// deletePages supprime toutes les pages d'une conversation (suppression de celle-ci).
func deletePages(convID string) {
	for _, k := range pageKeys(convID) {
		_ = putBytes(bkChatPages, k, nil)
	}
}

// olderEvents renvoie les `turns` derniers échanges strictement avant le seq
// `before`, et le nombre d'échanges encore plus anciens. Lit le journal vivant,
// puis les pages, de la plus récente à la plus ancienne, jusqu'à en avoir assez.
func (c *Conversation) olderEvents(convID string, before, turns int) ([]LogEvent, int, error) {
	c.mu.Lock()
	if convID != "" && convID != c.ID {
		c.mu.Unlock()
		return nil, 0, fmt.Errorf("conversation changée")
	}
	id := c.ID
	var evs []LogEvent
	for _, ev := range c.Log {
		if ev.Seq < before {
			evs = append(evs, ev)
		}
	}
	c.mu.Unlock()

	keys := pageKeys(id)
	for i := len(keys) - 1; i >= 0 && len(userTurnStarts(evs)) <= turns; i-- {
		var page []LogEvent
		if !getStoreJSON(bkChatPages, keys[i], &page) {
			return nil, 0, fmt.Errorf("page illisible (mémoire verrouillée ?)")
		}
		kept := page[:0]
		for _, ev := range page {
			if ev.Seq < before {
				kept = append(kept, ev)
			}
		}
		evs = append(kept, evs...)
		keys = keys[:i]
	}
	starts := userTurnStarts(evs)
	more := 0
	if len(starts) > turns {
		cut := starts[len(starts)-turns]
		more = len(starts) - turns
		evs = evs[cut:]
	}
	// Échanges des pages pas encore lues : comptés d'après leur clé.
	more += pagedTurns(keys)
	return evs, more, nil
}

// pagedTurns : nombre total d'échanges rangés dans ces pages.
func pagedTurns(keys []string) int {
	n := 0
	for _, k := range keys {
		n += pageTurns(k)
	}
	return n
}

// handleChatOlder (GET ?before=<seq>&turns=<n>&conv=<id>) : un lot d'échanges plus
// anciens de la conversation active, prêts à rejouer (même format que le flux).
func handleChatOlder(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	before, _ := strconv.Atoi(q.Get("before"))
	turns, _ := strconv.Atoi(q.Get("turns"))
	if turns <= 0 || turns > olderMaxTurns {
		turns = 20
	}
	evs, more, err := conv.olderEvents(strings.TrimSpace(q.Get("conv")), before, turns)
	if err != nil {
		sendJSON(w, 409, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	first := 0
	if len(evs) > 0 {
		first = evs[0].Seq
	}
	sendJSON(w, 200, map[string]any{"ok": true, "events": coalesceReplay(evs, 0), "more": more, "before": first})
}

// fullLog : le journal COMPLET (pages + journal vivant), pour l'export. Le reste du
// code ne lit que le journal vivant, borné.
func (c *Conversation) fullLog() []LogEvent {
	c.mu.Lock()
	id := c.ID
	live := append([]LogEvent(nil), c.Log...)
	c.mu.Unlock()
	if !paged(id) {
		return live
	}
	var all []LogEvent
	for _, k := range pageKeys(id) {
		var page []LogEvent
		if getStoreJSON(bkChatPages, k, &page) {
			all = append(all, page...)
		}
	}
	return append(all, live...)
}
