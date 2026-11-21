package ajean

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Historique des passages d'une tâche (#143) : chaque fin de passage s'ajoute
// en tête, avec son issue, et la liste reste bornée.
func TestTaskHistory(t *testing.T) {
	testHome(t)
	tk := Task{ID: newTaskID(), Name: "Veille", Prompt: "x", Schedule: "@every 2h", Enabled: true}
	if err := saveTask(tk); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Second)
	recordTaskEnd(tk.ID, start, "première réponse", nil)
	recordTaskEnd(tk.ID, start, "", errors.New("moteur injoignable"))
	recordTaskEnd(tk.ID, start, "", context.Canceled)
	got, _ := getTask(tk.ID)
	if len(got.History) != 3 {
		t.Fatalf("3 passages attendus, %d", len(got.History))
	}
	if got.History[0].Status != "stopped" || got.History[1].Status != "error" || got.History[2].Status != "ok" {
		t.Fatalf("ordre ou statuts inattendus : %+v", got.History)
	}
	if got.History[2].Report != "première réponse" || got.History[1].Error != "moteur injoignable" || got.History[0].DurMs <= 0 {
		t.Fatalf("contenu inattendu : %+v", got.History)
	}

	for i := 0; i < taskHistoryMax+5; i++ {
		recordTaskEnd(tk.ID, start, fmt.Sprintf("réponse %d", i), nil)
	}
	got, _ = getTask(tk.ID)
	if len(got.History) != taskHistoryMax {
		t.Fatalf("historique non borné : %d", len(got.History))
	}
	if want := fmt.Sprintf("réponse %d", taskHistoryMax+4); got.History[0].Report != want {
		t.Fatalf("le plus récent doit être en tête : %q", got.History[0].Report)
	}
}

// Tâche script : seule la sortie standard part chez l'utilisateur.
func TestScriptStdout(t *testing.T) {
	for out, want := range map[string]string{
		"exit: 0": "",
		"exit: 0\n\nstdout:\nPerla a repondu.\nSujet : Re\n": "Perla a repondu.\nSujet : Re",
		"exit: 0\n\nstdout:\nalerte\n\nstderr:\nwarning":     "alerte",
		"exit: 0\n\nstderr:\nseulement stderr":               "",
	} {
		if got := scriptStdout(out); got != want {
			t.Fatalf("scriptStdout(%q) = %q, attendu %q", out, got, want)
		}
	}
}
