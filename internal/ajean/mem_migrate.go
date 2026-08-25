package ajean

// mem_migrate.go — bascule chiffrer / déchiffrer la mémoire, sans jamais risquer
// de la perdre. Règles :
//   - auto-test crypto AVANT de toucher quoi que ce soit ;
//   - snapshot complet AVANT toute bascule ;
//   - écriture atomique + relecture vérifiée de CHAQUE page ;
//   - journal de migration pour reprendre proprement après un crash ;
//   - une page à moitié migrée reste lisible : le contenu est auto-descriptif
//     (magic), donc pages chiffrées et pages claires coexistent sans danger.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func migrationJournalPath() string { return filepath.Join(memoryDir(), ".migration") }

type migrationJournal struct {
	Op        string    `json:"op"` // "encrypt" | "decrypt"
	StartedAt time.Time `json:"started_at"`
}

func writeMigrationJournal(op string) error {
	b, _ := json.Marshal(migrationJournal{Op: op, StartedAt: time.Now().UTC()})
	return memWriteFileAtomic(migrationJournalPath(), b, 0o600)
}

func clearMigrationJournal() { os.Remove(migrationJournalPath()) }

func readMigrationJournal() (*migrationJournal, bool) {
	b, err := os.ReadFile(migrationJournalPath())
	if err != nil {
		return nil, false
	}
	var j migrationJournal
	if json.Unmarshal(b, &j) != nil {
		return nil, false
	}
	return &j, true
}

// mdPages liste les noms de fichiers .md du dossier mémoire (les pages).
func mdPages() []string {
	entries, err := os.ReadDir(memoryDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			out = append(out, e.Name())
		}
	}
	return out
}

// EnableMemEncryption chiffre la mémoire. Génère la DEK, la wrappe sous le mot de
// passe ET sous une clé de récupération (renvoyée : à montrer UNE fois à
// l'utilisateur), puis chiffre chaque page en vérifiant. Idempotent-safe : si
// déjà activé, renvoie une erreur claire.
func EnableMemEncryption(password string) (recoveryKey string, err error) {
	if strings.TrimSpace(password) == "" {
		return "", fmt.Errorf("mot de passe vide")
	}
	if memEncActive() {
		return "", fmt.Errorf("le chiffrement est déjà activé")
	}
	if err := memCryptoSelfTest(); err != nil {
		return "", fmt.Errorf("auto-test crypto échoué, chiffrement refusé : %w", err)
	}
	if _, err := snapshotMemory("avant-chiffrement"); err != nil {
		return "", fmt.Errorf("snapshot de sécurité impossible, chiffrement annulé : %w", err)
	}

	v, dek, err := newVault()
	if err != nil {
		return "", err
	}
	if err := v.addSecretWrap(dek, wrapPassword, "principal", password); err != nil {
		return "", err
	}
	recoveryKey, err = newRecoveryKey()
	if err != nil {
		return "", err
	}
	if err := v.addSecretWrap(dek, wrapRecovery, "récupération", normalizeRecovery(recoveryKey)); err != nil {
		return "", err
	}
	if err := saveVault(v); err != nil {
		return "", fmt.Errorf("écriture du keyvault : %w", err)
	}

	// À partir d'ici la DEK est en RAM et le chiffrement est actif : writeMemFile
	// chiffrera. On journalise, on migre chaque page, on lève le journal.
	setMemDEK(dek)
	if err := SetConfigKey("MEM_ENCRYPTED", "1"); err != nil {
		return "", err
	}
	if err := writeMigrationJournal("encrypt"); err != nil {
		return "", err
	}
	if err := reencryptPlaintextPages(); err != nil {
		// Journal laissé en place : la reprise au démarrage réessaiera. Les pages
		// déjà chiffrées sont valides, les pages claires restent lisibles.
		return recoveryKey, fmt.Errorf("chiffrement partiel (sera repris) : %w", err)
	}
	// Étend le chiffrement aux conversations (fil courant + archives + index).
	if err := reencryptChatStores(); err != nil {
		return recoveryKey, fmt.Errorf("chiffrement conversations partiel (sera repris) : %w", err)
	}
	clearMigrationJournal()
	scrubPlaintextResidue() // sinon le texte en clair traînerait à côté du chiffré
	return recoveryKey, nil
}

// scrubPlaintextResidue efface le texte en clair laissé sur le disque APRÈS un
// chiffrement réussi et vérifié : les .bak clairs (copies de sauvegarde d'une
// écriture) et le snapshot « avant-chiffrement » (copie intégrale en clair).
// Sans ça, chiffrer ne servirait à rien — le clair resterait lisible à côté.
// Sûr : les pages chiffrées ont été relues et validées une par une, le keyvault
// (3 copies + clé de récupération) garantit la récupération.
func scrubPlaintextResidue() {
	if entries, err := os.ReadDir(memoryDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".bak") {
				continue
			}
			p := filepath.Join(memoryDir(), e.Name())
			if b, err := os.ReadFile(p); err == nil && !looksEncrypted(b) {
				os.Remove(p) // .bak en clair : à retirer
			}
		}
	}
	for _, s := range listSnapshots() {
		if strings.Contains(s.Reason, "avant-chiffrement") {
			os.RemoveAll(filepath.Join(snapshotsRoot(), s.ID))
		}
	}
}

// reencryptPlaintextPages (re)chiffre toutes les pages encore en clair et vérifie
// chacune par relecture. Sûr à rejouer.
func reencryptPlaintextPages() error {
	for _, name := range mdPages() {
		p, _ := safeMemPath(name)
		raw, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("lecture %s : %w", name, err)
		}
		if looksEncrypted(raw) {
			continue // déjà fait
		}
		if err := writeMemFile(name, raw); err != nil { // chiffre (actif+déverrouillé)
			return fmt.Errorf("chiffrement %s : %w", name, err)
		}
		// Vérifie que la page se relit à l'identique.
		back, err := memReadPage(name)
		if err != nil || string(back) != string(raw) {
			return fmt.Errorf("vérification post-chiffrement de %s échouée", name)
		}
	}
	return nil
}

// DisableMemEncryption remet la mémoire en clair. Exige que la mémoire soit
// déverrouillée (DEK en RAM). Snapshot d'abord, puis chaque page est déchiffrée,
// écrite en clair et vérifiée ; enfin le keyvault est retiré.
func DisableMemEncryption() error {
	if !memEncActive() {
		return fmt.Errorf("le chiffrement n'est pas activé")
	}
	if !memUnlocked() {
		return fmt.Errorf("mémoire verrouillée : déverrouille-la avant de déchiffrer")
	}
	if _, err := snapshotMemory("avant-dechiffrement"); err != nil {
		return fmt.Errorf("snapshot de sécurité impossible, déchiffrement annulé : %w", err)
	}
	if err := writeMigrationJournal("decrypt"); err != nil {
		return err
	}

	// On garde le drapeau LEVÉ et la DEK en RAM pendant toute l'opération : on
	// écrit le clair via writeMemPlain (qui ignore le drapeau), on vérifie CHAQUE
	// page, et on ne baisse le drapeau qu'à la toute fin. Ainsi une interruption
	// laisse un état parfaitement lisible et RE-JOUABLE (le drapeau est encore
	// levé, donc DisableMemEncryption peut être relancé).
	if err := decryptAllPages(); err != nil {
		return err
	}
	if err := decryptChatStores(); err != nil {
		return err
	}
	// Toutes les pages sont en clair et vérifiées : on peut baisser le drapeau,
	// retirer le keyvault et purger la DEK.
	if err := SetConfigKey("MEM_ENCRYPTED", ""); err != nil {
		return err
	}
	removeVault()
	clearMemDEK()
	clearMigrationJournal()
	return nil
}

// decryptAllPages réécrit en clair toutes les pages encore chiffrées, en
// vérifiant chacune. Exige la DEK en RAM. Sûr à rejouer (idempotent).
func decryptAllPages() error {
	for _, name := range mdPages() {
		p, _ := safeMemPath(name)
		raw, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("lecture %s : %w", name, err)
		}
		if !looksEncrypted(raw) {
			continue // déjà en clair
		}
		plain, err := decodeMemContent(raw)
		if err != nil {
			return fmt.Errorf("déchiffrement %s impossible, on n'écrase rien : %w", name, err)
		}
		if err := writeMemPlain(name, plain); err != nil {
			return fmt.Errorf("écriture claire %s : %w", name, err)
		}
		back, err := os.ReadFile(p)
		if err != nil || looksEncrypted(back) || string(back) != string(plain) {
			return fmt.Errorf("vérification post-déchiffrement de %s échouée", name)
		}
	}
	return nil
}

// resumeMemMigration reprend une migration interrompue au démarrage. Pour une
// migration "encrypt", si la DEK est déjà en RAM (déverrouillée), on rejoue le
// (re)chiffrement des pages claires ; sinon on laisse le journal en place pour un
// prochain déverrouillage. Ne perd jamais de données.
func resumeMemMigration() {
	j, ok := readMigrationJournal()
	if !ok {
		return
	}
	switch j.Op {
	case "encrypt":
		if memEncActive() && memUnlocked() {
			if err := reencryptPlaintextPages(); err == nil {
				_ = reencryptChatStores()
				clearMigrationJournal()
				scrubPlaintextResidue()
			}
		}
	case "decrypt":
		// Déchiffrement interrompu : si la mémoire est déverrouillée et le drapeau
		// encore levé, on termine proprement (écrit le clair restant), puis on
		// baisse le drapeau. Sans DEK, on laisse le journal : l'état reste lisible
		// et l'écran de santé signalera le mélange.
		if memEncActive() && memUnlocked() {
			if err := decryptAllPages(); err == nil {
				_ = SetConfigKey("MEM_ENCRYPTED", "")
				removeVault()
				clearMemDEK()
				clearMigrationJournal()
			}
		}
	}
}
