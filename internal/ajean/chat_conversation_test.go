package ajean

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestConv crée une conversation isolée (le global `conv` sert au process réel).
func newTestConv() *Conversation {
	c := &Conversation{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// Subscribe doit rejouer les événements déjà journalisés PUIS suivre le direct,
// et se terminer quand le contexte (la connexion) est annulé.
func TestSubscribeReplayAndLive(t *testing.T) {
	c := newTestConv()
	c.appendDelta(c.epoch, map[string]any{"user": "salut"})
	c.appendDelta(c.epoch, map[string]any{"content": "bon"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan int, 32)
	go c.Subscribe(ctx, 0, func(m map[string]any) bool {
		if s, ok := m["seq"].(int); ok {
			got <- s
		}
		return true
	})

	// Les 2 événements déjà présents (replay).
	waitSeq(t, got, 1)
	waitSeq(t, got, 2)
	// Un événement en direct après abonnement.
	c.appendDelta(c.epoch, map[string]any{"content": "jour"})
	waitSeq(t, got, 3)
}

// Un abonné qui démarre à from=N ne reçoit que ce qui est plus récent que N.
func TestSubscribeFromOffset(t *testing.T) {
	c := newTestConv()
	c.appendDelta(c.epoch, map[string]any{"user": "a"})
	c.appendDelta(c.epoch, map[string]any{"user": "b"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan int, 8)
	go c.Subscribe(ctx, 1, func(m map[string]any) bool {
		if s, ok := m["seq"].(int); ok {
			got <- s
		}
		return true
	})
	// from=1 → on saute le seq 1, on reçoit 2 en premier.
	waitSeq(t, got, 2)
}

// Reset bump l'epoch et pousse un {reset:true} aux abonnés, qui repartent de 0.
func TestResetNotifiesSubscribers(t *testing.T) {
	c := newTestConv()
	c.appendDelta(c.epoch, map[string]any{"user": "x"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resetSeen := make(chan bool, 4)
	go c.Subscribe(ctx, 0, func(m map[string]any) bool {
		if _, ok := m["reset"]; ok {
			resetSeen <- true
		}
		return true
	})
	// laisse l'abonné consommer le replay initial
	time.Sleep(20 * time.Millisecond)
	c.Reset()
	select {
	case <-resetSeen:
	case <-time.After(time.Second):
		t.Fatal("l'abonné n'a pas reçu l'événement reset")
	}
	if c.Seq != 0 || len(c.Log) != 0 {
		t.Fatalf("après Reset: Seq=%d len(Log)=%d, attendu 0/0", c.Seq, len(c.Log))
	}
}

// Un Reset survenu pendant un tour invalide l'epoch : les deltas du tour en
// cours sont jetés au lieu de polluer la nouvelle conversation (Seq repartis
// de zéro, messages fantômes).
func TestAppendDeltaStaleEpochDropped(t *testing.T) {
	c := newTestConv()
	epoch := c.epoch // capturé comme au début d'un tour
	c.appendDelta(epoch, map[string]any{"user": "avant"})
	c.Reset()
	c.appendDelta(epoch, map[string]any{"content": "fantôme"}) // tour périmé
	if c.Seq != 0 || len(c.Log) != 0 {
		t.Fatalf("delta périmé accepté après Reset: Seq=%d len(Log)=%d", c.Seq, len(c.Log))
	}
	c.appendDelta(c.epoch, map[string]any{"user": "nouveau"}) // nouveau tour
	if c.Seq != 1 || len(c.Log) != 1 {
		t.Fatalf("delta du nouvel epoch refusé: Seq=%d len(Log)=%d", c.Seq, len(c.Log))
	}
}

// Les handlers de contrôle répondent en JSON sans dépendre du modèle.
func TestChatControlHandlers(t *testing.T) {
	// reset → conversation vide
	rr := httptest.NewRecorder()
	handleChatReset(rr, httptest.NewRequest("POST", "/api/chat/reset", nil))
	if rr.Code != 200 {
		t.Fatalf("reset code %d", rr.Code)
	}
	// state → seq 0, pas de génération
	rr = httptest.NewRecorder()
	handleChatState(rr, httptest.NewRequest("GET", "/api/chat/state", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "\"seq\":0") {
		t.Fatalf("state inattendu: %d %s", rr.Code, rr.Body.String())
	}
	// send message vide → 400
	rr = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/chat/send", strings.NewReader(`{"message":""}`))
	req.Header.Set("Content-Type", "application/json")
	handleChatSend(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("send vide devrait être 400, obtenu %d", rr.Code)
	}
}

func waitSeq(t *testing.T, ch <-chan int, want int) {
	t.Helper()
	select {
	case s := <-ch:
		if s != want {
			t.Fatalf("seq reçu %d, attendu %d", s, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout en attendant seq %d", want)
	}
}

// Régression : un client qui a déjà affiché une PARTIE d'un bloc de texte et qui se
// reconnecte APRÈS la compaction de fin de tour recevait le bloc entier sans savoir
// qu'il en avait déjà le début → la réponse s'affichait en double (1re copie
// tronquée à l'endroit exact où le client en était). Le bloc coalescé doit porter
// `seq0` et, quand `from` tombe dedans, l'indicateur `replace`.
func TestReplayMarksReplaceWhenClientSawPartOfBlock(t *testing.T) {
	c := newTestConv()
	c.appendDelta(c.epoch, map[string]any{"user": "salut"})    // seq 1
	c.appendDelta(c.epoch, map[string]any{"content": "Oui, "}) // seq 2
	c.appendDelta(c.epoch, map[string]any{"content": "j'ai "}) // seq 3
	c.appendDelta(c.epoch, map[string]any{"content": "accès"}) // seq 4
	c.mu.Lock()
	c.compactLogLocked() // fin de tour : les 3 deltas deviennent UN événement seq 4
	c.mu.Unlock()

	// Le client avait vu jusqu'au seq 3 (milieu du bloc) : il doit recevoir le bloc
	// entier AVEC replace, pour remplacer sa bulle au lieu d'y concaténer.
	out := coalesceReplay(c.Log, 3)
	if len(out) != 1 {
		t.Fatalf("attendu 1 événement rejoué, obtenu %d (%v)", len(out), out)
	}
	if got := out[0]["content"]; got != "Oui, j'ai accès" {
		t.Fatalf("texte rejoué = %q, attendu le bloc entier", got)
	}
	if r, _ := out[0]["replace"].(bool); !r {
		t.Fatalf("replace absent alors que le client avait déjà affiché le début du bloc")
	}

	// Client qui n'a rien vu du bloc : pas de replace (il doit juste l'ajouter).
	out = coalesceReplay(c.Log, 1)
	if len(out) != 1 {
		t.Fatalf("attendu 1 événement, obtenu %d", len(out))
	}
	if _, ok := out[0]["replace"]; ok {
		t.Fatalf("replace ne doit PAS être posé quand le client n'a rien vu du bloc")
	}
}

// Le direct sélectionne les événements neufs par dichotomie (c.Log est trié par
// Seq). Deux cas limites doivent rester justes : un journal TRONQUÉ, dont le
// premier Seq est très supérieur au `from` du client, et un client déjà à jour.
func TestLiveSelectionSurJournalTronque(t *testing.T) {
	c := newTestConv()
	// Journal amputé de son début, comme après la troncature à maxLogEvents.
	c.Log = []LogEvent{
		{Seq: 500, Delta: map[string]any{"user": "a"}},
		{Seq: 501, Delta: map[string]any{"user": "b"}},
		{Seq: 502, Delta: map[string]any{"user": "c"}},
	}
	c.Seq = 502

	for _, tc := range []struct {
		from  int
		first int // premier seq attendu
	}{
		{from: 0, first: 500},   // client neuf : il reçoit tout ce qui reste
		{from: 500, first: 501}, // au milieu du journal
		{from: 501, first: 502},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		got := make(chan int, 8)
		go c.Subscribe(ctx, tc.from, func(m map[string]any) bool {
			if s, ok := m["seq"].(int); ok {
				got <- s
			}
			return true
		})
		waitSeq(t, got, tc.first)
		cancel()
	}

	// Client déjà à jour : rien à rejouer, mais le direct doit suivre.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan int, 8)
	go c.Subscribe(ctx, 502, func(m map[string]any) bool {
		if s, ok := m["seq"].(int); ok {
			got <- s
		}
		return true
	})
	c.appendDelta(c.epoch, map[string]any{"content": "suite"})
	waitSeq(t, got, 503)
}

// --- Régressions v0.12.7 : journal d'affichage (outils, replay, changement de session) ---

// compactLogLocked doit coalescer les événements tool_used d'un même appel en un
// seul (le done=true, qui porte l'état final) : les intermédiaires (annonce, frappe
// du corps) ne servent qu'à l'affichage en direct et faisaient enfler le journal
// jusqu'à la troncature des premiers messages.
func TestCompactLogCoalesceTools(t *testing.T) {
	c := newTestConv()
	c.Log = []LogEvent{
		{Seq: 1, Delta: map[string]any{"user": "édite"}},
		{Seq: 2, Delta: map[string]any{"tool_used": map[string]any{"name": "edit", "label": "f"}}},
		{Seq: 3, Delta: map[string]any{"tool_used": map[string]any{"name": "edit", "label": "f", "typing": true, "body": "a"}}},
		{Seq: 4, Delta: map[string]any{"tool_used": map[string]any{"name": "edit", "label": "f", "result": "ok", "done": true}}},
		{Seq: 5, Delta: map[string]any{"content": "fini"}},
	}
	c.mu.Lock()
	c.compactLogLocked()
	c.mu.Unlock()
	nTool := 0
	for _, ev := range c.Log {
		if tu, ok := ev.Delta["tool_used"].(map[string]any); ok {
			nTool++
			if done, _ := tu["done"].(bool); !done {
				t.Error("un tool_used non terminé a survécu à la compaction")
			}
		}
	}
	if nTool != 1 {
		t.Fatalf("compactLogLocked garde %d tool_used au lieu de 1", nTool)
	}
}

// coalesceReplay ne doit émettre un outil qu'UNE fois même si un événement non-outil
// (stats, un bout de raisonnement) tombe entre l'annonce (done=false) et le done=true.
func TestCoalesceReplayNoDoubleTool(t *testing.T) {
	events := []LogEvent{
		{Seq: 1, Delta: map[string]any{"tool_used": map[string]any{"name": "bash", "label": "ls"}}},
		{Seq: 2, Delta: map[string]any{"stats": map[string]any{"gen_tokens": 3}}},
		{Seq: 3, Delta: map[string]any{"tool_used": map[string]any{"name": "bash", "label": "ls", "result": "x", "done": true}}},
	}
	n := 0
	for _, ev := range coalesceReplay(events, 0) {
		if _, ok := ev["tool_used"]; ok {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("outil émis %d fois au replay (attendu 1)", n)
	}
}

// Un abonné qui arrive avec un `from` SUPÉRIEUR au dernier Seq du journal (curseur
// hérité d'une autre session, plus récente) doit obtenir le fil COMPLET, pas rien :
// sans ce garde-fou, ouvrir une session plus ancienne la montrait tronquée/vide.
func TestSubscribeStaleFromCrossSession(t *testing.T) {
	c := newTestConv()
	c.appendDelta(c.epoch, map[string]any{"user": "premier message"})
	c.appendDelta(c.epoch, map[string]any{"content": "réponse"})
	// Le journal va jusqu'à Seq 2 ; on s'abonne avec from=9999 (curseur d'une autre session).
	ctx, cancel := context.WithCancel(context.Background())
	var got []map[string]any
	done := make(chan struct{})
	go func() {
		c.Subscribe(ctx, 9999, func(ev map[string]any) bool {
			got = append(got, ev)
			if ev["caught_up"] != nil {
				cancel()
			}
			return true
		})
		close(done)
	}()
	<-done
	seen := ""
	for _, ev := range got {
		if u, ok := ev["user"].(string); ok {
			seen += u
		}
	}
	if !strings.Contains(seen, "premier message") {
		t.Fatalf("le premier message n'a pas été rejoué avec un from périmé (reçu: %q)", seen)
	}
}
