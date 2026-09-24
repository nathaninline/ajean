package ajean

import (
	"net"
	"strings"
	"testing"
	"time"
)

// #52 : une release en plein envoi (Windows pas encore arrivé) ne doit pas être choisie.
func TestPickLlamaReleaseSkipsFresh(t *testing.T) {
	now := time.Date(2026, 9, 24, 8, 11, 0, 0, time.UTC)
	rels := []llamaRelease{
		{TagName: "latest", PublishedAt: now.Add(-time.Hour), Assets: []ghAsset{{Name: "nightly-tag.txt", State: "uploaded"}}},
		{TagName: "b2", PublishedAt: now.Add(-time.Minute), Assets: []ghAsset{
			{Name: "llama-b2-bin-macos-arm64.tar.gz", State: "uploaded"},
			{Name: "llama-b2-bin-win-cuda-13.4-x64.zip", State: "starter"},
		}},
		{TagName: "b1", PublishedAt: now.Add(-40 * time.Minute), Assets: []ghAsset{
			{Name: "llama-b1-bin-win-vulkan-x64.zip", State: "uploaded"},
		}},
	}
	tag, assets, err := pickLlamaRelease(rels, now)
	if err != nil || tag != "b1" || len(assets) != 1 {
		t.Fatalf("got %q %v %v, want b1", tag, assets, err)
	}
}

// Rien de plus ancien : on garde la release récente, sans ses fichiers inachevés.
func TestPickLlamaReleaseOnlyFresh(t *testing.T) {
	now := time.Now()
	rels := []llamaRelease{{TagName: "b2", PublishedAt: now.Add(-time.Minute), Assets: []ghAsset{
		{Name: "llama-b2-bin-win-vulkan-x64.zip", State: "uploaded"},
		{Name: "llama-b2-bin-win-cuda-13.4-x64.zip", State: "starter"},
	}}}
	tag, assets, err := pickLlamaRelease(rels, now)
	if err != nil || tag != "b2" || len(assets) != 1 || strings.Contains(assets[0].Name, "cuda") {
		t.Fatalf("got %q %v %v", tag, assets, err)
	}
}

// Comportement historique conservé : première release avec binaires, pointeur « latest » sauté.
func TestPickLlamaReleaseLegacy(t *testing.T) {
	rels := []llamaRelease{
		{TagName: "latest", Assets: []ghAsset{{Name: "nightly-tag.txt"}}},
		{TagName: "b9", Assets: []ghAsset{{Name: "llama-b9-bin-win-cpu-x64.zip"}}},
	}
	if tag, _, err := pickLlamaRelease(rels, time.Now()); err != nil || tag != "b9" {
		t.Fatalf("got %q %v", tag, err)
	}
	if _, _, err := pickLlamaRelease(nil, time.Now()); err == nil {
		t.Fatal("erreur attendue sans release")
	}
}

func TestArgValueLastWins(t *testing.T) {
	a := []string{"bin", "--port", "8080", "--host", "0.0.0.0", "--port", "9000"}
	if argValue(a, "--port") != "9000" || argValue(a, "--host") != "0.0.0.0" || argValue(a, "--nope") != "" {
		t.Fatal("argValue")
	}
}

// #80 : port occupé → erreur claire ; port libre → OK.
func TestWaitPortFree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if err := waitPortFree("0.0.0.0", port, 600*time.Millisecond); err == nil || !strings.Contains(err.Error(), port) {
		t.Fatalf("port occupé non détecté : %v", err)
	}
	ln.Close()
	if err := waitPortFree("0.0.0.0", port, time.Second); err != nil {
		t.Fatalf("port libre refusé : %v", err)
	}
}

// Un moteur qui libère le port pendant l'attente ne doit pas bloquer le démarrage.
func TestWaitPortFreeReleasedDuringWait(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	go func() { time.Sleep(700 * time.Millisecond); ln.Close() }()
	if err := waitPortFree("127.0.0.1", port, 5*time.Second); err != nil {
		t.Fatalf("attendu libre après fermeture : %v", err)
	}
}
