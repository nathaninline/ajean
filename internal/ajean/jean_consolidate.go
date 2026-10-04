package ajean

// jean_consolidate.go — le SOMMEIL de Jean : la consolidation de sa mémoire.
//
// La révision (jean_reflect.go) ajoute au fil de l'eau. Sans ménage, au bout de
// quelques mois la mémoire devient un tas : un même fait dans le profil ET une
// fiche, une leçon contredite par une correction plus récente, une fiche dont la
// moitié des étapes ont été rapiécées. La consolidation relit TOUTE la mémoire
// d'un coup, à froid, et la remet d'aplomb : fusionner, trancher les
// contradictions (la plus récente gagne), ranger chaque chose à sa place.
//
// Quand : une fois que Jean est resté inactif assez longtemps pour que son
// contexte soit vidé au prochain message (JEAN_IDLE_HOURS). Le tour part d'un
// contexte neuf, ce qui évince le cache de la conversation ; mais ce cache-là
// serait jeté de toute façon au prochain message. Coût réel : nul. Et seulement
// si la mémoire a bougé depuis la dernière consolidation.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	jeanConsolidateMaxIter   = 24
	jeanConsolidateFullChars = 16000 // fiches données en entier sous ce total, sinon leur index
	jeanConsolidateLogLines  = 40    // derniers changements de mémoire montrés
)

// jeanMemDirty : la mémoire durable (profil, leçons, fiches) a changé depuis la
// dernière consolidation. Posé par jeanWrite.
var jeanMemDirty atomic.Bool

var jeanConsolidateTimer struct {
	t *time.Timer
}

func jeanConsolidateDelay() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("AJEAN_JEAN_CONSOLIDATE_DELAY")); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	h := jeanIdleHours()
	if h <= 0 {
		h = 3
	}
	return time.Duration(h*float64(time.Hour)) + 2*time.Minute
}

// jeanConsolidateArm : (re)programme la consolidation après la dernière activité.
// Appelée sous jeanReflect.mu.
func jeanConsolidateArmLocked(d time.Duration) {
	if jeanConsolidateTimer.t != nil {
		jeanConsolidateTimer.t.Stop()
	}
	jeanConsolidateTimer.t = time.AfterFunc(d, runJeanConsolidate)
}

func runJeanConsolidate() {
	if !jeanMemDirty.Load() && !jeanMemChangedSinceConsolidation() {
		return
	}
	// Une révision encore due passe d'abord : elle voit le fil, pas nous.
	jeanReflect.mu.Lock()
	pending := jeanReflect.pending + jeanReflect.orphanN
	jeanReflect.mu.Unlock()
	if pending > 0 {
		runJeanReflect()
	}

	// Ordre des verrous : c.mu jamais sous jeanReflect.mu (voir runJeanReflect).
	c := conv
	c.mu.Lock()
	busy := c.Generating
	c.mu.Unlock()
	jeanReflect.mu.Lock()
	if busy || jeanReflect.cancel != nil || !healthCheck() {
		jeanConsolidateArmLocked(10 * time.Minute)
		jeanReflect.mu.Unlock()
		return
	}
	caps, temp := jeanReflect.caps, jeanReflect.temp
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	ctx = context.WithValue(ctx, jeanReflectKey{}, true)
	jeanReflect.cancel = cancel
	jeanReflect.mu.Unlock()
	defer cancel()

	dump, err := jeanMemoryDump()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[jean] consolidation impossible : %v\n", err)
		jeanReflect.mu.Lock()
		jeanReflect.cancel = nil
		jeanReflect.mu.Unlock()
		return
	}
	jeanMemDirty.Store(false) // ce que la consolidation écrit la re-salira : normal, on ne relance pas pour ça
	msgs := []Message{{Role: "user", Content: jeanConsolidatePrompt() + "\n\n" + dump}}
	if sp := readSysPrompt(); sp != "" {
		msgs = append([]Message{{Role: "system", Content: sp}}, msgs...)
	}
	before := jeanSnapMemory()
	r := jeanSilentRun(ctx, cancel, InjectSkills(msgs, caps), caps, temp, jeanConsolidateMaxIter, "consolidation")
	// Même interrompue, une consolidation a pu écrire : le garde-fou passe toujours.
	if rep := jeanGuardReport(jeanGuardCheck(before)); rep != "" {
		jeanMu.Lock()
		_ = jeanAppendLocked("consolidation", rep)
		jeanMu.Unlock()
		fmt.Fprintf(os.Stderr, "[jean] %s\n", rep)
	}
	jeanMemDirty.Store(false)
	jeanMarkConsolidated()

	jeanReflect.mu.Lock()
	aborted := ctx.Err() == context.Canceled && r.iter < jeanConsolidateMaxIter
	jeanReflect.cancel = nil
	if aborted || (r.err != nil && r.iter == 0) {
		jeanMemDirty.Store(true) // à refaire
		jeanConsolidateArmLocked(jeanConsolidateDelay())
		jeanReflect.mu.Unlock()
		return
	}
	jeanReflect.mu.Unlock()

	if len(r.used) > 0 {
		sum := strings.Join(r.used, " · ")
		if r.said != "" && !strings.EqualFold(r.said, "ok") {
			sum = clipRunes(r.said, 400) + " (" + sum + ")"
		}
		jeanMu.Lock()
		_ = jeanAppendLocked("consolidation", "Mémoire consolidée : "+sum)
		jeanMu.Unlock()
	}
	fmt.Fprintf(os.Stderr, "[jean] consolidation : %d écriture(s), prompt %d tokens\n", len(r.used), r.totalToks)
}

// jeanMemoryDump : toute la mémoire durable, en un texte lisible par le modèle.
func jeanMemoryDump() (string, error) {
	jeanMu.Lock()
	defer jeanMu.Unlock()
	facts, err := jeanProfile()
	if err != nil {
		return "", err
	}
	ls, err := jeanLessons()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("## PROFILE\n")
	for _, f := range facts {
		b.WriteString("- " + f.Key + ": " + f.Value + "\n")
	}
	b.WriteString("\n## LESSONS\n" + numberedLessons(ls) + "\n")
	if rs := jeanRulesLocked(); len(rs) > 0 {
		b.WriteString("\n## RULES YOUR HARNESS ENFORCES (jean_rule)\n")
		for _, r := range rs {
			b.WriteString(r.String() + "\n")
		}
	}
	fs := jeanFiches(true)
	total := 0
	for _, f := range fs {
		total += len(f.Content)
	}
	b.WriteString("\n## FICHES (most recently changed first)\n")
	userLoc := loc(defaultTaskTZ())
	usage := jeanUsageLocked()
	for _, f := range fs {
		use := jeanFicheUsageLine(f.Name, usage)
		changed := time.UnixMilli(f.ModTime).In(userLoc).Format("2006-01-02 15:04")
		if total <= jeanConsolidateFullChars {
			fmt.Fprintf(&b, "\n### fiche %s (when: %s) · changed %s · %s\n%s\n", f.Name, f.When, changed, use, f.Content)
		} else {
			fmt.Fprintf(&b, "- %s (when: %s) · changed %s · %s · %d chars, jean_read to open\n", f.Name, f.When, changed, use, len([]rune(f.Content)))
		}
	}
	// Ce qui existe vraiment dans son espace : une fiche qui cite un script
	// disparu se repère sans outil (la consolidation n'a pas bash).
	for _, d := range []struct{ label, dir string }{{"SCRIPTS FOLDER", jeanScriptsDir()}, {"WORKSPACE", jeanWorkspace()}} {
		ents, err := os.ReadDir(d.dir)
		if err != nil {
			continue
		}
		var names []string
		for _, e := range ents {
			n := e.Name()
			if e.IsDir() {
				n += "/"
			}
			names = append(names, n)
			if len(names) == 60 {
				names = append(names, "…")
				break
			}
		}
		fmt.Fprintf(&b, "\n## FILES IN YOUR %s (top level)\n%s\n", d.label, strings.Join(names, ", "))
	}
	// Les derniers changements, datés : c'est ce qui permet de trancher une
	// contradiction (la version la plus récente l'emporte).
	var log []jeanEntry
	for _, e := range jeanJournalAll() {
		switch e.Kind {
		case "profil", "lecon", "fiche", "revision", "consolidation":
			log = append(log, e)
		}
	}
	if len(log) > jeanConsolidateLogLines {
		log = log[len(log)-jeanConsolidateLogLines:]
	}
	if len(log) > 0 {
		// Un texte du journal qui n'est plus dans la mémoire a été retiré ou
		// corrigé depuis (par Jean, l'utilisateur ou une correction à la main).
		// Vu en test : sans ce marquage, le ménage a remis dans une fiche une
		// fausse croyance corrigée le matin même, en la croyant « plus récente ».
		current := b.String()
		b.WriteString("\n## RECENT MEMORY CHANGES (oldest first)\n")
		for _, e := range log {
			text := strings.ReplaceAll(e.Text, "\n", " ")
			mark := ""
			if jeanStaleChange(text, current) {
				mark = " [NO LONGER IN MEMORY: removed or corrected since, never bring it back]"
			}
			fmt.Fprintf(&b, "- %s %s: %s%s\n", e.When.In(userLoc).Format("2006-01-02 15:04"), e.Kind, clipRunes(text, 300), mark)
		}
	}
	return b.String(), nil
}

func jeanConsolidatePrompt() string {
	return `[MEMORY CONSOLIDATION. Silent maintenance while the user is away: nobody reads your answer.]
Below is your whole long-term memory. Tidy it so that future you finds the right thing at once and is never misled. Work through it:
1. Duplicates: the same information in two places → keep it only where it belongs (facts about the user → profile; a rule learned from a mistake → lessons; how to do a task → that task's fiche), remove the other copy (jean_forget, or jean_patch the fiche). Never move a lesson into the profile: lessons are the rules you must never break again (often after a repeated mistake); when a profile line repeats a lesson, remove the profile line.
2. Contradictions between two places of the CURRENT memory: the most recent change wins (see RECENT MEMORY CHANGES). The current memory is the truth: never bring back something that only appears in RECENT MEMORY CHANGES, it was removed or corrected on purpose.
3. A lesson that only concerns one fiche → jean_lesson with fiche=<name>, then jean_forget lecon-N.
4. Profile lines that are guessed, vague or outdated → fix (jean_remember same key) or jean_forget.
5. Fiches: make unclear or patched-up steps clean and precise with jean_patch. Keep every step and pitfall that is still true. When a value changed (a number, a duration, a rate, a path, a method), replace the old value: never leave the new one next to the old, and check that the rest of the line still agrees with it.
6. Overlapping fiches (same task, or one tool/method replaced by a newer one): the newer fiche is the reference. Merge what is still true into it, then make the older one point to it (jean_patch) or jean_forget it if nothing is left. A fiche must never send you to an outdated method.
7. A fiche section "## Restauré (à réintégrer)" holds lines a past tidy-up lost by mistake: put each back where it belongs in that fiche (jean_patch), then remove the section.
8. Dead weight: a fiche not opened for 60+ days and not changed recently, about something that is over or replaced (an old one-off project, an abandoned method) → jean_forget it: it is archived, not lost (jean_search still finds it, reading it brings it back). Keep fiches for things that come back from time to time (yearly events, rare but real procedures). Profile lines about things that are over → jean_forget.
Rules: never invent anything; never delete something true and useful; prefer few precise edits. If everything is already clean, change nothing.
When done, reply with one short line saying what you changed (or just OK).`
}

// Marqueur de la dernière consolidation : sa date seule compte. Le drapeau
// jeanMemDirty ne survit pas à un redémarrage ; la comparaison des dates, si.
const jeanConsolidatedMark = ".consolidated"

func jeanMarkConsolidated() {
	p := filepath.Join(jeanDir(), jeanConsolidatedMark)
	_ = os.WriteFile(p, nil, 0o600)
	now := time.Now()
	_ = os.Chtimes(p, now, now)
}

// jeanMemChangedSinceConsolidation : un fichier du profil, des leçons ou d'une
// fiche est-il plus récent que la dernière consolidation ?
func jeanMemChangedSinceConsolidation() bool {
	var last time.Time
	if st, err := os.Stat(filepath.Join(jeanDir(), jeanConsolidatedMark)); err == nil {
		last = st.ModTime()
	}
	ents, _ := os.ReadDir(jeanDir())
	for _, e := range ents {
		n := e.Name()
		if n != jeanProfileFile && n != jeanLessonsFile && !(strings.HasPrefix(n, jeanFichePrefix) && strings.HasSuffix(n, ".md")) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(last) {
			return true
		}
	}
	return false
}

// jeanStaleChange : le contenu d'une entrée du journal (après « : ») a-t-il
// disparu de la mémoire actuelle ? Comparaison par mots, comme les autres
// garde-fous. Une entrée sans contenu (« Fiche « x » retouchée ») n'est jamais
// marquée.
func jeanStaleChange(text, current string) bool {
	i := strings.Index(text, " : ")
	if i < 0 {
		return false
	}
	content := text[i+3:]
	if j := strings.Index(content, "Maintenant : "); j >= 0 {
		content = content[j+len("Maintenant : "):] // profil remplacé : seule la nouvelle valeur compte
	}
	cw := jeanWords(content)
	if len(cw) < 5 {
		return false
	}
	for _, line := range strings.Split(current, "\n") {
		if jeanSimilar(cw, jeanWords(line)) >= 0.6 {
			return false
		}
	}
	return true
}
