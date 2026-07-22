package jean

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
