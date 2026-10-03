package ajean

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #106 : aucun schéma d'outil Jean ne doit porter « required: null ».
func TestJeanToolSchemasSansRequiredNull(t *testing.T) {
	for _, tl := range jeanTools() {
		b, _ := json.Marshal(tl.Function.Parameters)
		if strings.Contains(string(b), `"required":null`) {
			t.Errorf("%s : %s", tl.Function.Name, b)
		}
	}
}

// Issue #105 : l'arrêt du battement attend la goroutine, même bloquée sur le verrou.
func TestHeartbeatStopAttendLaGoroutine(t *testing.T) {
	w := &hbWriter{}
	mu, stop := sseHeartbeat(w, nil)
	mu.Lock()
	time.Sleep(4100 * time.Millisecond) // un battement attend maintenant le verrou
	doneStop := make(chan struct{})
	go func() { stop(); close(doneStop) }()
	time.Sleep(50 * time.Millisecond)
	mu.Unlock()
	<-doneStop
	w.closed = true // la réponse est rendue : plus aucune écriture permise
	time.Sleep(100 * time.Millisecond)
	if w.lateWrites > 0 {
		t.Fatalf("%d écriture(s) après l'arrêt", w.lateWrites)
	}
	stop() // deux arrêts : sans panique
}

type hbWriter struct {
	closed     bool
	lateWrites int
}

func (h *hbWriter) Header() http.Header { return http.Header{} }
func (h *hbWriter) WriteHeader(int)     {}
func (h *hbWriter) Write(b []byte) (int, error) {
	if h.closed {
		h.lateWrites++
	}
	return len(b), nil
}
