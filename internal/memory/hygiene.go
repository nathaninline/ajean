package memory

import (
	"regexp"
	"strings"
)

// Les garde-fous mécaniques de l'écriture. Les consignes ne suffisent pas : un
// modèle finit par mettre une clé d'API dans le profil (relu à CHAQUE message),
// par ranger un piège dans la mauvaise fiche ou par recopier une leçon qu'une
// fiche dit déjà.

var (
	secretWordRe = regexp.MustCompile(`(?i)\b(mdp|mot de passe|password|passwd|pwd|identifiants?|credentials?|api[ _-]?key|cl[eé] (d')?api|token|jeton|secret)\b`)
	secretBlobRe = regexp.MustCompile(`\b[A-Za-z0-9_\-]{32,}\b`)
	// Ce qui suit un mot comme « identifiants » sans être une valeur : « identifiants
	// dans la fiche serveur » est justement le renvoi qu'on veut dans le profil.
	notSecretNext = map[string]bool{
		"dans": true, "voir": true, "in": true, "see": true, "fiche": true, "a": true, "en": true, "sur": true,
		"du": true, "de": true, "des": true, "le": true, "la": true, "les": true, "pas": true, "non": true,
		"inconnu": true, "unknown": true,
	}
)

// looksSecret : la valeur contient-elle un identifiant ou une clé ? Soit une
// longue suite de lettres et de chiffres (clé d'API, jeton), soit un mot comme
// « mot de passe » suivi d'une valeur.
func looksSecret(v string) bool {
	for _, blob := range secretBlobRe.FindAllString(v, -1) {
		// Une clé mêle chiffres et lettres ; un long mot n'en est pas une.
		if strings.ContainsAny(blob, "0123456789") && strings.ContainsAny(fold(blob), "abcdefghijklmnopqrstuvwxyz") {
			return true
		}
	}
	for _, loc := range secretWordRe.FindAllStringIndex(v, -1) {
		next := strings.Fields(strings.TrimLeft(v[loc[1]:], " :="))
		if len(next) > 0 && !notSecretNext[strings.Trim(fold(next[0]), ".,;()")] {
			return true
		}
	}
	return false
}

// saidInFiches : le nom d'une fiche dont une ligne dit déjà la même chose que
// text ("" si aucune).
func saidInFiches(text string, fs []fiche) string {
	mine := words(text)
	for _, f := range fs {
		for _, line := range strings.Split(f.Content, "\n") {
			if sameIdea(mine, words(line)) {
				return f.Name
			}
		}
	}
	return ""
}

// pitfallAsked : les pièges déjà refusés une fois pour « mauvaise fiche ».
// Redemandé à l'identique, le piège passe : le modèle a confirmé son choix.
// Protégé par mu.
var pitfallAsked = map[string]bool{}

// misfiledPitfall : le nom d'une autre fiche qui colle nettement mieux au sujet
// du piège que la fiche visée ("" si la fiche visée convient, ou si le modèle
// insiste). Le texte est comparé au but de chaque fiche (nom et « quand »),
// sans les mots communs à la plupart des fiches.
func misfiledPitfall(target, text string, fs []fiche) string {
	if len(fs) < 2 {
		return ""
	}
	purposes := make([]map[string]bool, len(fs))
	freq := map[string]int{}
	for i, f := range fs {
		purposes[i] = words(strings.ReplaceAll(f.Name, "-", " ") + " " + f.When)
		for w := range purposes[i] {
			freq[w]++
		}
	}
	mine := words(text)
	best, bestScore, targetScore := "", 0, 0
	for i, f := range fs {
		score := 0
		for w := range purposes[i] {
			if mine[w] && freq[w]*2 <= len(fs) {
				score++
			}
		}
		if f.Name == target {
			targetScore = score
		} else if score > bestScore {
			best, bestScore = f.Name, score
		}
	}
	key := target + "\x00" + fold(text)
	if bestScore <= targetScore || pitfallAsked[key] {
		return ""
	}
	pitfallAsked[key] = true
	return best
}
