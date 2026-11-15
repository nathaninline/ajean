package memory

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ajean/internal/config"
	"ajean/internal/home"
)

// Le sommeil : la consolidation nocturne de la mémoire. Un autre paquet mène
// le modèle ; celui-ci fournit la matière (Dump), prend une copie avant, et
// vérifie après que rien ne s'est perdu.
//
// Le ménage a le droit de déplacer, fusionner, reformuler. Il n'a pas le droit
// de perdre. À la fin, chaque unité disparue (ligne du profil, leçon, règle,
// ligne significative d'une fiche) doit soit avoir été retirée exprès par un
// outil, soit se retrouver ailleurs dans la mémoire par ses mots importants.
// Sinon elle est restaurée. La vérification est déterministe, sans IA : c'est
// ce qui permet de faire confiance au ménage sans jamais aller le relire.
//
// Un sommeil dure : pendant ce temps, l'utilisateur supprime une fiche dans
// l'interface, une conversation oublie une leçon. Ces retraits sont voulus eux
// aussi. C'est donc chaque opération de retrait de ce paquet qui se signale au
// sommeil en cours (noteRemovedLocked, noteDroppedLocked), quel que soit
// l'appelant : le garde-fou ne remet que ce qui a disparu sans passer par elles.

const (
	sleepsKept    = 10
	guardCoverage = 0.7 // part des mots importants d'une ligne à retrouver dans un même fichier
	unitMinChars  = 12  // « - Lait », un séparateur : trop court pour juger
	restoredHead  = "## Restauré (à réintégrer)"
	sleepIDLayout = "20060102-150405.000"

	statusRunning = "en cours"
	statusRestore = "restauration"
)

// snapshot : la mémoire à un instant, fichier (chemin relatif) → contenu.
type snapshot map[string]string

// Protégés par mu.
var (
	// sleeping : le sommeil en cours (nil sinon). Deux ménages à la fois, ou
	// une restauration en plein ménage, se marcheraient dessus.
	sleeping *Sleep
	// lastCopyMS : l'heure de la dernière copie. Deux copies dans la même
	// milliseconde auraient le même identifiant.
	lastCopyMS int64
)

// snapshotLocked lit toute la mémoire. Un fichier illisible fait échouer la
// lecture entière : le prendre pour absent ferait croire au garde-fou que son
// contenu a disparu, et à une restauration qu'il faut le supprimer.
func snapshotLocked() (snapshot, error) {
	rels := []string{profileFile, lessonsFile, rulesFile}
	for _, dir := range []string{fichesDir, archivesDir} {
		ents, err := os.ReadDir(memPath(dir))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				rels = append(rels, dir+"/"+e.Name())
			}
		}
	}
	snap := snapshot{}
	for _, rel := range rels {
		s, err := readFile(memPath(rel))
		if err != nil {
			return nil, err
		}
		if s != "" {
			snap[rel] = s
		}
	}
	return snap, nil
}

// ficheOf : le nom de la fiche que porte ce fichier ("" si ce n'est pas une
// fiche de la liste : profil, archive…).
func ficheOf(rel string) string {
	name, ok := strings.CutPrefix(rel, fichesDir+"/")
	if !ok || strings.Contains(name, "/") {
		return ""
	}
	return strings.TrimSuffix(name, ".md")
}

func archiveOf(rel string) string {
	name, ok := strings.CutPrefix(rel, archivesDir+"/")
	if !ok {
		return ""
	}
	return strings.TrimSuffix(name, ".md")
}

// ---- les copies -----------------------------------------------------------------

type sleepMeta struct {
	ID      string   `json:"id"`
	Started int64    `json:"started"` // millisecondes depuis l'époque
	Ended   int64    `json:"ended,omitempty"`
	Status  string   `json:"status"`
	Summary string   `json:"summary,omitempty"`
	Changes []string `json:"changes,omitempty"`
}

// endStamp : l'heure de fin d'un sommeil, arrondie à la milliseconde
// SUPÉRIEURE : elle ne doit jamais précéder la dernière écriture du sommeil,
// sinon la mémoire semblerait avoir changé depuis et un sommeil repartirait.
func endStamp() int64 { return time.Now().UnixMilli() + 1 }

func sleepDir(id string) string      { return statePath("sleeps", id) }
func sleepMetaPath(id string) string { return filepath.Join(sleepDir(id), "meta.json") }

// saveCopy écrit une copie de la mémoire et renvoie son identifiant : sa date,
// à la milliseconde, pour que l'ordre des noms soit celui du temps.
func saveCopy(snap snapshot, meta sleepMeta) (string, error) {
	lastCopyMS = max(time.Now().UnixMilli(), lastCopyMS+1)
	id := strings.Replace(time.UnixMilli(lastCopyMS).Format(sleepIDLayout), ".", "-", 1)
	for rel, s := range snap {
		if err := home.WriteFile(filepath.Join(sleepDir(id), "before", filepath.FromSlash(rel)), []byte(s)); err != nil {
			return "", err
		}
	}
	meta.ID, meta.Started = id, lastCopyMS
	return id, writeState(sleepMetaPath(id), meta)
}

func loadCopy(id string) (snapshot, error) {
	if _, err := os.Stat(sleepMetaPath(id)); err != nil {
		return nil, fmt.Errorf("copie « %s » introuvable", id)
	}
	root := filepath.Join(sleepDir(id), "before")
	snap := snapshot{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // copie d'une mémoire vide : pas de dossier, rien à lire
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snap[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	return snap, err
}

// sleepMetas : les copies gardées, de la plus ancienne à la plus récente.
func sleepMetas() []sleepMeta {
	ents, _ := os.ReadDir(statePath("sleeps"))
	var out []sleepMeta
	for _, e := range ents {
		var m sleepMeta
		if readState(sleepMetaPath(e.Name()), &m) == nil && e.IsDir() && m.ID == e.Name() {
			out = append(out, m)
		}
	}
	return out
}

func pruneCopies() {
	metas := sleepMetas()
	for i := 0; i < len(metas)-sleepsKept; i++ {
		_ = os.RemoveAll(sleepDir(metas[i].ID))
	}
}

// SleepInfo : un sommeil (ou une restauration) et son bilan, pour l'interface.
type SleepInfo struct {
	ID      string   `json:"id"`
	When    string   `json:"when"`
	Status  string   `json:"status"`
	Summary string   `json:"summary"`
	Changes []string `json:"changes"`
}

func (m sleepMeta) when() string {
	return time.UnixMilli(max(m.Started, m.Ended)).In(config.Location()).Format(entryTimeLayout)
}

// Sleeps : les copies gardées, les plus récentes d'abord. Un status
// « restauration » désigne la copie prise avant un retour en arrière.
func Sleeps() []SleepInfo {
	mu.Lock()
	defer mu.Unlock()
	out := []SleepInfo{}
	metas := sleepMetas()
	for i := len(metas) - 1; i >= 0; i-- {
		m := metas[i]
		changes := m.Changes
		if changes == nil {
			changes = []string{}
		}
		out = append(out, SleepInfo{ID: m.ID, When: m.when(), Status: m.Status, Summary: m.Summary, Changes: changes})
	}
	return out
}

// LastSleep : la fin du dernier sommeil, quelle qu'en soit l'issue (zéro s'il
// n'y en a jamais eu).
func LastSleep() time.Time {
	mu.Lock()
	defer mu.Unlock()
	var last int64
	for _, m := range sleepMetas() {
		last = max(last, m.Ended)
	}
	if last == 0 {
		return time.Time{}
	}
	return time.UnixMilli(last)
}

// RestoreSleep remet la mémoire comme avant ce sommeil. L'état actuel est
// d'abord copié : une restauration se défait comme un sommeil.
func RestoreSleep(id string) error {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return errors.New("identifiant de sommeil invalide")
	}
	mu.Lock()
	defer mu.Unlock()
	if sleeping != nil {
		return errors.New("un sommeil est en cours : réessaie dans un moment")
	}
	target, err := loadCopy(id)
	if err != nil {
		return err
	}
	var meta sleepMeta
	if err := readState(sleepMetaPath(id), &meta); err != nil {
		return err
	}
	current, err := snapshotLocked()
	if err != nil {
		return err
	}
	note := "Copie prise avant de remettre la mémoire comme avant le sommeil du " + meta.when()
	restore := sleepMeta{Status: statusRestore, Summary: note, Ended: endStamp()}
	restoreID, err := saveCopy(current, restore)
	if err != nil {
		return err
	}
	pruneCopies()
	// Les fichiers sont réécrits sans passer par les fiches : leur cache ne
	// doit rien garder d'avant.
	clear(ficheCache)
	for rel := range current {
		if _, kept := target[rel]; !kept {
			if err := os.Remove(memPath(rel)); err != nil {
				return err
			}
		}
	}
	for rel, s := range target {
		if current[rel] != s {
			if err := home.WriteFile(memPath(rel), []byte(s)); err != nil {
				return err
			}
		}
	}
	logLocked("sommeil", "Mémoire remise comme avant le sommeil du "+meta.when()+" (à la demande de l'utilisateur)")
	// La restauration est datée APRÈS ses écritures : la mémoire ne paraît pas
	// « changée depuis le dernier sommeil », et la nuit suivante ne refait pas le
	// ménage que l'utilisateur vient d'annuler.
	restore.ID, restore.Started, restore.Ended = restoreID, lastCopyMS, endStamp()
	return writeState(sleepMetaPath(restoreID), restore)
}

// ---- un sommeil -----------------------------------------------------------------

// Sleep : un sommeil en cours.
type Sleep struct {
	id      string
	started int64 // millisecondes depuis l'époque
	before  snapshot
	// removed : ce qui a été retiré EXPRÈS depuis le début du sommeil (voir
	// les fonctions *Mark). Protégé par mu.
	removed map[string]bool
	// dropped : par fiche, les mots des passages ôtés exprès.
	dropped  map[string]map[string]bool
	finished bool
}

// Les marques d'un retrait voulu, une par genre d'unité.
func factMark(key string) string   { return "fact:" + key }
func lessonMark(n int) string      { return fmt.Sprintf("%s%d", lessonPrefix, n) }
func ruleMark(n int) string        { return fmt.Sprintf("%s%d", rulePrefix, n) }
func ficheMark(name string) string { return "fiche:" + name }

// noteRemovedLocked signale au sommeil en cours, s'il y en a un, qu'une unité
// est retirée exprès : le garde-fou ne la remettra pas.
func noteRemovedLocked(mark string) {
	if sleeping != nil {
		sleeping.removed[mark] = true
	}
}

// noteDroppedLocked signale au sommeil en cours qu'un passage d'une fiche est
// ôté exprès : ses mots n'ont plus à se retrouver.
func noteDroppedLocked(name, passage string) {
	if sleeping == nil {
		return
	}
	if sleeping.dropped[name] == nil {
		sleeping.dropped[name] = map[string]bool{}
	}
	for w := range words(passage) {
		sleeping.dropped[name][w] = true
	}
}

// BeginSleep ouvre un sommeil : prend une copie de la mémoire, sur laquelle
// s'appuieront le garde-fou et un éventuel retour en arrière.
func BeginSleep() (*Sleep, error) {
	mu.Lock()
	defer mu.Unlock()
	if sleeping != nil {
		return nil, errors.New("un sommeil est déjà en cours")
	}
	before, err := snapshotLocked()
	if err != nil {
		return nil, err
	}
	id, err := saveCopy(before, sleepMeta{Status: statusRunning})
	if err != nil {
		return nil, err
	}
	sleeping = &Sleep{id: id, started: lastCopyMS, before: before, removed: map[string]bool{}, dropped: map[string]map[string]bool{}}
	return sleeping, nil
}

// Finish ferme le sommeil : passe le garde-fou, écrit le bilan (ce qui a été
// ajouté, modifié, archivé, restauré) et ne garde que les dernières copies.
// status : « ok », « interrompu » ou « erreur ». Renvoie ce que le garde-fou a
// dû restaurer.
func (s *Sleep) Finish(status, summary string) (restored []string) {
	mu.Lock()
	defer mu.Unlock()
	if s.finished {
		return nil
	}
	s.finished, sleeping = true, nil
	after, err := snapshotLocked()
	if err == nil {
		restored = s.guardLocked(after)
		after, err = snapshotLocked()
	}
	var changes []string
	if err != nil {
		// Sans lecture sûre, restaurer écraserait peut-être ce qui est seulement
		// illisible : on ne touche à rien de plus et le bilan le dit.
		changes = []string{"mémoire illisible à la fin du sommeil, garde-fou incomplet : " + err.Error()}
	} else {
		changes = describeChanges(s.before, after)
	}
	for _, r := range restored {
		changes = append(changes, "restauré : "+r)
	}
	meta := sleepMeta{ID: s.id, Started: s.started, Ended: endStamp(), Status: status, Summary: clip(strings.TrimSpace(summary), noteMaxChars), Changes: changes}
	if err := writeState(sleepMetaPath(s.id), meta); err != nil {
		fmt.Fprintf(os.Stderr, "[memory] bilan du sommeil non écrit : %v\n", err)
	}
	pruneCopies()
	return restored
}

// describeChanges : ce qui diffère entre deux états, en clair.
func describeChanges(before, after snapshot) []string {
	label := func(rel string) string {
		switch {
		case rel == profileFile:
			return "profil"
		case rel == lessonsFile:
			return "leçons"
		case rel == rulesFile:
			return "règles"
		case ficheOf(rel) != "":
			return "fiche " + ficheOf(rel)
		}
		return "archive " + archiveOf(rel)
	}
	var out []string
	for rel, now := range after {
		old, existed := before[rel]
		switch {
		case existed && old == now:
		case existed:
			added, removed := lineDiff(old, now)
			out = append(out, fmt.Sprintf("modifié : %s (+%d −%d lignes)", label(rel), added, removed))
		case archiveOf(rel) != "" && before[ficheRel(archiveOf(rel), false)] != "":
			out = append(out, "archivé : fiche "+archiveOf(rel))
		case ficheOf(rel) != "" && before[ficheRel(ficheOf(rel), true)] != "":
			out = append(out, "ressorti des archives : fiche "+ficheOf(rel))
		default:
			out = append(out, "ajouté : "+label(rel))
		}
	}
	for rel := range before {
		_, kept := after[rel]
		archived := ficheOf(rel) != "" && after[ficheRel(ficheOf(rel), true)] != ""
		revived := archiveOf(rel) != "" && after[ficheRel(archiveOf(rel), false)] != ""
		if !kept && !archived && !revived {
			out = append(out, "supprimé : "+label(rel))
		}
	}
	sort.Strings(out)
	return out
}

// lineDiff : combien de lignes distinctes sont apparues et ont disparu.
func lineDiff(old, now string) (added, removed int) {
	set := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(s, "\n") {
			if t := strings.TrimSpace(l); t != "" {
				m[t] = true
			}
		}
		return m
	}
	a, b := set(old), set(now)
	for l := range b {
		if !a[l] {
			added++
		}
	}
	for l := range a {
		if !b[l] {
			removed++
		}
	}
	return added, removed
}

// ---- le garde-fou ---------------------------------------------------------------

// significantLines : les lignes d'une fiche qui portent une information.
func significantLines(content string) []string {
	var out []string
	for _, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if runeLen(t) >= unitMinChars && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "```") {
			out = append(out, t)
		}
	}
	return out
}

// fileWords : les mots importants de chaque fichier de la mémoire.
type fileWords []map[string]bool

func wordsByFile(snap snapshot) fileWords {
	docs := make(fileWords, 0, len(snap))
	for _, content := range snap {
		docs = append(docs, words(content))
	}
	return docs
}

// hold : ces mots se retrouvent-ils, pour l'essentiel, dans un même fichier de
// la mémoire ? (déplacé ou reformulé : rien de perdu). dropped : des mots ôtés
// exprès, qui n'ont plus à se retrouver.
func (docs fileWords) hold(terms, dropped map[string]bool) bool {
	if len(terms) == 0 {
		return false
	}
	need := guardCoverage * float64(len(terms))
	gone := 0
	for w := range terms {
		if dropped[w] {
			gone++
		}
	}
	if float64(gone) >= need {
		return true
	}
	for _, doc := range docs {
		found := 0
		for w := range terms {
			if doc[w] || dropped[w] {
				found++
			}
		}
		if float64(found) >= need {
			return true
		}
	}
	return false
}

// guardLocked compare la mémoire (after) à la copie d'avant et restaure ce qui
// s'est perdu. Renvoie la liste lisible des restaurations.
func (s *Sleep) guardLocked(after snapshot) []string {
	docs := wordsByFile(after)
	restored := s.guardProfileLocked(after, docs)
	restored = append(restored, s.guardLessonsLocked(after, docs)...)
	restored = append(restored, s.guardRulesLocked(after)...)
	return append(restored, s.guardFichesLocked(after, docs)...)
}

// guardProfileLocked remet les lignes du profil perdues. Une clé toujours là
// avec une autre valeur a été remplacée par plus récent : remettre l'ancienne
// contredirait la mémoire.
func (s *Sleep) guardProfileLocked(after snapshot, docs fileWords) (restored []string) {
	facts := parseProfile(after[profileFile])
	have := map[string]bool{}
	for _, f := range facts {
		have[f.Key] = true
	}
	for _, f := range parseProfile(s.before[profileFile]) {
		if have[f.Key] || s.removed[factMark(f.Key)] || docs.hold(words(f.Value), nil) {
			continue
		}
		facts = append(facts, f)
		restored = append(restored, "profil "+f.Key)
	}
	if len(restored) > 0 {
		s.report(writeProfileLocked(facts))
	}
	return restored
}

// guardLessonsLocked remet les leçons perdues, avec leur numéro.
func (s *Sleep) guardLessonsLocked(after snapshot, docs fileWords) (restored []string) {
	lessons := parseLessons(after[lessonsFile])
	numbers, texts := map[int]bool{}, map[string]bool{}
	for _, l := range lessons {
		numbers[l.N], texts[fold(l.Text)] = true, true
	}
	for _, l := range parseLessons(s.before[lessonsFile]) {
		if numbers[l.N] || texts[fold(l.Text)] || s.removed[lessonMark(l.N)] || docs.hold(words(l.Text), nil) {
			continue
		}
		lessons = append(lessons, l)
		restored = append(restored, "leçon « "+clip(l.Text, 80)+" »")
	}
	if len(restored) > 0 {
		s.report(writeLessonsLocked(lessons))
	}
	return restored
}

// guardRulesLocked remet les règles perdues. Trop courtes pour se « retrouver
// ailleurs », elles sont là ou non.
func (s *Sleep) guardRulesLocked(after snapshot) (restored []string) {
	rules := parseRules(after[rulesFile])
	froms, taken := map[string]bool{}, map[int]bool{}
	for _, r := range rules {
		froms[r.From], taken[r.N] = true, true
	}
	for _, r := range parseRules(s.before[rulesFile]) {
		if froms[r.From] || s.removed[ruleMark(r.N)] {
			continue
		}
		for taken[r.N] {
			r.N++
		}
		taken[r.N] = true
		rules = append(rules, r)
		restored = append(restored, "règle "+r.describe())
	}
	if len(restored) > 0 {
		s.report(writeRulesLocked(rules))
	}
	return restored
}

// guardFichesLocked remet les lignes perdues des fiches. Revenir à l'ancienne
// version annulerait aussi ce que le ménage y a fusionné à raison : seules les
// lignes perdues reviennent, dans une section que le prochain sommeil intégrera.
func (s *Sleep) guardFichesLocked(after snapshot, docs fileWords) (restored []string) {
	var names []string
	for rel := range s.before {
		if name := ficheOf(rel); name != "" && !s.removed[ficheMark(name)] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		old := parseFiche(name, s.before[ficheRel(name, false)])
		current := after[ficheRel(name, false)]
		var missing []string
		for _, line := range significantLines(old.Content) {
			terms := words(line)
			if len(terms) > 0 && !strings.Contains(current, line) && !docs.hold(terms, s.dropped[name]) {
				missing = append(missing, line)
			}
		}
		if len(missing) == 0 {
			continue
		}
		f := Fiche{Name: name, When: old.When}
		if current != "" {
			f = parseFiche(name, current)
		}
		if !strings.Contains(f.Content, restoredHead) {
			f.Content = strings.TrimSpace(f.Content + "\n\n" + restoredHead)
		}
		for _, line := range missing {
			restored = append(restored, "fiche "+name+" : « "+clip(line, 80)+" »")
			if !strings.HasPrefix(line, "- ") && !startsNumbered(line) {
				line = "- " + line
			}
			f.Content += "\n" + line
		}
		s.report(writeFicheLocked(f))
	}
	return restored
}

// report : une restauration qui échoue ne doit pas passer inaperçue, sans
// pour autant empêcher les autres.
func (s *Sleep) report(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "[memory] sommeil %s : restauration non écrite : %v\n", s.id, err)
	}
}

// startsNumbered : la ligne est-elle déjà un élément de liste numérotée (« 3. … ») ?
func startsNumbered(s string) bool {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i > 0 && i < len(s) && s[i] == '.'
}
