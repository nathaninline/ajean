//go:build windows

package ajean

import (
	"fmt"
	"path/filepath"
)

// Migration du dossier juste après une mise à jour, sous Windows.
//
// C'est le chaînon qui manquait. Les utilisateurs ne relancent pas `install` et
// ne retéléchargent pas l'exécutable : ils font `ajean update`, ou cliquent le
// bouton de l'interface. Or ce chemin-là ne tentait rien — l'élévation n'était
// câblée que dans `install` et dans l'installeur de mise à jour. Résultat : un
// poste sans droits administrateur restait sur %ProgramData%\jean pour toujours,
// sans qu'aucune fenêtre ne propose jamais de le régler.
//
// Contrairement à ce qu'on pourrait croire, le binaire en cours d'exécution
// n'empêche PAS de renommer le dossier qui le contient : Windows verrouille le
// fichier .exe lui-même, pas le nom de ses dossiers parents. Vérifié sur banc
// d'essai. La migration peut donc se faire depuis l'application en marche.
func postUpdateMigrate() {
	if !retryHomeMigration() && !elevateForHomeMigration() {
		return
	}
	fmt.Printf("%s dossier de données migré vers %s\n", green("✓"), AjeanHome())
	relinkAfterMigration()
}

// relinkAfterMigration répare ce que le déplacement laisse derrière lui.
//
// Le binaire installé vit DANS le dossier de données (%AJEAN_HOME%\bin). Le
// renommer déplace donc aussi le programme : le raccourci du menu Démarrer et
// l'entrée de PATH désignent un chemin qui n'existe plus. Sans cette étape, la
// migration « réussit » et l'utilisateur se retrouve avec un raccourci mort et
// une commande `ajean` introuvable — soit exactement ce qu'on cherchait à éviter.
func relinkAfterMigration() {
	target := installedExePath()
	binDir := filepath.Dir(target)

	if _, err := addToUserPath(binDir); err != nil {
		fmt.Printf("  %s PATH non mis à jour (%v) — ajoute %s à la main\n", dim("•"), err, binDir)
	}
	// L'ancienne entrée ne désigne plus rien : la laisser encombrerait le PATH
	// d'un chemin mort, et ferait échouer un `jean` tapé par habitude.
	if legacyBin := filepath.Join(legacyDefaultHome(), "bin"); legacyBin != binDir {
		_, _ = removeFromUserPath(legacyBin)
	}
	ensureShortcuts(target)
}
