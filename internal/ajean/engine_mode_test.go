package ajean

import "testing"

// TestIsEngineOnlyEnv vérifie que la variable d'environnement AJEAN_ENGINE_ONLY
// active le mode core, quelle que soit la config persistée. On teste uniquement
// la branche environnement (pas d'écriture en base) pour rester hermétique.
func TestIsEngineOnlyEnv(t *testing.T) {
	// Sans variable : le résultat dépend de la config, on ne l'affirme pas ici
	// (la base peut être vide en test). On vérifie seulement les valeurs vraies.
	for _, v := range []string{"1", "true", "on", "yes", "TRUE", "On"} {
		t.Setenv("AJEAN_ENGINE_ONLY", v)
		if !isEngineOnly() {
			t.Errorf("AJEAN_ENGINE_ONLY=%q devrait activer le mode core", v)
		}
	}
	// Valeurs qui n'activent pas (au sens de la fonction) : on désactive l'env et
	// on s'assure que la fonction retombe sur la config sans paniquer.
	for _, v := range []string{"0", "false", "no", ""} {
		t.Setenv("AJEAN_ENGINE_ONLY", v)
		_ = isEngineOnly() // ne doit pas paniquer
	}
}
