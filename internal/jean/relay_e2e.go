package jean

// relay_e2e.go — chiffrement bout-en-bout du chat entre le NAVIGATEUR et CET agent, de
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
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

func newGCM(key []byte) (cipher.AEAD, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

// handleE2EChat : chat chiffré de bout en bout, AUTHENTIFIÉ. L'enveloppe est
// déchiffrée avec la clé du canal authentifié (liée à l'identité appairée de
// l'utilisateur) — un relais ne peut ni la lire ni la forger. Chaque événement SSE
// est chiffré. Le relais ne voit que de l'opaque.
func handleE2EChat(w http.ResponseWriter, r *http.Request) {
	plain, key, err := e2eAuthOpenReq(r)
	if err != nil {
		http.Error(w, "e2e: "+err.Error(), http.StatusForbidden)
		return
	}
	gcm, err := newGCM(key)
	if err != nil {
		http.Error(w, "e2e: clé", 500)
		return
	}
	var body chatReq
	if err := json.Unmarshal(plain, &body); err != nil {
		http.Error(w, "e2e: requête", 400)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	// no-transform : interdit à Cloudflare (proxy orange sur ajean.link) de
	// bufferiser/compresser le flux — sinon les événements arrivent en retard.
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	mu, stop := sseHeartbeat(w, flusher)
	defer stop()
	emit := func(obj map[string]any) bool {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": obj}}})
		nonce := make([]byte, 12)
		if _, err := rand.Read(nonce); err != nil {
			return false
		}
		sealedEv := append(nonce, gcm.Seal(nil, nonce, b, nil)...)
		mu.Lock()
		defer mu.Unlock()
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

// handleE2EReq : proxy de CONTRÔLE chiffré de bout en bout. Même enveloppe que le
// chat, mais le clair décrit un appel d'API interne {method, path, body}. On le
// redispatche dans le handler web LOCAL (inner, déjà authentifié), puis on chiffre
// la réponse {status, body}. Résultat : toute la gestion du serveur (presets, VRAM,
// skills, service…) transite par le relais SANS qu'il en voie le contenu. « Zéro
// exception » : le tunnel refuse tout /api/* en clair, seuls /api/e2e/* passent.
func handleE2EReq(w http.ResponseWriter, r *http.Request, inner http.Handler) {
	plain, key, err := e2eAuthOpenReq(r)
	if err != nil {
		http.Error(w, "e2e: "+err.Error(), http.StatusForbidden)
		return
	}
	gcm, err := newGCM(key)
	if err != nil {
		http.Error(w, "e2e: clé", 500)
		return
	}
	var req struct {
		Method string          `json:"method"`
		Path   string          `json:"path"`
		Body   json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(plain, &req); err != nil {
		writeE2EResp(w, gcm, 400, []byte(`{"error":"requête invalide"}`))
		return
	}
	// Sécurité : on ne dispatche QUE des chemins d'API internes, jamais /api/e2e/*
	// (pas de récursion) ni autre chose (pas d'accès à l'UI/aux assets par ce biais).
	if !strings.HasPrefix(req.Path, "/api/") || strings.HasPrefix(req.Path, "/api/e2e") {
		writeE2EResp(w, gcm, 403, []byte(`{"error":"chemin interdit"}`))
		return
	}
	method := req.Method
	if method == "" {
		method = "GET"
	}
	// Un chemin/méthode mal formé ferait paniquer httptest.NewRequest : on récupère.
	defer func() {
		if rec := recover(); rec != nil {
			writeE2EResp(w, gcm, 400, []byte(`{"error":"requête malformée"}`))
		}
	}()
	var bodyReader io.Reader
	if len(req.Body) > 0 && string(req.Body) != "null" {
		bodyReader = bytes.NewReader(req.Body)
	}
	ir := httptest.NewRequest(method, req.Path, bodyReader)
	ir.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	inner.ServeHTTP(rec, ir)
	respBody := rec.Body.Bytes()
	if len(respBody) == 0 {
		respBody = []byte("null")
	} else if !json.Valid(respBody) {
		// Réponse non-JSON (ex : http.Error en texte) : on l'enveloppe en chaîne.
		respBody, _ = json.Marshal(string(respBody))
	}
	writeE2EResp(w, gcm, rec.Code, respBody)
}

// writeE2EResp chiffre {status, body} avec la clé du chat et l'écrit en base64
// (nonce||ct). Le relais ne voit que de l'opaque.
func writeE2EResp(w http.ResponseWriter, gcm cipher.AEAD, status int, body []byte) {
	out, _ := json.Marshal(map[string]any{"status": status, "body": json.RawMessage(body)})
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		http.Error(w, "e2e: nonce", 500)
		return
	}
	sealed := append(nonce, gcm.Seal(nil, nonce, out, nil)...)
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(sealed)))
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
