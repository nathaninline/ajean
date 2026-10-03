package ajean

// jean_lessons.go — 4ᵉ rangement de la mémoire de Jean : les LEÇONS (inspiré de
// MEMORY.md d'Hermes Agent). Le profil dit qui est l'utilisateur ; les leçons
// disent ce que Jean a appris à ses dépens : une correction de l'utilisateur, une
// méthode qui a échoué avant celle qui marche, une bizarrerie de l'environnement.
//
//   - Leçon GÉNÉRALE : lessons.md, des lignes `- [lecon-N] texte` TOUJOURS injectées (sous
//     le profil). Budget dur comme le profil : plein = refus, Jean doit fusionner
//     ou oublier (jean_forget lecon-N). C'est la contrainte qui consolide.
//   - Leçon d'une FICHE : ajoutée à la section « ## Pièges » de la fiche, lue
//     avec elle au moment où elle sert (et pas avant : zéro coût de contexte).
//
// jean_patch complète : retoucher une fiche par remplacement exact, sans la
// réécrire entière (un petit modèle qui recopie 5 000 caractères en perd).

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	jeanLessonsFile     = "lessons.md"
	jeanLessonsSeqFile  = "lessons-seq.txt" // prochain numéro de leçon (jamais réutilisé)
	jeanLessonsMaxChars = 2500              // ~600 tokens toujours injectés : à ne pas gonfler
	jeanLessonMaxChars  = 300
	jeanPitfallsHead    = "## Pièges"
)

// Chaque leçon garde son numéro à vie : avec une liste renumérotée, retirer
// lecon-1 puis lecon-2 effaçait la mauvaise (vu en essai : le modèle oublie
// plusieurs leçons d'affilée en se fiant aux numéros qu'il a sous les yeux).
type jeanLessonT struct {
	N    int
	Text string
}

func parseJeanLessons(s string) []jeanLessonT {
	var out []jeanLessonT
	next := 1
	for _, l := range strings.Split(s, "\n") {
		t, ok := strings.CutPrefix(strings.TrimRight(l, "\r"), "- ")
		if !ok || strings.TrimSpace(t) == "" {
			continue
		}
		n := 0
		if rest, ok := strings.CutPrefix(t, "[lecon-"); ok {
			if i := strings.Index(rest, "] "); i > 0 {
				if v, err := strconv.Atoi(rest[:i]); err == nil && v > 0 {
					n, t = v, rest[i+2:]
				}
			}
		}
		if n == 0 {
			n = next
		}
		if n >= next {
			next = n + 1
		}
		out = append(out, jeanLessonT{n, strings.TrimSpace(t)})
	}
	return out
}

func renderJeanLessons(ls []jeanLessonT) string {
	var b strings.Builder
	b.WriteString("# Leçons de Jean\n\n")
	for _, l := range ls {
		fmt.Fprintf(&b, "- [lecon-%d] %s\n", l.N, l.Text)
	}
	return b.String()
}

func jeanLessons() ([]jeanLessonT, error) {
	s, err := jeanRead(jeanLessonsFile)
	if err != nil {
		return nil, err
	}
	return parseJeanLessons(s), nil
}

// JeanLesson (outil jean_lesson) : une leçon générale, ou attachée à une fiche.
func JeanLesson(text, fiche string) (string, error) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "", fmt.Errorf("leçon vide")
	}
	if n := len([]rune(text)); n > jeanLessonMaxChars {
		return "", fmt.Errorf("leçon trop longue (%d car., max %d) : une phrase, la règle à suivre et pourquoi", n, jeanLessonMaxChars)
	}
	if strings.TrimSpace(fiche) != "" {
		name := normFicheName(fiche)
		if msg := jeanCheckPitfall(name, text); msg != "" {
			return msg, nil
		}
		return jeanFicheAddPitfall(name, text)
	}
	if d := jeanDupInFiches(text); d != "" {
		return fmt.Sprintf("[ok] déjà dit dans la fiche « %s » : rien ajouté", d), nil
	}
	jeanMu.Lock()
	defer jeanMu.Unlock()
	ls, err := jeanLessons()
	if err != nil {
		return "", err
	}
	// Un numéro n'est JAMAIS réutilisé, même après un oubli : sinon « oublier
	// lecon-1 puis en écrire une » redonnait lecon-1, et un appel en retard
	// (révision, consolidation) visait la nouvelle au lieu de l'ancienne.
	next := 1
	if s, _ := jeanRead(jeanLessonsSeqFile); s != "" {
		next, _ = strconv.Atoi(strings.TrimSpace(s))
	}
	for _, l := range ls {
		if foldSearch(l.Text) == foldSearch(text) {
			return fmt.Sprintf("[ok] leçon déjà connue (lecon-%d)", l.N), nil
		}
		if l.N >= next {
			next = l.N + 1
		}
	}
	if next < 1 {
		next = 1
	}
	all := append(ls, jeanLessonT{next, text})
	if len(renderJeanLessons(all)) > jeanLessonsMaxChars {
		return "", fmt.Errorf("leçons pleines (%d car. max). Fusionne deux leçons proches (jean_forget lecon-N puis une seule jean_lesson qui les résume) ou oublie la moins utile. Leçons :\n%s",
			jeanLessonsMaxChars, numberedLessons(ls))
	}
	if err := jeanWrite(jeanLessonsFile, renderJeanLessons(all)); err != nil {
		return "", err
	}
	_ = jeanWrite(jeanLessonsSeqFile, strconv.Itoa(next+1))
	_ = jeanAppendLocked("lecon", text)
	return fmt.Sprintf("[ok] leçon retenue (lecon-%d)", next), nil
}

func numberedLessons(ls []jeanLessonT) string {
	var b strings.Builder
	for _, l := range ls {
		fmt.Fprintf(&b, "lecon-%d: %s\n", l.N, l.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}

// jeanForgetLesson retire « lecon-N » (trace gardée au journal). Les autres
// leçons gardent leur numéro.
func jeanForgetLesson(key string) (string, bool, error) {
	k, isLesson := strings.ToLower(strings.TrimSpace(key)), false
	for _, p := range []string{"lecon-", "leçon-", "lecon ", "leçon "} {
		if rest, ok := strings.CutPrefix(k, p); ok {
			k, isLesson = rest, true
			break
		}
	}
	if !isLesson {
		return "", false, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(k))
	if err != nil {
		return "", true, fmt.Errorf("« %s » : une leçon s'oublie par son numéro, ex. lecon-3", key)
	}
	jeanMu.Lock()
	defer jeanMu.Unlock()
	ls, err := jeanLessons()
	if err != nil {
		return "", true, err
	}
	for i, l := range ls {
		if l.N != n {
			continue
		}
		ls = append(ls[:i], ls[i+1:]...)
		if err := jeanWrite(jeanLessonsFile, renderJeanLessons(ls)); err != nil {
			return "", true, err
		}
		_ = jeanAppendLocked("lecon", "Leçon retirée : "+l.Text)
		return fmt.Sprintf("[ok] lecon-%d retirée", n), true, nil
	}
	return "", true, fmt.Errorf("lecon-%d n'existe pas. Leçons :\n%s", n, numberedLessons(ls))
}

// jeanLessonsBlock : la section injectée dans le bloc Jean memory.
func jeanLessonsBlock() string {
	ls, err := jeanLessons()
	if err != nil || len(ls) == 0 {
		return ""
	}
	return "\nLessons you learned (follow them; edit with jean_lesson / jean_forget lecon-N):\n" + numberedLessons(ls) + "\n"
}

// jeanFicheAddPitfall ajoute une leçon à la section « Pièges » d'une fiche.
func jeanFicheAddPitfall(name, text string) (string, error) {
	jeanMu.Lock()
	defer jeanMu.Unlock()
	s, err := jeanRead(ficheFile(name))
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", fmt.Errorf("fiche « %s » introuvable (sans fiche, la leçon est générale : omets 'fiche')", name)
	}
	f := parseFiche(name, s)
	if strings.Contains(foldSearch(f.Content), foldSearch(text)) {
		return "[ok] piège déjà noté dans la fiche", nil
	}
	if i := strings.Index(f.Content, jeanPitfallsHead); i >= 0 {
		// Ajout en fin de section (avant le titre ## suivant s'il y en a un).
		rest := f.Content[i+len(jeanPitfallsHead):]
		end := len(f.Content)
		if j := strings.Index(rest, "\n## "); j >= 0 {
			end = i + len(jeanPitfallsHead) + j
		}
		f.Content = strings.TrimRight(f.Content[:end], "\n") + "\n- " + text + "\n" + f.Content[end:]
	} else {
		f.Content += "\n\n" + jeanPitfallsHead + "\n- " + text
	}
	if n := len([]rune(f.Content)); n > jeanFicheMaxChars {
		return "", fmt.Errorf("fiche « %s » pleine : raccourcis-la (jean_patch) avant d'ajouter un piège", name)
	}
	if err := jeanWrite(ficheFile(name), "# "+name+"\nwhen: "+f.When+"\n\n"+strings.TrimSpace(f.Content)+"\n"); err != nil {
		return "", err
	}
	_ = jeanAppendLocked("lecon", fmt.Sprintf("Fiche « %s », piège ajouté : %s", name, text))
	return fmt.Sprintf("[ok] piège ajouté à la fiche « %s »", name), nil
}

// JeanPatch (outil jean_patch) : remplacement exact dans une fiche. old doit y
// figurer une seule fois ; new vide = suppression du passage.
func JeanPatch(name, old, repl string) (string, error) {
	name = normFicheName(name)
	if old == "" {
		return "", fmt.Errorf("'old' vide : donne le passage exact à remplacer (pour ajouter un piège : jean_lesson avec fiche)")
	}
	jeanMu.Lock()
	defer jeanMu.Unlock()
	s, err := jeanRead(ficheFile(name))
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", fmt.Errorf("fiche « %s » introuvable", name)
	}
	f := parseFiche(name, s)
	switch n := strings.Count(f.Content, old); {
	case n == 0:
		return "", fmt.Errorf("passage introuvable dans « %s » : jean_read la fiche et recopie-le exactement", name)
	case n > 1:
		return "", fmt.Errorf("passage présent %d fois dans « %s » : allonge 'old' pour qu'il soit unique", n, name)
	}
	f.Content = strings.TrimSpace(strings.Replace(f.Content, old, repl, 1))
	if n := len([]rune(f.Content)); n > jeanFicheMaxChars {
		return "", fmt.Errorf("fiche trop longue après modification (%d car., max %d)", n, jeanFicheMaxChars)
	}
	if err := jeanWrite(ficheFile(name), "# "+name+"\nwhen: "+f.When+"\n\n"+f.Content+"\n"); err != nil {
		return "", err
	}
	_ = jeanAppendLocked("fiche", fmt.Sprintf("Fiche « %s » retouchée", name))
	return fmt.Sprintf("[ok] fiche « %s » modifiée", name), nil
}
