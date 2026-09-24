package ajean

import (
	"strings"
	"testing"
	"time"
)

func kinds(ks []key) []keyKind {
	var out []keyKind
	for _, k := range ks {
		out = append(out, k.kind)
	}
	return out
}

func TestParseKeysBasics(t *testing.T) {
	ks, rest := parseKeys([]byte("aé\x7f\x1b[D\x1b[1;5C\x1b[3~\x03\r"), false)
	if len(rest) != 0 {
		t.Fatalf("reste %q", rest)
	}
	want := []keyKind{keyRune, keyRune, keyBackspace, keyLeft, keyWordRight, keyDelete, keyCtrlC, keyEnter}
	if got := kinds(ks); len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("touche %d : got %v want %v", i, got[i], want[i])
			}
		}
	}
	if ks[1].r != 'é' {
		t.Fatalf("accent perdu : %q", ks[1].r)
	}
}

func TestParseKeysPartialEscape(t *testing.T) {
	ks, rest := parseKeys([]byte("x\x1b["), false)
	if len(ks) != 1 || string(rest) != "\x1b[" {
		t.Fatalf("séquence partielle mal gérée : %v %q", kinds(ks), rest)
	}
	ks, rest = parseKeys(append(rest, 'A'), false)
	if len(ks) != 1 || ks[0].kind != keyUp || len(rest) != 0 {
		t.Fatalf("suite de séquence : %v %q", kinds(ks), rest)
	}
	// Échap seul en fin de flux.
	ks, _ = parseKeys([]byte{0x1b}, true)
	if len(ks) != 1 || ks[0].kind != keyEsc {
		t.Fatalf("Échap : %v", kinds(ks))
	}
}

func TestParseKeysPaste(t *testing.T) {
	ks, rest := parseKeys([]byte("\x1b[200~ligne 1\nligne 2\x1b[201~z"), false)
	if len(rest) != 0 || len(ks) != 2 || ks[0].kind != keyPaste || ks[0].text != "ligne 1\nligne 2" || ks[1].r != 'z' {
		t.Fatalf("bracketed paste : %v %q", kinds(ks), rest)
	}
	// Collage incomplet : on attend la suite.
	if ks, rest := parseKeys([]byte("\x1b[200~abc"), false); len(ks) != 0 || len(rest) == 0 {
		t.Fatalf("collage partiel : %v %q", kinds(ks), rest)
	}
	// Collage sans bracketed paste (conhost) : un CR au milieu n'envoie pas.
	ks, _ = parseKeys([]byte("un\r\ndeux"), false)
	if len(ks) != 1 || ks[0].kind != keyPaste {
		t.Fatalf("collage brut : %v", kinds(ks))
	}
	// Frappe normale d'Entrée : pas un collage.
	ks, _ = parseKeys([]byte("\r"), false)
	if len(ks) != 1 || ks[0].kind != keyEnter {
		t.Fatalf("Entrée : %v", kinds(ks))
	}
}

func TestParseKeysAltEnterNewline(t *testing.T) {
	for _, in := range []string{"\x1b\r", "\n", "\x1b[13;2u"} {
		ks, _ := parseKeys([]byte(in), false)
		if len(ks) != 1 || ks[0].kind != keyNewline {
			t.Fatalf("%q : %v", in, kinds(ks))
		}
	}
}

func TestEditorLayoutWrapAndCursor(t *testing.T) {
	colorOn = false
	e := &lineEditor{prompt: "› ", cont: "  "}
	e.buf = []rune("abcdefgh")
	e.cur = len(e.buf)
	rows, r, c := e.layout(6, false)
	// "› abcd" (6) | "efgh" : curseur après h.
	if len(rows) != 2 || rows[0] != "› abcd" || rows[1] != "efgh" || r != 1 || c != 4 {
		t.Fatalf("rows=%q cur=%d,%d", rows, r, c)
	}
	e.buf = []rune("ab\ncd")
	e.cur = 3
	rows, r, c = e.layout(40, false)
	if len(rows) != 2 || rows[1] != "  cd" || r != 1 || c != 2 {
		t.Fatalf("multi-ligne rows=%q cur=%d,%d", rows, r, c)
	}
	// Ligne pleine pile à la largeur : le curseur passe à la ligne suivante.
	e.buf = []rune("abcd")
	e.cur = 4
	rows, r, c = e.layout(6, false)
	if r != 1 || c != 0 || len(rows) != 2 {
		t.Fatalf("bord droit rows=%q cur=%d,%d", rows, r, c)
	}
}

func TestEditorWordAndLineMoves(t *testing.T) {
	b := []rune("git commit -m fix\nsecond line")
	if wordLeft(b, 17) != 14 || wordRight(b, 0) != 3 {
		t.Fatalf("mots : %d %d", wordLeft(b, 17), wordRight(b, 0))
	}
	if lineStart(b, 20) != 18 || lineEnd(b, 2) != 17 {
		t.Fatal("début/fin de ligne")
	}
	if got := moveVertical(b, 20, -1); got != 2 {
		t.Fatalf("haut : %d", got)
	}
}

func TestEditorGhostCompletion(t *testing.T) {
	e := &lineEditor{completions: []string{"/help", "/new", "/model", "/copy"}}
	e.buf = []rune("/mo")
	e.cur = 3
	if g := e.ghost(); g != "del" {
		t.Fatalf("ghost %q", g)
	}
	e.buf, e.cur = []rune("/x"), 2
	if g := e.ghost(); g != "" {
		t.Fatalf("ghost inattendu %q", g)
	}
}

func TestMarkdownLines(t *testing.T) {
	colorOn = false
	m := &mdRenderer{}
	cases := []struct{ in, want string }{
		{"# Titre", "Titre"},
		{"- un **gras** et `code`", "• un gras et code"},
		{"  - sous-liste", "  ◦ sous-liste"},
		{"1. premier", "1. premier"},
		{"> citation", "│ citation"},
		{"voir [doc](https://x.y)", "voir doc (https://x.y)"},
		{"- [x] fait", "☑ fait"},
	}
	for _, c := range cases {
		if got := m.line(c.in, 80); got != c.want {
			t.Fatalf("%q → %q, attendu %q", c.in, got, c.want)
		}
	}
	if got := m.line("```go", 80); got != "╭─ go" || !m.inCode {
		t.Fatalf("ouverture de bloc : %q", got)
	}
	if got := m.line("x := **pas du gras**", 80); got != "│ x := **pas du gras**" {
		t.Fatalf("code interprété : %q", got)
	}
	if got := m.line("```", 80); got != "╰─" || m.inCode {
		t.Fatalf("fermeture de bloc : %q", got)
	}
}

func TestToolResultLinesBash(t *testing.T) {
	colorOn = false
	res := "exit: 0\n\nstdout:\na\nb\nc\nd\ne\nf"
	out := toolResultLines(&ToolUsedEvent{Name: "bash", Result: res, Done: true}, 80, 1200*time.Millisecond)
	if !strings.Contains(out, "ok · 6 lignes · 1,2 s") || !strings.Contains(out, "… 2 lignes de plus") {
		t.Fatalf("résumé bash :\n%s", out)
	}
	out = toolResultLines(&ToolUsedEvent{Name: "bash", Result: "exit: 2\n\nstderr:\nboom", Done: true}, 80, time.Second)
	if !strings.Contains(out, "code de sortie 2") || !strings.Contains(out, "boom") {
		t.Fatalf("échec bash :\n%s", out)
	}
	out = toolResultLines(&ToolUsedEvent{Name: "edit", Done: true, Diff: []DiffLine{{Op: "-", Text: "old"}, {Op: "+", Text: "new"}}}, 80, 0)
	if !strings.Contains(out, "+1 −1") || !strings.Contains(out, "- old") || !strings.Contains(out, "+ new") {
		t.Fatalf("diff :\n%s", out)
	}
}

func TestTerminalCapsTools(t *testing.T) {
	names := func(c Caps) string {
		var n []string
		for _, tl := range EnabledTools(c) {
			n = append(n, tl.Function.Name)
		}
		return strings.Join(n, ",")
	}
	if got := names(Caps{Agent: true, Terminal: true}); !strings.HasPrefix(got, "bash,write,edit") || strings.Contains(got, "task_") || strings.Contains(got, "mem_") || strings.Contains(got, "recall") {
		t.Fatalf("outils terminal : %s", got)
	}
	if got := names(Caps{Terminal: true}); got != "" {
		t.Fatalf("outils sans agent : %s", got)
	}
	p := baseSystemPrompt(Caps{Agent: true, Terminal: true})
	if !strings.Contains(p, "terminal") || strings.Contains(p, "task_create") || strings.Contains(p, "memory") {
		t.Fatalf("prompt terminal :\n%s", p)
	}
	if machineSystemPrompt(Caps{Agent: true, Terminal: true}) != "" {
		t.Fatal("le briefing machine (workspace, scripts, mémoire) ne doit pas partir en mode terminal")
	}
}

func TestFmtHelpers(t *testing.T) {
	if fmtTokens(950) != "950" || fmtTokens(1500) != "1,5k" || fmtTokens(32768) != "33k" || fmtTokens(4000) != "4k" {
		t.Fatalf("fmtTokens %s %s %s %s", fmtTokens(950), fmtTokens(1500), fmtTokens(32768), fmtTokens(4000))
	}
	if fmtDuration(2500*time.Millisecond) != "2,5 s" || fmtDuration(75*time.Second) != "1 min 15 s" {
		t.Fatal("fmtDuration")
	}
	if visibleWidth("\x1b[36mab\x1b[0m日") != 4 {
		t.Fatal("visibleWidth")
	}
}

func TestParseKeysRepeatedEnterIsNotPaste(t *testing.T) {
	ks, _ := parseKeys([]byte("\r\r"), false)
	if len(ks) != 2 || ks[0].kind != keyEnter || ks[1].kind != keyEnter {
		t.Fatalf("Entrée répétée : %v", kinds(ks))
	}
	ks, _ = parseKeys([]byte("ok\r"), false)
	if len(ks) != 3 || ks[2].kind != keyEnter {
		t.Fatalf("frappe + Entrée : %v", kinds(ks))
	}
}

// En mode terminal, la compaction ne doit pas archiver de blocs « recall » :
// l'outil recall n'y est pas fourni, le modèle ne pourrait pas les rappeler.
func TestTerminalHasNoRecallTool(t *testing.T) {
	for _, tl := range EnabledTools(Caps{Agent: true, Terminal: true}) {
		if strings.HasPrefix(tl.Function.Name, "recall") {
			t.Fatalf("outil %s en mode terminal", tl.Function.Name)
		}
	}
}
