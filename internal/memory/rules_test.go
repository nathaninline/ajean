package memory

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRules(t *testing.T) {
	setup(t)
	rule := func(from, to, why string) string {
		return run(t, "memory_rule", map[string]any{"from": from, "to": to, "why": why})
	}
	mustOK(t, rule("—", ", ", "pas de tiret cadratin"))
	mustOK(t, rule("mail", "courriel", ""))
	if out := rule("mail", "message", "plus simple"); !strings.Contains(out, "regle-2 mise à jour") {
		t.Errorf("même texte : mise à jour attendue, reçu %s", out)
	}
	mustErr(t, rule("", "x", ""), "'from' vide")
	mustErr(t, rule("chat", "chaton", ""), "contient le texte interdit")
	rs := Rules()
	if len(rs) != 2 || rs[0] != (Rule{1, "—", ", ", "pas de tiret cadratin"}) || rs[1] != (Rule{2, "mail", "message", "plus simple"}) {
		t.Fatalf("règles : %+v", rs)
	}
	if got := ApplyRules("Un mail—deux mails"); got != "Un message, deux messages" {
		t.Errorf("ApplyRules : %q", got)
	}
	if !strings.Contains(ContextBlock(), `regle-1: "—" -> ", " (pas de tiret cadratin)`) {
		t.Errorf("règles absentes du bloc de contexte :\n%s", ContextBlock())
	}
	mustOK(t, run(t, "memory_forget", map[string]any{"key": "regle-1"}))
	if err := ForgetRule(1); err == nil {
		t.Error("retirer deux fois devait échouer")
	}
	if got := ApplyRules("a — b"); got != "a — b" {
		t.Errorf("règle retirée encore appliquée : %q", got)
	}
}

// Le filtre de flux doit donner exactement ApplyRules(texte entier), quel que
// soit le découpage : à chaque octet, donc aussi au milieu d'un texte interdit
// et au milieu d'un caractère UTF-8.
func TestRuleFilterEveryChunking(t *testing.T) {
	setup(t)
	for _, r := range [][2]string{
		{"—", ", "},               // caractère de plusieurs octets
		{"mail", "courriel"},      // mot
		{"ab", "a"},               // la sortie d'un remplacement ne doit pas être relue par la même règle
		{"été", "hiver"},          // commence et finit par un caractère de plusieurs octets
		{"aa", "b"},               // occurrences qui se chevauchent
		{"courriels", "messages"}, // s'applique à la sortie d'une règle précédente
		{"…", ""},                 // suppression
	} {
		if _, err := setRule(r[0], r[1], ""); err != nil {
			t.Fatal(err)
		}
	}
	text := "Voilà — un mail… abb, aaa été—fin. Deux mails, l'ét" + "é" + " ma"
	want := ApplyRules(text)
	if want == text || !strings.Contains(want, "messages") || strings.Contains(want, "—") {
		t.Fatalf("les règles ne s'appliquent pas : %q", want)
	}

	feed := func(chunks ...string) string {
		f := NewRuleFilter()
		var out strings.Builder
		for _, c := range chunks {
			shown := f.Push(c)
			if !utf8.ValidString(shown) {
				t.Fatalf("caractère coupé à l'affichage : %q (morceaux %q)", shown, chunks)
			}
			out.WriteString(shown)
		}
		out.WriteString(f.Flush())
		return out.String()
	}

	if got := feed(text); got != want {
		t.Fatalf("en un morceau : %q, attendu %q", got, want)
	}
	bytes := make([]string, len(text))
	for i := 0; i < len(text); i++ {
		bytes[i] = text[i : i+1]
	}
	if got := feed(bytes...); got != want {
		t.Fatalf("octet par octet : %q, attendu %q", got, want)
	}
	for i := 0; i <= len(text); i++ {
		for j := i; j <= len(text); j++ {
			if got := feed(text[:i], text[i:j], text[j:]); got != want {
				t.Fatalf("coupé en %d et %d : %q, attendu %q", i, j, got, want)
			}
		}
	}
}

func TestRuleFilterWithoutRules(t *testing.T) {
	setup(t)
	f := NewRuleFilter()
	if got := f.Push("bonjour é") + f.Push("té") + f.Flush(); got != "bonjour été" {
		t.Errorf("sans règle, le texte passe tel quel : %q", got)
	}
}
