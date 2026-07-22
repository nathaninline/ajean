package jean

import (
	"strings"
	"testing"
	"time"
)

func TestIsHexPub(t *testing.T) {
	ok := strings.Repeat("ab", 32) // 64 hex chars
	cases := map[string]bool{
		ok:                              true,
		strings.ToUpper(ok):             true,
		ok[:62]:                         false, // trop court
		ok + "cd":                       false, // trop long
		strings.Repeat("zz", 32):        false, // pas hexadécimal
		"":                              false,
		strings.Repeat("ab", 31) + "g1": false,
	}
	for in, want := range cases {
		if got := isHexPub(in); got != want {
			t.Errorf("isHexPub(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestHashPairCodeNormalise(t *testing.T) {
	// Le code est normalisé (majuscules + trim) avant hachage : l'utilisateur
	// peut le taper en minuscules ou avec des espaces autour.
	ref := hashPairCode("ABCD-1234")
	for _, v := range []string{"abcd-1234", "  ABCD-1234  ", "Abcd-1234\n"} {
		if hashPairCode(v) != ref {
			t.Errorf("hashPairCode(%q) devrait égaler hashPairCode(\"ABCD-1234\")", v)
		}
	}
	if hashPairCode("ABCD-1235") == ref {
		t.Error("codes différents → hashs différents attendus")
	}
}

func TestReplayCheck(t *testing.T) {
	replayMu.Lock()
	replaySeen = map[string]time.Time{}
	replayMu.Unlock()

	ts := time.Now().UnixMilli()
	if replayCheck("pubA", ts, "iv1") {
		t.Fatal("première vue : ne doit pas être un rejeu")
	}
	if !replayCheck("pubA", ts, "iv1") {
		t.Fatal("même (upub|ts|iv) revu : doit être détecté comme rejeu")
	}
	// Une composante différente = requête distincte, pas un rejeu.
	if replayCheck("pubA", ts, "iv2") || replayCheck("pubB", ts, "iv1") || replayCheck("pubA", ts+1, "iv1") {
		t.Error("clé différente (iv/upub/ts) ne doit pas être vue comme rejeu")
	}
}

func TestReplayCheckPurge(t *testing.T) {
	replayMu.Lock()
	replaySeen = map[string]time.Time{}
	old := time.Now().Add(-3 * e2eAuthWindowMs * time.Millisecond)
	for i := 0; i < 5000; i++ {
		replaySeen[strings.Repeat("x", 8)+string(rune(i))] = old
	}
	replayMu.Unlock()

	// Au-delà de 4096 entrées, les entrées hors fenêtre sont purgées au passage.
	replayCheck("pub", time.Now().UnixMilli(), "iv")
	replayMu.Lock()
	n := len(replaySeen)
	replayMu.Unlock()
	if n > 10 {
		t.Errorf("purge attendue des vieilles entrées, il en reste %d", n)
	}
}

func TestPairLockout(t *testing.T) {
	pairReset()
	t.Cleanup(pairReset)

	if pairLocked() {
		t.Fatal("pas de tentative : pas de verrou")
	}
	for i := 0; i < 9; i++ {
		pairRecordFail()
	}
	if pairLocked() {
		t.Fatal("9 échecs : pas encore verrouillé (seuil = 10)")
	}
	pairRecordFail()
	if !pairLocked() {
		t.Fatal("10 échecs : appairage verrouillé attendu")
	}
	// Fenêtre de 5 min expirée → le verrou saute et le compteur repart.
	pairFailMu.Lock()
	pairLockAt = time.Now().Add(-6 * time.Minute)
	pairFailMu.Unlock()
	if pairLocked() {
		t.Fatal("fenêtre expirée : le verrou doit être levé")
	}
	if pairLocked() {
		t.Fatal("après reset du compteur, toujours déverrouillé")
	}
}

func TestE2EAuthAAD(t *testing.T) {
	if string(e2eAuthAAD("pub", 42)) != "pub|42" {
		t.Errorf("AAD inattendu : %q", e2eAuthAAD("pub", 42))
	}
}
