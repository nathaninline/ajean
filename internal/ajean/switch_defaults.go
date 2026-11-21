package ajean

// switch_defaults.go — accès internet et contrôle du navigateur, activés par
// défaut depuis la 0.21.1.
//
// Avant, « éteint » s'écrivait en effaçant la clé : une clé absente ne disait
// donc pas si l'utilisateur l'avait coupée ou jamais touchée. Désormais l'état
// est écrit dans les deux sens ("1" / "0"), et migrateSwitchDefaults fixe une
// clé absente au premier démarrage : allumée sur une installation neuve, éteinte
// (c'est-à-dire telle qu'elle était) sur une installation existante. Une mise à
// jour ne change donc rien à ce que l'utilisateur avait réglé.

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

func migrateSwitchDefaults() {
	fresh := installIsNew()
	for _, k := range defaultOnSwitches {
		if getStr(bkState, k) == "" {
			_ = putSwitch(k, fresh)
		}
	}
}
