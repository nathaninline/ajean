package machine

import "testing"

func TestUnit(t *testing.T) {
	for in, want := range map[string]string{"llm-server": "llm-server.service", "a@b.service": "a@b.service"} {
		if got, err := Unit(in); err != nil || got != want {
			t.Fatalf("Unit(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "-x", "a;b", "x.timer", "a b", "../x"} {
		if _, err := Unit(bad); err == nil {
			t.Fatalf("Unit(%q) accepté", bad)
		}
	}
}
