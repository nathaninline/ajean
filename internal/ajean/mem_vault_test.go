package ajean

import (
	"bytes"
	"os"
	"testing"
)

func TestVaultCreateUnlock(t *testing.T) {
	testHome(t)
	v, dek, err := newVault()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.addSecretWrap(dek, wrapPassword, "principal", "hunter2"); err != nil {
		t.Fatal(err)
	}
	got, kind, err := v.unlockWith("hunter2")
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if kind != wrapPassword {
		t.Fatalf("kind attendu password, obtenu %s", kind)
	}
	if !bytes.Equal(got, dek) {
		t.Fatal("DEK déballée ≠ DEK d'origine")
	}
	if _, _, err := v.unlockWith("mauvais"); err == nil {
		t.Fatal("un mauvais mot de passe n'aurait pas dû ouvrir")
	}
}

// Rotation : re-wrapper sous un nouveau mot de passe ne doit PAS changer la DEK.
func TestVaultRotation(t *testing.T) {
	testHome(t)
	v, dek, _ := newVault()
	v.addSecretWrap(dek, wrapPassword, "principal", "ancien")
	if err := v.addSecretWrap(dek, wrapPassword, "principal", "nouveau"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := v.unlockWith("ancien"); err == nil {
		t.Fatal("l'ancien mot de passe aurait dû être remplacé")
	}
	got, _, err := v.unlockWith("nouveau")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, dek) {
		t.Fatal("la rotation a changé la DEK")
	}
}

// La clé de récupération rouvre la mémoire même si le wrap mot de passe est retiré.
func TestVaultRecovery(t *testing.T) {
	testHome(t)
	v, dek, _ := newVault()
	v.addSecretWrap(dek, wrapPassword, "principal", "motdepasse")
	rec, err := newRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	v.addSecretWrap(dek, wrapRecovery, "récupération", normalizeRecovery(rec))

	// On détruit l'accès mot de passe.
	if _, err := v.removeWrap(wrapPassword, ""); err != nil {
		t.Fatal(err)
	}
	got, kind, err := v.unlockWith(normalizeRecovery(rec))
	if err != nil {
		t.Fatalf("récupération: %v", err)
	}
	if kind != wrapRecovery || !bytes.Equal(got, dek) {
		t.Fatal("la clé de récupération n'a pas rouvert la mémoire")
	}
}

func TestVaultRefusesLastWrapRemoval(t *testing.T) {
	testHome(t)
	v, dek, _ := newVault()
	v.addSecretWrap(dek, wrapPassword, "principal", "x")
	if _, err := v.removeWrap(wrapPassword, ""); err == nil {
		t.Fatal("retirer le dernier wrap aurait dû être refusé")
	}
}

// Persistance : sauver puis relire depuis les 3 copies, y compris après
// destruction d'une ou deux copies.
func TestVaultPersistRedundancy(t *testing.T) {
	testHome(t)
	v, dek, _ := newVault()
	v.addSecretWrap(dek, wrapPassword, "principal", "pw")
	if err := saveVault(v); err != nil {
		t.Fatal(err)
	}
	// Détruit la copie primaire et la db, il reste le backup.
	os.Remove(vaultPathPrimary())
	putBytes(bkState, vaultDBKey, nil)
	got, err := loadVault()
	if err != nil {
		t.Fatalf("load après perte de 2 copies: %v", err)
	}
	if got == nil {
		t.Fatal("keyvault introuvable alors que le backup existe")
	}
	// loadVault doit avoir réparé les copies manquantes.
	if _, err := os.Stat(vaultPathPrimary()); err != nil {
		t.Fatal("la copie primaire n'a pas été réparée")
	}
	d, _, err := got.unlockWith("pw")
	if err != nil || !bytes.Equal(d, dek) {
		t.Fatal("keyvault relu inutilisable")
	}
}

func TestMemDEKState(t *testing.T) {
	if memUnlocked() {
		clearMemDEK()
	}
	if _, err := currentDEK(); err == nil {
		t.Fatal("currentDEK devrait échouer quand verrouillé")
	}
	dek, _ := randBytes(memDEKLen)
	setMemDEK(dek)
	if !memUnlocked() {
		t.Fatal("devrait être déverrouillé")
	}
	got, err := currentDEK()
	if err != nil || !bytes.Equal(got, dek) {
		t.Fatal("currentDEK incohérent")
	}
	clearMemDEK()
	if memUnlocked() {
		t.Fatal("devrait être verrouillé après clear")
	}
}

// Zéro-connaissance : le secret détenu par le SERVEUR (clé de pilotage web_key)
// ne doit PAS pouvoir ouvrir le coffre, et stripServerOpenableWraps retire tout
// wrap qui le permettrait.
func TestServerCannotOpenVault(t *testing.T) {
	testHome(t)
	putStr(bkState, "web_key", "cle-de-pilotage-serveur")
	v, dek, _ := newVault()
	v.addSecretWrap(dek, wrapPassword, "principal", "mon-mdp-client")
	// La clé de pilotage du serveur n'ouvre pas le coffre.
	if _, _, err := v.unlockWith("cle-de-pilotage-serveur"); err == nil {
		t.Fatal("la clé serveur n'aurait jamais dû ouvrir le coffre")
	}
	// Si un wrap « clé d'accès » (ouvrable par le serveur) traînait, on le retire.
	v.addSecretWrap(dek, wrapE2ERoot, "clé d'accès", "cle-de-pilotage-serveur")
	saveVault(v)
	if got, _, err := v.unlockWith("cle-de-pilotage-serveur"); err != nil || !bytes.Equal(got, dek) {
		t.Fatal("pré-condition: le wrap clé d'accès devrait ouvrir avant strip")
	}
	stripServerOpenableWraps()
	v2, _ := loadVault()
	if _, _, err := v2.unlockWith("cle-de-pilotage-serveur"); err == nil {
		t.Fatal("après strip, la clé serveur ne doit plus ouvrir le coffre")
	}
	if _, _, err := v2.unlockWith("mon-mdp-client"); err != nil {
		t.Fatal("le mot de passe client doit toujours ouvrir")
	}
}

func TestRecoveryKeyFormat(t *testing.T) {
	rec, err := newRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	if normalizeRecovery(rec) == "" {
		t.Fatal("clé de récupération vide")
	}
	if normalizeRecovery("abcd-efgh") != "ABCDEFGH" {
		t.Fatalf("normalisation inattendue: %q", normalizeRecovery("abcd-efgh"))
	}
}
