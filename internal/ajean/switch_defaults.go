package ajean

// switch_defaults.go — accès internet et contrôle du navigateur, activés par
// défaut depuis la 0.21.1.
//
// Une installation EXISTANTE n'est pas touchée : rien n'y est écrit (une mise à
// jour ne change rien, cf. TestMigration018RienNeChange). Sur une installation
// NEUVE, les deux interrupteurs sont allumés une seule fois ; le repère
// switchDefaultsDone empêche de les rallumer si l'utilisateur les coupe ensuite.

import "os"

var defaultOnSwitches = []string{"internet", "computer"}

// putSwitch écrit l'état d'un interrupteur activé par défaut.
func putSwitch(key string, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return putStr(bkState, key, v)
}

// installIsNew : rien n'a encore été réglé ici (ni preset, ni configuration).
func installIsNew() bool {
	if len(ReadConfig()) > 0 {
		return false
	}
	if ents, err := os.ReadDir(presetsDir()); err == nil && len(ents) > 0 {
		return false
	}
	return true
}

const switchDefaultsDone = "switch_defaults_done"

func migrateSwitchDefaults() {
	if getStr(bkState, switchDefaultsDone) != "" || !installIsNew() {
		return
	}
	for _, k := range defaultOnSwitches {
		if getStr(bkState, k) == "" {
			_ = putSwitch(k, true)
		}
	}
	_ = putStr(bkState, switchDefaultsDone, "1")
}
