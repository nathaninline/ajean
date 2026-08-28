package ajean

import "testing"

// Échantillon proche d'une vraie sortie `rocm-smi --json`, PRÉCÉDÉ de l'exception
// que rocm-smi crache sur certaines machines multi-GPU AMD (issue #49) : le parseur
// doit sauter le bruit, lire les deux cartes, convertir la VRAM (octets → MiB) et
// choisir la température de point chaud (junction).
func TestRocmParseJSON(t *testing.T) {
	sample := []byte(`Exception caught: map::at
{
  "card0": {
    "Card Series": "Navi 48 [Radeon RX 9070 XT]",
    "Card Model": "0x7550",
    "VRAM Total Memory (B)": "17163091968",
    "VRAM Total Used Memory (B)": "16108814336",
    "Temperature (Sensor edge) (C)": "48.0",
    "Temperature (Sensor junction) (C)": "61.0",
    "GPU use (%)": "3"
  },
  "card1": {
    "Card Series": "Granite Ridge [Radeon Graphics]",
    "VRAM Total Memory (B)": "536870912",
    "VRAM Total Used Memory (B)": "21495808",
    "Temperature (Sensor edge) (C)": "47.0",
    "GPU use (%)": "0"
  }
}`)
	gpus := rocmParseJSON(sample)
	if len(gpus) != 2 {
		t.Fatalf("attendu 2 cartes, obtenu %d", len(gpus))
	}
	c0 := gpus[0]
	if c0["name"] != "Navi 48 [Radeon RX 9070 XT]" {
		t.Errorf("nom card0 = %v", c0["name"])
	}
	// 17163091968 / 1048576 = 16368 MiB
	if c0["total"] != 16368 {
		t.Errorf("total card0 = %v (attendu 16368 MiB)", c0["total"])
	}
	if c0["used"] != 15362 { // 16108814336 / 1048576
		t.Errorf("used card0 = %v (attendu 15362 MiB)", c0["used"])
	}
	if c0["util"] != 3 {
		t.Errorf("util card0 = %v", c0["util"])
	}
	if c0["temp"] != 61 { // junction, pas edge (48)
		t.Errorf("temp card0 = %v (attendu 61, junction)", c0["temp"])
	}
	// card1 : pas de junction → repli sur edge (47).
	if gpus[1]["temp"] != 47 {
		t.Errorf("temp card1 = %v (attendu 47, edge)", gpus[1]["temp"])
	}
}

// Un JSON vide ou sans carte ne doit rien renvoyer (pas de faux GPU).
func TestRocmParseJSONEmpty(t *testing.T) {
	if g := rocmParseJSON([]byte(`{}`)); g != nil {
		t.Errorf("JSON vide devrait donner nil, obtenu %v", g)
	}
	if g := rocmParseJSON([]byte(`pas du json`)); g != nil {
		t.Errorf("sortie non-JSON devrait donner nil, obtenu %v", g)
	}
}
