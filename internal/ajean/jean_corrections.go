package ajean

// jean_corrections.go — repérer, sans IA, les messages où l'utilisateur
// CORRIGE Jean, et surtout ceux où il lui reproche une erreur DÉJÀ corrigée.
//
// C'est le cœur de la confiance : une correction ne doit jamais attendre (ni
// être noyée dans une révision générale), et une erreur qui revient prouve que
// la règle apprise n'a pas suffi. Dans ce cas la révision reçoit l'ordre de la
// durcir (leçon stricte, toujours sous les yeux), pas de la noter une 2ᵉ fois.
// Une détection de trop ne coûte qu'une révision un peu plus tôt : on préfère
// les faux positifs aux corrections ratées.

import "regexp"

const (
	jeanCorrNone = iota
	jeanCorrection
	jeanCorrRepeat
)

var (
	jeanRepeatRe = regexp.MustCompile(`\b(je (te )?l'?ai (deja )?dit|je te l'avais dit|je t'avais (deja )?dit|je t'ai deja|deja dit|combien de fois|encore une fois|toujours pas|tu recommences|tu refais|de nouveau|une fois de plus|i (already )?told you|again\b|how many times|still not)`)
	jeanCorrRe   = regexp.MustCompile(`(^|[.!?]\s*)(non|nan|non non|pas du tout|no)\b|\b(pas comme ca|c'est faux|c'est pas (ca|ce que)|ce n'est pas (ca|ce que)|tu t'es trompe|tu te trompes|erreur|errone|pas ce que (je|j'ai)|je voulais|j'ai dit|je t'ai dit|arrete de|evite de|evite les|plus jamais|ne fais plus|ne mets plus|n'utilise plus|pas besoin de|je prefere|a partir de maintenant|desormais|dorenavant|la prochaine fois|retiens|n'oublie pas|wrong|that's not|don't do|stop doing|from now on|next time)\b`)
)

// jeanCorrectionKind : le message corrige-t-il Jean, et l'erreur est-elle répétée ?
func jeanCorrectionKind(text string) int {
	s := foldSearch(jeanStripNow(text)) // sans la date ni la note d'arrière-plan
	switch {
	case jeanRepeatRe.MatchString(s):
		return jeanCorrRepeat
	case jeanCorrRe.MatchString(s):
		return jeanCorrection
	}
	return jeanCorrNone
}
