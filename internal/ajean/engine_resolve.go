// engine_resolve.go — quel llama-server lance un preset ?
//
// La clé BIN d'un preset désigne son moteur. Depuis la 0.21 elle a trois sens,
// et c'est ICI, dans resolveBin, qu'ils sont départagés : tout le code qui a
// besoin du binaire d'une configuration (lancement, pré-vol, drapeaux de
// chargement, vidéo, embeddings, liste des cartes…) passe par cette fonction
// au lieu de lire cfg["BIN"].
//
//	BIN vers un moteur de la 0.18      compilé, précompilé « dernière release »,
//	(tout chemin hors backends/engine)  fork, chemin au choix : lancé comme en
//	                                    0.18, y compris le suivi du précompilé
//	                                    d'une release à l'autre (prebuiltResolveBin)
//	BIN dans backends/engine/<tag>/    le moteur épinglé : celui de la version
//	                                    COURANTE, même si le chemin enregistré
//	                                    est celui d'un ancien tag
//	BIN vide                           le moteur épinglé (preset créé en 0.20,
//	                                    installation neuve)
package ajean

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// reEngineTag : un dossier de version du moteur épinglé (backends/engine/b12257).
// Un fork que l'utilisateur aurait nommé « engine » avant la 0.21 vit dans le
// même dossier (backends/engine/build/bin/…) : ce n'est pas le moteur épinglé.
var reEngineTag = regexp.MustCompile(`^b[0-9]+(\.tmp)?$`)

// engineOwns : p désigne-t-il un binaire du moteur épinglé, quelle que soit la
// version installée ?
func engineOwns(p string) bool {
	if strings.TrimSpace(p) == "" {
		return false
	}
	norm := func(s string) string {
		return strings.ToLower(filepath.ToSlash(filepath.Clean(s)))
	}
	rest, ok := strings.CutPrefix(norm(p), norm(engineRoot())+"/")
	if !ok {
		return false
	}
	tag, _, _ := strings.Cut(rest, "/")
	return reEngineTag.MatchString(tag)
}

// engineReservedDir : dir est-il backends/engine, le dossier du moteur épinglé ?
// Un backend personnalisé ne peut ni s'y installer ni l'emporter en se
// désinstallant. Exception : le clone d'un fork que l'utilisateur aurait nommé
// « engine » avant la 0.21 (un dépôt git), qui reste le sien.
func engineReservedDir(dir string) bool {
	return samePath(dir, engineRoot()) && !isDir(filepath.Join(dir, ".git"))
}

// resolveBin renvoie le llama-server que lance cette configuration, et dit si
// c'est le moteur épinglé. Pour le moteur épinglé, bin vaut "" tant qu'il n'est
// pas installé (engineEnsure l'installe au démarrage du service). Pour un
// moteur de la 0.18, bin est le chemin enregistré (rendu absolu, et suivi d'une
// release du précompilé à l'autre), qu'il existe ou non : à l'appelant de dire
// qu'il manque, comme en 0.18.
//
// Ne regarde QUE la clé BIN : c'est à l'appelant d'écarter d'abord les
// configurations qui ne lancent pas llama-server (voir usesLlamaServer).
func resolveBin(cfg map[string]string) (bin string, pinned bool) {
	raw := strings.TrimSpace(cfg["BIN"])
	if raw == "" {
		return engineBin(), true
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(AjeanHome(), raw)
	}
	if engineOwns(raw) {
		return engineBin(), true
	}
	return prebuiltResolveBin(raw), false
}

// serveBin : le llama-server que « ajean serve » lance pour cette configuration.
// Un moteur de la 0.18 est rendu tel quel, sans rien installer. Le moteur
// épinglé est installé ici s'il manque (preset venu de la 0.20, installation
// neuve, mise à jour d'AJEAN qui change la version épinglée).
func serveBin(cfg map[string]string) (string, error) {
	bin, pinned := resolveBin(cfg)
	if !pinned {
		return bin, nil
	}
	bin, err := engineEnsure(
		func(s string) { fmt.Fprintln(os.Stderr, "[ajean serve]   "+s) },
		func(s string) { fmt.Fprintln(os.Stderr, "[ajean serve] moteur : "+s) },
	)
	if err != nil {
		return "", fmt.Errorf("moteur AJEAN (llama.cpp %s) indisponible : %w", enginePin(), err)
	}
	return bin, nil
}

// usesLlamaServer : cette configuration lance-t-elle un llama-server local ?
// Non pour une API, un Service, le GPU cloud, MoE et ExLlama, qui ont chacun
// leur propre lancement et ne lisent pas BIN.
func usesLlamaServer(cfg map[string]string) bool {
	return !isExternalConfig(cfg) && !isCloudConfig(cfg) && !isMoeConfig(cfg) && !isExl3Config(cfg)
}

// usesPinnedEngine : la configuration tourne-t-elle sur le moteur épinglé ?
func usesPinnedEngine(cfg map[string]string) bool {
	if !usesLlamaServer(cfg) {
		return false
	}
	_, pinned := resolveBin(cfg)
	return pinned
}

// activeBin : le llama-server de la configuration active, "" s'il n'y en a pas
// (preset sans llama-server, moteur épinglé pas encore installé).
func activeBin() string {
	cfg := ReadConfig()
	if !usesLlamaServer(cfg) {
		return ""
	}
	bin, _ := resolveBin(cfg)
	return bin
}

// anyEngineInstalled : y a-t-il déjà, sur cette machine, un moteur capable de
// faire tourner un modèle ? Sert à ne pas télécharger le moteur épinglé
// (plusieurs centaines de Mo) chez quelqu'un qui a déjà le sien.
func anyEngineInstalled() bool {
	if engineInstalled() != nil || engineDevBin() != "" {
		return true
	}
	if len(listBackendBins()) > 0 || prebuiltServerBin() != "" {
		return true // llama.cpp compilé, fork, précompilé
	}
	if exl3ReadInstalled() != nil || moeEngineInstalled() != "" {
		return true
	}
	// Un llama-server rangé ailleurs, désigné par la configuration active.
	if raw := strings.TrimSpace(ReadConfig()["BIN"]); raw != "" {
		if bin, pinned := resolveBin(map[string]string{"BIN": raw}); !pinned && isFile(bin) {
			return true
		}
	}
	return false
}

// engineWanted : faut-il installer le moteur épinglé sans qu'on le demande ?
// Oui si la configuration active tourne dessus (BIN vide ou dans
// backends/engine/), ou si aucun moteur n'est installé. Jamais chez quelqu'un
// dont la configuration désigne un moteur de la 0.18 : pour lui, c'est un bouton.
func engineWanted() bool {
	if engineDevBin() != "" || engineInstalled() != nil {
		return false
	}
	if cfg := ReadConfig(); usesLlamaServer(cfg) {
		return usesPinnedEngine(cfg)
	}
	return !anyEngineInstalled()
}
