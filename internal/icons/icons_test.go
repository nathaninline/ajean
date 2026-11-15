package icons

import "testing"

func TestIcon(t *testing.T) {
	for in, want := range map[string]string{"wallet": "wallet", " Chart ": "chart", "💸": Default, "": Default, "inconnu": Default} {
		if got := Icon(in); got != want {
			t.Errorf("Icon(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestColor(t *testing.T) {
	for in, want := range map[string]string{"#2F7D5B": "#2f7d5b", "#abc": "#aabbcc", " #000000 ": "#000000", "red": "", "#12345": "", "": "", "#12345g": ""} {
		if got := Color(in); got != want {
			t.Errorf("Color(%q) = %q, attendu %q", in, got, want)
		}
	}
}
