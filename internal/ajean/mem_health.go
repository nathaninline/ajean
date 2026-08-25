package ajean

// mem_health.go — état de santé de la mémoire pour l'UI : chiffrement actif ?
// verrouillé ? toutes les pages déchiffrables ? combien de copies du keyvault ?
// dernier snapshot ? C'est le tableau de bord qui rassure d'un coup d'œil.

import (
	"os"
	"strings"
)

// MemHealth résume l'état de la mémoire (sérialisé tel quel vers l'UI).
type MemHealth struct {
	Encrypted     bool   `json:"encrypted"`      // chiffrement activé (drapeau)
	Fully         bool   `json:"fully"`          // TOUT est réellement chiffré au repos (pages + conversations)
	Locked        bool   `json:"locked"`         // chiffré mais DEK absente
	Pages         int    `json:"pages"`          // nb de pages
	Readable      int    `json:"readable"`       // pages déchiffrables (ou claires)
	Unreadable    int    `json:"unreadable"`     // pages illisibles (chiffrées + verrouillé, ou corrompues)
	Mixed         bool   `json:"mixed"`          // mélange clair/chiffré (migration en cours ?)
	VaultCopies   int    `json:"vault_copies"`   // nb de copies du keyvault présentes (sur 3)
	HasRecovery   bool   `json:"has_recovery"`   // une clé de récupération est enregistrée
	Snapshots     int    `json:"snapshots"`      // nb de snapshots locaux
	LastSnapshot  string `json:"last_snapshot"`  // ID du snapshot le plus récent
	MigrationBusy bool   `json:"migration_busy"` // une migration est en cours (journal présent)
}

// memHealth calcule l'état courant.
func memHealth() MemHealth {
	h := MemHealth{Encrypted: memEncActive(), Locked: memEncActive() && !memUnlocked()}

	clair, chiffre := 0, 0
	for _, name := range mdPages() {
		h.Pages++
		p, _ := safeMemPath(name)
		raw, err := os.ReadFile(p)
		if err != nil {
			h.Unreadable++
			continue
		}
		if looksEncrypted(raw) {
			chiffre++
			if _, err := decodeMemContent(raw); err == nil {
				h.Readable++
			} else {
				h.Unreadable++
			}
		} else {
			clair++
			h.Readable++
		}
	}
	h.Mixed = clair > 0 && chiffre > 0

	// Fully : la case « activer le chiffrement » ne doit être cochée QUE si TOUT est
	// réellement chiffré au repos — aucune page en clair ET aucune valeur de
	// conversation en clair. Vérifiable même verrouillé (looksEncrypted lit le magic
	// sans la DEK). Un chiffrement partiel/raté laisse donc la case DÉCOCHÉE.
	if h.Encrypted {
		full := clair == 0 && h.Pages == chiffre // toutes les pages chiffrées (0 en clair)
		if full {
			for _, b := range encryptedBuckets {
				if !bucketFullyEncrypted(b) {
					full = false
					break
				}
			}
		}
		h.Fully = full
	}

	// Copies du keyvault présentes.
	if _, err := os.Stat(vaultPathPrimary()); err == nil {
		h.VaultCopies++
	}
	if _, err := os.Stat(vaultPathBackup()); err == nil {
		h.VaultCopies++
	}
	if len(getBytes(bkState, vaultDBKey)) > 0 {
		h.VaultCopies++
	}
	if v, _ := loadVault(); v != nil {
		h.HasRecovery = v.hasKind(wrapRecovery)
	}

	snaps := listSnapshots()
	h.Snapshots = len(snaps)
	if len(snaps) > 0 {
		h.LastSnapshot = snaps[0].ID
	}
	_, h.MigrationBusy = readMigrationJournal()
	return h
}

// memStatusLabel : phrase courte pour les logs / la CLI.
func memStatusLabel() string {
	h := memHealth()
	switch {
	case !h.Encrypted:
		return "mémoire en clair"
	case h.Locked:
		return "mémoire chiffrée — verrouillée"
	default:
		return strings.TrimSpace("mémoire chiffrée — déverrouillée")
	}
}
