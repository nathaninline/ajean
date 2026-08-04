package ajean

import (
	"os"
	"strings"
)

// envAny lit la première variable d'environnement renseignée parmi names.
//
// Sert au renommage jean → AJEAN : chaque réglage s'appelle désormais AJEAN_*,
// mais l'ancien nom JEAN_* reste lu en second. Ces variables sont posées à la
// main — dans un shell, un fichier d'unité, un docker-compose, un script de
// déploiement — et rien ne nous permet de les retrouver pour les réécrire. Les
// cesser de les lire, c'est casser silencieusement des installations qui
// marchaient, avec un réglage qui redevient sa valeur par défaut sans le dire.
func envAny(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}
