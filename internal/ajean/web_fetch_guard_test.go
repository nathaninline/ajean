package ajean

import (
	"context"
	"net"
	"net/url"
	"testing"
)

func TestWebIPBlocked(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.10", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"100.127.255.254", true},
		{"0.0.0.0", true},
		{"fe80::1", true},
		{"fd00::1", true},
		{"::ffff:127.0.0.1", true},
		{"224.0.0.1", true},
		{"8.8.8.8", false},
		{"100.128.0.1", false},
		{"2606:4700:4700::1111", false},
	}
	for _, c := range cases {
		if got := webIPBlocked(net.ParseIP(c.ip)); got != c.blocked {
			t.Errorf("webIPBlocked(%s) = %v, attendu %v", c.ip, got, c.blocked)
		}
	}
	if !webIPBlocked(nil) {
		t.Error("une adresse illisible doit être refusée")
	}
}

func TestWebDestCheck(t *testing.T) {
	t.Setenv("AJEAN_WEB_ALLOW_LOCAL", "")
	for _, raw := range []string{"http://127.0.0.1:8090/api/config", "http://[::1]/", "http://192.168.0.1/", "http://169.254.169.254/latest/meta-data/", "http://localhost:8090/"} {
		u, _ := url.Parse(raw)
		if err := webDestCheck(context.Background(), u); err == nil {
			t.Errorf("%s aurait dû être refusée", raw)
		}
	}
	u, _ := url.Parse("https://93.184.216.34/")
	if err := webDestCheck(context.Background(), u); err != nil {
		t.Errorf("une adresse publique est refusée : %v", err)
	}
	t.Setenv("AJEAN_WEB_ALLOW_LOCAL", "1")
	u, _ = url.Parse("http://127.0.0.1:8090/")
	if err := webDestCheck(context.Background(), u); err != nil {
		t.Errorf("AJEAN_WEB_ALLOW_LOCAL=1 doit lever le filtre : %v", err)
	}
}

func TestWebDialControl(t *testing.T) {
	t.Setenv("AJEAN_WEB_ALLOW_LOCAL", "")
	if err := webDialControl("tcp4", "127.0.0.1:80", nil); err == nil {
		t.Error("connexion à la boucle locale acceptée")
	}
	if err := webDialControl("tcp6", "[fe80::1%eth0]:80", nil); err == nil {
		t.Error("connexion lien-local acceptée")
	}
	if err := webDialControl("tcp4", "8.8.8.8:443", nil); err != nil {
		t.Errorf("connexion publique refusée : %v", err)
	}
}
