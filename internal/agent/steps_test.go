package agent

import "testing"

func TestStepsRoundTrip(t *testing.T) {
	steps := []Step{{Name: "tools", Label: "run nas", Desc: "Lecture de l'espace disque"}, {Name: "memory_search", Label: "mail"}}
	c := &conversation{id: "x", title: "t", log: []Item{
		{Role: "user", Text: "Question", TS: 1_700_000_000_000},
		{Role: "assistant", Text: "Réponse\navec deux lignes", TS: 1_700_000_005_000, Model: "M", Steps: steps},
	}}
	back := parse("x", c.render())
	if len(back.log) != 2 {
		t.Fatalf("%d messages relus", len(back.log))
	}
	got := back.log[1]
	if len(got.Steps) != 2 || got.Steps[0] != steps[0] || got.Steps[1] != steps[1] {
		t.Fatalf("étapes relues : %+v", got.Steps)
	}
	if got.Text != "Réponse\navec deux lignes" {
		t.Fatalf("texte relu : %q", got.Text)
	}
}
