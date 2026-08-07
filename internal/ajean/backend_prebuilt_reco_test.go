package ajean

import (
	"runtime"
	"testing"
)

// Le seul cas où compiler vaut mieux que télécharger : Linux + CUDA. llama.cpp
// ne publie pas de binaire CUDA pour Linux (seul Windows a bin-win-cuda-*), donc
// le précompilé y retombe sur Vulkan. Ailleurs, l'asset officiel exploite bien
// la machine (CUDA sous Windows, Metal sous macOS, ROCm sous Linux).
func TestRecommendedMode(t *testing.T) {
	cases := []struct {
		backend  string
		wantMode string
		wantWhy  bool
	}{
		{"cuda", "opt", true}, // le cas qui motive toute la fonction
		{"rocm", "fast", false},
		{"vulkan", "fast", false},
		{"cpu", "fast", false},
		{"metal", "fast", false},
	}
	for _, c := range cases {
		got := recommendedMode(c.backend)
		wantMode, wantWhy := c.wantMode, c.wantWhy
		// La règle ne vise que Linux : sur les autres OS le précompilé reste
		// conseillé même avec CUDA détecté.
		if runtime.GOOS != "linux" {
			wantMode, wantWhy = "fast", false
		}
		if got["mode"] != wantMode {
			t.Errorf("backend %q sur %s : mode = %v, attendu %q", c.backend, runtime.GOOS, got["mode"], wantMode)
		}
		if hasWhy := got["why"] != ""; hasWhy != wantWhy {
			t.Errorf("backend %q sur %s : why non vide = %v, attendu %v", c.backend, runtime.GOOS, hasWhy, wantWhy)
		}
	}
}
