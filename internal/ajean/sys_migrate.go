package ajean

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Migration du dossier de données jean → ajean.
//
// C'est le seul endroit du renommage qui peut faire PERDRE des données : le
// dossier contient la config, les clés, la mémoire, les conversations, et
// surtout des .gguf qui pèsent des dizaines de gigaoctets. Trois règles en
// découlent, et elles expliquent tout le code ci-dessous :
//
//  1. On déplace par os.Rename DANS LE DOSSIER PARENT de l'ancien. Un rename
//     intra-volume est atomique et instantané — il ne recopie pas les 40 Go, et
//     il n'existe aucun instant où les données seraient à moitié quelque part.
//     Choisir le parent de l'ancien chemin (et pas le défaut de la plateforme)
//     garantit qu'on reste sur le même volume, donc que le rename est bien un
//     rename et non un copy+delete déguisé.
//
//  2. Si le rename échoue — service en cours qui tient des handles sous Windows,
//     droits insuffisants, volume monté en lecture seule — on CONTINUE SUR
//     L'ANCIEN CHEMIN. Une machine qui n'a pas migré marche exactement comme
//     avant ; une machine qui a perdu ses modèles, non. La migration est donc
//     retentée à chaque démarrage jusqu'à ce qu'elle passe.
//
//  3. Aucune suppression, jamais. Le pire cas est « rien n'a bougé ».

var (
	homeOnce sync.Once
	homePath string
)

// migratedDefaultHome renvoie le dossier de données par défaut, en migrant
// l'ancien dossier « jean » vers « ajean » à la première résolution du process.
// Le résultat est mis en cache : deux appels ne doivent jamais désigner deux
// dossiers différents, sinon une moitié du programme écrirait à côté de l'autre.
//
// N'est PAS appelé quand $AJEAN_HOME/$JEAN_HOME ou /etc/default/* imposent un
// chemin : un choix explicite de l'utilisateur ne se migre pas.
func migratedDefaultHome() string {
	homeOnce.Do(func() { homePath = resolveDefaultHome() })
	return homePath
}

func resolveDefaultHome() string {
	return migrateHome(defaultAjeanHome(), legacyDefaultHome())
}

// migrateHome contient toute la logique de migration, isolée des chemins réels
// de la plateforme pour être testable telle quelle. Renvoie le dossier à
// utiliser — le nouveau si la migration a réussi ou n'était pas nécessaire,
// l'ancien si elle a échoué.
func migrateHome(target, legacy string) string {
	if isDir(target) {
		return target // déjà migré (ou installation neuve déjà faite)
	}
	if !isDir(legacy) {
		return target // installation neuve : rien à migrer
	}

	// Même parent que l'ancien dossier ⇒ même volume ⇒ rename atomique.
	sibling := filepath.Join(filepath.Dir(legacy), filepath.Base(target))
	if isDir(sibling) {
		return sibling
	}
	if err := os.Rename(legacy, sibling); err != nil {
		// Cas le plus courant sous Windows : un service AJEAN tourne encore et
		// tient un handle dans le dossier. On reste sur l'ancien chemin — tout
		// fonctionne — et on retentera au prochain démarrage.
		fmt.Fprintf(os.Stderr, "[info] dossier de données pas encore migré vers %s (%v) — on continue sur %s\n",
			sibling, err, legacy)
		return legacy
	}
	fmt.Fprintf(os.Stderr, "[ok] dossier de données migré : %s → %s\n", legacy, sibling)
	return sibling
}
