//go:build !windows

package ajean

// Pendant non-Windows de sys_elevate_windows.go.
//
// L'élévation ponctuelle n'a de sens que sous Windows : ailleurs, l'installation
// se fait déjà sous `sudo ajean install`, qui a les droits nécessaires et migre
// l'agencement complet (voir sys_layout.go). Il n'y a donc rien à relancer.

const migrateHomeArg = "migrate-home-elevated"

func cmdMigrateHomeElevated() error { return nil }

func elevateForHomeMigration() bool { return false }
