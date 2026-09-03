package ajean

import (
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// TestLoadConversationRetriesThroughLockContention vérifie le comportement
// sous contention DB, avec une réserve honnête : ceci ne reproduit PAS la
// panne réelle avec certitude. bolt.Open a déjà son propre délai d'attente
// (Options.Timeout, voir withDB) qui absorbe seul ce scénario précis — testé
// en repassant ce test sur le code D'AVANT le correctif : il passe déjà,
// preuve que cette contention-là n'exerçait pas le vrai bug. Gardé quand même
// comme filet de non-régression sur le nouveau chemin (getStoreBytesErr +
// boucle de tentatives), et parce qu'il documente honnêtement la limite de ce
// qu'on a pu vérifier — voir le commentaire de LoadConversation pour le reste.
func TestLoadConversationRetriesThroughLockContention(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AJEAN_HOME", home)

	// 1. Écrit une conversation, à travers l'API normale (chiffrement etc. gérés).
	conv.mu.Lock()
	conv.ID = "conv-under-test"
	conv.Messages = []Message{{Role: "user", Content: "bonjour"}}
	conv.mu.Unlock()
	conv.persist()

	// 2. Vide l'état en RAM pour forcer un VRAI rechargement depuis la base,
	// comme au démarrage d'un process qui ne connaît encore rien.
	conv.mu.Lock()
	conv.ID = ""
	conv.Messages = nil
	conv.mu.Unlock()

	// 3. Tient le verrou exclusif de la base pendant une fenêtre courte, dans une
	// goroutine séparée — imite la base momentanément indisponible.
	db, err := bolt.Open(dbPath(), 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("impossible de prendre le verrou pour le test : %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(400 * time.Millisecond)
		db.Close()
	}()

	// 4. LoadConversation() doit RÉESSAYER jusqu'à obtenir la base (relâchée à
	// 400ms) plutôt que d'abandonner sur le tout premier essai raté.
	start := time.Now()
	LoadConversation()
	elapsed := time.Since(start)

	conv.mu.Lock()
	gotID := conv.ID
	gotMsgs := len(conv.Messages)
	conv.mu.Unlock()

	if gotID != "conv-under-test" || gotMsgs != 1 {
		t.Fatalf("conversation perdue malgré la contention temporaire : id=%q messages=%d (attendu id=%q messages=1)",
			gotID, gotMsgs, "conv-under-test")
	}
	if elapsed < 300*time.Millisecond {
		t.Fatalf("succès trop rapide (%v) : le test n'a probablement pas exercé la contention prévue", elapsed)
	}
	wg.Wait()
}

// TestLoadConversationStaysEmptyWhenGenuinelyAbsent vérifie que le correctif
// (distinguer erreur d'accès et absence réelle) n'a pas réintroduit un blocage
// ou une boucle sur le cas normal : base saine, mais rien d'enregistré encore.
func TestLoadConversationStaysEmptyWhenGenuinelyAbsent(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	conv.mu.Lock()
	conv.ID = ""
	conv.Messages = nil
	conv.mu.Unlock()
	start := time.Now()
	LoadConversation()
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Fatalf("cas normal (rien à charger) anormalement lent : %v — une seule tentative devrait suffire", elapsed)
	}
	conv.mu.Lock()
	empty := conv.ID == ""
	conv.mu.Unlock()
	if !empty {
		t.Fatal("conv.ID renseigné alors qu'aucune conversation n'avait été enregistrée")
	}
}
