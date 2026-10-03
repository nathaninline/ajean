package ajean

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJeanStreamFilter(t *testing.T) {
	rs := []jeanRule{
		{N: 1, Kind: "replace", From: " — ", To: ", "},
		{N: 2, Kind: "replace", From: "—", To: ", "},
		{N: 3, Kind: "no_numbered_lists"},
	}
	in := "Bonjour — voici le plan :\n1. Café\n2. Code — beaucoup\n  10) sous-point\nAnnée 2026. Fin—vraiment.\n```\n1. dans le code — intact\n```\n3. après"
	want := "Bonjour, voici le plan :\n- Café\n- Code, beaucoup\n  - sous-point\nAnnée 2026. Fin, vraiment.\n```\n1. dans le code — intact\n```\n- après"
	// Tous les découpages en deux, et un découpage caractère par caractère.
	for cut := 0; cut <= len(in); cut++ {
		f := newJeanStreamFilter(rs)
		got := f.Push(in[:cut]) + f.Push(in[cut:]) + f.Flush()
		if got != want {
			t.Fatalf("coupure à %d :\n%q\nveut\n%q", cut, got, want)
		}
	}
	f := newJeanStreamFilter(rs)
	var b strings.Builder
	for _, r := range in {
		b.WriteString(f.Push(string(r)))
	}
	if got := b.String() + f.Flush(); got != want {
		t.Fatalf("caractère par caractère :\n%q", got)
	}
	// Rien ne reste bloqué longtemps : une phrase sans règle part tout de suite.
	f = newJeanStreamFilter(rs)
	if out := f.Push("Une phrase normale qui s'écrit"); out != "Une phrase normale qui s'écrit" {
		t.Fatalf("retenue inutile : %q", out)
	}
	if newJeanStreamFilter(nil) != nil {
		t.Fatal("aucune règle = aucun filtre")
	}
}

func TestJeanRulesToolAndViolations(t *testing.T) {
	testHome(t)
	if _, err := JeanRule("add", "replace", "—", ", ", 0, "Alice déteste"); err != nil {
		t.Fatal(err)
	}
	if _, err := JeanRule("add", "max_list_items", "", "", 5, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := JeanRule("add", "be_nice", "", "", 0, ""); err == nil {
		t.Fatal("kind inconnu accepté")
	}
	if got := jeanApplyRules("A — B"); strings.Contains(got, "—") || !strings.Contains(got, ",") {
		t.Fatalf("remplacement : %q", got)
	}
	if !strings.Contains(jeanContextMessage().Content.(string), "rule-1") {
		t.Fatal("règles absentes du bloc mémoire")
	}
	long := "Voici :\n- a\n- b\n- c\n- d\n- e\n- f\nFin"
	if v := jeanRuleViolations(long); len(v) != 1 || !strings.Contains(v[0], "6 items") {
		t.Fatalf("violation non vue : %v", v)
	}
	ok := "Deux listes :\n- a\n- b\n- c\n\nAutre :\n- d\n- e\n- f\n  - sous\n```\n- x\n- y\n- z\n- w\n- v\n- u\n```"
	if v := jeanRuleViolations(ok); len(v) != 1 {
		// les deux listes séparées par une ligne vide comptent ensemble : 6 > 5
		t.Logf("listes séparées par du texte : %v", v)
	}
	sep := "Liste 1 :\n- a\n- b\n- c\nTexte entre.\n- d\n- e\n- f"
	if v := jeanRuleViolations(sep); len(v) != 0 {
		t.Fatalf("deux listes distinctes vues comme une : %v", v)
	}
	if _, err := JeanRule("remove", "", "rule-2", "", 0, ""); err != nil {
		t.Fatal(err)
	}
	if v := jeanRuleViolations(long); len(v) != 0 {
		t.Fatal("règle retirée encore appliquée")
	}
}

func TestJeanFicheCorrectedAfterUse(t *testing.T) {
	testHome(t)
	_, _ = JeanSave("courses", "liste de courses", "ajouter en MAJUSCULES")
	extra := []Message{{Role: "assistant", ToolCalls: []ToolCall{{Function: ToolCallFunc{Name: "jean_read", Arguments: `{"name":"Courses"}`}}}}}
	jeanReflect.mu.Lock()
	jeanReflect.pending, jeanReflect.corr, jeanReflect.corrMsg, jeanReflect.lastFiches = 0, 0, nil, nil
	jeanReflect.mu.Unlock()
	jeanReflectNoteTurn(Caps{Jean: true}, 0.7, "ajoute du lait", nil, jeanFichesRead(extra), 1)
	jeanReflectNoteTurn(Caps{Jean: true}, 0.7, "Non, une majuscule au début seulement", nil, nil, 0)
	jeanReflect.mu.Lock()
	msgs := strings.Join(jeanReflect.corrMsg, "\n")
	jeanReflect.pending, jeanReflect.corr, jeanReflect.corrMsg, jeanReflect.lastFiches = 0, 0, nil, nil
	if jeanReflect.timer != nil {
		jeanReflect.timer.Stop()
	}
	if jeanConsolidateTimer.t != nil {
		jeanConsolidateTimer.t.Stop()
	}
	jeanReflect.mu.Unlock()
	if !strings.Contains(msgs, "fiche(s) courses") {
		t.Fatalf("fiche suspecte non signalée : %s", msgs)
	}
	jeanMu.Lock()
	u := jeanUsageLocked()
	jeanMu.Unlock()
	if u["courses"] == nil || u["courses"].Corrected != 1 {
		t.Fatalf("correction non comptée : %+v", u["courses"])
	}
}

func TestJeanReplaceSpacing(t *testing.T) {
	rs := []jeanRule{{N: 1, Kind: "replace", From: "—", To: "-"}}
	in := "Elle ne demande rien — pas même qu'on la regarde — et pourtant. Fin—vraiment. Début —fin."
	want := "Elle ne demande rien, pas même qu'on la regarde, et pourtant. Fin, vraiment. Début,fin."
	for cut := 0; cut <= len(in); cut++ {
		f := newJeanStreamFilter(rs)
		if got := f.Push(in[:cut]) + f.Push(in[cut:]) + f.Flush(); got != want {
			t.Fatalf("coupure %d : %q", cut, got)
		}
	}
}

func TestJeanReflectBeforeClear(t *testing.T) {
	jeanReflect.mu.Lock()
	jeanReflect.pending, jeanReflect.orphan, jeanReflect.orphanN, jeanReflect.work = 3, nil, 0, 0
	jeanReflect.mu.Unlock()
	old := []Message{{Role: "user", Content: "combien de vues ?"}, {Role: "assistant", Content: "7 245"}}
	jeanReflectBeforeClear(old)
	jeanReflect.mu.Lock()
	n, p, kept := jeanReflect.orphanN, jeanReflect.pending, len(jeanReflect.orphan)
	jeanReflect.timer.Stop()
	jeanReflect.orphan, jeanReflect.orphanN = nil, 0
	jeanReflect.mu.Unlock()
	if n != 3 || p != 0 || kept != 2 {
		t.Fatalf("fil vidé non gardé pour révision : orphanN=%d pending=%d msgs=%d", n, p, kept)
	}
	if jeanWorkBrief(4) != "" || !strings.Contains(jeanWorkBrief(9), "9 tool calls") {
		t.Fatal("brief de tâche laborieuse")
	}
}

func TestJeanNoDuplicateFiche(t *testing.T) {
	testHome(t)
	_, _ = JeanSave("youtube-vues", "Quand Alice demande les vues / stats de ses vidéos YouTube (chaîne Alice Demo)", "rss puis watch")
	_, _ = JeanSave("courses", "Quand Alice demande d'ajouter à sa liste de courses", "x")
	_, _ = JeanSave("depenses", "Quand Alice envoie des dépenses à enregistrer", "x")
	if _, err := JeanSave("youtube-alice-views", "Quand Alice demande ses vues, abonnés ou ses dernières vidéos sur sa chaîne YouTube Alice Demo", "y"); err == nil || !strings.Contains(err.Error(), "youtube-vues") {
		t.Fatalf("doublon accepté : %v", err)
	}
	if _, err := JeanSave("meteo", "Quand Alice demande la météo de demain", "z"); err != nil {
		t.Fatalf("fiche distincte refusée : %v", err)
	}
	if _, err := JeanSave("youtube-vues", "Quand Alice demande ses vues YouTube", "v2"); err != nil {
		t.Fatalf("mise à jour sous le même nom refusée : %v", err)
	}
}

func TestJeanBackgroundNotes(t *testing.T) {
	testHome(t)
	_, _ = JeanSave("youtube-vues", "Quand Alice demande ses vues YouTube", "rss")
	jeanQueueNotes([]string{jeanNoteFor("jean_save", "youtube-vues"), jeanNoteFor("jean_lesson", "Ne jamais scraper la page /videos")})
	if jeanTakeNotes(true) != "" {
		t.Fatal("contexte neuf : rien à annoncer")
	}
	jeanQueueNotes([]string{jeanNoteFor("jean_save", "youtube-vues")})
	n := jeanTakeNotes(false)
	if !strings.Contains(n, "fiche youtube-vues saved (when: Quand Alice demande ses vues YouTube)") {
		t.Fatalf("note : %q", n)
	}
	msg := "[Friday 2026-10-02 22:00 (Europe/Paris)] " + n + "combien de vues ?"
	if got := jeanStripNow(msg); got != "combien de vues ?" {
		t.Fatalf("strip : %q", got)
	}
	// Une leçon citée dans la note ne doit pas passer pour une correction de l'utilisateur.
	jeanQueueNotes([]string{jeanNoteFor("jean_lesson", "plus jamais de tirets, je t'ai déjà dit")})
	if k := jeanCorrectionKind("[x] " + jeanTakeNotes(false) + "salut"); k != jeanCorrNone {
		t.Fatalf("note prise pour une correction : %d", k)
	}
}

func TestJeanReflectScriptWrite(t *testing.T) {
	testHome(t)
	ctx := withJeanSpace(context.Background())
	_ = os.MkdirAll(jeanScriptsDir(), 0o755)
	_ = os.WriteFile(filepath.Join(jeanScriptsDir(), "ok.py"), []byte("print(1)"), 0o644)
	cases := []struct {
		tool, file string
		want       bool
	}{
		{"write", filepath.Join(jeanScriptsDir(), "nouveau.py"), true},
		{"write", filepath.Join(jeanScriptsDir(), "ok.py"), false}, // écraser un script testé : non
		{"edit", filepath.Join(jeanScriptsDir(), "ok.py"), false},
		{"write", filepath.Join(jeanWorkspace(), "x.py"), false},
		{"bash", "", false},
		{"write", "montage.py", true}, // nom nu : dans le dossier des scripts
		{"write", "ok.py", false},     // nom nu d'un script existant : pas d'écrasement
	}
	for _, c := range cases {
		if got := jeanReflectScriptWrite(ctx, c.tool, map[string]any{"file": c.file}); got != c.want {
			t.Errorf("%s %s : %v, veut %v", c.tool, c.file, got, c.want)
		}
	}
}

func TestJeanReflectReadFile(t *testing.T) {
	testHome(t)
	ctx := withJeanSpace(context.Background())
	ws := jeanWorkspace()
	for cmd, want := range map[string]bool{
		`type "` + filepath.Join(ws, "poster.py") + `"`: true,
		"cat poster.py":                  true,
		"type poster.py & del poster.py": false,
		`type "` + filepath.Join(AjeanHome(), "ajean.db") + `"`: false,
		"python poster.py": false,
		`copy /Y "` + filepath.Join(ws, "poster.py") + `" "` + filepath.Join(jeanScriptsDir(), "poster.py") + `"`: true,
		`copy "` + filepath.Join(jeanScriptsDir(), "a.py") + `" "` + filepath.Join(ws, "a.py") + `"`:              false,
	} {
		if got := jeanReflectReadFile(ctx, "bash", map[string]any{"command": cmd}); got != want {
			t.Errorf("%q : %v, veut %v", cmd, got, want)
		}
	}
}

func TestJeanRuleTiretSimpleEpargneLesNoms(t *testing.T) {
	f := newJeanStreamFilter([]jeanRule{{Kind: "replace", From: " - ", To: ", "}})
	in := "Voici la vue - en direct : ![webcam](capture-20261003-182751.png) à Saint-Jean"
	got := f.Push(in) + f.Flush()
	want := "Voici la vue, en direct : ![webcam](capture-20261003-182751.png) à Saint-Jean"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
