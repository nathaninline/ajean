package ajean

import (
	"fmt"
	"testing"
)

// Une conversation sans fin : le journal vivant reste borné, et la remontée du fil
// retrouve TOUS les échanges, dans l'ordre, lot par lot, sans trou ni doublon.
func TestChatPagesInfiniteConversation(t *testing.T) {
	testHome(t)
	c := &Conversation{ID: jeanConvID, Mode: "jean"}
	const total = 300
	for i := 0; i < total; i++ {
		c.Seq++
		c.Log = append(c.Log, LogEvent{Seq: c.Seq, Delta: map[string]any{"user": fmt.Sprint(i)}})
		c.Seq++
		c.Log = append(c.Log, LogEvent{Seq: c.Seq, Delta: map[string]any{"content": "r"}})
		c.Seq++
		c.Log = append(c.Log, LogEvent{Seq: c.Seq, Delta: map[string]any{"turn_done": true}})
		c.pageOutLocked()
	}
	live := len(userTurnStarts(c.Log))
	if live > pageKeepTurns+pageMinTurns {
		t.Fatalf("journal vivant non borné : %d échanges", live)
	}
	keys := pageKeys(jeanConvID)
	if len(keys) == 0 || live+pagedTurns(keys) != total {
		t.Fatalf("échanges perdus : %d vivants + %d en pages ≠ %d", live, pagedTurns(keys), total)
	}

	// Chargement initial : 20 derniers échanges, le reste annoncé.
	cut, hidden := tailCut(c.Log, c.ID, 20)
	if hidden != total-20 {
		t.Fatalf("masqués = %d, veut %d", hidden, total-20)
	}

	// Remontée : on doit retrouver 279, 278, … 0 exactement.
	before, want := cut+1, total-21
	for before > 0 {
		evs, more, err := c.olderEvents(jeanConvID, before, 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) == 0 {
			break
		}
		starts := userTurnStarts(evs)
		for j := len(starts) - 1; j >= 0; j-- {
			if got := evs[starts[j]].Delta["user"]; got != fmt.Sprint(want) {
				t.Fatalf("échange %v, veut %d", got, want)
			}
			want--
		}
		if more != want+1 {
			t.Fatalf("plus anciens annoncés = %d, veut %d", more, want+1)
		}
		before = evs[0].Seq
	}
	if want != -1 {
		t.Fatalf("remontée incomplète : arrêtée avant l'échange %d", want)
	}

	// Supprimer la conversation supprime ses pages.
	deletePages(jeanConvID)
	if len(pageKeys(jeanConvID)) != 0 {
		t.Fatal("pages non supprimées")
	}
}

// Les autres conversations ne sont jamais découpées.
func TestChatPagesOnlyJean(t *testing.T) {
	testHome(t)
	c := &Conversation{ID: "123", Mode: "project"}
	for i := 0; i < 400; i++ {
		c.Seq++
		c.Log = append(c.Log, LogEvent{Seq: c.Seq, Delta: map[string]any{"user": "x"}})
	}
	c.pageOutLocked()
	if len(c.Log) != 400 || len(pageKeys("123")) != 0 {
		t.Fatal("une conversation normale a été découpée")
	}
}

// L'export d'une conversation sans fin contient aussi les échanges rangés en pages.
func TestChatPagesExportComplete(t *testing.T) {
	testHome(t)
	c := &Conversation{ID: jeanConvID, Mode: "jean"}
	for i := 0; i < 250; i++ {
		c.Seq++
		c.Log = append(c.Log, LogEvent{Seq: c.Seq, Delta: map[string]any{"user": fmt.Sprint(i)}})
		c.pageOutLocked()
	}
	if got := len(userTurnStarts(c.fullLog())); got != 250 {
		t.Fatalf("export : %d échanges sur 250", got)
	}
}
