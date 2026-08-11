package nodewire

import (
	"bytes"
	"testing"
)

// La clé de canal doit être IDENTIQUE des deux bouts (poste priv + agent pub =
// agent priv + poste pub).
func TestChannelKeySymmetric(t *testing.T) {
	aPriv, aPub, _ := GenKeyPair()
	bPriv, bPub, _ := GenKeyPair()
	ka, err := ChannelKey(aPriv, bPub)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := ChannelKey(bPriv, aPub)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ka, kb) {
		t.Fatal("les deux bouts n'obtiennent pas la même clé de canal")
	}
}

// Un aller-retour chiffré passe ; un frame rejoué est rejeté.
func TestChanRoundTripAndReplay(t *testing.T) {
	aPriv, aPub, _ := GenKeyPair()
	bPriv, bPub, _ := GenKeyPair()
	ka, _ := ChannelKey(aPriv, bPub) // poste
	kb, _ := ChannelKey(bPriv, aPub) // agent

	poste, _ := NewChan(ka, true)
	agent, _ := NewChan(kb, false)

	f := poste.Seal([]byte("dir C:\\"))
	got, err := agent.Open(f)
	if err != nil || string(got) != "dir C:\\" {
		t.Fatalf("aller-retour: %q err=%v", got, err)
	}
	// Rejeu du même frame → rejeté (compteur non croissant).
	if _, err := agent.Open(f); err == nil {
		t.Fatal("un frame rejoué aurait dû être rejeté")
	}
	// Sens inverse.
	f2 := agent.Seal([]byte("exit: 0"))
	got2, err := poste.Open(f2)
	if err != nil || string(got2) != "exit: 0" {
		t.Fatalf("retour: %q err=%v", got2, err)
	}
}

// SealTo doit produire le format ephPub32||nonce12||ct que l'agent sait ouvrir.
func TestSealToFormat(t *testing.T) {
	_, pub, _ := GenKeyPair()
	blob, err := SealTo(pub, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if len(blob) < 32+12+16 {
		t.Fatalf("sceau trop court: %d octets", len(blob))
	}
}
