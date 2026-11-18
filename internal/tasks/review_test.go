package tasks

import (
	"strings"
	"testing"
)

func reviewReady(t *testing.T) {
	t.Helper()
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := Init(); err != nil {
		t.Fatal(err)
	}
}

func TestNewTaskWaitsAndSaysWhatItDoes(t *testing.T) {
	reviewReady(t)
	task := mustSave(t, Input{Name: ptr("Veille"), Prompt: ptr("Vérifier le prix du vol"), InMinutes: 1}, now())
	got := Changes()
	if len(got) != 1 || got[0].Kind != "new" || !strings.Contains(got[0].Detail, "Vérifier le prix du vol") || !strings.Contains(got[0].Detail, "Quand :") {
		t.Fatalf("%+v", got)
	}
	mu.Lock()
	ok := validLocked(task)
	mu.Unlock()
	if ok {
		t.Fatal("une tâche non validée ne doit pas partir")
	}
	if err := Accept(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	ok = validLocked(task)
	mu.Unlock()
	if !ok || len(Changes()) != 0 {
		t.Fatalf("après validation : %v, %v", ok, Changes())
	}
}

func TestEditShowsOnlyTheDifferenceAndRejectRestores(t *testing.T) {
	reviewReady(t)
	task := mustSave(t, Input{Name: ptr("Rappel"), Prompt: ptr("Appeler Marie"), InMinutes: 5}, now())
	if err := Accept(); err != nil {
		t.Fatal(err)
	}
	mustSave(t, Input{ID: task.ID, Prompt: ptr("Appeler Paul")}, now())
	mustSave(t, Input{Name: ptr("Nouvelle"), Prompt: ptr("Autre chose"), InMinutes: 9}, now())
	got := Changes()
	if len(got) != 2 {
		t.Fatalf("toutes les modifications ensemble : %+v", got)
	}
	var edit Change
	for _, c := range got {
		if c.ID == task.ID {
			edit = c
		}
	}
	if !strings.Contains(edit.Detail, "Avant : Appeler Marie") || !strings.Contains(edit.Detail, "Après : Appeler Paul") || strings.Contains(edit.Detail, "Nom") {
		t.Fatalf("différence : %q", edit.Detail)
	}
	if err := Reject(); err != nil {
		t.Fatal(err)
	}
	all := List()
	if len(all) != 1 || all[0].Prompt != "Appeler Marie" || len(Changes()) != 0 {
		t.Fatalf("après refus : %+v", all)
	}
}

func TestDeletedTaskComesBackIfRefused(t *testing.T) {
	reviewReady(t)
	task := mustSave(t, Input{Name: ptr("Garde"), Prompt: ptr("x"), Every: 1, Unit: "day"}, now())
	if err := Accept(); err != nil {
		t.Fatal(err)
	}
	if err := Delete(task.ID); err != nil {
		t.Fatal(err)
	}
	if c := Changes(); len(c) != 1 || c[0].Kind != "deleted" {
		t.Fatalf("%+v", c)
	}
	if err := Reject(); err != nil {
		t.Fatal(err)
	}
	if all := List(); len(all) != 1 || all[0].ID != task.ID {
		t.Fatalf("%+v", all)
	}
}

func TestUserOwnChangesNeedNoValidation(t *testing.T) {
	reviewReady(t)
	task, err := SaveByUser(Input{Name: ptr("Moi"), Prompt: ptr("x"), InMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(Changes()) != 0 {
		t.Fatalf("%+v", Changes())
	}
	if err := DeleteByUser(task.ID); err != nil || len(Changes()) != 0 {
		t.Fatalf("%v, %+v", err, Changes())
	}
}

func TestRuntimeStateIsNotAChange(t *testing.T) {
	reviewReady(t)
	task := mustSave(t, Input{Name: ptr("Une fois"), Prompt: ptr("x"), InMinutes: 1}, now())
	if err := Accept(); err != nil {
		t.Fatal(err)
	}
	if err := update(func(f *file) error {
		s := f.find(task.ID)
		s.Enabled, s.Runs = false, []Run{{Status: statusOK}}
		s.Next = task.Created
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if c := Changes(); len(c) != 0 {
		t.Fatalf("une exécution n'est pas une modification : %+v", c)
	}
}
