package ajean

import (
	"context"
	"strings"
	"testing"
)

// runSilentModel joue un tour où le modèle appelle un outil puis se tait
// (réponse vide) autant de fois que le script le dit. Renvoie ce que
// l'utilisateur a vu, les messages gardés et les requêtes reçues par le moteur.
func runSilentModel(t *testing.T, script ...scriptedReply) (shown string, extra []Message, reqs []map[string]any) {
	t.Helper()
	testHome(t)
	if err := MemAdd("gouts.md", "# Goûts\n\nAime le thé vert."); err != nil {
		t.Fatal(err)
	}
	eng := newScriptedEngine(t, script...)
	var content strings.Builder
	extra, err := runChat(context.Background(), []Message{{Role: "user", Content: "que sais-tu de mes goûts ?"}}, 0.7,
		Caps{Mem: MemOnDemand}, func(ev StreamEvent) bool {
			if ev.Content != "" {
				content.WriteString(ev.Content)
			}
			return true
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return content.String(), extra, eng.requests()
}

// Le modèle a travaillé (un outil) puis ne dit plus rien, relances comprises : une
// dernière demande part SANS outils, et sa réponse est celle du tour. La consigne
// ne reste pas dans le fil gardé.
func TestFinalAnswerWhenModelStaysSilent(t *testing.T) {
	shown, extra, reqs := runSilentModel(t,
		scriptedReply{tool: "mem_search", args: `{"query":"thé"}`},
		scriptedReply{text: ""}, // pensé sans agir
		scriptedReply{text: ""}, // après la 1re relance
		scriptedReply{text: ""}, // après la 2e relance
		scriptedReply{text: "J'ai trouvé la page gouts.md, rien d'autre."},
	)
	if len(reqs) != 5 {
		t.Fatalf("%d requêtes, 5 attendues (outil, 3 silences, réponse finale)", len(reqs))
	}
	for i, r := range reqs[:4] {
		if _, ok := r["tools"]; !ok {
			t.Fatalf("requête %d : les outils devaient encore être annoncés", i)
		}
	}
	last := reqs[4]
	if _, ok := last["tools"]; ok {
		t.Fatal("la dernière demande annonce encore des outils")
	}
	msgs, _ := last["messages"].([]any)
	tail, _ := msgs[len(msgs)-1].(map[string]any)
	if tail["role"] != "user" || tail["content"] != finalAnswerNote {
		t.Fatalf("dernière demande inattendue : %v", tail)
	}
	if shown != "J'ai trouvé la page gouts.md, rien d'autre." {
		t.Fatalf("réponse affichée : %q", shown)
	}
	for _, m := range extra {
		if s, _ := m.Content.(string); strings.Contains(s, "Do not call any tool") {
			t.Fatalf("la consigne est restée dans le fil gardé : %q", s)
		}
	}
	// L'étape reste dans le fil : l'appel et son résultat, dans l'ordre.
	if len(extra) != 2 || len(extra[0].ToolCalls) != 1 || extra[1].Role != "tool" {
		t.Fatalf("étapes gardées : %+v", extra)
	}
}

// Même la dernière demande reste sans réponse : on le dit, une fois, et on
// n'insiste pas davantage.
func TestFinalAnswerAskedOnlyOnce(t *testing.T) {
	shown, _, reqs := runSilentModel(t,
		scriptedReply{tool: "mem_search", args: `{"query":"thé"}`},
		scriptedReply{text: ""}, scriptedReply{text: ""}, scriptedReply{text: ""},
		scriptedReply{text: ""}, // la dernière demande aussi
		scriptedReply{text: "ne doit jamais être demandé"},
	)
	if len(reqs) != 5 {
		t.Fatalf("%d requêtes, 5 attendues", len(reqs))
	}
	if !strings.Contains(shown, "n'a pas produit de réponse") {
		t.Fatalf("le silence final n'est pas signalé : %q", shown)
	}
}

// Sans outils il n'y a rien à rapporter, et une révision silencieuse de la
// mémoire a le droit de ne rien dire.
func TestWantFinalAnswer(t *testing.T) {
	ctx := context.Background()
	if !wantFinalAnswer(ctx, 3, false) {
		t.Fatal("outils annoncés, jamais demandé : la dernière demande doit partir")
	}
	if wantFinalAnswer(ctx, 0, false) {
		t.Fatal("sans outils, pas de dernière demande")
	}
	if wantFinalAnswer(ctx, 3, true) {
		t.Fatal("outils déjà coupés : la demande a déjà été faite")
	}
	if wantFinalAnswer(context.WithValue(ctx, jeanReflectKey{}, true), 3, false) {
		t.Fatal("pas de dernière demande pendant une révision de la mémoire")
	}
}
