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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	t   *time.Timer
	due time.Time // échéance à l'heure murale
	gen uint64    // un pas d'un minuteur remplacé ne relance rien
}

// jeanConsolidateStep : le minuteur avance par pas d'au plus une minute et compare
// l'heure murale à l'échéance. Un seul time.AfterFunc suit l'horloge monotone, qui
// s'arrête pendant une veille (WSL2, Mac, Linux en S3) : la consolidation partait
// en retard de toute la durée de la veille, parfois hors de la nuit (#159).
const jeanConsolidateStep = time.Minute

// jeanLockedLogged : « mémoire verrouillée » n'est écrit qu'une fois par attente.
var jeanLockedLogged atomic.Bool

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
	jeanConsolidateTimer.gen++
	jeanConsolidateTimer.due = time.Now().Round(0).Add(d) // Round(0) : sans lecture monotone
	jeanConsolidateTimer.t = time.AfterFunc(min(d, jeanConsolidateStep), jeanConsolidateTick(jeanConsolidateTimer.gen))
}

// jeanConsolidateTick : un pas du minuteur ; lance la consolidation une fois
// l'échéance murale passée, sinon reprogramme le pas suivant.
func jeanConsolidateTick(gen uint64) func() {
	return func() {
		jeanReflect.mu.Lock()
		if gen != jeanConsolidateTimer.gen {
			jeanReflect.mu.Unlock()
			return
		}
		if left := jeanConsolidateTimer.due.Sub(time.Now().Round(0)); left > 0 {
			jeanConsolidateTimer.t = time.AfterFunc(min(left, jeanConsolidateStep), jeanConsolidateTick(gen))
			jeanReflect.mu.Unlock()
			return
		}
		jeanReflect.mu.Unlock()
		runJeanConsolidate()
	}
}

// jeanConsolidateArmAtStart : au démarrage du serveur. Le minuteur ne vit qu'en
// mémoire et n'était armé que par un échange avec Jean : une machine relancée
// sans lui parler ne consolidait jamais, même si sa mémoire avait changé la
// veille. On l'arme donc ici dès que la mémoire a bougé depuis la dernière
// consolidation (comparaison des dates, qui survit au redémarrage).
func jeanConsolidateArmAtStart() {
	if !jeanReflectEnabled() || !jeanMemChangedSinceConsolidation() {
		return
	}
	now := time.Now()
	d, why := jeanUntilSleepWindow(now), "mémoire modifiée depuis la dernière"
	if jeanSleepWindowMissed(now) {
		// une nuit est passée sans elle (machine éteinte, en veille, mémoire
		// verrouillée à 3 h) : rattrapage, après le démarrage du moteur (#166)
		d, why = 10*time.Minute, "nuit manquée, rattrapage"
	}
	if d == 0 {
		d = 5 * time.Minute // déjà dans la plage : laisser le moteur démarrer
	}
	jeanReflect.mu.Lock()
	jeanConsolidateArmLocked(d)
	jeanReflect.mu.Unlock()
	fmt.Fprintf(os.Stderr, "[jean] consolidation programmée vers %s (%s)\n", now.Add(d).In(loc(defaultTaskTZ())).Format("02/01 15:04"), why)
}

// jeanSleepWindowMissed : une plage de nuit s'est terminée après un changement de
// mémoire sans qu'aucune consolidation n'ait suivi. Sur un poste éteint la nuit, la
// consolidation était sinon reportée à chaque démarrage au 3 h suivant, lui aussi
// manqué : jamais faite (#166). Une machine allumée la nuit a consolidé dans sa
// plage, donc rien ne change pour elle.
func jeanSleepWindowMissed(now time.Time) bool {
	last, changed := jeanMemTimes()
	if !changed.After(last) {
		return false
	}
	n := now.In(loc(defaultTaskTZ()))
	end := time.Date(n.Year(), n.Month(), n.Day(), jeanSleepHour(), 0, 0, 0, n.Location()).Add(jeanSleepSpan * time.Hour)
	if end.After(n) {
		end = end.AddDate(0, 0, -1) // la dernière plage terminée
	}
	return changed.Before(end)
}

// jeanTurnCaps : les réglages du dernier tour Jean, ou ceux d'un tour Jean par
// défaut si aucun n'a eu lieu depuis le démarrage (consolidation armée par
// jeanConsolidateArmAtStart). Sans ça elle serait partie sans aucun outil.
// Appelée sous jeanReflect.mu.
func jeanTurnCapsLocked() (Caps, float64) {
	caps, temp := jeanReflect.caps, jeanReflect.temp
	if !caps.Jean {
		caps = globalCaps()
		caps.Mem = MemOff
		caps.Jean = true
	}
	if temp == 0 {
		temp = 0.7
	}
	return caps, temp
}

// Le sommeil ne démarre que dans sa plage de deux heures, à l'heure de
// l'utilisateur (3 h par défaut, réglable) : en journée il occupait les cartes et
// faisait croire à une utilisation inconnue.
const (
	jeanSleepDefaultHour = 3
	jeanSleepSpan        = 2 // heures
)

// jeanSleepHour : heure de début du sommeil (0-23), réglée par l'utilisateur.
func jeanSleepHour() int {
	v := strings.TrimSpace(getStr(bkState, "jean_sleep_hour"))
	if h, err := strconv.Atoi(v); err == nil && h >= 0 && h < 24 {
		return h
	}
	return jeanSleepDefaultHour
}

// jeanSleepForce : sommeil demandé à la main (bouton) ; passe outre la plage
// horaire et l'absence de changement en mémoire, une seule fois.
var jeanSleepForce atomic.Bool

// jeanSleeping : consolidation en cours ; l'interface montre Jean endormi.
var jeanSleeping atomic.Bool

// jeanInSleepWindow : l'heure tombe dans la plage (qui peut passer minuit).
func jeanInSleepWindow(h int) bool { return (h-jeanSleepHour()+24)%24 < jeanSleepSpan }

// jeanUntilSleepWindow : 0 si on est dans la plage du sommeil, sinon le délai
// jusqu'à sa prochaine ouverture.
func jeanUntilSleepWindow(now time.Time) time.Duration {
	now = now.In(loc(defaultTaskTZ()))
	if jeanInSleepWindow(now.Hour()) {
		return 0
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), jeanSleepHour(), 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(now)
}

func runJeanConsolidate() {
	forced := jeanSleepForce.Load()
	if !forced && !jeanMemDirty.Load() && !jeanMemChangedSinceConsolidation() {
		return
	}
	if !forced && os.Getenv("AJEAN_JEAN_CONSOLIDATE_DELAY") == "" {
		if d := jeanUntilSleepWindow(time.Now()); d > 0 && !jeanSleepWindowMissed(time.Now()) {
			jeanReflect.mu.Lock()
			jeanConsolidateArmLocked(d)
			jeanReflect.mu.Unlock()
			fmt.Fprintf(os.Stderr, "[jean] consolidation reportée à la plage de nuit (%s)\n", time.Now().Add(d).In(loc(defaultTaskTZ())).Format("02/01 15:04"))
			return
		}
	}
	// Occupé ou mémoire verrouillée : on repasse plus tard SANS rien basculer. La
	// bascule vers le modèle du sommeil change le modèle chargé ; faite avant ce
	// contrôle, elle coupait une réponse en cours pour rien (#162).
	busy := conv.isGenerating()
	jeanReflect.mu.Lock()
	if busy || jeanReflect.cancel != nil {
		jeanConsolidateArmLocked(10 * time.Minute)
		jeanReflect.mu.Unlock()
		return
	}
	jeanReflect.mu.Unlock()
	if memEncActive() && !memUnlocked() {
		// Après un démarrage à froid, la clé n'arrive qu'au déverrouillage (qui
		// reprogramme aussi) : on réessaie, au lieu d'abandonner jusqu'au prochain
		// échange avec Jean (#159).
		if !jeanLockedLogged.Swap(true) {
			fmt.Fprintf(os.Stderr, "[jean] consolidation en attente : mémoire verrouillée\n")
		}
		jeanReflect.mu.Lock()
		jeanConsolidateArmLocked(30 * time.Minute)
		jeanReflect.mu.Unlock()
		return
	}
	jeanLockedLogged.Store(false)

	// Une révision encore due passe d'abord : elle voit le fil, pas nous.
	jeanReflect.mu.Lock()
	pending := jeanReflect.pending + jeanReflect.orphanN
	jeanReflect.mu.Unlock()
	if pending > 0 {
		runJeanReflect()
	}

	// Preset du sommeil : la nuit tourne sur le modèle choisi pour ça, pas sur
	// celui laissé actif la veille (un petit modèle rapide raserait la mémoire).
	// On revient ensuite au preset de la veille, sauf si quelqu'un a changé entre-temps.
	if id := jeanSleepPreset(); id != "" {
		if prev := activePresetID(); prev != id {
			if err := ensurePreset(id, 15*time.Minute); err != nil {
				fmt.Fprintf(os.Stderr, "[jean] consolidation reportée, preset du sommeil : %v\n", err)
				jeanReflect.mu.Lock()
				jeanConsolidateArmLocked(time.Hour)
				jeanReflect.mu.Unlock()
				return
			}
			defer func() {
				if prev != "" && activePresetID() == id {
					if err := ensurePreset(prev, 15*time.Minute); err != nil {
						fmt.Fprintf(os.Stderr, "[jean] retour au preset %s après le sommeil : %v\n", prev, err)
					}
				}
			}()
		}
	}

	// Ordre des verrous : c.mu jamais sous jeanReflect.mu (voir runJeanReflect).
	// (Revu après la bascule : une conversation a pu démarrer pendant le chargement.)
	c := conv
	c.mu.Lock()
	busy = c.Generating
	c.mu.Unlock()
	jeanReflect.mu.Lock()
	if busy || jeanReflect.cancel != nil || !healthCheck() {
		jeanConsolidateArmLocked(10 * time.Minute)
		jeanReflect.mu.Unlock()
		return
	}
	caps, temp := jeanTurnCapsLocked()
	ctx, cancel := context.WithTimeout(context.Background(), jeanSleepTimeout())
	ctx = context.WithValue(ctx, jeanReflectKey{}, true)
	jeanReflect.cancel = cancel
	jeanReflect.mu.Unlock()
	defer cancel()
	defer jeanSleepForce.Store(false) // sommeil manuel : servi
	jeanSleeping.Store(true)
	defer jeanSleeping.Store(false)

	sum, rerr := jeanConsolidateRun(ctx, cancel, caps, temp)
	jeanReflect.mu.Lock()
	jeanReflect.cancel = nil
	if rerr != nil {
		if rerr == errJeanSleepRetry {
			jeanConsolidateArmLocked(30 * time.Minute) // à retenter, pas perdue (#159)
		} else {
			jeanMemDirty.Store(true) // à refaire
			jeanConsolidateArmLocked(jeanConsolidateDelay())
		}
		jeanReflect.mu.Unlock()
		fmt.Fprintf(os.Stderr, "[jean] consolidation interrompue : %v\n", rerr)
		return
	}
	jeanReflect.mu.Unlock()
	fmt.Fprintf(os.Stderr, "[jean] consolidation : %s\n", sum)
}

var errJeanSleepRetry = fmt.Errorf("mémoire illisible, à retenter")

// jeanSleepGroups : groupes de fiches donnés en entier au dernier dump (v2).
var jeanSleepGroups int

// jeanConsolidateRun : le sommeil lui-même, sans planification ni bascule de
// preset (partagé avec le banc d'essai « ajean jean-sleep-test »). Renvoie le
// bilan lisible.
func jeanConsolidateRun(ctx context.Context, cancel context.CancelFunc, caps Caps, temp float64) (string, error) {
	start := time.Now()
	jeanSleepGroups = 0
	ctx = context.WithValue(ctx, jeanSleepCtxKey{}, true)
	before := jeanSnapMemory()
	dump, err := jeanMemoryDump()
	if err != nil {
		return "", errJeanSleepRetry
	}
	jeanMemDirty.Store(false) // ce que la consolidation écrit la re-salira : normal, on ne relance pas pour ça
	prompt, maxIter, ver := jeanConsolidatePrompt(), jeanConsolidateMaxIter, "v1"
	if !jeanSleepV1 {
		prompt, ver = jeanSleepV2Prompt(), "v2"
		if jeanSleepGroups > 0 {
			maxIter = jeanSleepV2MaxIter
		}
	}
	msgs := []Message{{Role: "user", Content: prompt + "\n\n" + dump}}
	if sp := readSysPrompt(); sp != "" {
		msgs = append([]Message{{Role: "system", Content: sp}}, msgs...)
	}
	r := jeanSilentRun(ctx, cancel, InjectSkills(msgs, caps), caps, temp, maxIter, "consolidation")
	aborted := ctx.Err() == context.Canceled && r.iter < maxIter
	if ver == "v2" && errors.Is(ctx.Err(), context.Canceled) && r.iter < maxIter { // plafond d'actions ou délai : fin normale, le travail reste
		// Tout ou rien : un sommeil coupé ne laisse pas une mémoire à moitié rangée.
		if err := jeanRollback(before); err == nil && len(r.used) > 0 {
			jeanMu.Lock()
			_ = jeanAppendLocked("consolidation", "Sommeil interrompu : ses changements ont été annulés ("+strings.Join(r.used, " · ")+")")
			jeanMu.Unlock()
		}
		jeanMemDirty.Store(true)
		jeanSleepSaveAfter(jeanLastSnapID)
		jeanSleepSaveMeta(jeanLastSnapID, "sommeil", "interrompu", fmt.Sprintf("Sommeil interrompu, ses %d changement(s) ont été annulés", len(r.used)))
		return "", fmt.Errorf("interrompu (%d écriture(s) annulée(s))", len(r.used))
	}
	// Même interrompue (v1), une consolidation a pu écrire : le garde-fou passe toujours.
	if rep := jeanGuardReport(jeanGuardCheck(before)); rep != "" {
		jeanMu.Lock()
		_ = jeanAppendLocked("consolidation", rep)
		jeanMu.Unlock()
		fmt.Fprintf(os.Stderr, "[jean] %s\n", rep)
	}
	jeanMemDirty.Store(false)
	jeanMarkConsolidated()
	if aborted || (r.err != nil && r.iter == 0) {
		jeanMemDirty.Store(true)
		return "", fmt.Errorf("interrompu")
	}
	stats := fmt.Sprintf("%s · %d écriture(s) · %d action(s) · %d groupe(s) · prompt %d tokens · %s", ver, len(r.used), r.iter, jeanSleepGroups, r.totalToks, time.Since(start).Round(time.Second))
	sum := "rien à changer"
	if len(r.used) > 0 {
		sum = strings.Join(r.used, " · ")
		if r.said != "" && !strings.EqualFold(r.said, "ok") {
			sum = clipRunes(r.said, 400) + " (" + sum + ")"
		}
	}
	if len(r.used) > 0 || ver == "v2" {
		jeanMu.Lock()
		_ = jeanAppendLocked("consolidation", "Mémoire consolidée : "+sum)
		jeanMu.Unlock()
	}
	jeanSleepSaveAfter(jeanLastSnapID)
	jeanSleepSaveMeta(jeanLastSnapID, "sommeil", "ok", sum)
	return stats + " · " + sum, nil
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
	userLoc := loc(defaultTaskTZ())
	usage := jeanUsageLocked()
	if !jeanSleepV1 {
		jeanSleepGroups = jeanSleepV2Fiches(&b, fs, usage)
		fs = nil // la v2 a écrit la partie fiches
	} else {
		b.WriteString("\n## FICHES (most recently changed first)\n")
	}
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
	// Les erreurs qui reviennent (jean_consolidate_brief.go).
	b.WriteString(jeanBriefSections())
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
9. ERRORS MET AGAIN AND AGAIN (if listed): make sure a lesson, or the fiche of that task, says what works, so the error is not met again.
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
	last, changed := jeanMemTimes()
	return changed.After(last)
}

// jeanMemTimes : la date de la dernière consolidation et celle du dernier
// changement du profil, des leçons ou d'une fiche.
func jeanMemTimes() (last, changed time.Time) {
	if st, err := os.Stat(filepath.Join(jeanDir(), jeanConsolidatedMark)); err == nil {
		last = st.ModTime()
	}
	ents, _ := os.ReadDir(jeanDir())
	for _, e := range ents {
		n := e.Name()
		if n != jeanProfileFile && n != jeanLessonsFile && !(strings.HasPrefix(n, jeanFichePrefix) && strings.HasSuffix(n, ".md")) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(changed) {
			changed = info.ModTime()
		}
	}
	return last, changed
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

// jeanSleepPreset : id du preset qui fait la consolidation ("" = preset actif).
// En base et non dans la config : une bascule de preset réécrit la config.
func jeanSleepPreset() string { return getStr(bkState, "jean_sleep_preset") }

// GET : preset du sommeil + presets disponibles. POST {preset} : le règle.
func handleJeanSleep(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var b struct {
			Preset *string `json:"preset"`
			Hour   *int    `json:"hour"`
			Now    bool    `json:"now"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Preset != nil {
			if err := putStr(bkState, "jean_sleep_preset", strings.TrimSpace(*b.Preset)); err != nil {
				sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		}
		if b.Hour != nil {
			if *b.Hour < 0 || *b.Hour > 23 {
				sendJSON(w, 400, map[string]any{"ok": false, "error": "heure invalide"})
				return
			}
			if err := putStr(bkState, "jean_sleep_hour", strconv.Itoa(*b.Hour)); err != nil {
				sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			jeanConsolidateArmAtStart() // reprogramme sur la nouvelle plage
		}
		if b.Now {
			jeanReflect.mu.Lock()
			running := jeanReflect.cancel != nil
			if !running {
				jeanSleepForce.Store(true)
				jeanConsolidateArmLocked(time.Second)
			}
			jeanReflect.mu.Unlock()
			if running {
				sendJSON(w, 409, map[string]any{"ok": false, "error": "AJEAN est déjà en train de réfléchir ou de dormir"})
				return
			}
			fmt.Fprintf(os.Stderr, "[jean] sommeil lancé à la main\n")
		}
		sendJSON(w, 200, map[string]any{"ok": true, "hour": jeanSleepHour()})
		return
	}
	ps := modelChoices()
	jeanReflect.mu.Lock()
	sleeping := jeanReflect.cancel != nil && jeanSleepForce.Load()
	jeanReflect.mu.Unlock()
	sendJSON(w, 200, map[string]any{"ok": true, "preset": jeanSleepPreset(), "presets": ps, "hour": jeanSleepHour(), "sleeping": sleeping || jeanSleepForce.Load()})
}
