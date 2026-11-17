package llm

import "testing"

func TestPartialDesc(t *testing.T) {
	for in, want := range map[string]string{
		`{"desc`: "",
		`{"description": "Recherche de l'espace di`:       "Recherche de l'espace",
		`{"description": "Recherche de l'espace disque"`:  "Recherche de l'espace disque",
		`{"action":"run","description":"Lecture \"x\" du`: `Lecture "x"`,
		`{"description":"Mot`:                             "",
	} {
		if got := partialDesc(in); got != want {
			t.Errorf("partialDesc(%q) = %q, attendu %q", in, got, want)
		}
	}
}
