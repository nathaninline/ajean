package memory

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"ajean/internal/home"
	"ajean/internal/tool"
)

// Les leçons : ce que l'assistant a appris à ses dépens (une correction de
// l'utilisateur, une méthode qui a échoué avant celle qui marche). Une leçon
// générale est relue à chaque message, donc à budget dur comme le profil ; une
// leçon propre à une fiche va dans sa section « Pièges », lue avec elle au
// moment où elle sert, sans rien coûter avant.

const (
	lessonsMaxChars = 2500 // ~600 tokens payés à chaque message
	lessonMaxChars  = 300
	lessonsHead     = "# Leçons\n\n"
	lessonPrefix    = "lecon-"
)

// Lesson : une leçon générale. Son numéro lui reste à vie.
type Lesson struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

func parseLessons(s string) []Lesson {
	out := []Lesson{}
	next := 1
	for _, l := range strings.Split(s, "\n") {
		t, ok := strings.CutPrefix(strings.TrimRight(l, "\r"), "- ")
		if !ok || strings.TrimSpace(t) == "" {
			continue
		}
		n := 0
		if rest, ok := strings.CutPrefix(t, "["+lessonPrefix); ok {
			if num, text, ok := strings.Cut(rest, "] "); ok {
				if v, err := strconv.Atoi(num); err == nil && v > 0 {
					n, t = v, text
				}
			}
		}
		if n == 0 {
			n = next // ligne ajoutée à la main, sans numéro
		}
		next = max(next, n+1)
		out = append(out, Lesson{n, strings.TrimSpace(t)})
	}
	return out
}

func renderLessons(ls []Lesson) string {
	var b strings.Builder
	b.WriteString(lessonsHead)
	for _, l := range ls {
		fmt.Fprintf(&b, "- [%s%d] %s\n", lessonPrefix, l.N, l.Text)
	}
	return b.String()
}

// lessonsLocked lit les leçons. Une lecture ratée est une erreur, pas une
// liste vide : réécrire à partir d'elle effacerait toutes les leçons.
func lessonsLocked() ([]Lesson, error) {
	s, err := readFile(memPath(lessonsFile))
	if err != nil {
		return nil, fmt.Errorf("leçons illisibles : %w", err)
	}
	return parseLessons(s), nil
}

func writeLessonsLocked(ls []Lesson) error {
	return home.WriteFile(memPath(lessonsFile), []byte(renderLessons(ls)))
}

// Lessons : les leçons générales, dans l'ordre du fichier (aucune si le
// fichier est illisible).
func Lessons() []Lesson {
	mu.Lock()
	defer mu.Unlock()
	ls, err := lessonsLocked()
	if err != nil {
		return []Lesson{}
	}
	return ls
}

func numberedLessons(ls []Lesson) string {
	var b strings.Builder
	for _, l := range ls {
		fmt.Fprintf(&b, "%s%d: %s\n", lessonPrefix, l.N, l.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}

// nextLessonLocked : le numéro de la prochaine leçon. Un numéro n'est JAMAIS
// réutilisé, même après un oubli : sinon « oublier lecon-1 puis en écrire une »
// redonnerait lecon-1, et un appel tardif viserait la nouvelle au lieu de
// l'ancienne. D'où l'erreur si le compteur est illisible : le deviner pourrait
// redonner un numéro retiré.
func nextLessonLocked(ls []Lesson) (int, error) {
	s, err := readFile(statePath("lesson-seq"))
	if err != nil {
		return 0, err
	}
	next := 1
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		next = max(next, v)
	}
	for _, l := range ls {
		next = max(next, l.N+1)
	}
	return next, nil
}

// addLesson (outil memory_lesson) : une leçon générale, ou le piège d'une
// fiche. Renvoie ce qui a été fait : avec « [ok] » si la mémoire a changé,
// sans lui si elle disait déjà ça.
func addLesson(text, ficheName string) (string, error) {
	text = squash(text)
	if text == "" {
		return "", errors.New("leçon vide")
	}
	if n := runeLen(text); n > lessonMaxChars {
		return "", fmt.Errorf("leçon trop longue (%d car., max %d) : une phrase, la règle à suivre et pourquoi", n, lessonMaxChars)
	}
	mu.Lock()
	defer mu.Unlock()
	fs := fichesLocked(false)
	if dup := saidInFiches(text, fs); dup != "" {
		return fmt.Sprintf("déjà dit dans la fiche « %s » : rien ajouté", dup), nil
	}
	if strings.TrimSpace(ficheName) != "" {
		return addPitfallLocked(normFicheName(ficheName), text, fs)
	}
	ls, err := lessonsLocked()
	if err != nil {
		return "", err
	}
	mine := words(text)
	for _, l := range ls {
		if fold(l.Text) == fold(text) {
			return fmt.Sprintf("leçon déjà connue (%s%d) : rien ajouté", lessonPrefix, l.N), nil
		}
		if sameIdea(mine, words(l.Text)) {
			return "", fmt.Errorf("%s%d dit déjà presque la même chose : « %s ». Pour la préciser : memory_forget %s%d puis une seule memory_lesson qui dit tout", lessonPrefix, l.N, l.Text, lessonPrefix, l.N)
		}
	}
	next, err := nextLessonLocked(ls)
	if err != nil {
		return "", err
	}
	all := append(ls, Lesson{next, text})
	if len(renderLessons(all)) > lessonsMaxChars {
		return "", fmt.Errorf("leçons pleines (%d car. max). Fusionne deux leçons proches (memory_forget %sN puis une seule memory_lesson qui les résume) ou oublie la moins utile. Leçons :\n%s",
			lessonsMaxChars, lessonPrefix, numberedLessons(ls))
	}
	if err := writeLessonsLocked(all); err != nil {
		return "", err
	}
	if err := home.WriteFile(statePath("lesson-seq"), []byte(strconv.Itoa(next+1))); err != nil {
		return "", err
	}
	logLocked(kindLesson, text)
	return tool.OK("leçon retenue (%s%d)", lessonPrefix, next), nil
}

// ForgetLesson retire une leçon (trace gardée au journal). Les autres gardent
// leur numéro.
func ForgetLesson(n int) error {
	mu.Lock()
	defer mu.Unlock()
	ls, err := lessonsLocked()
	if err != nil {
		return err
	}
	for i, l := range ls {
		if l.N != n {
			continue
		}
		// Le compteur retient le numéro retiré même si c'était le plus grand.
		next, err := nextLessonLocked(ls)
		if err != nil {
			return err
		}
		if err := home.WriteFile(statePath("lesson-seq"), []byte(strconv.Itoa(next))); err != nil {
			return err
		}
		if err := writeLessonsLocked(append(ls[:i], ls[i+1:]...)); err != nil {
			return err
		}
		noteRemovedLocked(lessonMark(n))
		logLocked(kindLesson, "Leçon retirée : "+l.Text)
		return nil
	}
	if len(ls) == 0 {
		return fmt.Errorf("%s%d n'existe pas : aucune leçon enregistrée", lessonPrefix, n)
	}
	return fmt.Errorf("%s%d n'existe pas. Leçons :\n%s", lessonPrefix, n, numberedLessons(ls))
}

// addPitfallLocked ajoute une leçon à la section « Pièges » d'une fiche.
func addPitfallLocked(name, text string, fs []fiche) (string, error) {
	f, ok := loadFicheLocked(name, false)
	if !ok {
		return "", fmt.Errorf("fiche « %s » introuvable (sans fiche, la leçon est générale : omets 'fiche')", name)
	}
	if strings.Contains(fold(f.Content), fold(text)) {
		return fmt.Sprintf("piège déjà noté dans la fiche « %s » : rien ajouté", name), nil
	}
	if better := misfiledPitfall(name, text, fs); better != "" {
		return "", fmt.Errorf("ce piège semble concerner la fiche « %s » plutôt que « %s ». Rappelle memory_lesson avec fiche=%s ; si « %s » est vraiment la bonne, refais le même appel", better, name, better, name)
	}
	limit := ficheLimit(f)
	f.Content = withPitfall(f.Content, text)
	if runeLen(f.Content) > limit {
		return "", fmt.Errorf("fiche « %s » pleine : raccourcis-la (fiche_patch) avant d'ajouter un piège", name)
	}
	if err := writeFicheLocked(f); err != nil {
		return "", err
	}
	logLocked(kindLesson, fmt.Sprintf("Fiche « %s », piège ajouté : %s", name, text))
	return tool.OK("piège ajouté à la fiche « %s »", name), nil
}

// withPitfall : le contenu d'une fiche, un piège de plus à la fin de sa section
// « Pièges » (créée en bas de fiche si elle manque). La section est cherchée
// par sa ligne de titre exacte : « ### Pièges », dans une section venue d'une
// fusion, appartient à l'ancienne fiche et non à celle-ci.
func withPitfall(content, text string) string {
	lines := strings.Split(content, "\n")
	head := slices.IndexFunc(lines, func(l string) bool { return strings.TrimSpace(l) == pitfallsHead })
	if head < 0 {
		return content + "\n\n" + pitfallsHead + "\n- " + text
	}
	// La section s'arrête au titre de même niveau qui la suit, s'il y en a un.
	end := len(lines)
	for i := head + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	last := end
	for last > head+1 && strings.TrimSpace(lines[last-1]) == "" {
		last--
	}
	out := append(slices.Clone(lines[:last]), "- "+text)
	if end < len(lines) {
		out = append(append(out, ""), lines[end:]...)
	}
	return strings.Join(out, "\n")
}
