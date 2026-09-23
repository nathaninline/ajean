package ajean

import (
	"net/http/httptest"
	"testing"
)

// Une page web tierce ne doit pas pouvoir piloter l'API (CSRF / DNS rebinding),
// sans gêner l'UI servie par ajean lui-même ni les clients hors navigateur.
func TestCrossSiteReject(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		hdr     map[string]string
		keyed   bool
		blocked bool
	}{
		{"site tiers (Origin)", "127.0.0.1:8090", map[string]string{"Origin": "https://evil.example"}, false, true},
		{"site tiers (Sec-Fetch-Site)", "127.0.0.1:8090", map[string]string{"Sec-Fetch-Site": "cross-site"}, false, true},
		{"origine null (iframe sandbox)", "127.0.0.1:8090", map[string]string{"Origin": "null"}, false, true},
		{"DNS rebinding sans clé", "evil.example.com:8090", map[string]string{"Origin": "http://evil.example.com:8090"}, false, true},
		{"domaine public AVEC clé (proxy)", "ia.example.com", map[string]string{"Origin": "https://ia.example.com"}, true, false},
		{"UI locale", "127.0.0.1:8090", map[string]string{"Origin": "http://127.0.0.1:8090", "Sec-Fetch-Site": "same-origin"}, false, false},
		{"script sans Origin", "localhost:8090", nil, false, false},
		{"LAN par IP", "192.168.1.50:8090", map[string]string{"Origin": "http://192.168.1.50:8090"}, false, false},
		{"nom sans point", "srvnathan:8090", nil, false, false},
		{"nom .local", "srvnathan.local:8090", nil, false, false},
		{"IPv6", "[::1]:8090", map[string]string{"Origin": "http://[::1]:8090"}, false, false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "http://"+c.host+"/api/chat", nil)
		r.Host = c.host
		for k, v := range c.hdr {
			r.Header.Set(k, v)
		}
		got := crossSiteReject(r, c.keyed) != ""
		if got != c.blocked {
			t.Errorf("%s : bloqué=%v, attendu %v", c.name, got, c.blocked)
		}
	}
}
