package ajean

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestArgValueLastWins(t *testing.T) {
	a := []string{"bin", "--port", "8080", "--host", "0.0.0.0", "--port", "9000"}
	if argValue(a, "--port") != "9000" || argValue(a, "--host") != "0.0.0.0" || argValue(a, "--nope") != "" {
		t.Fatal("argValue")
	}
}

// #80 : port occupé → erreur claire ; port libre → OK.
func TestWaitPortFree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if err := waitPortFree("0.0.0.0", port, 600*time.Millisecond); err == nil || !strings.Contains(err.Error(), port) {
		t.Fatalf("port occupé non détecté : %v", err)
	}
	ln.Close()
	if err := waitPortFree("0.0.0.0", port, time.Second); err != nil {
		t.Fatalf("port libre refusé : %v", err)
	}
}

// Un moteur qui libère le port pendant l'attente ne doit pas bloquer le démarrage.
func TestWaitPortFreeReleasedDuringWait(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	go func() { time.Sleep(700 * time.Millisecond); ln.Close() }()
	if err := waitPortFree("127.0.0.1", port, 5*time.Second); err != nil {
		t.Fatalf("attendu libre après fermeture : %v", err)
	}
}
