//go:build !linux && !windows

package ajean

// backend_moe_other.go : plateformes sans moteur MoE (macOS et les autres).
//
// Le moteur MoE ne tourne que sous Linux (binaire de la release) et sous
// Windows (compilé sur place) : moeDetect le refuse ailleurs. Ces fonctions ne
// sont là que pour que le paquet se COMPILE partout — backend_moe.go, sans
// contrainte de plateforme, les appelle ; sans elles, `go build ./...` et
// `go vet ./...` tombent en « undefined » sur darwin, et le job macOS de la CI
// avec. Elles ne font rien d'utile : celle qui compte (moeRunInstaller,
// moeWriteLauncher) est atteinte après moeDetect, qui a déjà refusé la
// plateforme ; elles le redisent, sans surprendre.

import (
	"fmt"
	"path/filepath"
)

const moeOtherPlatform = "moteur MoE disponible uniquement sous Linux et Windows"

// moeEngineDir : le dossier d'attente du moteur. Il n'y en a pas ici ; le
// chemin sert à moeEngineMarker (marqueur d'installation) et aux tests.
func moeEngineDir() string { return filepath.Join(moeSrcDir(), "engine-cuda12") }

// moePrepareEngine : préparation du moteur — inexistante hors Linux/Windows.
func moePrepareEngine() error { return fmt.Errorf("%s", moeOtherPlatform) }

// moeEnsureEngine : la porte d'entrée commune (appelée par serveMoe), qui
// n'est atteinte que si moeDetect a accepté la plateforme.
func moeEnsureEngine() error { return fmt.Errorf("%s", moeOtherPlatform) }

// moeRunInstaller : l'installeur du moteur — même remarque.
func moeRunInstaller(family, quant string, env moeEnv, pre string) error {
	return fmt.Errorf("%s", moeOtherPlatform)
}

// moeVenvPython : le Python de l'environnement du moteur (chemin unix, jamais
// vérifié ici : moePreflight, qui le contrôle, n'aboutit pas sans moteur).
func moeVenvPython(srcDir string) string { return filepath.Join(srcDir, ".venv", "bin", "python") }

// moeWriteLauncher : le lanceur du serveur du moteur — inexistante ici.
func moeWriteLauncher(srcDir, py string) (string, []string, error) {
	return "", nil, fmt.Errorf("%s", moeOtherPlatform)
}

// moeAlias : lien vers un binaire du moteur, pour que le process porte le nom
// du moteur d'AJEAN. Rien à nommer ici : le chemin d'origine.
func moeAlias(target, name string) string { return target }
