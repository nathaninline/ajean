package ajean

import "testing"

// Une URL de branche copiée depuis GitHub donne le dépôt ET la branche (#116),
// y compris quand la branche contient des « / ».
func TestSplitGitHubTreeURL(t *testing.T) {
	cases := []struct{ in, repo, ref string }{
		{"https://github.com/ifm-ai/llama.cpp/tree/model/K2Horizon", "https://github.com/ifm-ai/llama.cpp.git", "model/K2Horizon"},
		{"https://github.com/o/r/tree/dev/", "https://github.com/o/r.git", "dev"},
	}
	for _, c := range cases {
		repo, ref, ok := splitGitHubTreeURL(c.in)
		if !ok || repo != c.repo || ref != c.ref {
			t.Errorf("%s → (%q, %q, %v), attendu (%q, %q)", c.in, repo, ref, ok, c.repo, c.ref)
		}
	}
	for _, in := range []string{
		"https://github.com/o/r",
		"https://github.com/o/r.git",
		"https://github.com/o/r/blob/main/README.md",
		"https://gitlab.com/o/r/tree/dev",
	} {
		if _, _, ok := splitGitHubTreeURL(in); ok {
			t.Errorf("%s ne devrait pas être découpée", in)
		}
	}
}
