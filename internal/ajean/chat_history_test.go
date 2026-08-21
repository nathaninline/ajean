package ajean

import (
	"sync"
	"testing"
)

// newHistTestConv : conversation isolée (le global `conv` sert au process réel),
// même fabrique que les autres tests de conversation.
func newHistTestConv() *Conversation {
	c := &Conversation{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// Le cycle complet de l'historique : archiver (clear chat), lister, recharger
// (qui archive la courante à son tour), supprimer.
func TestChatHistoryCycle(t *testing.T) {
	testHome(t)
	c := newHistTestConv()

	// Conversation A.
	c.appendDelta(c.epoch, map[string]any{"user": "première question"})
	c.appendDelta(c.epoch, map[string]any{"content": "réponse A"})
	c.appendDelta(c.epoch, map[string]any{"user": "et une deuxième"})

	// clear chat → A part à l'historique, la conversation courante est vidée.
	idA := c.ArchiveAndReset()
	if idA == "" {
		t.Fatal("ArchiveAndReset aurait dû archiver une conversation non vide")
	}
	if len(c.Log) != 0 || len(c.Messages) != 0 {
		t.Fatalf("après clear chat la conversation devrait être vide (log=%d msgs=%d)", len(c.Log), len(c.Messages))
	}

	list := listArchives()
	if len(list) != 1 {
		t.Fatalf("attendu 1 archive, obtenu %d", len(list))
	}
	if list[0].Title != "première question" {
		t.Errorf("titre = %q, attendu %q", list[0].Title, "première question")
	}
	if list[0].Turns != 2 {
		t.Errorf("turns = %d, attendu 2", list[0].Turns)
	}

	// Conversation B en cours.
	c.appendDelta(c.epoch, map[string]any{"user": "conversation B"})
	c.appendDelta(c.epoch, map[string]any{"content": "réponse B"})

	// Recharger A : A redevient active, B est archivée à son tour, A disparaît de
	// la liste (elle n'est plus une archive).
	if err := c.RestoreArchive(idA); err != nil {
		t.Fatalf("RestoreArchive: %v", err)
	}
	if len(c.Log) == 0 {
		t.Fatal("après restauration la conversation active devrait contenir le fil de A")
	}
	if got := archiveTitle(c.Log); got != "première question" {
		t.Errorf("conversation active = %q, attendu celle de A", got)
	}

	list = listArchives()
	if len(list) != 1 {
		t.Fatalf("attendu 1 archive (B) après restauration de A, obtenu %d", len(list))
	}
	if list[0].Title != "conversation B" {
		t.Errorf("l'archive restante devrait être B, obtenu %q", list[0].Title)
	}

	// Suppression définitive de B.
	if err := deleteArchive(list[0].ID); err != nil {
		t.Fatalf("deleteArchive: %v", err)
	}
	if n := len(listArchives()); n != 0 {
		t.Fatalf("après suppression, 0 archive attendue, obtenu %d", n)
	}

	// Restaurer un id inconnu remonte une erreur, sans toucher à la conversation.
	if err := c.RestoreArchive("inexistant"); err == nil {
		t.Error("RestoreArchive d'un id inconnu aurait dû échouer")
	}
}

// Une conversation vide ne s'archive pas (clear chat sur un fil déjà vide ne
// crée pas d'entrée fantôme).
func TestArchiveEmptyIsNoop(t *testing.T) {
	testHome(t)
	c := newHistTestConv()
	if id := c.ArchiveAndReset(); id != "" {
		t.Errorf("une conversation vide ne devrait pas être archivée, id=%q", id)
	}
	if n := len(listArchives()); n != 0 {
		t.Errorf("aucune archive attendue, obtenu %d", n)
	}
}
