// Package secrets garde les mots de passe et les clés hors de la mémoire : un
// coffre chiffré (.ajean/vault.json) que seuls les outils autorisés lisent, et
// la détection qui empêche d'en écrire dans les fiches ou les copies.
//
// La clé du coffre est un fichier de la machine (.ajean/vault.key). Le coffre
// protège donc une copie isolée des données, pas une machine déjà compromise.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"ajean/internal/home"
)

// Masked remplace un secret dans ce qui est gardé ou montré au modèle.
const Masked = "[secret masqué]"

var (
	mu     sync.Mutex
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
)

func keyPath() string   { return home.State("vault.key") }
func vaultPath() string { return home.State("vault.json") }

func key() ([]byte, error) {
	raw, err := os.ReadFile(keyPath())
	if err == nil {
		k, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if derr != nil || len(k) != 32 {
			return nil, errors.New("clé du coffre illisible")
		}
		return k, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, home.WriteFile(keyPath(), []byte(base64.StdEncoding.EncodeToString(k)))
}

func gcm() (cipher.AEAD, error) {
	k, err := key()
	if err != nil {
		return nil, err
	}
	b, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func load() (map[string]string, error) {
	out := map[string]string{}
	raw, err := os.ReadFile(vaultPath())
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	return out, json.Unmarshal(raw, &out)
}

func store(v map[string]string) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return home.WriteFile(vaultPath(), raw)
}

func normalize(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !nameRe.MatchString(name) {
		return "", errors.New("nom de secret invalide : minuscules, chiffres, point, tiret ou _")
	}
	return name, nil
}

// Set chiffre et range un secret.
func Set(name, value string) error {
	name, err := normalize(name)
	if err != nil {
		return err
	}
	if value == "" {
		return errors.New("secret vide")
	}
	mu.Lock()
	defer mu.Unlock()
	a, err := gcm()
	if err != nil {
		return err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	v, err := load()
	if err != nil {
		return err
	}
	v[name] = base64.StdEncoding.EncodeToString(a.Seal(nonce, nonce, []byte(value), []byte(name)))
	return store(v)
}

// Get déchiffre un secret.
func Get(name string) (string, error) {
	name, err := normalize(name)
	if err != nil {
		return "", err
	}
	mu.Lock()
	defer mu.Unlock()
	v, err := load()
	if err != nil {
		return "", err
	}
	enc, ok := v[name]
	if !ok {
		return "", fmt.Errorf("secret « %s » absent du coffre (commande : ajean secret set %s)", name, name)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	a, err := gcm()
	if err != nil {
		return "", err
	}
	if len(raw) < a.NonceSize() {
		return "", errors.New("secret illisible")
	}
	plain, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], []byte(name))
	if err != nil {
		return "", errors.New("secret illisible")
	}
	return string(plain), nil
}

// Delete retire un secret du coffre.
func Delete(name string) error {
	name, err := normalize(name)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	v, err := load()
	if err != nil {
		return err
	}
	if _, ok := v[name]; !ok {
		return fmt.Errorf("secret « %s » absent du coffre", name)
	}
	delete(v, name)
	return store(v)
}

// Names : les noms des secrets, jamais leurs valeurs.
func Names() ([]string, error) {
	mu.Lock()
	defer mu.Unlock()
	v, err := load()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(v))
	for k := range v {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// ---- détection ------------------------------------------------------------------

var (
	labelled = regexp.MustCompile("(?i)\\b(password|passwd|passphrase|pwd|mot de passe|mdp|api[ _-]?key|cl[ée] api|access[ _-]?token|auth[ _-]?token|token|secret)\\b([^\\n:=]{0,24}[:=]\\s*[`\"']?)([^\\s`\"',;)]{4,})")
	piped    = regexp.MustCompile(`(?i)\becho\s+(['"]?)([^\s'"|]{3,})(['"]?)\s*\|\s*sudo\s+-S`)
	tokens   = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{20,})\b`)
)

// placeholder : une valeur qui désigne un secret sans le contenir (variable,
// exemple, code qui va le chercher ailleurs).
func placeholder(v string) bool {
	if strings.ContainsAny(v, "[](){}<>$%*") || strings.HasPrefix(v, "secret:") {
		return true
	}
	switch strings.ToLower(strings.TrimRight(v, ".")) {
	case "none", "null", "true", "false", "vide", "empty", "absent", "aucun", "aucune", "required", "requis", "obligatoire",
		"masqué", "masque", "xxxx", "xxxxx", "xxxxxx", "exemple", "example", "changeme", "password", "secret", "token":
		return true
	}
	return strings.Contains(v, ".") && !strings.ContainsAny(v, "0123456789") // cfg.password, os.environ.x
}

// Redact remplace tout secret reconnu.
func Redact(s string) string {
	s = labelled.ReplaceAllStringFunc(s, func(m string) string {
		p := labelled.FindStringSubmatch(m)
		if placeholder(p[3]) {
			return m
		}
		return p[1] + p[2] + Masked
	})
	s = piped.ReplaceAllString(s, "echo "+Masked+" | sudo -S")
	return tokens.ReplaceAllString(s, Masked)
}

// Contains : le texte porte-t-il un secret reconnu ?
func Contains(s string) bool { return Redact(s) != s }
