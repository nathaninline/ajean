package nodeclient

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fetchFile doit rapatrier une tranche binaire exacte, encodée en base64, et
// signaler la fin de fichier — y compris pour du contenu non-UTF-8 (là où
// readFile, lui, casserait le binaire).
func TestFetchFileBinaryChunks(t *testing.T) {
	root := t.TempDir()
	// Contenu binaire (octets non-UTF-8 inclus) pour prouver qu'on ne le corrompt pas.
	data := make([]byte, 5000)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	parse := func(res string) nodeFetchLite {
		t.Helper()
		if strings.HasPrefix(res, "[erreur]") {
			t.Fatalf("fetch a échoué: %s", res)
		}
		var f nodeFetchLite
		if err := json.Unmarshal([]byte(res), &f); err != nil {
			t.Fatalf("JSON illisible (%v): %s", err, res)
		}
		return f
	}

	// Métadonnées (len<=0) : taille + nom, pas de données.
	meta := parse(fetchFile(root, "blob.bin", 0, 0))
	if meta.Size != int64(len(data)) || meta.Name != "blob.bin" || meta.Data != "" {
		t.Fatalf("meta inattendue: %+v", meta)
	}

	// Deux tranches qui recouvrent tout le fichier, recollées à l'identique.
	var got bytes.Buffer
	off := int64(0)
	for {
		f := parse(fetchFile(root, "blob.bin", off, 3000))
		b, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			t.Fatalf("base64 invalide: %v", err)
		}
		got.Write(b)
		off += int64(len(b))
		if f.EOF {
			break
		}
		if len(b) == 0 {
			t.Fatal("tranche vide sans EOF — boucle infinie évitée")
		}
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("contenu rapatrié != original (%d vs %d octets)", got.Len(), len(data))
	}
}

// fetchFile refuse tout chemin hors du dossier racine (confinement).
func TestFetchFileConfinement(t *testing.T) {
	root := t.TempDir()
	if res := fetchFile(root, "../secret", 0, 10); !strings.HasPrefix(res, "[erreur]") {
		t.Fatalf("un chemin hors racine aurait dû être refusé, obtenu: %s", res)
	}
}

// nodeFetchLite : miroir local minimal de la réponse JSON de fetchFile (le
// paquet nodeclient n'importe pas ajean, on redéclare donc la forme pour le test).
type nodeFetchLite struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Data string `json:"data"`
	EOF  bool   `json:"eof"`
}
