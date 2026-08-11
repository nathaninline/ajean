package nodewire

// crypto.go — chiffrement de bout en bout du canal poste↔agent, pour que le
// relais ajean.link ne voie que de l'opaque (« relais aveugle »), exactement
// comme le canal navigateur↔agent.
//
// Deux briques, toutes en stdlib (crypto/ecdh X25519 + AES-GCM) :
//   - un SCEAU anonyme vers la clé publique de l'agent (enrôlement) : format
//     IDENTIQUE à e2eSeal/e2eOpenSeal de l'agent (ephPub32 || nonce12 || ct,
//     clé = SHA256(shared || ephPub || agentPub)) — donc l'agent ouvre le sceau
//     du poste avec son code existant, sans dupliquer la crypto ;
//   - un CANAL authentifié à clés statiques : K = SHA256(ECDH(postePriv,
//     agentPub) || "ajean-node-chan-v1"). Le poste et l'agent le calculent tous
//     les deux ; le relais, qui n'a aucune des deux clés privées, non. Chaque
//     message est scellé AES-GCM avec un nonce à compteur (anti-rejeu / anti-
//     réordonnancement à l'intérieur d'une connexion).

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// GenKeyPair génère une paire X25519 et la renvoie en hex (priv, pub).
func GenKeyPair() (privHex, pubHex string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(k.Bytes()), hex.EncodeToString(k.PublicKey().Bytes()), nil
}

// PubFromPriv recalcule la clé publique hex à partir de la privée hex.
func PubFromPriv(privHex string) (string, error) {
	raw, err := hex.DecodeString(privHex)
	if err != nil {
		return "", err
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(k.PublicKey().Bytes()), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// SealTo scelle plaintext vers recipientPubHex (clé publique de l'agent). Le
// format est celui qu'ouvre e2eOpenSeal de l'agent : ephPub32 || nonce12 || ct.
func SealTo(recipientPubHex string, plaintext []byte) ([]byte, error) {
	recBytes, err := hex.DecodeString(recipientPubHex)
	if err != nil {
		return nil, err
	}
	recPub, err := ecdh.X25519().NewPublicKey(recBytes)
	if err != nil {
		return nil, err
	}
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := eph.ECDH(recPub)
	if err != nil {
		return nil, err
	}
	key := sealKey(shared, eph.PublicKey().Bytes(), recBytes)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, 32+12+len(ct))
	out = append(out, eph.PublicKey().Bytes()...)
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// sealKey — identique à sealKey de l'agent (relay_e2e.go). Ne pas modifier sans
// changer les deux : c'est ce qui garantit que l'agent ouvre le sceau du poste.
func sealKey(shared, ephPub, recipientPub []byte) []byte {
	h := sha256.New()
	h.Write(shared)
	h.Write(ephPub)
	h.Write(recipientPub)
	return h.Sum(nil)
}

// ChannelKey dérive la clé du canal poste↔agent à partir de la clé PRIVÉE (hex)
// d'un bout et de la clé PUBLIQUE (hex) de l'autre. Symétrique : les deux bouts
// obtiennent le même secret.
func ChannelKey(privHex, peerPubHex string) ([]byte, error) {
	pr, err := hex.DecodeString(privHex)
	if err != nil {
		return nil, err
	}
	priv, err := ecdh.X25519().NewPrivateKey(pr)
	if err != nil {
		return nil, err
	}
	pb, err := hex.DecodeString(peerPubHex)
	if err != nil {
		return nil, err
	}
	peer, err := ecdh.X25519().NewPublicKey(pb)
	if err != nil {
		return nil, err
	}
	ss, err := priv.ECDH(peer)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write(ss)
	h.Write([]byte("ajean-node-chan-v1"))
	return h.Sum(nil), nil
}

// Frame est un message chiffré du canal : nonce (side||compteur) + ciphertext.
type Frame struct {
	N []byte `json:"n"`
	C []byte `json:"c"`
}

// Chan chiffre/déchiffre les messages d'un canal, avec des nonces à compteur
// pour interdire rejeu et réordonnancement DANS la connexion. Chaque bout a un
// « côté » (1 octet) distinct → les nonces des deux sens ne se croisent jamais.
type Chan struct {
	gcm      cipher.AEAD
	sendSide byte
	recvSide byte
	sendCtr  uint64
	recvNext uint64
}

// NewChan construit un canal à partir de la clé partagée. initiator=true pour le
// POSTE (côté d'envoi 1), false pour l'AGENT (côté d'envoi 2). Les côtés doivent
// être opposés des deux bouts, sinon les nonces entreraient en collision.
func NewChan(key []byte, initiator bool) (*Chan, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	c := &Chan{gcm: gcm}
	if initiator {
		c.sendSide, c.recvSide = 1, 2
	} else {
		c.sendSide, c.recvSide = 2, 1
	}
	return c, nil
}

func (c *Chan) nonce(side byte, ctr uint64) []byte {
	n := make([]byte, 12)
	n[0] = side
	binary.BigEndian.PutUint64(n[1:9], ctr)
	return n
}

// Seal chiffre un message sortant.
func (c *Chan) Seal(plaintext []byte) Frame {
	n := c.nonce(c.sendSide, c.sendCtr)
	ct := c.gcm.Seal(nil, n, plaintext, nil)
	c.sendCtr++
	return Frame{N: n, C: ct}
}

// Open déchiffre un message entrant et vérifie l'ordre (compteur strictement
// croissant, bon côté) — un frame rejoué ou réordonné par le relais est rejeté.
func (c *Chan) Open(f Frame) ([]byte, error) {
	if len(f.N) != 12 || f.N[0] != c.recvSide {
		return nil, errors.New("nonce invalide")
	}
	ctr := binary.BigEndian.Uint64(f.N[1:9])
	if ctr < c.recvNext {
		return nil, fmt.Errorf("rejeu détecté (compteur %d < %d)", ctr, c.recvNext)
	}
	plain, err := c.gcm.Open(nil, f.N, f.C, nil)
	if err != nil {
		return nil, errors.New("authentification échouée")
	}
	c.recvNext = ctr + 1
	return plain, nil
}
