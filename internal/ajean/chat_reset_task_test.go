package ajean

import (
	"context"
	"testing"
)

// Issue #76 : ouvrir une nouvelle conversation (Reset) NE DOIT PAS annuler une
// tâche de fond en cours ni libérer son gate — sinon la tâche est tuée et son
// rapport perdu.
func TestResetDoesNotCancelRunningTask(t *testing.T) {
	c := newTestConv()
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.Generating = true
	c.cancel = cancel
	c.runningTaskID = "task-1"
	c.runningTaskName = "résumé quotidien"
	c.Messages = []Message{{Role: "user", Content: "salut"}}
	c.mu.Unlock()

	c.Reset()

	if ctx.Err() != nil {
		t.Fatal("Reset a annulé le contexte de la tâche (ne doit pas)")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.Generating {
		t.Error("le gate de génération de la tâche a été libéré par Reset")
	}
	if c.runningTaskID != "task-1" {
		t.Errorf("runningTaskID = %q, attendu task-1 (effacé par Reset)", c.runningTaskID)
	}
	if c.cancel == nil {
		t.Error("cancel de la tâche perdu → le bouton stop ne pourrait plus l'arrêter")
	}
	if len(c.Messages) != 0 {
		t.Error("le fil aurait quand même dû être vidé (nouvelle conversation)")
	}
}

// À l'inverse, un TOUR UTILISATEUR coincé doit bien être débloqué/annulé par Reset
// (c'est sa deuxième raison d'être).
func TestResetCancelsUserTurn(t *testing.T) {
	c := newTestConv()
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.Generating = true
	c.cancel = cancel // pas de runningTaskID → tour utilisateur
	c.mu.Unlock()

	c.Reset()

	if ctx.Err() == nil {
		t.Fatal("Reset aurait dû annuler le tour utilisateur")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Generating {
		t.Error("Generating aurait dû retomber à false après Reset d'un tour user")
	}
	if c.cancel != nil {
		t.Error("cancel aurait dû être nil après Reset d'un tour user")
	}
}
