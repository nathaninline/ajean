package ajean

import "testing"

// Issue #182 : la puce Radeon intégrée d'un Ryzen ne doit pas recevoir la VRAM
// de la carte dédiée.
func TestIntegratedGPUName(t *testing.T) {
	cases := map[string]bool{
		"AMD Radeon(TM) Graphics":        true,
		"AMD Radeon Graphics":            true,
		"AMD Radeon 780M Graphics":       true,
		"AMD Radeon(TM) Vega 8 Graphics": true,
		"Intel(R) UHD Graphics 630":      true,
		"AMD Radeon RX 6600M":            false,
		"AMD Radeon RX 7900 XTX":         false,
		"AMD Radeon PRO W7900 Graphics":  false,
		"NVIDIA GeForce RTX 3070":        false,
		"Radeon RX 580 Series":           false,
	}
	for name, want := range cases {
		if got := integratedGPUName(name); got != want {
			t.Errorf("%q : intégré=%v, attendu %v", name, got, want)
		}
	}
}
