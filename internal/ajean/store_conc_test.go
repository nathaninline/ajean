package ajean

import (
	"sync"
	"testing"
	"time"
)

// Deux « process » logiques qui martèlent la base en parallèle ne doivent ni
// s'exclure ni perdre d'écriture : c'est le scénario du serveur, où le service
// de lien tourne pendant qu'on tape des commandes.
func TestAccesConcurrent(t *testing.T) {
	testHome(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = SetConfigKey("K", "v")
				_ = ReadConfig()
			}
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("blocage sous accès concurrent")
	}
	if ReadConfig()["K"] != "v" {
		t.Fatal("écriture perdue")
	}
}
