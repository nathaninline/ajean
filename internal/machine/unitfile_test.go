package machine

import (
	"slices"
	"testing"
)

func TestSplitEnv(t *testing.T) {
	got := splitEnv(`"A=1 2" B=3 'C=x"y'`)
	want := []string{"A=1 2", "B=3", `C=x"y`}
	if !slices.Equal(got, want) {
		t.Fatalf("splitEnv = %q, attendu %q", got, want)
	}
	if q := quoteEnv(`A=dit "oui" \o/`); q != `"A=dit \"oui\" \\o/"` {
		t.Fatalf("quoteEnv = %s", q)
	}
	if back := splitEnv(quoteEnv(`A=dit "oui" \o/`)); len(back) != 1 || back[0] != `A=dit "oui" \o/` {
		t.Fatalf("aller-retour : %q", back)
	}
}
