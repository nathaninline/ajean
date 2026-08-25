package ajean

// mem_vault.go — le keyvault : la DEK de la mémoire, wrappée par une ou
// plusieurs KEK (mot de passe, clé de récupération, mot de passe app), et gardée
// en RAM le temps d'une session déverrouillée.
//
// Le keyvault est le POINT DE DÉFAILLANCE UNIQUE : le perdre = perdre la
// mémoire. Il est donc écrit en TROIS copies indépendantes, chacune suffisante
// seule, et contient plusieurs wraps indépendants de la même DEK. Perdre le mot
// de passe n'est pas fatal tant qu'il reste la clé de récupération.

import (
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Types de wrap. Chaque wrap re-chiffre la MÊME DEK sous une KEK différente.
const (
	wrapPassword = "password" // mot de passe mémoire choisi par l'utilisateur
	wrapRecovery = "recovery" // clé de récupération (secours anti-perte)
	wrapE2ERoot  = "e2e-root" // racine E2E de l'abonné (mot de passe app)
)

// checkPlain est le témoin scellé par la DEK dans le keyvault : le déballer
// prouve que la DEK reconstituée est la bonne (détecte un wrap corrompu).
var checkPlain = []byte("ajean-memoire-ok-v1")

type kekParams struct {
	T uint32 `json:"t"`
	M uint32 `json:"m"`
	P uint8  `json:"p"`
}

type vaultWrap struct {
	Kind  string     `json:"kind"`
	Label string     `json:"label"`
	Salt  string     `json:"salt"` // base64 du sel Argon2id propre à ce wrap
	KDF   kekParams  `json:"kdf"`
	Box   string     `json:"box"` // base64 de gcmSeal(KEK, DEK, aad)
}

type keyVault struct {
	V     int         `json:"v"`
	Check string      `json:"check"` // base64 de gcmSeal(DEK, checkPlain)
	Wraps []vaultWrap `json:"wraps"`
}

func vaultWrapAAD(kind string) []byte { return []byte("ajean-vault/" + kind) }

// --- Emplacements du keyvault (3 copies indépendantes) -----------------------

func vaultPathPrimary() string { return filepath.Join(memoryDir(), ".keyvault") }
func vaultPathBackup() string   { return filepath.Join(AjeanHome(), ".keyvault.bak") }

const vaultDBKey = "mem_keyvault"

// --- État en RAM de la DEK ----------------------------------------------------

var (
	memKeyMu sync.RWMutex
	memDEK   []byte // nil = mémoire verrouillée
)

// memUnlocked indique si la DEK est chargée en RAM (mémoire déverrouillée).
func memUnlocked() bool {
	memKeyMu.RLock()
	defer memKeyMu.RUnlock()
	return memDEK != nil
}

// setMemDEK charge la DEK en RAM (copie défensive).
func setMemDEK(dek []byte) {
	memKeyMu.Lock()
	memDEK = append([]byte(nil), dek...)
	memKeyMu.Unlock()
}

// clearMemDEK purge la DEK de la RAM (verrouille la mémoire).
func clearMemDEK() {
	memKeyMu.Lock()
	for i := range memDEK {
		memDEK[i] = 0
	}
	memDEK = nil
	memKeyMu.Unlock()
}

// currentDEK renvoie la DEK en RAM, ou une erreur si la mémoire est verrouillée.
func currentDEK() ([]byte, error) {
	memKeyMu.RLock()
	defer memKeyMu.RUnlock()
	if memDEK == nil {
		return nil, errors.New("mémoire verrouillée — aucune clé chargée")
	}
	return append([]byte(nil), memDEK...), nil
}

// --- Construction et manipulation du keyvault --------------------------------

// newVault crée un keyvault autour d'une DEK fraîche : aucun wrap encore, mais
// le témoin est posé. Renvoie aussi la DEK en clair (à garder en RAM, jamais sur
// disque).
func newVault() (*keyVault, []byte, error) {
	dek, err := randBytes(memDEKLen)
	if err != nil {
		return nil, nil, err
	}
	check, err := gcmSeal(dek, checkPlain, nil)
	if err != nil {
		return nil, nil, err
	}
	return &keyVault{V: 1, Check: base64.StdEncoding.EncodeToString(check)}, dek, nil
}

// addSecretWrap (ou remplace) un wrap de la DEK sous un secret (mot de passe ou
// clé de récupération). kind identifie le wrap ; un même kind+label écrase le
// précédent (rotation de mot de passe = re-wrap, sans toucher aux pages).
func (v *keyVault) addSecretWrap(dek []byte, kind, label, secret string) error {
	salt, err := randBytes(memSaltLen)
	if err != nil {
		return err
	}
	kp := kekParams{T: argonTime, M: argonMemory, P: argonThreads}
	kek := deriveKEK(secret, salt, kp.T, kp.M, kp.P)
	box, err := gcmSeal(kek, dek, vaultWrapAAD(kind))
	if err != nil {
		return err
	}
	w := vaultWrap{
		Kind:  kind,
		Label: label,
		Salt:  base64.StdEncoding.EncodeToString(salt),
		KDF:   kp,
		Box:   base64.StdEncoding.EncodeToString(box),
	}
	for i := range v.Wraps {
		if v.Wraps[i].Kind == kind && v.Wraps[i].Label == label {
			v.Wraps[i] = w
			return nil
		}
	}
	v.Wraps = append(v.Wraps, w)
	return nil
}

// removeWrap retire les wraps d'un kind (révocation). Renvoie le nombre retiré.
// Refuse de vider le dernier wrap (sinon la DEK deviendrait irrécupérable).
func (v *keyVault) removeWrap(kind, label string) (int, error) {
	kept := v.Wraps[:0:0]
	removed := 0
	for _, w := range v.Wraps {
		if w.Kind == kind && (label == "" || w.Label == label) {
			removed++
			continue
		}
		kept = append(kept, w)
	}
	if removed > 0 && len(kept) == 0 {
		return 0, errors.New("refus : ce serait le dernier accès à la mémoire")
	}
	v.Wraps = kept
	return removed, nil
}

// unlockWith tente de reconstituer la DEK à partir d'un secret : essaie chaque
// wrap, et ne renvoie la DEK que si le témoin la valide. Renvoie le kind du wrap
// qui a marché (utile pour l'UI).
func (v *keyVault) unlockWith(secret string) ([]byte, string, error) {
	if len(v.Wraps) == 0 {
		return nil, "", errors.New("keyvault sans aucun wrap")
	}
	for _, w := range v.Wraps {
		salt, err := base64.StdEncoding.DecodeString(w.Salt)
		if err != nil {
			continue
		}
		box, err := base64.StdEncoding.DecodeString(w.Box)
		if err != nil {
			continue
		}
		kek := deriveKEK(secret, salt, w.KDF.T, w.KDF.M, w.KDF.P)
		dek, err := gcmOpen(kek, box, vaultWrapAAD(w.Kind))
		if err != nil {
			continue // mauvais secret pour ce wrap, on essaie le suivant
		}
		if err := v.validate(dek); err != nil {
			continue
		}
		return dek, w.Kind, nil
	}
	return nil, "", errors.New("secret incorrect (aucun wrap ne correspond)")
}

// validate confirme qu'une DEK candidate ouvre bien le témoin du keyvault.
func (v *keyVault) validate(dek []byte) error {
	check, err := base64.StdEncoding.DecodeString(v.Check)
	if err != nil {
		return err
	}
	got, err := gcmOpen(dek, check, nil)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(got, checkPlain) != 1 {
		return errors.New("témoin du keyvault invalide")
	}
	return nil
}

func (v *keyVault) hasKind(kind string) bool {
	for _, w := range v.Wraps {
		if w.Kind == kind {
			return true
		}
	}
	return false
}

// --- Persistance redondée -----------------------------------------------------

// saveVault écrit le keyvault dans ses TROIS copies. Réussit si AU MOINS une
// copie a pu être écrite (on ne veut jamais bloquer sur une copie inaccessible),
// mais renvoie l'erreur agrégée sinon.
func saveVault(v *keyVault) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var errs []string
	okAny := false
	if err := os.MkdirAll(memoryDir(), 0o755); err == nil {
		if err := memWriteFileAtomic(vaultPathPrimary(), data, 0o600); err == nil {
			okAny = true
		} else {
			errs = append(errs, "primaire: "+err.Error())
		}
	} else {
		errs = append(errs, "memoryDir: "+err.Error())
	}
	if err := memWriteFileAtomic(vaultPathBackup(), data, 0o600); err == nil {
		okAny = true
	} else {
		errs = append(errs, "backup: "+err.Error())
	}
	if err := putBytes(bkState, vaultDBKey, data); err == nil {
		okAny = true
	} else {
		errs = append(errs, "db: "+err.Error())
	}
	if !okAny {
		return fmt.Errorf("aucune copie du keyvault écrite : %s", strings.Join(errs, " ; "))
	}
	return nil
}

// loadVault lit le keyvault depuis la première copie valide (primaire, backup,
// db), puis RÉPARE silencieusement les copies manquantes/corrompues à partir de
// celle qui a marché. Renvoie (nil, nil) si aucun keyvault n'existe encore.
func loadVault() (*keyVault, error) {
	sources := []func() ([]byte, error){
		func() ([]byte, error) { return os.ReadFile(vaultPathPrimary()) },
		func() ([]byte, error) { return os.ReadFile(vaultPathBackup()) },
		func() ([]byte, error) {
			b := getBytes(bkState, vaultDBKey)
			if len(b) == 0 {
				return nil, os.ErrNotExist
			}
			return b, nil
		},
	}
	var found *keyVault
	anyPresent := false
	healthy := 0 // nb de copies présentes ET valides
	for _, src := range sources {
		b, err := src()
		if err != nil || len(b) == 0 {
			continue
		}
		anyPresent = true
		var v keyVault
		if json.Unmarshal(b, &v) == nil && v.V > 0 && len(v.Wraps) > 0 {
			healthy++
			if found == nil {
				vv := v
				found = &vv
			}
		}
	}
	if found == nil {
		if anyPresent {
			return nil, errors.New("keyvault présent mais illisible dans toutes les copies")
		}
		return nil, nil
	}
	// Ré-harmonise UNIQUEMENT si une des trois copies manque ou est corrompue :
	// sinon loadVault (appelé à chaque sondage de santé) réécrirait le disque en
	// boucle pour rien.
	if healthy < 3 {
		_ = saveVault(found)
	}
	return found, nil
}

// vaultExists indique si un keyvault est présent (mémoire chiffrée configurée).
func vaultExists() bool {
	v, _ := loadVault()
	return v != nil
}

// stripServerOpenableWraps retire du coffre tout wrap ouvrable par un secret que
// le SERVEUR détient (la clé de pilotage web, jadis ajoutée comme wrap
// « clé d'accès »). Objectif : le serveur ne doit JAMAIS pouvoir déchiffrer seul.
// La clé de chiffrement ne vit que côté client (localStorage). Idempotent ;
// refuse de vider le coffre (removeWrap garde toujours au moins un wrap).
func stripServerOpenableWraps() {
	v, _ := loadVault()
	if v == nil || !v.hasKind(wrapE2ERoot) {
		return
	}
	if n, err := v.removeWrap(wrapE2ERoot, ""); err == nil && n > 0 {
		_ = saveVault(v)
	}
}

// removeVault efface les trois copies du keyvault (après un déchiffrement complet
// où il n'a plus lieu d'être).
func removeVault() {
	os.Remove(vaultPathPrimary())
	os.Remove(vaultPathBackup())
	putBytes(bkState, vaultDBKey, nil)
}

// --- Clé de récupération ------------------------------------------------------

// recoveryEnc : base32 Crockford (32 caractères, sans les ambigus I/L/O/U),
// pour une saisie humaine sans confusion.
var recoveryEnc = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newRecoveryKey génère une clé de récupération lisible : 20 octets aléatoires,
// encodés en groupes de 4 (ex. ABCD-EFGH-JKMN-...). C'est un SECRET fort, à
// noter hors ligne ; il rouvre la mémoire même si le mot de passe est perdu.
func newRecoveryKey() (string, error) {
	raw, err := randBytes(20)
	if err != nil {
		return "", err
	}
	s := recoveryEnc.EncodeToString(raw)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}

// normalizeRecovery met une clé de récupération saisie sous forme canonique
// (majuscules, sans séparateurs) pour la dérivation de KEK.
func normalizeRecovery(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.NewReplacer("-", "", " ", "", "\t", "").Replace(s)
	return s
}
