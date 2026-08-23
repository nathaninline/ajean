package ajean

import (
	"sync"
	"testing"
)

// newHistTestConv : conversation isolée (le global `conv` sert au process réel),
// même fabrique que les autres tests de conversation. Un id de session est
// attribué, comme pour la vraie conversation.
func newHistTestConv() *Conversation {
	c := &Conversation{ID: newSessionID()}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// Cycle complet des sessions : la conversation active se reflète dans la liste
// sous son id stable ; « nouvelle session » la sauvegarde et repart vierge ;
// ouvrir une session garde tout le monde dans la liste (pas de doublon, pas de
// perte) ; supprimer retire l'entrée.
func TestSessionCycle(t *testing.T) {
	testHome(t)
	c := newHistTestConv()
	idA := c.ID // la session A = l'id courant

	// Conversation A.
	c.appendDelta(c.epoch, map[string]any{"user": "première question"})
	c.appendDelta(c.epoch, map[string]any{"content": "réponse A"})
	c.appendDelta(c.epoch, map[string]any{"user": "et une deuxième"})

	// Nouvelle session → A est sauvegardée sous son id, la courante repart vierge
	// avec un NOUVEL id.
	newID := c.NewSession()
	if newID == idA {
		t.Fatal("NewSession aurait dû attribuer un nouvel id de session")
	}
	if len(c.Log) != 0 || len(c.Messages) != 0 {
		t.Fatalf("après nouvelle session la conversation devrait être vide (log=%d msgs=%d)", len(c.Log), len(c.Messages))
	}

	list := listArchives()
	if len(list) != 1 {
		t.Fatalf("attendu 1 session (A), obtenu %d", len(list))
	}
	if list[0].ID != idA || list[0].Title != "première question" {
		t.Errorf("session listée = {id:%q,titre:%q}, attendu A", list[0].ID, list[0].Title)
	}
	if list[0].Turns != 2 {
		t.Errorf("turns = %d, attendu 2", list[0].Turns)
	}

	// Conversation B dans la nouvelle session.
	c.appendDelta(c.epoch, map[string]any{"user": "conversation B"})
	c.appendDelta(c.epoch, map[string]any{"content": "réponse B"})
	idB := c.ID

	// Ouvrir A : B est sauvegardée sous SON id (elle RESTE listée), A redevient
	// active. Les deux sessions coexistent, aucun doublon.
	if err := c.OpenSession(idA); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if c.ID != idA {
		t.Errorf("après ouverture, id actif = %q, attendu %q", c.ID, idA)
	}
	if got := archiveTitle(c.Log); got != "première question" {
		t.Errorf("conversation active = %q, attendu celle de A", got)
	}

	list = listArchives()
	if len(list) != 2 {
		t.Fatalf("attendu 2 sessions (A et B) après ouverture de A, obtenu %d", len(list))
	}

	// Nom + favori sur A (active) : persistants après une nouvelle session.
	if err := renameArchive(idA, "Ma session A"); err != nil {
		t.Fatalf("renameArchive: %v", err)
	}
	c.setActiveTitleIfMatch(idA, "Ma session A") // ce que fait le handler
	if err := setArchiveFav(idA, true); err != nil {
		t.Fatalf("setArchiveFav: %v", err)
	}
	c.setActiveFavIfMatch(idA, true)
	c.NewSession() // sauvegarde A (avec nom+favori) puis repart vierge
	var gotA *convArchiveMeta
	for i := range listArchives() {
		if listArchives()[i].ID == idA {
			m := listArchives()[i]
			gotA = &m
		}
	}
	if gotA == nil {
		t.Fatal("la session A devrait toujours être listée")
	}
	if gotA.Title != "Ma session A" || !gotA.Fav {
		t.Errorf("A = {titre:%q,fav:%v}, attendu {Ma session A,true}", gotA.Title, gotA.Fav)
	}

	// Suppression définitive de B.
	if err := c.DeleteSession(idB); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	for _, m := range listArchives() {
		if m.ID == idB {
			t.Fatal("B aurait dû être supprimée")
		}
	}

	// Ouvrir un id inconnu remonte une erreur.
	if err := c.OpenSession("inexistant"); err == nil {
		t.Error("OpenSession d'un id inconnu aurait dû échouer")
	}
}

// clearAll (tout supprimer sauf favoris) garde les favoris ET la session active.
func TestDeleteNonFavKeepsActive(t *testing.T) {
	testHome(t)
	c := newHistTestConv()
	idActive := c.ID
	c.appendDelta(c.epoch, map[string]any{"user": "session active"})
	c.upsertSession() // reflète l'active dans la liste

	// Une autre session non favorite, archivée à part.
	other := &convArchive{ID: newSessionID(), SavedAt: 1, Title: "jetable",
		Log: []LogEvent{{Delta: map[string]any{"user": "x"}}}}
	if err := saveArchive(other); err != nil {
		t.Fatal(err)
	}

	n := deleteNonFavArchives(c.ID)
	if n != 1 {
		t.Fatalf("attendu 1 suppression, obtenu %d", n)
	}
	found := false
	for _, m := range listArchives() {
		if m.ID == idActive {
			found = true
		}
		if m.ID == other.ID {
			t.Fatal("la session jetable aurait dû être supprimée")
		}
	}
	if !found {
		t.Fatal("la session active ne doit pas être supprimée par « tout supprimer »")
	}
}

// Une nouvelle session sur un fil vide ne crée pas d'entrée fantôme.
func TestNewSessionEmptyIsNoop(t *testing.T) {
	testHome(t)
	c := newHistTestConv()
	c.NewSession()
	if n := len(listArchives()); n != 0 {
		t.Errorf("aucune session attendue pour un fil vide, obtenu %d", n)
	}
}
