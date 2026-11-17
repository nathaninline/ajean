package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"ajean/internal/config"
	"ajean/internal/home"
)

// Reprise de la mémoire de l'ancien AJEAN (jusqu'à la 0.20, mode « Jean »). Elle
// vivait dans memory/_jean, sous des formes proches des nôtres :
//
//	profile.md                 → profile.md
//	lessons.md, lessons-seq.txt→ lessons.md et le compteur des numéros
//	rules.json                 → rules.md (les remplacements ; les autres genres
//	                             de règle n'existent plus)
//	fiche-<nom>.md             → fiches/<nom>.md
//	archive-fiche-<nom>.md     → fiches/archives/<nom>.md
//	journal-AAAA-MM.md         → journal/AAAA-MM.md
//	usage-fiches.json, recalls.md → compteurs d'usage
//
// Rien de ce qui existe déjà ici n'est écrasé : une clé du profil, une fiche,
// une entrée du journal déjà présentes restent telles quelles. Relancer la
// reprise n'ajoute donc rien en double.

// ImportReport : ce qu'une reprise a fait.
type ImportReport struct {
	Facts, FactsSkipped   int
	Lessons               int
	Rules, RulesSkipped   int
	Fiches, FichesSkipped int // archives comprises
	Entries               int
	Notes                 []string // ce qui n'a pas pu être repris, et pourquoi
}

const legacyMagic = "AJEANMEMv1" // en tête d'un fichier chiffré par l'ancien AJEAN

// ErrLegacyEncrypted : la mémoire de l'ancien AJEAN est chiffrée.
var ErrLegacyEncrypted = errors.New("la mémoire de l'ancien AJEAN est chiffrée : désactive d'abord le chiffrement dans ses réglages, puis relance la reprise")

// Les en-têtes de l'ancien journal : le décalage horaire et le genre y sont
// parfois absents (premières versions).
var legacyHeadRe = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2} \d{2}:\d{2})(?: ([+-]\d{4}))? · ([0-9a-z]+)(?: · (\w+))?$`)

const legacyAssistantLabel = "Jean : "

func legacyRead(path string) (string, error) {
	s, err := readFile(path)
	if err == nil && strings.HasPrefix(s, legacyMagic) {
		return "", ErrLegacyEncrypted
	}
	return s, err
}

// LegacyDir : le dossier de la mémoire « Jean » dans un dossier de l'ancien
// AJEAN ("" s'il n'y en a pas). dir peut être le dossier de données de
// l'ancien AJEAN, ou directement son memory/_jean.
func LegacyDir(dir string) string {
	for _, d := range []string{filepath.Join(dir, "memory", "_jean"), dir} {
		for _, f := range []string{"profile.md", "lessons.md"} {
			if _, err := os.Stat(filepath.Join(d, f)); err == nil {
				return d
			}
		}
		if m, _ := filepath.Glob(filepath.Join(d, "journal-*.md")); len(m) > 0 {
			return d
		}
	}
	return ""
}

// ImportLegacy reprend la mémoire de l'ancien AJEAN rangée dans dir (son
// memory/_jean). À lancer AJEAN arrêté.
func ImportLegacy(dir string) (ImportReport, error) {
	mu.Lock()
	defer mu.Unlock()
	var rep ImportReport
	steps := []func(string, *ImportReport) error{importProfile, importLessons, importRules, importFiches, importJournal, importCounters}
	for _, step := range steps {
		if err := step(dir, &rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func importProfile(dir string, rep *ImportReport) error {
	s, err := legacyRead(filepath.Join(dir, "profile.md"))
	if err != nil {
		return err
	}
	facts, err := profileLocked()
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, f := range facts {
		have[f.Key] = true
	}
	added := 0
	for _, f := range parseProfile(s) {
		if have[f.Key] {
			continue
		}
		f.Value = clip(squash(f.Value), valueMaxChars)
		if runeLen(renderProfile(append(facts, f))) > profileMaxChars {
			rep.FactsSkipped++
			continue
		}
		facts, have[f.Key] = append(facts, f), true
		added++
	}
	if rep.FactsSkipped > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d ligne(s) du profil laissée(s) de côté : le profil est plein (%d caractères)", rep.FactsSkipped, profileMaxChars))
	}
	if added == 0 {
		return nil
	}
	rep.Facts = added
	return writeProfileLocked(facts)
}

func importLessons(dir string, rep *ImportReport) error {
	s, err := legacyRead(filepath.Join(dir, "lessons.md"))
	if err != nil {
		return err
	}
	old := parseLessons(s)
	cur, err := lessonsLocked()
	if err != nil {
		return err
	}
	next, err := nextLessonLocked(cur)
	if err != nil {
		return err
	}
	// Le compteur de l'ancien AJEAN : un numéro déjà donné là-bas ne doit pas resservir ici.
	if seq, err := legacyRead(filepath.Join(dir, "lessons-seq.txt")); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(seq)); err == nil {
			next = max(next, v)
		}
	}
	have, taken := map[string]bool{}, map[int]bool{}
	for _, l := range cur {
		have[fold(l.Text)], taken[l.N] = true, true
	}
	added := 0
	for _, l := range old {
		next = max(next, l.N+1)
	}
	for _, l := range old {
		l.Text = clip(squash(l.Text), lessonMaxChars)
		if l.Text == "" || have[fold(l.Text)] {
			continue
		}
		if taken[l.N] { // le numéro est pris ici : la leçon en reçoit un neuf
			l.N = next
			next++
		}
		cur, have[fold(l.Text)], taken[l.N] = append(cur, l), true, true
		added++
	}
	if added == 0 {
		return nil
	}
	rep.Lessons = added
	if err := home.WriteFile(statePath("lesson-seq"), []byte(strconv.Itoa(next))); err != nil {
		return err
	}
	return writeLessonsLocked(cur)
}

func importRules(dir string, rep *ImportReport) error {
	s, err := legacyRead(filepath.Join(dir, "rules.json"))
	if err != nil || strings.TrimSpace(s) == "" {
		return err
	}
	var old []struct {
		Kind, From, To, Why string
	}
	if err := json.Unmarshal([]byte(s), &old); err != nil {
		rep.Notes = append(rep.Notes, "règles d'écriture illisibles (rules.json) : non reprises")
		return nil
	}
	cur, err := rulesLocked()
	if err != nil {
		return err
	}
	next := 1
	have := map[string]bool{}
	for _, r := range cur {
		have[r.From], next = true, max(next, r.N+1)
	}
	added := 0
	for _, o := range old {
		switch {
		case o.Kind != "" && o.Kind != "replace":
			rep.RulesSkipped++
			rep.Notes = append(rep.Notes, fmt.Sprintf("règle « %s » non reprise : ce genre de règle n'existe plus", o.Kind))
		case o.From == "" || have[o.From]:
		case runeLen(o.From) > ruleMaxChars || runeLen(o.To) > ruleMaxChars || strings.Contains(o.To, o.From) || len(cur) >= rulesMax:
			rep.RulesSkipped++
			rep.Notes = append(rep.Notes, fmt.Sprintf("règle %s non reprise : elle ne tient pas dans les limites d'ici", strconv.Quote(o.From)))
		default:
			cur = append(cur, Rule{N: next, From: o.From, To: o.To, Why: squash(o.Why)})
			have[o.From] = true
			next++
			added++
		}
	}
	if added == 0 {
		return nil
	}
	rep.Rules = added
	return writeRulesLocked(cur)
}

func importFiches(dir string, rep *ImportReport) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		base, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || e.IsDir() {
			continue
		}
		name, archived := "", false
		if n, ok := strings.CutPrefix(base, "archive-fiche-"); ok {
			name, archived = n, true
		} else if n, ok := strings.CutPrefix(base, "fiche-"); ok {
			name = n
		} else {
			continue
		}
		if !ficheNameRe.MatchString(name) {
			rep.FichesSkipped++
			rep.Notes = append(rep.Notes, fmt.Sprintf("fiche %q non reprise : nom non valable ici", name))
			continue
		}
		if _, exists := findFicheLocked(name); exists {
			rep.FichesSkipped++
			continue
		}
		s, err := legacyRead(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		f := parseFiche(name, s)
		f.Archived = archived
		f.When = clip(squash(f.When), mergeWhenMaxChars)
		if f.When == "" {
			f.When = strings.ReplaceAll(name, "-", " ")
		}
		if f.Content == "" {
			continue
		}
		if err := writeFicheLocked(f); err != nil {
			return err
		}
		rep.Fiches++
	}
	return nil
}

// parseLegacyJournal lit un mois de l'ancien journal et le ramène à nos formes :
// décalage horaire toujours présent, genre toujours présent, réponse de
// l'assistant sous notre étiquette.
func parseLegacyJournal(month, s string) []entry {
	var out []entry
	var body []string
	open := false
	flush := func() {
		if !open {
			return
		}
		e := &out[len(out)-1]
		e.text = strings.TrimSpace(strings.Join(body, "\n"))
		if e.kind == kindExchange {
			e.text = strings.Replace(e.text, "\n"+legacyAssistantLabel, "\n"+assistantLabel, 1)
		}
		e.folded = fold(e.text)
	}
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, "\r")
		if m := legacyHeadRe.FindStringSubmatch(l); m != nil {
			var at time.Time
			var err error
			if m[2] != "" {
				at, err = time.Parse(headLayout, m[1]+" "+m[2])
			} else {
				at, err = time.ParseInLocation(entryTimeLayout, m[1], config.Location())
			}
			if err == nil {
				flush()
				kind := kindRe.ReplaceAllString(strings.ToLower(m[4]), "")
				if kind == "" { // premières versions : pas de genre
					kind = kindNote
				}
				out = append(out, entry{id: m[3], at: at, kind: kind, month: month})
				body, open = nil, true
				continue
			}
		}
		if open {
			body = append(body, l)
		}
	}
	flush()
	return out
}

func importJournal(dir string, rep *ImportReport) error {
	files, _ := filepath.Glob(filepath.Join(dir, "journal-*.md"))
	sort.Strings(files)
	monthRe := regexp.MustCompile(`^\d{4}-\d{2}$`)
	for _, file := range files {
		month := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(file), "journal-"), ".md")
		if !monthRe.MatchString(month) {
			continue
		}
		s, err := legacyRead(file)
		if err != nil {
			return err
		}
		entries := append([]entry{}, monthLocked(month)...)
		have := map[string]bool{}
		for _, e := range entries {
			have[e.id] = true
		}
		added := 0
		for _, e := range parseLegacyJournal(month, s) {
			if e.text == "" || have[e.id] {
				continue
			}
			entries, have[e.id] = append(entries, e), true
			added++
		}
		if added == 0 {
			continue
		}
		// Le journal se lit dans l'ordre du fichier : les entrées reprises se
		// glissent à leur date parmi celles d'ici.
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
		path := journalPath(month)
		if err := home.WriteFile(path, []byte(renderJournal(month, entries))); err != nil {
			return err
		}
		delete(journalCache, path)
		rep.Entries += added
	}
	return nil
}

// importCounters reprend l'usage des fiches et les rappels du journal, s'il n'y
// en a pas encore ici. Des compteurs perdus ne coûtent rien : une erreur de
// lecture n'arrête pas la reprise.
func importCounters(dir string, _ *ImportReport) error {
	if _, err := os.Stat(usagePath()); errors.Is(err, os.ErrNotExist) {
		var use map[string]ficheUse
		if s, err := legacyRead(filepath.Join(dir, "usage-fiches.json")); err == nil && json.Unmarshal([]byte(s), &use) == nil && len(use) > 0 {
			_ = writeState(usagePath(), use)
		}
	}
	if _, err := os.Stat(statePath("recalls.json")); errors.Is(err, os.ErrNotExist) {
		var recalls map[string]recallCount
		if s, err := legacyRead(filepath.Join(dir, "recalls.md")); err == nil && json.Unmarshal([]byte(s), &recalls) == nil && len(recalls) > 0 {
			_ = writeState(statePath("recalls.json"), recalls)
		}
	}
	return nil
}
