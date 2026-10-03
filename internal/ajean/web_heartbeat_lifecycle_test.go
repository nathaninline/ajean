package ajean

import (
	"net/http/httptest"
	"testing"
	"time"
)

// A pending tick must finish before stop allows the HTTP handler to return.
func TestHeartbeatStopJoinsPendingTick(t *testing.T) {
	w := httptest.NewRecorder()
	mu, stop := sseHeartbeat(w, w)
	mu.Lock()
	time.Sleep(4200 * time.Millisecond)
	returned := make(chan struct{})
	go func() { stop(); close(returned) }()
	select {
	case <-returned:
		mu.Unlock()
		t.Fatal("stop returned while a heartbeat still waited to use the writer")
	case <-time.After(100 * time.Millisecond):
	}
	mu.Unlock()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("stop did not join the heartbeat")
	}
	stop() // repeated cleanup is safe
}
