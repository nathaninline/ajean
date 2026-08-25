package ajean

// mem_io.go — couche d'accès transparente aux pages mémoire. C'est le SEUL
// endroit qui sait si une page est chiffrée ou non : chat_memory.go passe par
// ici et n'a jamais à s'en soucier.
//
// Choix d'implémentation : le NOM du fichier ne change pas (toujours foo.md).
// C'est le CONTENU qui est chiffré en place, précédé d'un magic (voir
// mem_crypto.go) qui rend chaque fichier auto-descriptif. Avantage : rien à
// changer dans le listing, la recherche ou la validation de chemins — la
// détection se fait sur le contenu, pas sur l'extension.

import (
	"errors"
	"os"
	"strings"
)

// errMemLocked : page chiffrée alors que la mémoire est verrouillée (DEK absente).
var errMemLocked = errors.New("page chiffrée, mémoire verrouillée")

// memEncActive indique si le chiffrement de la mémoire est activé (réglage
// MEM_ENCRYPTED). Indépendant de l'état verrouillé/déverrouillé.
func memEncActive() bool {
	switch strings.ToLower(strings.TrimSpace(ReadConfig()["MEM_ENCRYPTED"])) {
	case "1", "on", "true", "yes", "oui":
		return true
	}
	return false
}

// decodeMemContent transforme le contenu BRUT d'un fichier en texte lisible :
//   - pas chiffré (pas de magic) : renvoyé tel quel.
//   - chiffré + DEK en RAM : déchiffré.
//   - chiffré + verrouillé : errMemLocked.
func decodeMemContent(raw []byte) ([]byte, error) {
	if !looksEncrypted(raw) {
		return raw, nil
	}
	dek, err := currentDEK()
	if err != nil {
		return nil, errMemLocked
	}
	return decPage(dek, raw)
}

// memReadPage lit une page par son nom et renvoie son texte clair. Erreur si le
// fichier est absent, ou chiffré alors que la mémoire est verrouillée.
func memReadPage(name string) ([]byte, error) {
	p, err := safeMemPath(name)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return decodeMemContent(raw)
}

// memPageText lit une page et renvoie (texte, lisible). Ne renvoie jamais
// d'erreur : une page chiffrée non déchiffrable donne readable=false (utile pour
// lister/chercher sans planter quand la mémoire est verrouillée).
func memPageText(name string) (string, bool) {
	b, err := memReadPage(name)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// encodeMemContent prépare le contenu à écrire sur disque selon l'état du
// chiffrement : chiffré si activé ET déverrouillé, clair sinon. Renvoie une
// erreur si le chiffrement est activé mais la mémoire verrouillée (on refuse
// alors d'écrire, plutôt que d'écrire en clair une donnée censée être chiffrée).
func encodeMemContent(plain []byte) ([]byte, error) {
	if !memEncActive() {
		return plain, nil
	}
	dek, err := currentDEK()
	if err != nil {
		return nil, errMemLocked
	}
	return encPage(dek, plain)
}

// writeMemFile écrit (crée ou remplace) une page de façon sûre : encode selon
// l'état du chiffrement, garde un .bak de la version précédente, écrit de façon
// atomique et vérifiée. Un échec laisse l'ancienne version intacte.
func writeMemFile(name string, plain []byte) error {
	p, err := safeMemPath(name)
	if err != nil {
		return err
	}
	out, err := encodeMemContent(plain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(memoryDir(), 0o755); err != nil {
		return err
	}
	// .bak de la version précédente (best-effort) : filet en cas de pépin juste
	// après le rename.
	if old, err := os.ReadFile(p); err == nil {
		_ = memWriteFileAtomic(p+".bak", old, 0o600)
	}
	return memWriteFileVerified(p, out, 0o600)
}

// writeMemPlain écrit une page EN CLAIR quel que soit l'état du chiffrement.
// Réservé au déchiffrement (DisableMemEncryption) : il faut pouvoir poser du
// clair pendant que le drapeau MEM_ENCRYPTED est encore levé, sinon writeMemFile
// re-chiffrerait ce qu'on cherche justement à déchiffrer.
func writeMemPlain(name string, plain []byte) error {
	p, err := safeMemPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(memoryDir(), 0o755); err != nil {
		return err
	}
	if old, err := os.ReadFile(p); err == nil {
		_ = memWriteFileAtomic(p+".bak", old, 0o600)
	}
	return memWriteFileVerified(p, plain, 0o600)
}
