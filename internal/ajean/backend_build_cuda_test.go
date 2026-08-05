package ajean

import (
	"errors"
	"path/filepath"
	"testing"
)

// Le shim /usr/bin/nvcc d'Ubuntu ne doit JAMAIS l'emporter sur un vrai toolkit :
// CMake déduit la racine du toolkit du chemin de nvcc, et /usr ne contient ni
// cuda_runtime.h ni cudart → « CUDA Toolkit not found » alors que nvcc est là.
func TestFindNvccUnixIgnoreLeShimQuandUnToolkitExiste(t *testing.T) {
	if !isFile("/usr/local/cuda/bin/nvcc") {
		t.Skip("pas de toolkit /usr/local/cuda sur cette machine")
	}
	got := findNvccUnix(func(string) (string, error) { return "/usr/bin/nvcc", nil })
	if got == "/usr/bin/nvcc" {
		t.Error("le shim /usr/bin/nvcc a été préféré au toolkit /usr/local/cuda")
	}
}

// Sans toolkit installé, on retombe sur le PATH plutôt que de renoncer à CUDA.
func TestFindNvccUnixRetombeSurLePath(t *testing.T) {
	if isFile("/usr/local/cuda/bin/nvcc") {
		t.Skip("un toolkit est installé ici : le repli n'est pas exerçable")
	}
	if got := findNvccUnix(func(string) (string, error) { return "/usr/bin/nvcc", nil }); got != "/usr/bin/nvcc" {
		t.Errorf("repli PATH = %q, attendu /usr/bin/nvcc", got)
	}
	if got := findNvccUnix(func(string) (string, error) { return "", errors.New("absent") }); got != "" {
		t.Errorf("aucun nvcc nulle part : %q, attendu \"\"", got)
	}
}

// cudaToolkitRoot remonte <root>/bin/nvcc → <root>, et refuse les racines qui
// n'apprennent rien à CMake (/usr, layout inattendu).
func TestCudaToolkitRoot(t *testing.T) {
	cases := map[string]string{
		"/usr/local/cuda/bin/nvcc":      "/usr/local/cuda",
		"/usr/local/cuda-12.8/bin/nvcc": "/usr/local/cuda-12.8",
		"/usr/bin/nvcc":                 "", // racine déduite = /usr : inutile
		"/opt/nvcc":                     "", // pas de dossier bin
		"":                              "",
	}
	for in, want := range cases {
		if got := filepath.ToSlash(cudaToolkitRoot(filepath.FromSlash(in))); got != want {
			t.Errorf("cudaToolkitRoot(%q) = %q, attendu %q", in, got, want)
		}
	}
}
