package netguard

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestControl(t *testing.T) {
	t.Setenv("AJEAN_TOOLS_LAN", "")
	cases := []struct {
		address string
		allowed bool
	}{
		{"93.184.216.34:443", true},
		{"[2606:2800:220:1:248:1893:25c8:1946]:443", true},
		{"127.0.0.1:8080", false},
		{"127.8.9.10:80", false},
		{"[::1]:80", false},
		{"[::ffff:127.0.0.1]:80", false},
		{"0.0.0.0:80", false},
		{"[::]:80", false},
		{"169.254.169.254:80", false},
		{"[fe80::1%eth0]:80", false},
		{"224.0.0.1:80", false},
		{"[ff02::1]:80", false},
		{"192.168.1.1:80", false},
		{"10.0.0.5:80", false},
		{"172.16.3.4:80", false},
		{"[fd00::1]:80", false},
		{"pas-une-ip:80", false},
		{"sans-port", false},
	}
	for _, c := range cases {
		err := Control("tcp", c.address, nil)
		if (err == nil) != c.allowed {
			t.Errorf("Control(%q) = %v, autorisé attendu : %v", c.address, err, c.allowed)
		}
	}
}

func TestControlLAN(t *testing.T) {
	t.Setenv("AJEAN_TOOLS_LAN", "1")
	if err := Control("tcp", "192.168.1.1:80", nil); err != nil {
		t.Errorf("réseau local autorisé attendu, reçu : %v", err)
	}
	if err := Control("tcp", "127.0.0.1:80", nil); err == nil {
		t.Error("la boucle locale doit rester refusée même avec AJEAN_TOOLS_LAN")
	}
}

func TestCheckURL(t *testing.T) {
	t.Setenv("AJEAN_TOOLS_LAN", "")
	ctx := context.Background()
	refused := []string{
		"",
		"example.com",
		"file:///C:/Windows/win.ini",
		"ftp://93.184.216.34/",
		"javascript:alert(1)",
		"http://",
		"http://localhost/",
		"http://LOCALHOST:8080/x",
		"http://localhost./",
		"http://app.localhost/",
		"http://127.0.0.1:8080/",
		"http://[::1]/",
		"http://[::ffff:127.0.0.1]/",
		"http://0.0.0.0/",
		"http://169.254.169.254/latest/meta-data",
		"https://192.168.0.1/",
		"http://10.1.2.3/",
	}
	for _, raw := range refused {
		if err := CheckURL(ctx, raw); err == nil {
			t.Errorf("CheckURL(%q) accepté, refus attendu", raw)
		}
	}
	for _, raw := range []string{"https://93.184.216.34/page?q=1", "http://[2606:2800:220:1:248:1893:25c8:1946]/"} {
		if err := CheckURL(ctx, raw); err != nil {
			t.Errorf("CheckURL(%q) = %v, accepté attendu", raw, err)
		}
	}
}

func TestCheckURLLAN(t *testing.T) {
	t.Setenv("AJEAN_TOOLS_LAN", "1")
	if err := CheckURL(context.Background(), "http://192.168.0.1/"); err != nil {
		t.Errorf("réseau local autorisé attendu, reçu : %v", err)
	}
}

// Branché sur un vrai client, Control coupe la connexion avant qu'elle parte.
func TestControlBlocksDial(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer srv.Close()

	client := &http.Client{Transport: &http.Transport{
		DialContext: (&net.Dialer{Timeout: 5 * time.Second, Control: Control}).DialContext,
	}}
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("la requête vers la boucle locale aurait dû échouer")
	}
	if reached {
		t.Error("le serveur local a été joint")
	}
}
