package ajean

import (
	"path/filepath"
	"testing"
)

// cmpVersion doit comparer NUMÉRIQUEMENT, là où un tri de chaînes se trompe :
// 12.10 > 12.4 et 13.4 > 9.0 (les deux pièges du tri lexicographique).
func TestCmpVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"12.10", "12.4", 1},  // piège lexicographique classique
		{"12.4", "12.10", -1}, // symétrique
		{"13.4", "9.0", 1},    // majeur à deux chiffres vs un chiffre
		{"9.0", "13.4", -1},
		{"13.4", "13.4", 0},
		{"13.4.1", "13.4", 1}, // composant supplémentaire
		{"13.4", "13.4.0", 0}, // 13.4 == 13.4.0
		{"", "1.0", -1},       // vide < toute version
		{"1.0", "", 1},
		{"", "", 0},
	}
	for _, c := range cases {
		if got := cmpVersion(c.a, c.b); got != c.want {
			t.Errorf("cmpVersion(%q, %q) = %d, attendu %d", c.a, c.b, got, c.want)
		}
	}
}

// pathVersion extrait le premier numéro de version pointé d'un chemin, quel que
// soit le séparateur (Windows/Unix).
func TestPathVersion(t *testing.T) {
	cases := map[string]string{
		filepath.FromSlash(`C:/Program Files/NVIDIA GPU Computing Toolkit/CUDA/v13.4/bin`): "13.4",
		"/usr/local/cuda-12.8/lib64":  "12.8",
		"/usr/local/cuda/bin/nvcc":    "", // pas de version dans le chemin
		"/opt/cuda-11.10.2/bin":       "11.10.2",
	}
	for in, want := range cases {
		if got := pathVersion(in); got != want {
			t.Errorf("pathVersion(%q) = %q, attendu %q", in, got, want)
		}
	}
}

// highestVersionedPath choisit la version la plus élevée sémantiquement, et ne
// laisse jamais un chemin sans version l'emporter sur un chemin versionné.
func TestHighestVersionedPath(t *testing.T) {
	if got := highestVersionedPath(nil); got != "" {
		t.Errorf("liste vide = %q, attendu \"\"", got)
	}
	got := highestVersionedPath([]string{
		"/usr/local/cuda-12.4/bin/nvcc",
		"/usr/local/cuda-12.10/bin/nvcc",
		"/usr/local/cuda-9.0/bin/nvcc",
	})
	if got != "/usr/local/cuda-12.10/bin/nvcc" {
		t.Errorf("= %q, attendu cuda-12.10", got)
	}
	// Un chemin versionné doit primer sur un chemin sans version.
	got = highestVersionedPath([]string{
		"/usr/local/cuda/bin/nvcc", // sans version
		"/usr/local/cuda-13.4/bin/nvcc",
	})
	if got != "/usr/local/cuda-13.4/bin/nvcc" {
		t.Errorf("= %q, attendu cuda-13.4", got)
	}
}

// sortByVersionDesc ordonne du plus récent au plus ancien (sémantiquement).
func TestSortByVersionDesc(t *testing.T) {
	paths := []string{
		"/usr/local/cuda-12.4/lib64",
		"/usr/local/cuda-13.4/lib64",
		"/usr/local/cuda-12.10/lib64",
	}
	sortByVersionDesc(paths)
	want := []string{
		"/usr/local/cuda-13.4/lib64",
		"/usr/local/cuda-12.10/lib64",
		"/usr/local/cuda-12.4/lib64",
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("position %d = %q, attendu %q", i, paths[i], want[i])
		}
	}
}

// cudaJobsCap plafonne selon la RAM (~2 Go/job), borné par les cœurs, min 1 ;
// RAM inconnue = aucune restriction.
func TestCudaJobsCap(t *testing.T) {
	// La RAM réelle de la machine de test n'est pas déterministe, mais la borne
	// haute (nombre de cœurs) et la borne basse (>=1) sont des invariants.
	cpu := 16
	got := cudaJobsCap(cpu)
	if got < 1 {
		t.Errorf("cudaJobsCap(%d) = %d, doit être >= 1", cpu, got)
	}
	if got > cpu {
		t.Errorf("cudaJobsCap(%d) = %d, ne doit jamais dépasser le nombre de cœurs", cpu, got)
	}
}
