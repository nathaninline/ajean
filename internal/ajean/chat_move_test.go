package ajean

import "testing"

// #111 : déplacer une conversation rapide vers un projet la convertit en
// conversation de projet (elle quitte la liste Rapide) ; une conversation
// « Modèle de base » est refusée.
func TestMoveQuickConversationToProject(t *testing.T) {
	testHome(t)
	p, err := createProject("Travaux")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []*convArchive{
		{ID: "rapide", Title: "r", SavedAt: 1, Mode: "fast", Log: []LogEvent{histUser(1, "salut")}},
		{ID: "base", Title: "b", SavedAt: 2, Mode: "base", Log: []LogEvent{histUser(1, "salut")}},
	} {
		if err := saveArchive(a); err != nil {
			t.Fatal(err)
		}
	}
	if err := moveArchiveToProject("rapide", p.Slug); err != nil {
		t.Fatalf("déplacement refusé : %v", err)
	}
	a, _ := loadArchive("rapide")
	if a.Mode != "project" || a.Project != p.Slug {
		t.Fatalf("attendu mode project dans %s, obtenu mode=%q projet=%q", p.Slug, a.Mode, a.Project)
	}
	for _, m := range historyList("fast") {
		if m.ID == "rapide" {
			t.Fatal("la conversation déplacée ne doit plus être dans la liste Rapide")
		}
	}
	if err := moveArchiveToProject("base", p.Slug); err == nil {
		t.Fatal("une conversation Modèle de base ne doit pas être déplacée")
	}
}
