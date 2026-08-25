package ajean

import (
	"os"
	"testing"
)

// La case « chiffré » (health.Fully) ne doit être vraie QUE si TOUT est chiffré.
func TestFullyReflectsRealState(t *testing.T) {
	testHome(t)
	clearMemDEK()
	MemAdd("a.md", "# A\n")
	putBytes(bkChat, "conversation", []byte(`{"id":"s","log":[]}`))
	if _, err := EnableMemEncryption("pw"); err != nil {
		t.Fatal(err)
	}
	if !memHealth().Fully {
		t.Fatal("après chiffrement complet, Fully devrait être vrai")
	}
	// Une page en clair réapparue → Fully faux.
	p, _ := safeMemPath("a.md")
	os.WriteFile(p, []byte("# clair"), 0o600)
	if memHealth().Fully {
		t.Fatal("une page en clair aurait dû rendre Fully faux")
	}
	// Remets la page chiffrée, salis une conversation → Fully faux aussi.
	writeMemFile("a.md", []byte("# A\n"))
	putBytes(bkChatHist, "x", []byte("archive en clair"))
	if memHealth().Fully {
		t.Fatal("une conversation en clair aurait dû rendre Fully faux")
	}
}

// Les conversations (fil courant + archives) doivent être chiffrées à
// l'activation, illisibles verrouillé, et intactes après déchiffrement.
func TestConversationEncryptionLifecycle(t *testing.T) {
	testHome(t)
	clearMemDEK()

	// Un fil courant + une archive.
	putBytes(bkChat, "conversation", []byte(`{"id":"s1","log":[{"seq":1}],"messages":[]}`))
	arch := &convArchive{ID: "a1", Title: "Secret client", SavedAt: 42, Turns: 3}
	if err := saveArchive(arch); err != nil {
		t.Fatal(err)
	}
	// Une page mémoire, pour activer le chiffrement (il chiffre tout).
	if err := MemAdd("note.md", "# Note\n"); err != nil {
		t.Fatal(err)
	}

	if _, err := EnableMemEncryption("pw"); err != nil {
		t.Fatal(err)
	}

	// Sur disque, les valeurs de conversation doivent être chiffrées (magic).
	if raw := getBytes(bkChat, "conversation"); !looksEncrypted(raw) {
		t.Fatal("fil courant non chiffré sur disque")
	}
	if raw := getBytes(bkChatHist, "a1"); !looksEncrypted(raw) {
		t.Fatal("archive non chiffrée sur disque")
	}
	if raw := getBytes(bkChatMeta, "a1"); !looksEncrypted(raw) {
		t.Fatal("index de session non chiffré sur disque")
	}

	// Déverrouillé : lisible.
	if a, ok := loadArchive("a1"); !ok || a.Title != "Secret client" {
		t.Fatal("archive illisible alors que déverrouillé")
	}
	if list := listArchives(); len(list) != 1 || list[0].Title != "Secret client" {
		t.Fatalf("liste des sessions incorrecte déverrouillé: %+v", list)
	}

	// Verrouillé : plus rien de lisible, mais rien de perdu.
	clearMemDEK()
	if _, ok := loadArchive("a1"); ok {
		t.Fatal("archive lisible alors que verrouillé")
	}
	if list := listArchives(); len(list) != 0 {
		t.Fatal("les sessions devraient être masquées quand verrouillé")
	}
	// putStoreBytes doit REFUSER d'écrire du clair quand verrouillé.
	if err := putStoreBytes(bkChat, "conversation", []byte("clair")); err != errStoreLocked {
		t.Fatalf("écriture clair aurait dû être refusée, obtenu %v", err)
	}
	if raw := getBytes(bkChat, "conversation"); !looksEncrypted(raw) {
		t.Fatal("le blob chiffré a été écrasé alors que verrouillé")
	}

	// Re-déverrouille et déchiffre : tout revient en clair et intact.
	v, _ := loadVault()
	dek, _, err := v.unlockWith("pw")
	if err != nil {
		t.Fatal(err)
	}
	setMemDEK(dek)
	if err := DisableMemEncryption(); err != nil {
		t.Fatal(err)
	}
	if raw := getBytes(bkChatHist, "a1"); looksEncrypted(raw) {
		t.Fatal("archive encore chiffrée après déchiffrement")
	}
	if a, ok := loadArchive("a1"); !ok || a.Title != "Secret client" {
		t.Fatal("archive perdue après déchiffrement")
	}
}
