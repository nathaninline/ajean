package ajean

import "testing"

// Une puce intégrée n'a pas de mémoire dédiée : sa jauge doit compter la
// mémoire partagée, sinon elle reste vide pendant qu'un modèle tourne dessus.
func TestAssignIntegratedUsage(t *testing.T) {
	gpus := []map[string]any{
		{"name": "NVIDIA GeForce RTX 4070", "used": 9000, "total": 12000, "util": 80},
		{"name": "Intel(R) Graphics", "used": 1400, "total": 18000, "util": 0},
	}
	rest := []windowsAdapterStat{
		{DedicatedMiB: 0, SharedMiB: 300, UtilPct: 2},
		{DedicatedMiB: 128, SharedMiB: 14000, UtilPct: 65},
	}
	assignIntegratedUsage(gpus, rest)
	if gpus[1]["used"] != 14128 || gpus[1]["util"] != 65 {
		t.Errorf("puce intégrée : %v", gpus[1])
	}
	if gpus[0]["used"] != 9000 || gpus[0]["util"] != 80 {
		t.Errorf("la carte dédiée ne doit pas être touchée : %v", gpus[0])
	}
	// Jamais plus que le total annoncé, et rien à faire sans adaptateur restant.
	solo := []map[string]any{{"name": "Intel(R) UHD Graphics", "used": 5, "total": 1000, "util": 0}}
	assignIntegratedUsage(solo, []windowsAdapterStat{{SharedMiB: 5000}})
	if solo[0]["used"] != 1000 {
		t.Errorf("plafond au total : %v", solo[0])
	}
	assignIntegratedUsage(solo, nil)
	if solo[0]["used"] != 1000 {
		t.Errorf("sans adaptateur, la valeur reste : %v", solo[0])
	}
}
