package auth

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// fresh : un dossier de données vide, un frein remis à zéro, un calcul rapide.
func fresh(t *testing.T) {
	t.Helper()
	t.Setenv("AJEAN_HOME", t.TempDir())
	t.Setenv("AJEAN_WEB_KEY", "")
	old := iterations
	iterations = 1000
	mu.Lock()
	tries, globalFails, globalUntil = map[string]*attempts{}, nil, time.Time{}
	mu.Unlock()
	t.Cleanup(func() { iterations = old; now = time.Now })
}

const good = "un bon mot de passe"

func TestPasswordIsStoredDerived(t *testing.T) {
	fresh(t)
	if Configured() {
		t.Fatal("aucun mot de passe au départ")
	}
	if err := SetPassword("court"); !errors.Is(err, ErrTooShort) {
		t.Errorf("mot de passe court : %v", err)
	}
	if err := SetPassword(good); err != nil {
		t.Fatal(err)
	}
	if !Configured() {
		t.Fatal("le mot de passe devrait exister")
	}
	data, err := os.ReadFile(file())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), good) {
		t.Error("le mot de passe est écrit en clair")
	}
	if err := Verify(good, "203.0.113.5"); err != nil {
		t.Errorf("bon mot de passe refusé : %v", err)
	}
	if err := Verify("un mauvais mot de passe", "203.0.113.5"); !errors.Is(err, ErrWrong) {
		t.Errorf("mauvais mot de passe : %v", err)
	}
}

func TestEnvKeyStandsInForThePassword(t *testing.T) {
	fresh(t)
	t.Setenv("AJEAN_WEB_KEY", "secret")
	if !Configured() || !FromEnv() {
		t.Fatal("AJEAN_WEB_KEY devrait tenir lieu de mot de passe")
	}
	if err := Verify("secret", "203.0.113.5"); err != nil {
		t.Errorf("clé refusée : %v", err)
	}
	if err := Verify("autre", "203.0.113.5"); !errors.Is(err, ErrWrong) {
		t.Errorf("mauvaise clé : %v", err)
	}
	// Un mot de passe enregistré prend le pas sur la variable.
	if err := SetPassword(good); err != nil {
		t.Fatal(err)
	}
	if FromEnv() || Verify("secret", "203.0.113.6") == nil {
		t.Error("la variable ne doit plus ouvrir une fois un mot de passe enregistré")
	}
}

func TestRepeatedFailuresLockTheAddress(t *testing.T) {
	fresh(t)
	if err := SetPassword(good); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	now = func() time.Time { return t0 }
	const ip = "203.0.113.9"
	var locked *LockedError
	for i := 1; i <= freeTries; i++ {
		err := Verify("faux", ip)
		if i < freeTries && !errors.Is(err, ErrWrong) {
			t.Fatalf("essai %d : %v", i, err)
		}
		if i == freeTries && !errors.As(err, &locked) {
			t.Fatalf("essai %d : attente attendue, reçu %v", i, err)
		}
	}
	// Bloquée : même le bon mot de passe attend. Une autre adresse n'est pas touchée.
	if err := Verify(good, ip); !errors.As(err, &locked) {
		t.Errorf("adresse bloquée : %v", err)
	}
	if err := Verify(good, "203.0.113.10"); err != nil {
		t.Errorf("autre adresse : %v", err)
	}
	// L'attente passée, on peut réessayer ; un nouvel échec double l'attente.
	now = func() time.Time { return t0.Add(firstWait + time.Second) }
	if err := Verify("faux", ip); !errors.As(err, &locked) || locked.Wait <= firstWait {
		t.Errorf("deuxième attente : %v", err)
	}
	now = func() time.Time { return t0.Add(maxWait) }
	if err := Verify(good, ip); err != nil {
		t.Errorf("après l'attente : %v", err)
	}
}

func TestManyAddressesTripTheSharedBrake(t *testing.T) {
	fresh(t)
	if err := SetPassword(good); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < globalTries; i++ {
		_ = Verify("faux", "198.51.100."+string(rune('0'+i%10))+string(rune('0'+i/10%10)))
	}
	var locked *LockedError
	if err := Verify(good, "192.0.2.77"); !errors.As(err, &locked) {
		t.Errorf("frein commun : %v", err)
	}
}

func TestIPv6CountsPerSubnet(t *testing.T) {
	if bucket("2001:db8:1:2:aaaa::1") != bucket("2001:db8:1:2:bbbb::2") {
		t.Error("deux adresses du même /64 doivent compter pour une")
	}
	if bucket("2001:db8:1:2::1") == bucket("2001:db8:1:3::1") {
		t.Error("deux /64 différents ne doivent pas se confondre")
	}
}

func TestSessions(t *testing.T) {
	fresh(t)
	if err := SetPassword(good); err != nil {
		t.Fatal(err)
	}
	a, err := Open("203.0.113.5", "navigateur A")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Open("203.0.113.6", "navigateur B")
	if !Valid(a) || !Valid(b) || Valid("") || Valid("inventé") {
		t.Fatal("validité des sessions")
	}
	data, _ := os.ReadFile(file())
	if strings.Contains(string(data), a) {
		t.Error("le jeton de session est écrit en clair")
	}
	if Count() != 2 {
		t.Errorf("%d sessions, attendu 2", Count())
	}
	if n := CloseOthers(a); n != 1 || !Valid(a) || Valid(b) {
		t.Errorf("fermeture des autres : %d fermée(s)", n)
	}
	Close(a)
	if Valid(a) {
		t.Error("session fermée encore valide")
	}

	// Une session sans visite s'éteint ; une visite la prolonge.
	c, _ := Open("203.0.113.5", "")
	t0 := time.Now()
	now = func() time.Time { return t0.Add(sessionLife - time.Hour) }
	if !Valid(c) {
		t.Fatal("session encore dans les temps")
	}
	now = func() time.Time { return t0.Add(2*sessionLife - 2*time.Hour) }
	if !Valid(c) {
		t.Error("la visite aurait dû prolonger la session")
	}
	now = func() time.Time { return t0.Add(4 * sessionLife) }
	if Valid(c) {
		t.Error("session expirée encore valide")
	}
}

func TestNewPasswordClosesEverySession(t *testing.T) {
	fresh(t)
	if err := SetPassword(good); err != nil {
		t.Fatal(err)
	}
	tok, _ := Open("203.0.113.5", "")
	if err := SetPassword("un autre mot de passe"); err != nil {
		t.Fatal(err)
	}
	if Valid(tok) {
		t.Error("l'ancienne session survit au changement de mot de passe")
	}
	tok, _ = Open("203.0.113.5", "")
	if err := RemovePassword(); err != nil {
		t.Fatal(err)
	}
	if Configured() || Valid(tok) {
		t.Error("retrait du mot de passe incomplet")
	}
}

// « ajean password » écrit le fichier pendant que le serveur tourne : il doit
// le voir sans redémarrer.
func TestFileChangedByAnotherProcessIsSeen(t *testing.T) {
	fresh(t)
	if err := SetPassword(good); err != nil {
		t.Fatal(err)
	}
	tok, _ := Open("203.0.113.5", "")
	if err := os.WriteFile(file(), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(file(), time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	if Configured() || Valid(tok) {
		t.Error("le fichier réécrit par ailleurs n'a pas été relu")
	}
}
