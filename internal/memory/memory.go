// Package memory est la mémoire à long terme d'AJEAN. Quatre rangements, pour
// que le contexte du modèle reste léger même après des années :
//
//   - le PROFIL : ce qu'on sait de l'utilisateur à coup sûr. Toujours sous les
//     yeux du modèle, donc à budget dur : plein, il faut remplacer ou oublier.
//   - les LEÇONS et les RÈGLES : ce que l'assistant a appris à ses dépens. Les
//     leçons sont relues à chaque message ; les règles d'écriture sont
//     appliquées par le programme (un remplacement mécanique ne dépend pas de
//     l'attention du modèle).
//   - les FICHES : procédures et guides gardés en entier. Seuls leur nom et
//     leur « quand s'en servir » sont toujours visibles ; le contenu n'entre
//     dans le contexte que quand la fiche sert.
//   - le JOURNAL : tout ce qui s'est passé. Jamais chargé : on n'y accède que
//     par la recherche (mots, jetons exacts, et sens si un modèle d'embeddings
//     est configuré) et par le rappel automatique, qui ne sortent que quelques
//     extraits.
//
// Rien ne s'oublie par l'âge : l'anniversaire d'un proche doit remonter dans
// deux ans. La nuit, un « sommeil » range la mémoire ; il a le droit de
// déplacer, fusionner et reformuler, pas de perdre (voir sleep.go).
//
// Sur le disque, tout ce qui est mémoire est du Markdown lisible :
//
//	MEMORY/profile.md            lignes « - clé: valeur »
//	MEMORY/lessons.md            lignes « - [lecon-N] texte »
//	MEMORY/rules.md              lignes « - [regle-N] "from" -> "to" # pourquoi »
//	MEMORY/fiches/<nom>.md       « # nom », « when: … », ligne vide, contenu
//	MEMORY/fiches/archives/      les fiches sorties de la liste (jamais perdues :
//	                             réenregistrer une fiche archivée la ressort,
//	                             archiver n'écrase jamais une archive)
//	MEMORY/journal/AAAA-MM.md    entrées « ## date · id · genre » puis le texte
//
// L'état technique vit sous .ajean/memory/ : compteurs de rappels
// (recalls.json), usage des fiches (fiche-usage.json), prochain numéro de leçon
// (lesson-seq), vecteurs du journal (vectors/AAAA-MM.json), copies d'avant
// sommeil (sleeps/<id>/).
//
// Plusieurs conversations et des travaux de fond appellent ce paquet en même
// temps : un seul verrou (mu) protège les fichiers. Les fonctions exportées le
// prennent ; les fonctions suffixées Locked supposent qu'il est tenu. Aucun
// appel réseau n'est fait verrou tenu.
package memory

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ajean/internal/home"
)

const (
	profileFile = "profile.md"
	lessonsFile = "lessons.md"
	rulesFile   = "rules.md"
	fichesDir   = "fiches"
	archivesDir = "fiches/archives"
	journalDir  = "journal"
)

// mu protège tous les fichiers de la mémoire et les caches du journal.
var mu sync.Mutex

// memPath : un fichier de la mémoire, donné par son chemin relatif (barres obliques).
func memPath(rel string) string { return filepath.Join(home.Memory(), filepath.FromSlash(rel)) }

// statePath : un fichier de l'état technique de la mémoire.
func statePath(elem ...string) string {
	return home.State(append([]string{"memory"}, elem...)...)
}

// readFile lit un fichier ; absent = vide (une mémoire neuve n'a rien écrit).
func readFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(raw), err
}

// readState charge un fichier d'état. Absent ou abîmé = état vide : un compteur
// perdu ne doit jamais bloquer la mémoire. Une lecture ratée (fichier tenu par
// un antivirus, une synchro) est en revanche signalée : qui compte réécrire le
// fichier ne doit pas le faire à partir d'un état vide.
func readState(path string, v any) error {
	s, err := readFile(path)
	if err != nil {
		return err
	}
	_ = json.Unmarshal([]byte(s), v)
	return nil
}

func writeState(path string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return home.WriteFile(path, raw)
}

// LastChange : dernière modification du profil, des leçons, des règles ou d'une
// fiche. Les dossiers comptent aussi : archiver ou supprimer une fiche ne
// change la date d'aucun fichier restant.
func LastChange() time.Time {
	mu.Lock()
	defer mu.Unlock()
	var last time.Time
	see := func(path string) {
		if info, err := os.Stat(path); err == nil && info.ModTime().After(last) {
			last = info.ModTime()
		}
	}
	for _, rel := range []string{profileFile, lessonsFile, rulesFile, fichesDir, archivesDir} {
		see(memPath(rel))
	}
	for _, rel := range []string{fichesDir, archivesDir} {
		ents, _ := os.ReadDir(memPath(rel))
		for _, e := range ents {
			if !e.IsDir() {
				see(filepath.Join(memPath(rel), e.Name()))
			}
		}
	}
	return last
}
