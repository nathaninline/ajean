package ajean

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestNodeCapIntersect(t *testing.T) {
	got := nodeCapIntersect(
		[]string{nodeCapShell, nodeCapRead, nodeCapWrite}, // autorisées par le propriétaire
		[]string{nodeCapRead, nodeCapList},                // déclarées par le poste
	)
	// Seul read est dans les deux ; l'ordre doit être canonique.
	if len(got) != 1 || got[0] != nodeCapRead {
		t.Fatalf("intersection attendue [read], obtenu %v", got)
	}
}

func TestNodeSanitizeCapsDropsUnknown(t *testing.T) {
	got := nodeSanitizeCaps([]string{"write", "danger", "read", "read"})
	// Ordre canonique (read avant write), doublon et inconnu retirés.
	want := []string{nodeCapRead, nodeCapWrite}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("attendu %v, obtenu %v", want, got)
	}
}

func TestNodeToolNameRoundTrip(t *testing.T) {
	name := nodeToolName("mon_poste", nodeCapShell)
	if !isNodeTool(name) {
		t.Fatalf("%s devrait être reconnu comme outil de poste", name)
	}
	slug, cap, ok := parseNodeToolName(name)
	if !ok || slug != "mon_poste" || cap != nodeCapShell {
		t.Fatalf("parse: ok=%v slug=%q cap=%q", ok, slug, cap)
	}
}

func TestNodeToolNameSlugWithDoubleUnderscore(t *testing.T) {
	// Un slug contenant « __ » ne doit pas casser la reconstitution : on coupe
	// sur le DERNIER séparateur, et la capacité n'en contient jamais.
	name := nodeToolName("a__b", nodeCapWrite)
	slug, cap, ok := parseNodeToolName(name)
	if !ok || slug != "a__b" || cap != nodeCapWrite {
		t.Fatalf("parse: ok=%v slug=%q cap=%q", ok, slug, cap)
	}
}

func TestNodeResolvePathConfinement(t *testing.T) {
	root := t.TempDir()

	// Chemin relatif normal : accepté, sous la racine.
	got, err := nodeResolvePath(root, "sous/dossier/f.txt")
	if err != nil {
		t.Fatalf("chemin relatif refusé à tort: %v", err)
	}
	if want := filepath.Join(root, "sous", "dossier", "f.txt"); got != want {
		t.Fatalf("attendu %q, obtenu %q", want, got)
	}

	// Traversée « .. » : refusée.
	if _, err := nodeResolvePath(root, "../evade.txt"); err == nil {
		t.Fatal("la traversée .. aurait dû être refusée")
	}
	if _, err := nodeResolvePath(root, "a/../../evade.txt"); err == nil {
		t.Fatal("la traversée imbriquée aurait dû être refusée")
	}

	// Chemin absolu hors racine : refusé.
	outside := filepath.Join(filepath.Dir(root), "ailleurs.txt")
	if runtime.GOOS == "windows" {
		outside = `C:\Windows\System32\evil.txt`
	}
	if _, err := nodeResolvePath(root, outside); err == nil {
		t.Fatalf("un chemin absolu hors racine (%s) aurait dû être refusé", outside)
	}

	// Chemin absolu SOUS la racine : accepté.
	inside := filepath.Join(root, "ok.txt")
	if _, err := nodeResolvePath(root, inside); err != nil {
		t.Fatalf("un chemin absolu sous la racine refusé à tort: %v", err)
	}

	// Racine vide : refusée (aucun dossier autorisé = aucun accès).
	if _, err := nodeResolvePath("", "f.txt"); err == nil {
		t.Fatal("une racine vide aurait dû refuser tout chemin")
	}
}
