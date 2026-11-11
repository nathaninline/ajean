package ajean

import (
	"math"
	"testing"
	"time"
)

func TestMeasuredGenRate(t *testing.T) {
	t0 := time.Unix(1000, 0)
	first, last := t0.Add(4500*time.Millisecond), t0.Add(15*time.Second)
	near := func(got, want float64) bool { return math.Abs(got-want) < 0.05 }

	// API distante : 247 tokens sur les 15 s de la requête, pas sur les 10,5 s visibles.
	if got := measuredGenRate("https://api.exemple.com/v1/chat/completions", 247, t0, first, last); !near(got, 16.47) {
		t.Errorf("distante = %.2f, attendu 16,47", got)
	}
	// Serveur local : décodage seul, la lecture du prompt n'est pas comptée.
	for _, u := range []string{"http://127.0.0.1:8000/v1/chat/completions", "http://localhost:8000/v1/chat/completions", "http://[::1]:8000/v1/chat/completions"} {
		if got := measuredGenRate(u, 247, t0, first, last); !near(got, 23.43) {
			t.Errorf("%s = %.2f, attendu 23,43", u, got)
		}
	}
	if got := measuredGenRate("https://api.exemple.com/v1", 1, t0, first, last); got != 0 {
		t.Errorf("un seul token = %.2f, attendu 0", got)
	}
	if got := measuredGenRate("https://api.exemple.com/v1", 50, t0, time.Time{}, last); got != 0 {
		t.Errorf("aucun token reçu = %.2f, attendu 0", got)
	}
	if remoteEndpoint("") || remoteEndpoint("http://0.0.0.0:1/v1") || !remoteEndpoint("http://192.168.1.20:8000/v1") {
		t.Error("remoteEndpoint : classement inattendu")
	}
}
