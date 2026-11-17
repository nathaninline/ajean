package agent

import "testing"

func TestModelRoundTrip(t *testing.T) {
	c := &conversation{id: "x", title: "t", log: []Item{
		{Role: "user", Text: "Salut", TS: 1_700_000_000_000},
		{Role: "assistant", Text: "Bonjour", TS: 1_700_000_005_000, Model: "Qwen 3.8 Flash Next"},
		{Role: "assistant", Text: "Coupé", TS: 1_700_000_009_000, Kind: kindFailed, Model: "Autre modèle"},
		{Role: "assistant", Text: "Ancien", TS: 1_700_000_010_000},
	}}
	back := parse("x", c.render())
	if len(back.log) != 4 {
		t.Fatalf("%d messages relus", len(back.log))
	}
	if g := back.log[1]; g.Model != "Qwen 3.8 Flash Next" || g.Kind != "" {
		t.Fatalf("relu : %+v", g)
	}
	if g := back.log[2]; g.Model != "Autre modèle" || g.Kind != kindFailed {
		t.Fatalf("relu : %+v", g)
	}
	if g := back.log[3]; g.Model != "" || g.Text != "Ancien" {
		t.Fatalf("relu : %+v", g)
	}
}
