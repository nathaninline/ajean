package ajean

// mem_store.go — chiffrement transparent de VALEURS de la base (bbolt), pour
// étendre le chiffrement au-delà des pages mémoire : conversations et archives
// contiennent aussi des infos sensibles.
//
// Même enveloppe que les pages (encPage/decPage + magic) : une valeur est
// chiffrée quand le chiffrement est actif ET la mémoire déverrouillée, en clair
// sinon. La détection se fait sur le magic, donc clair et chiffré cohabitent
// (utile pendant une migration ou un état verrouillé).

import (
	"encoding/json"
	"errors"
)

// errStoreLocked : écriture chiffrée demandée alors que la DEK n'est pas en RAM.
var errStoreLocked = errors.New("valeur chiffrée, mémoire verrouillée")

// putStoreBytes écrit une valeur, chiffrée si le chiffrement est actif ET
// déverrouillé. Si actif mais verrouillé, renvoie errStoreLocked SANS écrire
// (on ne remplace jamais une valeur chiffrée par du clair).
func putStoreBytes(bucket, key string, plain []byte) error {
	out, err := encodeMemContent(plain)
	if err != nil {
		return errStoreLocked
	}
	return putBytes(bucket, key, out)
}

// getStoreBytes lit une valeur et la déchiffre au besoin. Renvoie (nil, false)
// si absente, ou si chiffrée alors que la mémoire est verrouillée.
func getStoreBytes(bucket, key string) ([]byte, bool) {
	raw := getBytes(bucket, key)
	if len(raw) == 0 {
		return nil, false
	}
	dec, err := decodeMemContent(raw)
	if err != nil {
		return nil, false
	}
	return dec, true
}

// putStoreJSON sérialise puis écrit (chiffré si actif+déverrouillé).
func putStoreJSON(bucket, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return putStoreBytes(bucket, key, b)
}

// getStoreJSON lit puis décode (déchiffre au besoin). false si absent/verrouillé.
func getStoreJSON(bucket, key string, dst any) bool {
	b, ok := getStoreBytes(bucket, key)
	if !ok {
		return false
	}
	return json.Unmarshal(b, dst) == nil
}

// reencryptBucket (re)chiffre toutes les valeurs d'un bucket encore en clair,
// avec vérification par relecture. Sûr à rejouer. Exige la DEK en RAM.
func reencryptBucket(bucket string) error {
	for k, v := range allKV(bucket) {
		raw := []byte(v)
		if looksEncrypted(raw) {
			continue
		}
		if err := putStoreBytes(bucket, k, raw); err != nil {
			return err
		}
		back, ok := getStoreBytes(bucket, k)
		if !ok || string(back) != v {
			return errors.New("vérification post-chiffrement échouée pour " + bucket + "/" + k)
		}
	}
	return nil
}

// bucketFullyEncrypted indique qu'AUCUNE valeur du bucket n'est en clair (toutes
// portent le magic). Lisible sans la DEK. Utilisé pour savoir si le chiffrement
// est réellement complet.
func bucketFullyEncrypted(bucket string) bool {
	for _, v := range allKV(bucket) {
		if v != "" && !looksEncrypted([]byte(v)) {
			return false
		}
	}
	return true
}

// encryptedBuckets : les buckets dont les valeurs sont chiffrées quand le
// chiffrement est actif (conversation courante + archives + index de sessions).
// Les autres buckets (config, prefs, state, tasks) restent en clair : ils portent
// des réglages, pas des données personnelles de conversation.
var encryptedBuckets = []string{bkChat, bkChatHist, bkChatMeta, bkTracker}

// reencryptChatStores (re)chiffre les buckets de conversation. Exige la DEK.
func reencryptChatStores() error {
	for _, b := range encryptedBuckets {
		if err := reencryptBucket(b); err != nil {
			return err
		}
	}
	return nil
}

// decryptChatStores remet en clair les buckets de conversation. Exige la DEK.
func decryptChatStores() error {
	for _, b := range encryptedBuckets {
		if err := decryptBucket(b); err != nil {
			return err
		}
	}
	return nil
}

// decryptBucket remet en clair toutes les valeurs chiffrées d'un bucket. Exige
// la DEK en RAM. Sûr à rejouer.
func decryptBucket(bucket string) error {
	for k, v := range allKV(bucket) {
		raw := []byte(v)
		if !looksEncrypted(raw) {
			continue
		}
		plain, err := decodeMemContent(raw)
		if err != nil {
			return err
		}
		if err := putBytes(bucket, k, plain); err != nil {
			return err
		}
	}
	return nil
}
