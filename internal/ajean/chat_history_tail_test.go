package ajean

import "testing"

// historyCut ne garde que les N derniers échanges (un échange commence à `user`).
func TestHistoryCut(t *testing.T) {
	var log []LogEvent
	seq := 0
	add := func(d map[string]any) { seq++; log = append(log, LogEvent{Seq: seq, Delta: d}) }
	for i := 0; i < 5; i++ {
		add(map[string]any{"user": "q"})
		add(map[string]any{"content": "r"})
		add(map[string]any{"turn_done": true})
	}
	// 5 échanges, seq 1..15 ; les 2 derniers commencent aux seq 10 et 13.
	if cut, hidden := historyCut(log, 2); cut != 9 || hidden != 3 {
		t.Fatalf("tail=2 : cut=%d hidden=%d, attendu 9 et 3", cut, hidden)
	}
	if cut, hidden := historyCut(log, 5); cut != 0 || hidden != 0 {
		t.Fatalf("tail=5 (tout tient) : cut=%d hidden=%d, attendu 0 et 0", cut, hidden)
	}
	if cut, hidden := historyCut(log, 0); cut != 0 || hidden != 0 {
		t.Fatalf("tail=0 : pas de coupure attendue (cut=%d hidden=%d)", cut, hidden)
	}
	// Les événements rejoués après la coupure commencent bien au 4e échange.
	evs := coalesceReplay(log, 9)
	if u, _ := evs[0]["user"].(string); u != "q" || evs[0]["seq"] != 10 {
		t.Fatalf("premier événement rejoué inattendu : %v", evs[0])
	}
}
