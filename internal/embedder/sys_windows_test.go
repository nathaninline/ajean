package embedder

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// Si AJEAN disparaît sans avoir pu arrêter son moteur (fenêtre fermée,
// processus tué), Windows doit l'arrêter pour lui.
func TestEngineDiesWithItsParent(t *testing.T) {
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(os.Args[0], strconv.Itoa(port))
	parent.Env = append(os.Environ(), fakeEnv+"="+fakeParent)
	if err := parent.Run(); err != nil {
		t.Fatalf("le faux AJEAN n'a pas pu lancer son moteur : %v", err)
	}
	// À la sortie du parent, le serveur répondait. Il ne doit plus tenir son port.
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("le moteur a survécu à son parent")
		}
	}
}
