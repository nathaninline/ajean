package ajean

import "testing"

// TestPresetFingerprintIgnoresCosmetics vérifie que la détection du preset actif
// ne dépend QUE de la config effective (ensemble KEY=VALUE), pas de la mise en
// forme : ordre des lignes, commentaires (dont `# NAME=`), lignes vides, `export`,
// espaces, ni des clés « appareil » (preservedKeys) réappliquées par SwitchToPreset.
// C'est la régression « aucun preset en surbrillance après un reformatage de
// config.env » (toggle mémoire/internet, réordonnancement…).
func TestPresetFingerprintIgnoresCosmetics(t *testing.T) {
	base := []byte("# NAME=Mon preset\nMODEL=foo.gguf\nCTX=4096\nNGL=999\n")
	// Même config effective, présentée autrement.
	variant := []byte("export NGL=999\nMEM_MODE=off\n\nCTX=4096\n# un commentaire\nMODEL=foo.gguf\n")
	if presetFingerprint(base) != presetFingerprint(variant) {
		t.Fatal("empreintes différentes alors que la config effective est identique")
	}
	// Un vrai changement de valeur doit, lui, produire une empreinte différente.
	changed := []byte("MODEL=foo.gguf\nCTX=8192\nNGL=999\n")
	if presetFingerprint(base) == presetFingerprint(changed) {
		t.Fatal("empreintes identiques alors que CTX diffère")
	}
}
