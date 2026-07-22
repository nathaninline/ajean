package jean

// relay_e2eauth.go — authentification MUTUELLE + anti-rejeu du canal chiffré.
//
// Le scellé anonyme (boîte scellée) garantit la confidentialité mais PAS
// l'authenticité : n'importe qui connaissant la clé publique de l'agent (dont un
// relais compromis) peut fabriquer une enveloppe. Ici on ferme ce trou.
//
// Modèle : l'utilisateur a une identité X25519 dérivée de façon déterministe de son
// mot de passe (exportKey OPAQUE → racine R → uPriv) — le relais ne connaît pas R,
// donc ne peut PAS reproduire cette identité. La clé publique uPub est APPAIRÉE une
// fois à l'agent via un code affiché par « jean link » (canal hors-bande : le log du
// serveur, que le relais ne voit pas ; le code voyage scellé vers l'agent). Ensuite,
// chaque requête est chiffrée avec K = SHA256(ECDH(uPriv, agentPriv) || "authchan"),
// liée à uPub+horodatage (AAD) et protégée contre le rejeu. Seul le vrai utilisateur
// peut produire une requête valide ; le relais est totalement verrouillé.

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Fenêtre d'acceptation de l'horodatage (anti-rejeu) : ±90 s pour tolérer une
// dérive d'horloge raisonnable entre l'appareil et le serveur.
const e2eAuthWindowMs = 90_000

// ---- Identités utilisateur appairées ---------------------------------------

var (
	authOnce sync.Once
	authMu   sync.Mutex
	authSet  = map[string]bool{}
)

func authUsersPath() string { return filepath.Join(JeanHome(), ".authorized_users") }

func loadAuthUsers() {
	authOnce.Do(func() {
		b, err := os.ReadFile(authUsersPath())
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(b), "\n") {
			h := strings.ToLower(strings.TrimSpace(line))
			if isHexPub(h) {
				authSet[h] = true
			}
		}
	})
}

func isHexPub(h string) bool {
	if len(h) != 64 {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil
}

func isAuthorizedUser(uPubHex string) bool {
	loadAuthUsers()
	authMu.Lock()
	defer authMu.Unlock()
	return authSet[strings.ToLower(uPubHex)]
}

func authorizeUser(uPubHex string) error {
	uPubHex = strings.ToLower(strings.TrimSpace(uPubHex))
	if !isHexPub(uPubHex) {
		return fmt.Errorf("clé publique invalide")
	}
	loadAuthUsers()
	authMu.Lock()
	defer authMu.Unlock()
	if authSet[uPubHex] {
		return nil
	}
	authSet[uPubHex] = true
	_ = os.MkdirAll(JeanHome(), 0o755)
	f, err := os.OpenFile(authUsersPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(uPubHex + "\n")
	return err
}

// ---- Codes d'appairage : à la demande, usage unique, TTL 10 min --------------
//
// Générés par « jean link code » (ou affichés par « jean link »), ils sont
// partagés avec le worker « jean link --foreground » (systemd, autre process) via
// un fichier JeanHome/.pair_codes — sinon les deux process ne s'accorderaient pas.
// Stockés HACHÉS (SHA-256) : le fichier ne révèle aucun code. Chaque code expire
// au bout de 10 min et est consommé (retiré) au premier appairage réussi.

const pairCodeTTL = 10 * time.Minute

type pairEntry struct {
	Hash string `json:"h"` // hex SHA-256 du code
	Exp  int64  `json:"e"` // expiration (unix ms)
}

func pairCodesPath() string { return filepath.Join(JeanHome(), ".pair_codes") }

func hashPairCode(code string) string {
	h := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(h[:])
}

// loadPairEntries lit le store et purge au passage les entrées expirées.
func loadPairEntries() []pairEntry {
	b, err := os.ReadFile(pairCodesPath())
	if err != nil {
		return nil
	}
	var es []pairEntry
	if json.Unmarshal(b, &es) != nil {
		return nil
	}
	now := time.Now().UnixMilli()
	kept := es[:0]
	for _, e := range es {
		if e.Exp > now {
			kept = append(kept, e)
		}
	}
	return kept
}

func savePairEntries(es []pairEntry) error {
	if err := os.MkdirAll(JeanHome(), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(es)
	tmp := pairCodesPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, pairCodesPath()) // remplacement atomique
}

// newPairCode génère un code frais (usage unique, 10 min), le persiste haché et
// le retourne en clair.
func newPairCode() (string, error) {
	b := make([]byte, 5) // 40 bits → 8 caractères base32
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	es := append(loadPairEntries(), pairEntry{
		Hash: hashPairCode(code),
		Exp:  time.Now().Add(pairCodeTTL).UnixMilli(),
	})
	if err := savePairEntries(es); err != nil {
		return "", err
	}
	return code, nil
}

// consumePairCode renvoie true si le code est valide (non expiré, non déjà
// utilisé) et le CONSOMME (usage unique).
func consumePairCode(code string) bool {
	if strings.TrimSpace(code) == "" {
		return false
	}
	want := hashPairCode(code)
	es := loadPairEntries()
	out := make([]pairEntry, 0, len(es))
	found := false
	for _, e := range es {
		if !found && e.Hash == want {
			found = true // retiré du store = consommé
			continue
		}
		out = append(out, e)
	}
	if found {
		_ = savePairEntries(out)
	}
	return found
}

// Limitation des tentatives d'appairage (anti-brute-force du code via le relais).
var (
	pairFailMu sync.Mutex
	pairFails  int
	pairLockAt time.Time
)

func pairLocked() bool {
	pairFailMu.Lock()
	defer pairFailMu.Unlock()
	if pairFails >= 10 {
		if time.Since(pairLockAt) < 5*time.Minute {
			return true
		}
		pairFails = 0 // fenêtre expirée
	}
	return false
}

func pairRecordFail() {
	pairFailMu.Lock()
	defer pairFailMu.Unlock()
	pairFails++
	pairLockAt = time.Now()
}

func pairReset() {
	pairFailMu.Lock()
	defer pairFailMu.Unlock()
	pairFails = 0
}

// ---- Anti-rejeu -------------------------------------------------------------

var (
	replayMu   sync.Mutex
	replaySeen = map[string]time.Time{}
)

// replayCheck renvoie true si la requête (upub|ts|iv) a déjà été vue (= rejeu).
// Sinon elle l'enregistre. Purge opportuniste des entrées hors fenêtre.
func replayCheck(uPub string, ts int64, iv string) bool {
	key := uPub + "|" + fmt.Sprint(ts) + "|" + iv
	now := time.Now()
	replayMu.Lock()
	defer replayMu.Unlock()
	if len(replaySeen) > 4096 {
		for k, t := range replaySeen {
			if now.Sub(t) > 2*e2eAuthWindowMs*time.Millisecond {
				delete(replaySeen, k)
			}
		}
	}
	if _, ok := replaySeen[key]; ok {
		return true
	}
	replaySeen[key] = now
	return false
}

// ---- Dérivation de clé + ouverture authentifiée ----------------------------

// e2eAuthKey dérive la clé du canal authentifié pour l'utilisateur uPub :
// SHA256( ECDH(agentPriv, uPub) || "ajean-authchan-v1" ). Symétrique de la version
// navigateur (WASM) qui calcule ECDH(uPriv, agentPub) = même secret partagé.
func e2eAuthKey(uPubHex string) ([]byte, error) {
	priv, err := e2ePrivateKey()
	if err != nil {
		return nil, err
	}
	uPubBytes, err := hex.DecodeString(uPubHex)
	if err != nil {
		return nil, err
	}
	uPub, err := ecdh.X25519().NewPublicKey(uPubBytes)
	if err != nil {
		return nil, err
	}
	ss, err := priv.ECDH(uPub)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write(ss)
	h.Write([]byte("ajean-authchan-v1"))
	return h.Sum(nil), nil
}

func e2eAuthAAD(uPub string, ts int64) []byte {
	return []byte(uPub + "|" + fmt.Sprint(ts))
}

// e2eAuthOpenReq lit l'enveloppe authentifiée {upub, ts, iv, ct} d'une requête,
// vérifie l'appairage / l'horodatage / le rejeu, puis déchiffre. Renvoie le clair
// et la clé de canal (réutilisée pour chiffrer la réponse).
func e2eAuthOpenReq(r *http.Request) (plain []byte, key []byte, err error) {
	var env struct {
		UPub string `json:"upub"`
		Ts   int64  `json:"ts"`
		Iv   string `json:"iv"`
		Ct   string `json:"ct"`
	}
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		return nil, nil, fmt.Errorf("enveloppe invalide")
	}
	if !isAuthorizedUser(env.UPub) {
		return nil, nil, fmt.Errorf("appareil non appairé (confirme le code d'appairage)")
	}
	now := time.Now().UnixMilli()
	if d := now - env.Ts; d > e2eAuthWindowMs || d < -e2eAuthWindowMs {
		return nil, nil, fmt.Errorf("horodatage hors fenêtre")
	}
	if replayCheck(env.UPub, env.Ts, env.Iv) {
		return nil, nil, fmt.Errorf("rejeu détecté")
	}
	key, err = e2eAuthKey(env.UPub)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	iv, _ := base64.StdEncoding.DecodeString(env.Iv)
	ct, _ := base64.StdEncoding.DecodeString(env.Ct)
	plain, err = gcm.Open(nil, iv, ct, e2eAuthAAD(env.UPub, env.Ts))
	if err != nil {
		return nil, nil, fmt.Errorf("authentification échouée")
	}
	return plain, key, nil
}

// handleE2EPair : appairage d'une identité utilisateur. Le navigateur scelle
// {upub, code} vers la clé publique de l'agent (le relais ne peut ni l'ouvrir ni
// connaître le code, affiché uniquement dans le log du serveur). Si le code matche,
// uPub est enregistré comme autorisé.
func handleE2EPair(w http.ResponseWriter, r *http.Request) {
	if pairLocked() {
		http.Error(w, "trop de tentatives d'appairage, réessaie plus tard", http.StatusTooManyRequests)
		return
	}
	var body struct {
		Sealed string `json:"sealed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "appairage: requête invalide", 400)
		return
	}
	blob, err := base64.StdEncoding.DecodeString(body.Sealed)
	if err != nil {
		http.Error(w, "appairage: format", 400)
		return
	}
	plain, err := e2eOpenSeal(blob)
	if err != nil {
		pairRecordFail()
		http.Error(w, "appairage: sceau invalide", 400)
		return
	}
	var pm struct {
		UPub string `json:"upub"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(plain, &pm); err != nil {
		http.Error(w, "appairage: contenu", 400)
		return
	}
	if !consumePairCode(pm.Code) {
		pairRecordFail()
		http.Error(w, "appairage: code incorrect ou expiré", http.StatusForbidden)
		return
	}
	if err := authorizeUser(pm.UPub); err != nil {
		http.Error(w, "appairage: "+err.Error(), 500)
		return
	}
	pairReset()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}
