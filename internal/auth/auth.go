// Package auth garde la porte de l'interface : un mot de passe, des sessions,
// et un frein contre ceux qui essaient des mots de passe à la chaîne.
//
// Tant qu'aucun mot de passe n'existe, seule la machine elle-même peut ouvrir
// l'interface (le serveur le vérifie). Dès qu'il y en a un, tout le monde se
// connecte, la machine comprise.
//
// Rien de secret n'est écrit en clair dans .ajean/auth.json :
//   - le mot de passe y est dérivé (PBKDF2-SHA256, sel aléatoire) ;
//   - une session y est l'empreinte SHA-256 de son jeton. Le jeton lui-même ne
//     vit que dans le cookie du navigateur : lire le fichier ne permet pas
//     d'entrer.
//
// AJEAN_WEB_KEY, si elle est définie et qu'aucun mot de passe n'est enregistré,
// tient lieu de mot de passe.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"ajean/internal/home"
)

const (
	MinPassword = 10   // caractères
	maxPassword = 1024 // octets : au-delà, ce n'est plus un mot de passe

	saltBytes = 16
	keyBytes  = 32

	sessionLife  = 30 * 24 * time.Hour // sans visite pendant ce temps, la session s'éteint
	seenStep     = time.Hour           // on ne réécrit pas le fichier à chaque requête
	maxSessions  = 50                  // au-delà, les plus anciennes sont fermées
	freeTries    = 5                   // essais ratés avant la première attente
	firstWait    = 30 * time.Second    // puis elle double à chaque nouvel échec
	maxWait      = time.Hour
	globalWindow = 10 * time.Minute // fenêtre du frein commun à toutes les adresses
	globalTries  = 100              // essais ratés tolérés dans cette fenêtre
	globalWait   = time.Minute
	maxTracked   = 10_000 // adresses suivies au plus (la mémoire reste bornée)
)

// iterations : coût de PBKDF2-SHA256 (recommandation OWASP). Les tests l'abaissent.
var iterations = 600_000

var (
	ErrWrong    = errors.New("mot de passe incorrect")
	ErrTooShort = fmt.Errorf("mot de passe trop court : %d caractères au moins", MinPassword)
	ErrTooLong  = errors.New("mot de passe trop long")
)

// LockedError : trop d'essais ratés, il faut attendre.
type LockedError struct{ Wait time.Duration }

func (e *LockedError) Error() string {
	return fmt.Sprintf("trop d'essais : nouvel essai possible dans %d s", int(e.Wait.Seconds())+1)
}

type password struct {
	Salt string `json:"salt"`
	Hash string `json:"hash"`
	Iter int    `json:"iter"`
}

type session struct {
	Created time.Time `json:"created"`
	Seen    time.Time `json:"seen"`
	Agent   string    `json:"agent,omitempty"`
	IP      string    `json:"ip,omitempty"`
}

type state struct {
	Password *password           `json:"password,omitempty"`
	Sessions map[string]*session `json:"sessions,omitempty"` // clé : empreinte du jeton
}

type attempts struct {
	fails int
	last  time.Time
	until time.Time
}

var (
	mu     sync.Mutex
	st     state
	loaded string    // fichier dont st est l'image
	stamp  time.Time // sa date quand on l'a lu ou écrit

	tries       = map[string]*attempts{}
	globalFails []time.Time
	globalUntil time.Time

	// Dériver un mot de passe coûte du processeur, exprès. Deux à la fois au plus :
	// une rafale d'essais ne peut pas occuper toute la machine.
	hashing = make(chan struct{}, 2)

	now = time.Now // remplacée dans les tests
)

func file() string { return home.State("auth.json") }

// reload relit le fichier s'il a changé sous nos pieds (la commande « ajean
// password » l'écrit pendant que le serveur tourne). À appeler verrou tenu.
func reload() {
	path := file()
	info, err := os.Stat(path)
	if err != nil {
		if path != loaded || !stamp.IsZero() {
			st, loaded, stamp = state{}, path, time.Time{}
		}
		return
	}
	if path == loaded && info.ModTime().Equal(stamp) {
		return
	}
	var fresh state
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &fresh)
	}
	st, loaded, stamp = fresh, path, info.ModTime()
}

func save() error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	path := file()
	if err := home.WriteFile(path, data); err != nil {
		return err
	}
	_ = os.Chmod(path, 0o600)
	loaded = path
	if info, err := os.Stat(path); err == nil {
		stamp = info.ModTime()
	}
	return nil
}

func envKey() string { return os.Getenv("AJEAN_WEB_KEY") }

// Configured : un mot de passe protège-t-il l'interface ?
func Configured() bool {
	mu.Lock()
	defer mu.Unlock()
	reload()
	return st.Password != nil || envKey() != ""
}

// FromEnv : le mot de passe est-il celui d'AJEAN_WEB_KEY (aucun n'est enregistré) ?
func FromEnv() bool {
	mu.Lock()
	defer mu.Unlock()
	reload()
	return st.Password == nil && envKey() != ""
}

func derive(pw string, salt []byte, iter int) []byte {
	hashing <- struct{}{}
	defer func() { <-hashing }()
	key, err := pbkdf2.Key(sha256.New, pw, salt, iter, keyBytes)
	if err != nil {
		return nil
	}
	return key
}

// matches : pw est-il le bon mot de passe ? Verrou NON tenu (le calcul est long).
func matches(pw string) bool {
	mu.Lock()
	reload()
	p := st.Password
	mu.Unlock()
	if len(pw) > maxPassword {
		return false
	}
	if p == nil {
		key := envKey()
		if key == "" {
			return false
		}
		a, b := sha256.Sum256([]byte(pw)), sha256.Sum256([]byte(key))
		return subtle.ConstantTimeCompare(a[:], b[:]) == 1
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(p.Salt)
	want, err2 := base64.RawStdEncoding.DecodeString(p.Hash)
	if err1 != nil || err2 != nil || p.Iter <= 0 {
		return false
	}
	got := derive(pw, salt, p.Iter)
	return got != nil && subtle.ConstantTimeCompare(got, want) == 1
}

// SetPassword enregistre un nouveau mot de passe et ferme toutes les sessions :
// qui était connecté avec l'ancien ne le reste pas.
func SetPassword(pw string) error {
	if utf8.RuneCountInString(pw) < MinPassword {
		return ErrTooShort
	}
	if len(pw) > maxPassword {
		return ErrTooLong
	}
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key := derive(pw, salt, iterations)
	if key == nil {
		return errors.New("dérivation du mot de passe impossible")
	}
	mu.Lock()
	defer mu.Unlock()
	reload()
	st.Password = &password{
		Salt: base64.RawStdEncoding.EncodeToString(salt),
		Hash: base64.RawStdEncoding.EncodeToString(key),
		Iter: iterations,
	}
	st.Sessions = nil
	return save()
}

// RemovePassword retire le mot de passe et ferme toutes les sessions :
// l'interface redevient réservée à la machine elle-même.
func RemovePassword() error {
	mu.Lock()
	defer mu.Unlock()
	reload()
	st.Password, st.Sessions = nil, nil
	return save()
}

// ---- frein contre les essais à la chaîne -------------------------------------

// bucket : ce qui compte comme « une adresse ». En IPv6, un abonné dispose d'un
// /64 entier : on le compte pour un.
func bucket(ip string) string {
	p := net.ParseIP(ip)
	if p == nil {
		return ip
	}
	if p.To4() != nil {
		return p.String()
	}
	return p.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// Locked : combien de temps cette adresse doit encore attendre (0 : elle peut essayer).
func Locked(ip string) time.Duration {
	mu.Lock()
	defer mu.Unlock()
	return locked(bucket(ip))
}

func locked(b string) time.Duration {
	t := now()
	wait := globalUntil.Sub(t)
	if a := tries[b]; a != nil && a.until.Sub(t) > wait {
		wait = a.until.Sub(t)
	}
	if wait < 0 {
		return 0
	}
	return wait
}

func failed(b string) {
	t := now()
	a := tries[b]
	if a == nil {
		if len(tries) >= maxTracked {
			prune(t)
		}
		a = &attempts{}
		tries[b] = a
	}
	if t.Sub(a.last) > maxWait { // l'ardoise s'efface après une heure de calme
		a.fails = 0
	}
	a.fails++
	a.last = t
	if a.fails >= freeTries {
		wait := maxWait
		if shift := a.fails - freeTries; shift < 8 {
			wait = min(firstWait<<shift, maxWait)
		}
		a.until = t.Add(wait)
	}
	// Frein commun : des milliers d'adresses qui essaient chacune quelques fois
	// passent sous le frein par adresse, pas sous celui-ci.
	keep := globalFails[:0]
	for _, f := range globalFails {
		if t.Sub(f) < globalWindow {
			keep = append(keep, f)
		}
	}
	globalFails = append(keep, t)
	if len(globalFails) >= globalTries {
		globalUntil = t.Add(globalWait)
		globalFails = globalFails[:0]
	}
}

func prune(t time.Time) {
	for k, a := range tries {
		if t.Sub(a.last) > maxWait {
			delete(tries, k)
		}
	}
	if len(tries) >= maxTracked { // attaque très large : on repart de zéro, le frein commun tient
		tries = map[string]*attempts{}
	}
}

// Verify vérifie un mot de passe pour le compte d'une adresse, frein compris.
func Verify(pw, ip string) error {
	b := bucket(ip)
	mu.Lock()
	wait := locked(b)
	mu.Unlock()
	if wait > 0 {
		return &LockedError{Wait: wait}
	}
	if matches(pw) {
		mu.Lock()
		delete(tries, b)
		mu.Unlock()
		return nil
	}
	mu.Lock()
	failed(b)
	wait = locked(b)
	mu.Unlock()
	if wait > 0 {
		return &LockedError{Wait: wait}
	}
	return ErrWrong
}

// ---- sessions ------------------------------------------------------------------

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func expired(s *session, t time.Time) bool { return t.Sub(s.Seen) > sessionLife }

// Open ouvre une session et rend son jeton (à poser dans le cookie).
func Open(ip, agent string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if len(agent) > 200 {
		agent = agent[:200]
	}
	mu.Lock()
	defer mu.Unlock()
	reload()
	t := now()
	if st.Sessions == nil {
		st.Sessions = map[string]*session{}
	}
	for k, s := range st.Sessions {
		if expired(s, t) {
			delete(st.Sessions, k)
		}
	}
	if len(st.Sessions) >= maxSessions {
		keys := make([]string, 0, len(st.Sessions))
		for k := range st.Sessions {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return st.Sessions[keys[i]].Seen.Before(st.Sessions[keys[j]].Seen) })
		for _, k := range keys[:len(keys)-maxSessions+1] {
			delete(st.Sessions, k)
		}
	}
	st.Sessions[digest(token)] = &session{Created: t, Seen: t, Agent: agent, IP: ip}
	return token, save()
}

// Valid : ce jeton ouvre-t-il une session en cours ? Chaque visite la prolonge.
func Valid(token string) bool {
	if token == "" {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	reload()
	key := digest(token)
	s := st.Sessions[key]
	if s == nil {
		return false
	}
	t := now()
	if expired(s, t) {
		delete(st.Sessions, key)
		_ = save()
		return false
	}
	if t.Sub(s.Seen) > seenStep {
		s.Seen = t
		_ = save()
	}
	return true
}

// Close ferme une session.
func Close(token string) {
	mu.Lock()
	defer mu.Unlock()
	reload()
	if _, ok := st.Sessions[digest(token)]; ok {
		delete(st.Sessions, digest(token))
		_ = save()
	}
}

// CloseOthers ferme toutes les sessions sauf celle de ce jeton, et dit combien.
func CloseOthers(token string) int {
	mu.Lock()
	defer mu.Unlock()
	reload()
	keep, n := digest(token), 0
	for k := range st.Sessions {
		if k != keep {
			delete(st.Sessions, k)
			n++
		}
	}
	if n > 0 {
		_ = save()
	}
	return n
}

// Count : le nombre de sessions en cours.
func Count() int {
	mu.Lock()
	defer mu.Unlock()
	reload()
	n, t := 0, now()
	for _, s := range st.Sessions {
		if !expired(s, t) {
			n++
		}
	}
	return n
}
