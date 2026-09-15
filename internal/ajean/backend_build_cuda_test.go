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

// cudaMinArchForVersion encode le seuil d'arch supprimé par version de CUDA :
// c'est le cœur du correctif (CUDA 13 = min sm_75, CUDA 12 = min sm_50).
func TestCudaMinArchForVersion(t *testing.T) {
	cases := map[string]int{
		"13.4": 75, "13.0": 75, "14.1": 75,
		"12.8": 50, "12.0": 50,
		"11.8": 35,
		"":     0, // inconnue → ne bloque pas
		"9":    0,
	}
	for ver, want := range cases {
		if got := cudaMinArchForVersion(ver); got != want {
			t.Errorf("cudaMinArchForVersion(%q) = %d, attendu %d", ver, got, want)
		}
	}
}

// minArchCode retient le GPU le plus ancien (celui qui contraint le toolkit).
func TestMinArchCode(t *testing.T) {
	cases := map[string]int{
		"61":      61,
		"86;89":   86,
		"120;61":  61,
		"":        0,
		" 75 ;86": 75,
	}
	for archs, want := range cases {
		if got := minArchCode(archs); got != want {
			t.Errorf("minArchCode(%q) = %d, attendu %d", archs, got, want)
		}
	}
}

// La règle de compatibilité qui décide le message d'erreur : un GPU Pascal
// (sm_61) est refusé par CUDA 13 mais accepté par CUDA 12 ; un GPU Ampere passe
// partout.
func TestArchToolkitCompatibility(t *testing.T) {
	compat := func(archs, ver string) bool {
		mn := cudaMinArchForVersion(ver)
		g := minArchCode(archs)
		return g == 0 || mn == 0 || g >= mn
	}
	if compat("61", "13.4") {
		t.Error("Pascal (61) ne devrait PAS être compatible avec CUDA 13.4")
	}
	if !compat("61", "12.8") {
		t.Error("Pascal (61) devrait être compatible avec CUDA 12.8")
	}
	if !compat("86", "13.4") {
		t.Error("Ampere (86) devrait être compatible avec CUDA 13.4")
	}
	if compat("70", "13.0") {
		t.Error("Volta (70) ne devrait PAS être compatible avec CUDA 13.0")
	}
}

// archDisplay et cudaArchFamily produisent le message lisible pour l'utilisateur.
func TestArchDisplayAndFamily(t *testing.T) {
	if got := archDisplay(61); got != "6.1" {
		t.Errorf("archDisplay(61) = %q, attendu 6.1", got)
	}
	if got := archDisplay(120); got != "12.0" {
		t.Errorf("archDisplay(120) = %q, attendu 12.0", got)
	}
	if got := cudaArchFamily(61); got != "Pascal (GTX 10xx)" {
		t.Errorf("cudaArchFamily(61) = %q", got)
	}
	if got := cudaArchFamily(89); got != "" {
		t.Errorf("cudaArchFamily(89) devrait être vide (Ada, supporté), obtenu %q", got)
	}
}
