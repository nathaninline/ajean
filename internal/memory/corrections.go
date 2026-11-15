package memory

import (
	"regexp"
	"strings"
)

// Repérer, sans IA, les messages où l'utilisateur CORRIGE l'assistant, et
// surtout ceux où il lui reproche une erreur déjà corrigée. Une correction ne
// doit jamais attendre ; une erreur qui revient prouve que la règle apprise n'a
// pas suffi et qu'il faut la durcir, pas la noter une deuxième fois. Une
// détection de trop ne coûte qu'une révision un peu plus tôt : mieux vaut un
// faux positif qu'une correction ratée.

// Correction : ce qu'un message dit des erreurs de l'assistant.
type Correction int

const (
	NoCorrection   Correction = iota
	Corrected                 // l'utilisateur corrige l'assistant
	CorrectedAgain            // il lui reproche une erreur déjà corrigée
)

// Les expressions sont écrites sans accents : le message est replié avant.
var (
	repeatRe = regexp.MustCompile(`\b(je (te )?l'?ai (deja )?dit|je te l'avais dit|je t'avais (deja )?dit|je t'ai deja|deja dit|combien de fois|encore une fois|toujours pas|tu recommences|tu refais|de nouveau|une fois de plus|i (already )?told you|again\b|how many times|still not)`)
	fixRe    = regexp.MustCompile(`(^|[.!?]\s*)(non|nan|non non|pas du tout|no)\b|\b(pas comme ca|c'est faux|c'est pas (ca|ce que)|ce n'est pas (ca|ce que)|tu t'es trompe|tu te trompes|erreur|errone|pas ce que (je|j'ai)|je voulais|j'ai dit|je t'ai dit|arrete de|evite de|evite les|plus jamais|ne fais plus|ne mets plus|n'utilise plus|pas besoin de|je prefere|a partir de maintenant|desormais|dorenavant|la prochaine fois|retiens|n'oublie pas|wrong|that's not|don't do|stop doing|from now on|next time)\b`)
)

// DetectCorrection : le message corrige-t-il l'assistant ?
func DetectCorrection(text string) Correction {
	s := strings.ReplaceAll(fold(text), "’", "'")
	switch {
	case repeatRe.MatchString(s):
		return CorrectedAgain
	case fixRe.MatchString(s):
		return Corrected
	}
	return NoCorrection
}
