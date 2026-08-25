package ajean

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemCryptoSelfTest(t *testing.T) {
	if err := memCryptoSelfTest(); err != nil {
		t.Fatalf("auto-test crypto échoué : %v", err)
	}
}

func TestEncDecPageRoundTrip(t *testing.T) {
	dek, _ := randBytes(memDEKLen)
	cases := []string{"", "court", "# titre\n\nlignes\néàçù ✔\n", strings.Repeat("x", 100000)}
	for _, c := range cases {
		enc, err := encPage(dek, []byte(c))
		if err != nil {
			t.Fatalf("encPage: %v", err)
		}
		if !looksEncrypted(enc) {
			t.Fatal("looksEncrypted devrait être vrai sur une page chiffrée")
		}
		dec, err := decPage(dek, enc)
		if err != nil {
			t.Fatalf("decPage: %v", err)
		}
		if !bytes.Equal(dec, []byte(c)) {
			t.Fatalf("round-trip incohérent pour %q", c[:min(len(c), 20)])
		}
	}
}

func TestDecPagePlaintextDetected(t *testing.T) {
	dek, _ := randBytes(memDEKLen)
	if _, err := decPage(dek, []byte("# page en clair")); err != errNotEncrypted {
		t.Fatalf("attendu errNotEncrypted, obtenu %v", err)
	}
}

func TestDecPageWrongKeyFails(t *testing.T) {
	dek, _ := randBytes(memDEKLen)
	bad, _ := randBytes(memDEKLen)
	enc, _ := encPage(dek, []byte("secret"))
	if _, err := decPage(bad, enc); err == nil {
		t.Fatal("une clé erronée n'aurait pas dû déchiffrer")
	}
}

func TestDecPageTamperedFails(t *testing.T) {
	dek, _ := randBytes(memDEKLen)
	enc, _ := encPage(dek, []byte("secret"))
	enc[len(enc)-1] ^= 0xFF // altère le tag
	if _, err := decPage(dek, enc); err == nil {
		t.Fatal("un blob altéré n'aurait pas dû déchiffrer")
	}
}

func TestWriteFileVerified(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "f.bin")
	data := []byte("contenu vérifié éàç")
	if err := memWriteFileVerified(p, data, 0o600); err != nil {
		t.Fatalf("écriture vérifiée: %v", err)
	}
	back, _ := os.ReadFile(p)
	if !bytes.Equal(back, data) {
		t.Fatal("relecture ≠ écrit")
	}
}
