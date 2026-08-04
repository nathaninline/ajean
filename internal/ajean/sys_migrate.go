package ajean

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

	// migrationDeferred retient pourquoi le dossier n'a pas pu être renommé,
	// pour que seules les commandes qui parlent d'emplacements en fassent état.
	migrationDeferred string
)

// homeMigrationNotice renvoie le message à montrer quand la migration attend
// encore, ou "" s'il n'y a rien à signaler. Renvoie aussi le conseil qui va
// avec : sous Windows, l'obstacle est presque toujours un manque de droits sur
// C:\ProgramData, et la réponse tient en une phrase.
func homeMigrationNotice() string {
	if migrationDeferred == "" {
		return ""
	}
	msg := migrationDeferred + "\n  Tout fonctionne : AJEAN continue d'utiliser ce dossier."
	if runtime.GOOS == "windows" {
		return msg + "\n  Pour aligner les noms, ferme AJEAN puis lance « ajean install » en administrateur."
	}
	return msg + "\n  Pour aligner les noms : sudo ajean install."
}

// renameCause extrait la cause réelle d'un échec de rename. os.Rename renvoie un
// *os.LinkError dont le texte répète les deux chemins complets — dans un message
// qui les cite déjà, ça donne trois fois la même chose et rend l'essentiel
// (« Accès refusé ») illisible. On ne garde que ce dernier.
func renameCause(err error) string {
	var le *os.LinkError
	if errors.As(err, &le) && le.Err != nil {
		return le.Err.Error()
	}
	return err.Error()
}

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

// retryHomeMigration relance la résolution — donc la migration — après avoir
// arrêté ce qui tournait. Renvoie true si le dossier a effectivement changé.
//
// Raison d'être : sous Windows, un rename de dossier échoue tant qu'un process
// y tient un handle. Or le service AJEAN détaché en tient en permanence, si bien
// que la migration tentée au démarrage échouerait indéfiniment et que la machine
// resterait sur l'ancien chemin. L'installateur, lui, a une fenêtre où tout est
// arrêté : c'est là qu'on retente.
//
// À N'APPELER QUE dans cette fenêtre, et avant que quoi que ce soit d'autre
// n'ait ouvert de fichier de données : réinitialiser le cache n'est ni atomique
// ni sûr vis-à-vis des goroutines, et surtout les chemins déjà calculés par
// l'appelant deviennent obsolètes (voir migrateThenResolveTarget).
func retryHomeMigration() bool {
	before := migratedDefaultHome()
	homeOnce = sync.Once{}
	homePath = ""
	return migratedDefaultHome() != before
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
		// On reste sur l'ancien chemin — tout fonctionne — et on retentera plus
		// tard. Deux causes courantes sous Windows : un service AJEAN qui tient
		// encore un handle, ou, bien plus fréquent, un utilisateur non
		// administrateur : renommer C:\ProgramData\jean exige d'écrire dans
		// C:\ProgramData, ce qu'un compte standard ne peut pas faire.
		//
		// On ENREGISTRE la raison au lieu de l'afficher. L'afficher condamnait
		// tout utilisateur non administrateur à voir un message d'erreur
		// technique avant CHAQUE commande, y compris `ajean help`, pour une
		// situation qui n'a aucune conséquence : son installation fonctionne.
		// Le message n'a sa place que là où il est actionnable — `ajean where`
		// et `ajean install`, voir homeMigrationNotice().
		migrationDeferred = fmt.Sprintf("dossier de données encore en %s — renommage en %s refusé (%s).",
			legacy, filepath.Base(sibling), renameCause(err))
		return legacy
	}
	fmt.Fprintf(os.Stderr, "[ok] dossier de données migré : %s → %s\n", legacy, sibling)
	rewriteHomeReferences(legacy, sibling)
	return sibling
}

// configFilesToRewrite liste les fichiers de configuration susceptibles de
// contenir un chemin ABSOLU vers le dossier de données. Volontairement restreint
// à des fichiers texte, petits et écrits par nous : on ne réécrit pas à l'aveugle
// les 51 000 fichiers d'un dossier de données.
func configFilesToRewrite(home string) []string {
	files := []string{
		filepath.Join(home, "config.env"),
		filepath.Join(home, "model_dirs.json"),
		filepath.Join(home, "mcp.json"),
		filepath.Join(home, "webprefs.json"),
	}
	// Les presets sont des config.env alternatives : ils portent le même BIN.
	presets, _ := filepath.Glob(filepath.Join(home, "configs", "*"))
	return append(files, presets...)
}

// rewriteHomeReferences réécrit les chemins absolus pointant vers l'ANCIEN
// dossier de données dans les fichiers de configuration.
//
// Sans ça, la migration casse l'installation qu'elle est censée préserver :
// `config.env` contient typiquement
//
//	BIN=C:\ProgramData\jean\backends\llama.cpp\build\bin\Release\llama-server.exe
//
// c'est-à-dire un chemin absolu VERS le dossier qu'on vient de renommer. Le
// dossier a bougé, la ligne pointe dans le vide, et llama-server ne démarre plus.
// Ça concerne tous ceux qui ont fait un `llamacpp install`, donc le cas nominal.
//
// Trois écritures d'un même chemin doivent être couvertes, sans quoi on croit
// avoir tout réécrit alors qu'il reste des références mortes :
//
//	C:\ProgramData\jean    forme native, dans config.env
//	C:/ProgramData/jean    Windows accepte les deux séparateurs dans une valeur
//	C:\\ProgramData\\jean  forme JSON, où l'antislash est échappé (model_dirs.json,
//	                       mcp.json, webprefs.json sont écrits par json.Marshal)
//
// Best-effort par fichier : un fichier illisible est sauté sans compromettre
// les autres.
func rewriteHomeReferences(oldHome, newHome string) {
	variants := [][2]string{{oldHome, newHome}}
	if slash := filepath.ToSlash(oldHome); slash != oldHome {
		variants = append(variants, [2]string{slash, filepath.ToSlash(newHome)})
		// Forme JSON : chaque antislash est doublé.
		variants = append(variants, [2]string{jsonEscapePath(oldHome), jsonEscapePath(newHome)})
	}
	for _, path := range configFilesToRewrite(newHome) {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out := string(b)
		for _, v := range variants {
			out = strings.ReplaceAll(out, v[0], v[1])
		}
		if out == string(b) {
			continue
		}
		fi, err := os.Stat(path)
		mode := os.FileMode(0o644)
		if err == nil {
			mode = fi.Mode()
		}
		if err := os.WriteFile(path, []byte(out), mode); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] %s non réécrit (%v) — vérifie les chemins absolus qu'il contient\n", path, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "[ok] chemins mis à jour dans %s\n", filepath.Base(path))
	}
	adoptLegacyStateFiles(newHome)
}

// jsonEscapePath renvoie l'écriture d'un chemin telle qu'elle apparaît DANS un
// fichier JSON : sous Windows, json.Marshal double chaque antislash. Le
// remplacement littéral doit donc chercher cette forme-là, sinon model_dirs.json
// et mcp.json gardent des chemins morts.
//
// Attention en modifiant : "\\" est UN antislash et "\\\\" en est deux. Une
// version antérieure utilisait des littéraux bruts et remplaçait en fait un
// antislash par lui-même, ce qui laissait passer le remplacement natif et
// produisait un JSON invalide (« invalid character 'U' in string escape code »),
// donc une liste de dossiers de modèles perdue. Couvert par un test.
func jsonEscapePath(p string) string { return strings.ReplaceAll(p, "\\", "\\\\") }

// adoptLegacyStateFiles reprend les fichiers d'état nommés d'après le service
// (jean.pid / jean.log → ajean.pid / ajean.log). Le fichier PID dit si le
// service tourne : ne pas le reprendre reviendrait à croire qu'il est arrêté et
// à en démarrer un second.
func adoptLegacyStateFiles(home string) {
	for _, ext := range []string{".pid", ".log"} {
		from := filepath.Join(home, legacyServiceName()+ext)
		to := filepath.Join(home, "ajean"+ext)
		if _, err := os.Stat(to); err == nil {
			continue
		}
		if _, err := os.Stat(from); err != nil {
			continue
		}
		_ = os.Rename(from, to)
	}
}
