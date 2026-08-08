package ajean

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestShardInfo(t *testing.T) {
	cases := []struct {
		name       string
		idx, total int
		ok         bool
	}{
		{"Laguna-S-2.1-UD-IQ4_XS-00001-of-00003.gguf", 1, 3, true},
		{"Laguna-S-2.1-UD-IQ4_XS-00002-of-00003.gguf", 2, 3, true},
		{"m-01-of-03.gguf", 1, 3, true},
		{"Qwen2.5-7B-Instruct-Q4_K_M.gguf", 0, 0, false},
		{"bidon-00004-of-00003.gguf", 0, 0, false}, // numérotation incohérente
		{"pas-un-gguf-00001-of-00002.bin", 0, 0, false},
	}
	for _, c := range cases {
		idx, total, ok := shardInfo(c.name)
		if idx != c.idx || total != c.total || ok != c.ok {
			t.Errorf("shardInfo(%q) = %d,%d,%v ; attendu %d,%d,%v", c.name, idx, total, ok, c.idx, c.total, c.ok)
		}
	}
}

func TestShardFamilyGardeLaLargeur(t *testing.T) {
	got := shardFamily("Laguna-00002-of-00003.gguf")
	want := []string{
		"Laguna-00001-of-00003.gguf",
		"Laguna-00002-of-00003.gguf",
		"Laguna-00003-of-00003.gguf",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("famille = %v ; attendu %v", got, want)
	}
	if solo := shardFamily("simple.gguf"); !reflect.DeepEqual(solo, []string{"simple.gguf"}) {
		t.Fatalf("fichier simple = %v", solo)
	}
}

// Le lien collé peut désigner n'importe quelle tranche : on remonte toujours à la
// première, et on ne réécrit QUE le nom de fichier final (le même libellé peut
// apparaître plus haut dans le chemin du dépôt).
func TestShardURLSet(t *testing.T) {
	base := "https://huggingface.co/unsloth/Laguna-S-2.1-GGUF/resolve/main/UD-IQ4_XS/"
	in := base + "Laguna-UD-IQ4_XS-00002-of-00003.gguf"
	urls, names := shardURLSet(in, "Laguna-UD-IQ4_XS-00002-of-00003.gguf")
	if len(urls) != 3 || len(names) != 3 {
		t.Fatalf("attendu 3 tranches, got %d/%d", len(urls), len(names))
	}
	if names[0] != "Laguna-UD-IQ4_XS-00001-of-00003.gguf" {
		t.Fatalf("première tranche = %q", names[0])
	}
	if urls[0] != base+"Laguna-UD-IQ4_XS-00001-of-00003.gguf" {
		t.Fatalf("URL de la première tranche = %q", urls[0])
	}
	if urls[2] != base+"Laguna-UD-IQ4_XS-00003-of-00003.gguf" {
		t.Fatalf("URL de la dernière tranche = %q", urls[2])
	}
	// Fichier simple : une seule URL, inchangée.
	u, n := shardURLSet(base+"m.gguf", "m.gguf")
	if len(u) != 1 || u[0] != base+"m.gguf" || n[0] != "m.gguf" {
		t.Fatalf("fichier simple mal traité : %v %v", u, n)
	}
}

func TestShardFamilyTailleEtManquantes(t *testing.T) {
	dir := t.TempDir()
	write := func(n string, size int) {
		if err := os.WriteFile(filepath.Join(dir, n), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("m-00001-of-00003.gguf", 100)
	write("m-00003-of-00003.gguf", 300)

	if got := shardFamilySize(dir, "m-00001-of-00003.gguf"); got != 400 {
		t.Fatalf("taille de la famille = %d ; attendu 400", got)
	}
	missing := shardFamilyMissing(dir, "m-00001-of-00003.gguf")
	if !reflect.DeepEqual(missing, []string{"m-00002-of-00003.gguf"}) {
		t.Fatalf("tranches manquantes = %v", missing)
	}
	write("m-00002-of-00003.gguf", 200)
	if m := shardFamilyMissing(dir, "m-00001-of-00003.gguf"); len(m) != 0 {
		t.Fatalf("famille complète signalée incomplète : %v", m)
	}
}

// Supprimer un modèle découpé doit emporter TOUTES ses tranches, sinon des
// dizaines de Go restent sur le disque sans que rien ne sache plus les effacer.
func TestDeleteModelFileEmporteLesTranches(t *testing.T) {
	testHome(t)
	dir := modelsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{"m-00001-of-00002.gguf", "m-00002-of-00002.gguf", "autre.gguf"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := deleteModelFile("m-00001-of-00002.gguf"); err != nil {
		t.Fatal(err)
	}
	for _, n := range names[:2] {
		if _, err := os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Fatalf("tranche %s non supprimée", n)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "autre.gguf")); err != nil {
		t.Fatal("un modèle sans rapport a été supprimé")
	}
}
