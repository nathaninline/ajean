package main

// e2e.go — chiffrement bout-en-bout du chat entre le NAVIGATEUR et CET agent, de
// sorte que le relais (ajean.link) ne voie que de l'opaque : « boîte noire ».
//
// Modèle : cet agent a une paire X25519 long-terme (clé privée locale, jamais
// transmise). Sa clé publique est publiée au relais et son EMPREINTE est affichée
// au `jean link` — l'utilisateur la confirme dans le portail, ce qui défait tout
// MITM du relais. Le navigateur dérive une racine R de son mot de passe (exportKey
// OPAQUE), la SCELLE vers la clé publique de l'agent (le relais ne peut pas
// l'ouvrir), puis chiffre/déchiffre le chat avec une clé dérivée de R.
//
// Aucune dépendance externe : crypto/ecdh (X25519) + AES-GCM de la stdlib.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func e2eKeyPath() string { return filepath.Join(JeanHome(), ".e2e_key") }

var (
	e2eOnce sync.Once
	e2eKey  *ecdh.PrivateKey
	e2eErr  error
	// rCache : racines R déjà descellées, indexées par kid (évite de resceller/ouvrir
	// à chaque requête). Protégé par mu.
	e2eMu     sync.Mutex
	e2eRoots  = map[string][]byte{}
)

// e2ePrivateKey charge (ou crée puis persiste) la clé privée X25519 de l'agent.
func e2ePrivateKey() (*ecdh.PrivateKey, error) {
	e2eOnce.Do(func() {
		if b, err := os.ReadFile(e2eKeyPath()); err == nil {
			raw, derr := hex.DecodeString(strings.TrimSpace(string(b)))
			if derr == nil {
				e2eKey, e2eErr = ecdh.X25519().NewPrivateKey(raw)
				return
			}
		}
		k, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			e2eErr = err
			return
		}
		_ = os.MkdirAll(JeanHome(), 0o755)
		if err := os.WriteFile(e2eKeyPath(), []byte(hex.EncodeToString(k.Bytes())+"\n"), 0o600); err != nil {
			e2eErr = err
			return
		}
		e2eKey = k
	})
	return e2eKey, e2eErr
}

// e2ePubHex retourne la clé publique X25519 de l'agent (hex), pour publication au relais.
func e2ePubHex() string {
	k, err := e2ePrivateKey()
	if err != nil {
		return ""
	}
	return hex.EncodeToString(k.PublicKey().Bytes())
}

// e2eFingerprint retourne une empreinte lisible de la clé publique (à comparer
// dans le portail). Format : 8 groupes hex de 4 = SHA-256(pub) tronqué.
func e2eFingerprint() string {
	k, err := e2ePrivateKey()
	if err != nil {
		return ""
	}
	h := sha256.Sum256(k.PublicKey().Bytes())
	s := hex.EncodeToString(h[:8]) // 16 hex
	var parts []string
	for i := 0; i < len(s); i += 4 {
		parts = append(parts, strings.ToUpper(s[i:i+4]))
	}
	return strings.Join(parts, "-")
}

// e2eOpenSeal ouvre une boîte scellée (ephPub32 || nonce12 || ct) chiffrée vers
// la clé publique de l'agent, et retourne le secret en clair (la racine R).
func e2eOpenSeal(blob []byte) ([]byte, error) {
	if len(blob) < 32+12+16 {
		return nil, fmt.Errorf("sceau trop court")
	}
	priv, err := e2ePrivateKey()
	if err != nil {
		return nil, err
	}
	ephPubBytes := blob[:32]
	nonce := blob[32:44]
	ct := blob[44:]
	ephPub, err := ecdh.X25519().NewPublicKey(ephPubBytes)
	if err != nil {
		return nil, err
	}
	shared, err := priv.ECDH(ephPub)
	if err != nil {
		return nil, err
	}
	key := sealKey(shared, ephPubBytes, priv.PublicKey().Bytes())
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ct, nil)
}

// sealKey dérive la clé AES de la boîte scellée (liée aux deux clés publiques).
func sealKey(shared, ephPub, agentPub []byte) []byte {
	h := sha256.New()
	h.Write(shared)
	h.Write(ephPub)
	h.Write(agentPub)
	return h.Sum(nil)
}

// chatKey dérive la clé AES-GCM du chat depuis la racine R.
func chatKey(r []byte) []byte {
	h := sha256.New()
	h.Write(r)
	h.Write([]byte("ajean-chat-v1"))
	return h.Sum(nil)
}

// kidOf identifie une racine R (8 premiers octets de SHA-256(R), hex).
func kidOf(r []byte) string {
	h := sha256.Sum256(r)
	return hex.EncodeToString(h[:8])
}

func newGCM(key []byte) (cipher.AEAD, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

// e2eResolveRoot retourne la racine R pour ce kid, en la descellant si besoin et
// en la mémorisant. sealedR peut être vide si R est déjà connue.
func e2eResolveRoot(kid string, sealed []byte) ([]byte, error) {
	e2eMu.Lock()
	if r, ok := e2eRoots[kid]; ok {
		e2eMu.Unlock()
		return r, nil
	}
	e2eMu.Unlock()
	if len(sealed) == 0 {
		return nil, fmt.Errorf("racine inconnue et aucun sceau fourni")
	}
	r, err := e2eOpenSeal(sealed)
	if err != nil {
		return nil, err
	}
	if kidOf(r) != kid {
		return nil, fmt.Errorf("kid ne correspond pas au sceau")
	}
	e2eMu.Lock()
	e2eRoots[kid] = r
	e2eMu.Unlock()
	return r, nil
}

// handleE2EChat : chat chiffré de bout en bout. Reçoit une enveloppe scellée,
// déchiffre la requête avec la clé dérivée de R, exécute le chat, et chiffre
// CHAQUE événement SSE. Le relais ne voit que de l'opaque.
func handleE2EChat(w http.ResponseWriter, r *http.Request) {
	var env struct{ Kid, SealedR, Iv, Ct string }
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		http.Error(w, "e2e: enveloppe invalide", 400)
		return
	}
	var sealed []byte
	if env.SealedR != "" {
		sealed, _ = base64.StdEncoding.DecodeString(env.SealedR)
	}
	root, err := e2eResolveRoot(env.Kid, sealed)
	if err != nil {
		http.Error(w, "e2e: "+err.Error(), 400)
		return
	}
	gcm, err := newGCM(chatKey(root))
	if err != nil {
		http.Error(w, "e2e: clé", 500)
		return
	}
	iv, _ := base64.StdEncoding.DecodeString(env.Iv)
	ct, _ := base64.StdEncoding.DecodeString(env.Ct)
	plain, err := gcm.Open(nil, iv, ct, nil)
	if err != nil {
		http.Error(w, "e2e: déchiffrement", 400)
		return
	}
	var body chatReq
	if err := json.Unmarshal(plain, &body); err != nil {
		http.Error(w, "e2e: requête", 400)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	emit := func(obj map[string]any) bool {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": obj}}})
		nonce := make([]byte, 12)
		if _, err := rand.Read(nonce); err != nil {
			return false
		}
		sealedEv := append(nonce, gcm.Seal(nil, nonce, b, nil)...)
		if _, err := w.Write([]byte("data: " + base64.StdEncoding.EncodeToString(sealedEv) + "\n\n")); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	runChatStream(r.Context(), body, emit)
}

// e2eSeal (côté agent : utilisé uniquement pour les tests) scelle un secret vers
// une clé publique X25519. La version navigateur est en WASM (wasmclient).
func e2eSeal(agentPub, secret []byte) ([]byte, error) {
	pub, err := ecdh.X25519().NewPublicKey(agentPub)
	if err != nil {
		return nil, err
	}
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		return nil, err
	}
	key := sealKey(shared, eph.PublicKey().Bytes(), agentPub)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, secret, nil)
	out := append([]byte{}, eph.PublicKey().Bytes()...)
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}
