package ajean

import "testing"

// TestAmdParseGpuDataWrapper vérifie le schéma « objet racine gpu_data » remonté
// par une RX 7800 XT (Bazzite), qui donnait des valeurs GPU à 0 (issue #39) :
// le parseur prenait le wrapper pour un GPU au lieu de déballer le tableau.
func TestAmdParseGpuDataWrapper(t *testing.T) {
	const j = `{
      "gpu_data": [
        {
          "gpu": 0,
          "asic": {"market_name": "AMD Radeon RX 7800 XT"},
          "mem_usage": {
            "total_vram": {"value": 16368, "unit": "MB"},
            "used_vram":  {"value": 14569, "unit": "MB"}
          },
          "usage": {"gfx_activity": {"value": 2, "unit": "%"}},
          "temperature": {"edge": {"value": 45, "unit": "C"}, "hotspot": {"value": 41, "unit": "C"}}
        }
      ]
    }`
	gpus := amdParseJSON([]byte(j))
	if len(gpus) != 1 {
		t.Fatalf("attendu 1 GPU, obtenu %d", len(gpus))
	}
	e := gpus[0]
	if got := amdNum(e["gpu"]); got != 0 {
		t.Errorf("index gpu = %d, attendu 0", got)
	}
	if got := amdDig(e, "mem_usage", "total_vram"); got != 16368 {
		t.Errorf("total_vram = %d, attendu 16368", got)
	}
	if got := amdDig(e, "mem_usage", "used_vram"); got != 14569 {
		t.Errorf("used_vram = %d, attendu 14569", got)
	}
	// hotspot (41) doit primer sur edge (45).
	if got := amdTemp(e); got != 41 {
		t.Errorf("temp = %d, attendu 41 (hotspot)", got)
	}
}

// TestAmdParsePlainArray : l'ancien schéma (tableau direct) reste géré.
func TestAmdParsePlainArray(t *testing.T) {
	const j = `[{"gpu": 0, "asic": {"market_name": "AMD GPU"}}]`
	gpus := amdParseJSON([]byte(j))
	if len(gpus) != 1 || amdNum(gpus[0]["gpu"]) != 0 {
		t.Fatalf("tableau direct mal décodé: %+v", gpus)
	}
}
