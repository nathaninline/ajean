package memory

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"ajean/internal/home"
	"ajean/internal/tool"
)

// Le profil : ce qu'on sait de l'utilisateur à coup sûr, relu à chaque message.
// D'où ses contraintes : une ligne par fait, une clé = une seule version du
// fait, un budget dur, et jamais de secret.

const (
	profileMaxChars = 4000 // ~1000 tokens payés à chaque message : à ne pas gonfler
	valueMaxChars   = 300
	profileHead     = "# Profil\n\n"
)

var (
	keyRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,47}$`)
	factRe = regexp.MustCompile(`^- ([a-z0-9][a-z0-9._-]{0,47}): (.*)$`)

	errNoFact = errors.New("clé absente du profil")
)

// Fact : une ligne du profil.
type Fact struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func parseProfile(s string) []Fact {
	out := []Fact{}
	for _, l := range strings.Split(s, "\n") {
		if m := factRe.FindStringSubmatch(strings.TrimRight(l, "\r")); m != nil {
			out = append(out, Fact{m[1], m[2]})
		}
	}
	return out
}

func renderProfile(facts []Fact) string {
	var b strings.Builder
	b.WriteString(profileHead)
	for _, f := range facts {
		b.WriteString("- " + f.Key + ": " + f.Value + "\n")
	}
	return b.String()
}

func profileLocked() ([]Fact, error) {
	s, err := readFile(memPath(profileFile))
	if err != nil {
		return nil, err
	}
	return parseProfile(s), nil
}

func writeProfileLocked(facts []Fact) error {
	return home.WriteFile(memPath(profileFile), []byte(renderProfile(facts)))
}

// Profile : les faits du profil, dans l'ordre du fichier.
func Profile() ([]Fact, error) {
	mu.Lock()
	defer mu.Unlock()
	return profileLocked()
}

// normKey ramène une clé à sa forme canonique : « Famille / Mère » → « famille.mere ».
func normKey(k string) string {
	k = strings.NewReplacer(" ", ".", "/", ".", ":", ".").Replace(fold(k))
	for strings.Contains(k, "..") {
		k = strings.ReplaceAll(k, "..", ".")
	}
	return strings.Trim(k, ".-_")
}

// checkFact valide une ligne du profil (clé déjà normalisée) et renvoie sa
// valeur ramenée à une ligne.
func checkFact(key, value string) (string, error) {
	if !keyRe.MatchString(key) {
		return "", errors.New("clé invalide : courte, en minuscules, ex. « famille.mere.anniversaire »")
	}
	value = squash(value)
	if value == "" {
		return "", errors.New("valeur vide (pour retirer une ligne : memory_forget)")
	}
	if looksSecret(value) {
		return "", errors.New("refusé : le profil est relu à chaque message, aucun mot de passe, clé ni jeton dedans. Range l'identifiant dans la fiche du service (fiche_save / fiche_patch) et ne mets ici qu'un renvoi (ex. « identifiants dans la fiche serveur-media »)")
	}
	if runeLen(value) > valueMaxChars {
		return "", fmt.Errorf("valeur trop longue (%d car. max) : garde l'essentiel, le détail va dans memory_note", valueMaxChars)
	}
	return value, nil
}

// UpdateFact pose une ligne du profil depuis l'interface. oldKey non vide et
// différent de key (une fois normalisés) = renommage : l'ancienne ligne part
// dans la même écriture que la nouvelle, donc reste si celle-ci est refusée.
func UpdateFact(oldKey, key, value string) error {
	oldKey, key = normKey(oldKey), normKey(key)
	value, err := checkFact(key, value)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	_, _, err = setFactLocked(oldKey, key, value)
	return err
}

// setFact (outil memory_remember) pose ou remplace un fait. Renvoie ce qui a
// été fait : sans « [ok] » si la ligne disait déjà ça, car rien n'est écrit.
func setFact(key, value string) (string, error) {
	key = normKey(key)
	value, err := checkFact(key, value)
	if err != nil {
		return "", err
	}
	mu.Lock()
	defer mu.Unlock()
	previous, found, err := setFactLocked("", key, value)
	switch {
	case err != nil:
		return "", err
	case !found:
		return tool.OK("« %s » retenu", key), nil
	case previous == value:
		return fmt.Sprintf("« %s » dit déjà ça dans le profil : rien changé", key), nil
	}
	return tool.OK("« %s » mis à jour (ancienne valeur gardée dans le journal)", key), nil
}

// setFactLocked écrit key = value et, si oldKey désigne une autre ligne, la
// retire (renommage). Renvoie la valeur que portait déjà key (found = false si
// la clé est nouvelle).
func setFactLocked(oldKey, key, value string) (previous string, found bool, err error) {
	facts, err := profileLocked()
	if err != nil {
		return "", false, err
	}
	at := func(k string) int { return slices.IndexFunc(facts, func(f Fact) bool { return f.Key == k }) }
	// Ce qui part au journal : une valeur remplacée ou retirée reste trouvable.
	var notes []string
	renamed := false
	if i := at(oldKey); i >= 0 && oldKey != key {
		renamed = true
		if at(key) < 0 {
			facts[i].Key = key // la ligne garde sa place sous son nouveau nom
			notes = append(notes, fmt.Sprintf("Profil « %s » renommé « %s »", oldKey, key))
		} else {
			notes = append(notes, fmt.Sprintf("Retiré du profil « %s » : %s", oldKey, facts[i].Value))
			facts = append(facts[:i], facts[i+1:]...)
		}
	}
	if i := at(key); i < 0 {
		facts = append(facts, Fact{key, value})
	} else {
		previous, found = facts[i].Value, true
		if previous == value && !renamed {
			return previous, true, nil
		}
		if previous != value {
			facts[i].Value = value
			notes = append(notes, fmt.Sprintf("Profil « %s » remplacé. Avant : %s. Maintenant : %s", key, previous, value))
		}
	}
	if len(renderProfile(facts)) > profileMaxChars {
		keys := make([]string, len(facts))
		for i, f := range facts {
			keys[i] = f.Key
		}
		return "", false, fmt.Errorf("profil plein (%d car. max). Fusionne ou raccourcis une ligne existante, ou retire la moins utile avec memory_forget (elle reste dans le journal). Clés : %s",
			profileMaxChars, strings.Join(keys, ", "))
	}
	if err := writeProfileLocked(facts); err != nil {
		return "", false, err
	}
	if renamed {
		noteRemovedLocked(factMark(oldKey))
	}
	for _, n := range notes {
		logLocked(kindProfile, n)
	}
	return previous, found, nil
}

// ForgetFact retire une ligne du profil ; sa valeur reste dans le journal.
func ForgetFact(key string) error {
	key = normKey(key)
	mu.Lock()
	defer mu.Unlock()
	facts, err := profileLocked()
	if err != nil {
		return err
	}
	for i, f := range facts {
		if f.Key != key {
			continue
		}
		if err := writeProfileLocked(append(facts[:i], facts[i+1:]...)); err != nil {
			return err
		}
		noteRemovedLocked(factMark(key))
		logLocked(kindProfile, fmt.Sprintf("Retiré du profil « %s » : %s", f.Key, f.Value))
		return nil
	}
	return errNoFact
}
