package ajean

import (
	"os"
	"strings"
	"testing"
)

// seedPages écrit quelques pages en clair dans un $AJEAN_HOME de test.
func seedPages(t *testing.T) map[string]string {
	t.Helper()
	pages := map[string]string{
		"projet.md":  "# Projet\n\nUn secret très important à ne jamais perdre.\n",
		"copine.md":  "# Perso\n\nAnniversaire le 3 mars. éàçù ✔\n",
		"vide.md":    "# Vide\n",
	}
	for name, content := range pages {
		if err := MemAdd(name, content); err != nil {
			t.Fatalf("MemAdd %s: %v", name, err)
		}
	}
	return pages
}

func TestEnableEncryptReadDisable(t *testing.T) {
	testHome(t)
	clearMemDEK()
	pages := seedPages(t)

	rec, err := EnableMemEncryption("motdepasse-fort")
	if err != nil {
		t.Fatalf("EnableMemEncryption: %v", err)
	}
	if strings.TrimSpace(rec) == "" {
		t.Fatal("clé de récupération vide")
	}
	if !memEncActive() || !memUnlocked() {
		t.Fatal("devrait être actif et déverrouillé après activation")
	}

	// Sur disque, les pages doivent être chiffrées (magic présent).
	for name := range pages {
		p, _ := safeMemPath(name)
		raw, _ := os.ReadFile(p)
		if !looksEncrypted(raw) {
			t.Fatalf("page %s pas chiffrée sur disque", name)
		}
	}
	// Mais lisibles via l'API (déverrouillé).
	for name, want := range pages {
		if got := MemContent(name); got != want {
			t.Fatalf("contenu %s après chiffrement: %q != %q", name, got, want)
		}
	}

	// Verrouillage : plus rien de lisible, mais on ne perd rien.
	clearMemDEK()
	if got := MemContent("projet.md"); got != "" {
		t.Fatal("une page chiffrée verrouillée ne devrait pas être lisible")
	}
	h := memHealth()
	if !h.Locked || h.Readable != 0 || h.Unreadable != len(pages) {
		t.Fatalf("santé verrouillée inattendue: %+v", h)
	}

	// Déverrouillage via mot de passe.
	v, _ := loadVault()
	dek, _, err := v.unlockWith("motdepasse-fort")
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}
	setMemDEK(dek)
	if got := MemContent("copine.md"); got != pages["copine.md"] {
		t.Fatal("contenu illisible après déverrouillage")
	}

	// Déchiffrement complet : retour en clair, keyvault retiré.
	if err := DisableMemEncryption(); err != nil {
		t.Fatalf("DisableMemEncryption: %v", err)
	}
	if memEncActive() {
		t.Fatal("chiffrement devrait être désactivé")
	}
	for name, want := range pages {
		p, _ := safeMemPath(name)
		raw, _ := os.ReadFile(p)
		if looksEncrypted(raw) {
			t.Fatalf("page %s encore chiffrée après déchiffrement", name)
		}
		if got := MemContent(name); got != want {
			t.Fatalf("contenu %s après déchiffrement altéré", name)
		}
	}
	if vaultExists() {
		t.Fatal("le keyvault aurait dû être retiré")
	}
}

// Récupération : après chiffrement, on doit pouvoir rouvrir avec la clé de
// récupération même sans le mot de passe.
func TestUnlockWithRecoveryAfterEnable(t *testing.T) {
	testHome(t)
	clearMemDEK()
	seedPages(t)
	rec, err := EnableMemEncryption("pw")
	if err != nil {
		t.Fatal(err)
	}
	clearMemDEK()
	v, _ := loadVault()
	if _, _, err := v.unlockWith(normalizeRecovery(rec)); err != nil {
		t.Fatalf("récupération après activation: %v", err)
	}
}

// Sécurité : quand la mémoire est chiffrée ET verrouillée, toute écriture doit
// être REFUSÉE (jamais écrire en clair une donnée censée être chiffrée).
func TestWriteRefusedWhenLocked(t *testing.T) {
	testHome(t)
	clearMemDEK()
	seedPages(t)
	if _, err := EnableMemEncryption("pw"); err != nil {
		t.Fatal(err)
	}
	clearMemDEK() // verrouille
	if err := MemAdd("nouvelle.md", "contenu"); err == nil {
		t.Fatal("MemAdd aurait dû être refusé (mémoire verrouillée)")
	}
	if err := MemSave("projet.md", "", "écrasé"); err == nil {
		t.Fatal("MemSave aurait dû être refusé (mémoire verrouillée)")
	}
	// Le fichier d'origine ne doit pas avoir été touché en clair.
	p, _ := safeMemPath("projet.md")
	raw, _ := os.ReadFile(p)
	if !looksEncrypted(raw) {
		t.Fatal("la page d'origine a été altérée alors que la mémoire était verrouillée")
	}
}

// Après un chiffrement réussi, AUCUN texte en clair ne doit subsister : ni .bak
// clair, ni snapshot « avant-chiffrement ».
func TestNoPlaintextResidueAfterEncrypt(t *testing.T) {
	testHome(t)
	clearMemDEK()
	seedPages(t)
	if _, err := EnableMemEncryption("pw"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(memoryDir())
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			b, _ := os.ReadFile(memoryDir() + "/" + e.Name())
			if !looksEncrypted(b) {
				t.Fatalf(".bak en clair résiduel : %s", e.Name())
			}
		}
	}
	for _, s := range listSnapshots() {
		if strings.Contains(s.Reason, "avant-chiffrement") {
			t.Fatalf("snapshot en clair avant-chiffrement resté : %s", s.ID)
		}
	}
}

// Reprise d'un chiffrement interrompu : journal présent + une page restée en
// clair, mémoire déverrouillée → resume doit finir le chiffrement.
func TestResumeInterruptedEncrypt(t *testing.T) {
	testHome(t)
	clearMemDEK()
	seedPages(t)
	if _, err := EnableMemEncryption("pw"); err != nil {
		t.Fatal(err)
	}
	// Simule une interruption : on repose une page en clair et on rearme le journal.
	if err := writeMemPlain("projet.md", []byte("# Projet\n\nclair résiduel\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeMigrationJournal("encrypt"); err != nil {
		t.Fatal(err)
	}
	resumeMemMigration()
	p, _ := safeMemPath("projet.md")
	raw, _ := os.ReadFile(p)
	if !looksEncrypted(raw) {
		t.Fatal("la reprise n'a pas re-chiffré la page restée en clair")
	}
	if _, busy := readMigrationJournal(); busy {
		t.Fatal("le journal aurait dû être levé après reprise réussie")
	}
}

// Un round-trip via snapshot : on prend une copie, on casse la mémoire, on
// restaure.
func TestSnapshotRestore(t *testing.T) {
	testHome(t)
	clearMemDEK()
	pages := seedPages(t)
	id, err := snapshotMemory("test")
	if err != nil || id == "" {
		t.Fatalf("snapshot: %v (id=%q)", err, id)
	}
	// Corrompt une page et en supprime une autre.
	pApath, _ := safeMemPath("projet.md")
	os.WriteFile(pApath, []byte("CORROMPU"), 0o600)
	MemDelete("copine.md")

	if err := restoreSnapshot(id); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for name, want := range pages {
		if got := MemContent(name); got != want {
			t.Fatalf("après restauration %s: %q != %q", name, got, want)
		}
	}
}
