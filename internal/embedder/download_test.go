package embedder

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// payload : un contenu à télécharger et son empreinte.
func payload(t *testing.T, size int) (data []byte, sum string) {
	t.Helper()
	data = make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	return data, hex.EncodeToString(h[:])
}

// site sert data et note l'en-tête Range de chaque requête. before, s'il est
// donné, peut répondre à la place (true : c'est fait).
type site struct {
	*httptest.Server
	mu     sync.Mutex
	ranges []string
}

func host(t *testing.T, data []byte, before func(n int, w http.ResponseWriter, r *http.Request) bool) *site {
	t.Helper()
	s := &site{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.ranges = append(s.ranges, r.Header.Get("Range"))
		n := len(s.ranges)
		s.mu.Unlock()
		if before != nil && before(n, w, r) {
			return
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *site) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ranges...)
}

func quiet(done, total int64) {}

func TestFetchResumesWhereItStopped(t *testing.T) {
	data, sum := payload(t, 1<<20)
	s := host(t, data, nil)
	dest := filepath.Join(t.TempDir(), "sous", "fichier.bin")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+partExt, data[:300_000], 0o644); err != nil {
		t.Fatal(err)
	}
	var last [2]int64
	err := fetch(t.Context(), s.URL, dest, int64(len(data)), sum, func(done, total int64) { last = [2]int64{done, total} })
	if err != nil {
		t.Fatal(err)
	}
	if got := s.seen(); len(got) != 1 || got[0] != "bytes=300000-" {
		t.Errorf("requêtes = %q, une seule reprise à 300000 attendue", got)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, data) {
		t.Error("le fichier repris n'est pas celui du serveur")
	}
	if _, err := os.Stat(dest + partExt); err == nil {
		t.Error("le fichier provisoire est resté")
	}
	if last != [2]int64{int64(len(data)), int64(len(data))} {
		t.Errorf("dernier avancement = %v", last)
	}

	// Déjà complet sous son nom provisoire : rien n'est redemandé au serveur.
	if err := os.Rename(dest, dest+partExt); err != nil {
		t.Fatal(err)
	}
	if err := fetch(t.Context(), s.URL, dest, int64(len(data)), sum, quiet); err != nil {
		t.Fatal(err)
	}
	if got := s.seen(); len(got) != 1 {
		t.Errorf("un fichier complet a été redemandé : %q", got)
	}
}

func TestFetchSurvivesACut(t *testing.T) {
	data, sum := payload(t, 400_000)
	s := host(t, data, func(n int, w http.ResponseWriter, r *http.Request) bool {
		if n > 1 {
			return false
		}
		w.Header().Set("Content-Length", "400000")
		_, _ = w.Write(data[:150_000])
		panic(http.ErrAbortHandler) // la connexion tombe en plein envoi
	})
	dest := filepath.Join(t.TempDir(), "fichier.bin")
	// Taille inconnue d'avance : celle qu'annonce le serveur fait foi.
	if err := fetch(t.Context(), s.URL, dest, 0, sum, quiet); err != nil {
		t.Fatal(err)
	}
	if got := s.seen(); len(got) != 2 || !strings.HasPrefix(got[1], "bytes=") {
		t.Errorf("requêtes = %q, une reprise attendue après la coupure", got)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, data) {
		t.Error("le fichier repris n'est pas celui du serveur")
	}
}

func TestFetchRefusesAnUnexpectedFile(t *testing.T) {
	data, _ := payload(t, 50_000)
	_, other := payload(t, 10)
	s := host(t, data, nil)
	dest := filepath.Join(t.TempDir(), "moteur.zip")
	err := fetch(t.Context(), s.URL, dest, int64(len(data)), other, quiet)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("erreur = %v, un refus d'empreinte était attendu", err)
	}
	for _, p := range []string{dest, dest + partExt} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s a été gardé", filepath.Base(p))
		}
	}
	// Une taille qui ne correspond pas est une erreur aussi.
	if err := fetch(t.Context(), s.URL, dest, int64(len(data))+1, "", quiet); err == nil {
		t.Error("un fichier plus court qu'attendu a été accepté")
	}
}

func TestFetchDoesNotInsistOnARefusal(t *testing.T) {
	s := host(t, nil, func(n int, w http.ResponseWriter, r *http.Request) bool {
		http.NotFound(w, r)
		return true
	})
	err := fetch(t.Context(), s.URL, filepath.Join(t.TempDir(), "x.bin"), 0, "", quiet)
	var refused statusError
	if !errors.As(err, &refused) || refused != http.StatusNotFound {
		t.Fatalf("erreur = %v, un 404 était attendu", err)
	}
	if got := s.seen(); len(got) != 1 {
		t.Errorf("%d requêtes pour un refus", len(got))
	}
}

func TestFetchCanBeCancelled(t *testing.T) {
	data, sum := payload(t, 200_000)
	s := host(t, data, func(n int, w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Content-Length", "200000")
		_, _ = w.Write(data[:80_000])
		w.(http.Flusher).Flush()
		<-r.Context().Done() // le reste n'arrive jamais
		return true
	})
	dest := filepath.Join(t.TempDir(), "fichier.bin")
	ctx, cancel := context.WithCancel(t.Context())
	err := fetch(ctx, s.URL, dest, int64(len(data)), sum, func(done, total int64) {
		if done >= 80_000 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("erreur = %v, une annulation était attendue", err)
	}
	if fi, err := os.Stat(dest + partExt); err != nil || fi.Size() != 80_000 {
		t.Errorf("ce qui était reçu devait être gardé pour la reprise (%v)", err)
	}
}
