package browser

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// localServer : un serveur sur la boucle locale qui compte les connexions
// reçues. C'est ce que le proxy ne doit jamais joindre.
func localServer(t *testing.T) (addr string, conns *atomic.Int32) {
	t.Helper()
	conns = new(atomic.Int32)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "secret local")
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String(), conns
}

func testProxy(t *testing.T) *proxy {
	t.Helper()
	p, err := startProxy()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	return p
}

// via rend un client HTTP qui passe par le proxy, comme le navigateur.
func via(p *proxy) *http.Client {
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: p.addr})},
	}
}

// connect demande un tunnel au proxy et rend la connexion avec sa réponse.
func connect(t *testing.T, p *proxy, target string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()
	c, err := net.DialTimeout("tcp", p.addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	r := bufio.NewReader(c)
	resp, err := http.ReadResponse(r, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("CONNECT %s : %v", target, err)
	}
	return c, r, resp
}

// localTargets : la même adresse locale, écrite en chiffres puis par un nom
// qui n'y mène qu'après résolution.
func localTargets(addr string) []string {
	_, port, _ := net.SplitHostPort(addr)
	return []string{addr, "localhost:" + port}
}

func TestProxyRefusesLocalConnect(t *testing.T) {
	addr, conns := localServer(t)
	p := testProxy(t)
	for _, target := range localTargets(addr) {
		_, _, resp := connect(t, p, target)
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "adresse locale refusée") {
			t.Errorf("CONNECT %s = %d %q, attendu 403 et la raison", target, resp.StatusCode, body)
		}
	}
	if n := conns.Load(); n != 0 {
		t.Errorf("le serveur local a reçu %d connexion(s)", n)
	}
}

func TestProxyRefusesLocalRequest(t *testing.T) {
	addr, conns := localServer(t)
	client := via(testProxy(t))
	for _, target := range localTargets(addr) {
		resp, err := client.Get("http://" + target + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "adresse locale refusée") {
			t.Errorf("GET http://%s/ = %d %q, attendu 403 et la raison", target, resp.StatusCode, body)
		}
	}
	if n := conns.Load(); n != 0 {
		t.Errorf("le serveur local a reçu %d connexion(s)", n)
	}
}

// Sans passer par le réseau : seule l'adresse du client et la forme de la
// requête comptent ici.
func TestProxyRefusesBadRequests(t *testing.T) {
	p := testProxy(t)
	cases := []struct{ name, remote, method, target string }{
		{"client hors de la machine", "203.0.113.7:4000", http.MethodGet, "http://93.184.216.34/"},
		{"client hors de la machine, tunnel", "192.168.1.20:4000", http.MethodConnect, "93.184.216.34:443"},
		{"adresse de client illisible", "ailleurs", http.MethodGet, "http://93.184.216.34/"},
		{"requête adressée au proxy lui-même", "127.0.0.1:4000", http.MethodGet, "/"},
		{"schéma autre que http", "127.0.0.1:4000", http.MethodGet, "ftp://93.184.216.34/"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.target, nil)
		req.RemoteAddr = c.remote
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s : %d, attendu 403", c.name, rec.Code)
		}
	}
}

// Le relais lui-même, essayé contre un serveur local : la garde est levée pour
// ce test, faute d'adresse publique joignable sans Internet.
func TestProxyRelays(t *testing.T) {
	addr, conns := localServer(t)
	p := testProxy(t)
	p.dial = (&net.Dialer{Timeout: 5 * time.Second}).DialContext

	resp, err := via(p).Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "secret local" {
		t.Errorf("requête en clair = %d %q", resp.StatusCode, body)
	}

	c, r, resp := connect(t, p, addr)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT = %d, attendu 200", resp.StatusCode)
	}
	fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", addr)
	resp, err = http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	if string(body) != "secret local" {
		t.Errorf("réponse dans le tunnel = %q", body)
	}
	if n := conns.Load(); n != 2 {
		t.Errorf("le serveur a reçu %d connexion(s), attendu 2", n)
	}

	// L'arrêt du proxy ferme le tunnel resté ouvert.
	p.close()
	if _, err := r.ReadByte(); err == nil {
		t.Error("le tunnel aurait dû être fermé avec le proxy")
	}
}

func TestProxyUnreachableHost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // plus personne n'écoute à cette adresse
	p := testProxy(t)
	p.dial = (&net.Dialer{Timeout: 5 * time.Second}).DialContext

	if _, _, resp := connect(t, p, addr); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("CONNECT vers un hôte injoignable = %d, attendu 502", resp.StatusCode)
	}
	resp, err := via(p).Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("requête vers un hôte injoignable = %d, attendu 502", resp.StatusCode)
	}
}
