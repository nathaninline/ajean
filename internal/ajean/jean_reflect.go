package ajean

// jean_reflect.go — la RÉVISION de Jean : la boucle d'apprentissage, façon
// « background review » d'Hermes Agent, pensée pour un modèle local.
//
// Pendant un échange, un petit modèle pense rarement à noter ce qu'il apprend :
// il est occupé à répondre. Plutôt que de compter sur lui, on repasse APRÈS,
// quand l'utilisateur ne parle plus : un tour silencieux où la seule consigne
// est « qu'est-ce qui mérite d'être retenu ? » (profil, leçons, fiches).
//
// Pourquoi c'est presque gratuit en local : la révision est envoyée comme la
// SUITE de la conversation de Jean, avec exactement le même préfixe (prompt
// système, outils, historique). llama.cpp reprend son cache : seul le court
// message de révision est calculé, pas tout le fil. Et le prochain vrai message
// de l'utilisateur démarre au même endroit que la révision : point de reprise
// du cache déjà posé, rien à recalculer non plus (cf. hybrid-cache).
//
// Elle n'occupe pas le gate de génération : un message de l'utilisateur ou une
// tâche l'annule sur le champ (jeanReflectAbort), et elle sera retentée plus tard.
// Le modèle n'y a droit qu'aux outils de mémoire de Jean (isJeanReflecting).
// Rien n'est ajouté à la conversation : seuls la mémoire et le journal bougent.
//
// JEAN_REFLECT=off dans config.env la désactive.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	jeanReflectBatch   = 3 // échanges en attente pour passer au délai court
	jeanReflectMaxIter = 8 // appels d'outils max d'une révision
)

// Délais d'inactivité avant la révision : court quand plusieurs échanges
// attendent, long pour un ou deux. AJEAN_JEAN_REFLECT_DELAY (secondes) les
// écrase tous deux : sert aux essais, pas à l'usage.
var jeanReflectSoon, jeanReflectLater = func() (time.Duration, time.Duration) {
	if n, err := strconv.Atoi(os.Getenv("AJEAN_JEAN_REFLECT_DELAY")); err == nil && n > 0 {
		return time.Duration(n) * time.Second, time.Duration(n) * time.Second
	}
	return 2 * time.Minute, 15 * time.Minute
}()

// jeanReflectUrgent : délai après une correction de l'utilisateur. Court, mais
// pas immédiat : il enchaîne souvent un 2ᵉ message qui précise.
var jeanReflectUrgent = func() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("AJEAN_JEAN_REFLECT_DELAY")); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 45 * time.Second
}()

type jeanReflectKey struct{}

// isJeanReflecting : le tour en cours est une révision (outils restreints).
func isJeanReflecting(ctx context.Context) bool {
	v, _ := ctx.Value(jeanReflectKey{}).(bool)
	return v
}

// jeanReflectAllowed : les seuls outils d'une révision (mémoire de Jean).
func jeanReflectAllowed(name string) bool {
	switch name {
	case "jean_search", "jean_remember", "jean_forget", "jean_note", "jean_save", "jean_read", "jean_lesson", "jean_patch", "jean_rule":
		return true
	}
	return false
}

var jeanReflect struct {
	mu      sync.Mutex
	pending int // échanges pas encore révisés
	caps    Caps
	temp    float64
	timer   *time.Timer
	cancel  context.CancelFunc // révision en cours
	corr    int                // pire correction en attente (jeanCorr*)
	corrMsg []string           // messages de correction de l'utilisateur, à citer
	// Fiches ouvertes au tour précédent : si le message suivant corrige Jean,
	// elles sont suspectes.
	lastFiches []string
	// Fil vidé (bouton ou inactivité) avant sa révision : gardé ici pour être
	// révisé quand même. orphanN = nombre d'échanges qu'il contient.
	orphan  []Message
	orphanN int
	// Appels d'outils du plus gros tour non révisé : une tâche qui a demandé de
	// chercher mérite une fiche, tout de suite.
	work int
}

func jeanReflectEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(ReadConfig()["JEAN_REFLECT"])) {
	case "off", "false", "0", "no", "non":
		return false
	}
	return true
}

// jeanReflectNoteTurn : fin d'un tour Jean réussi. (Re)programme la révision ;
// chaque nouveau tour repousse l'échéance : on attend que la conversation se pose.
func jeanReflectNoteTurn(caps Caps, temp float64, userText string, violations []string, fichesRead []string, toolCalls int) {
	if !jeanReflectEnabled() {
		return
	}
	jeanReflect.mu.Lock()
	defer jeanReflect.mu.Unlock()
	jeanReflect.pending++
	jeanReflect.caps, jeanReflect.temp = caps, temp
	// Règle appliquée enfreinte (détectée par le harnais, pas par l'utilisateur) :
	// traitée comme une erreur répétée, la règle existait déjà.
	for _, v := range violations {
		jeanReflect.corr = jeanCorrRepeat
		jeanReflect.corrMsg = append(jeanReflect.corrMsg, "[harness check] "+v)
	}
	k := jeanCorrectionKind(userText)
	// Correction juste après avoir suivi une fiche : c'est la fiche qui a mené à
	// l'erreur. On le compte et la révision doit la réparer, elle précisément.
	if k != jeanCorrNone && len(jeanReflect.lastFiches) > 0 {
		jeanNoteFicheCorrected(jeanReflect.lastFiches)
		jeanReflect.corrMsg = append(jeanReflect.corrMsg, "[harness check] This correction came right after you followed fiche(s) "+strings.Join(jeanReflect.lastFiches, ", ")+": fix that fiche (jean_patch) so that following it gives what the user wanted.")
	}
	jeanReflect.lastFiches = fichesRead
	jeanReflect.work = max(jeanReflect.work, toolCalls)
	if k != jeanCorrNone {
		jeanReflect.corr = max(jeanReflect.corr, k)
		jeanReflect.corrMsg = append(jeanReflect.corrMsg, clipRunes(jeanStripNow(userText), 400))
		if len(jeanReflect.corrMsg) > 5 {
			jeanReflect.corrMsg = jeanReflect.corrMsg[len(jeanReflect.corrMsg)-5:]
		}
	}
	jeanConsolidateArmLocked(jeanConsolidateDelay())
	jeanReflectArmLocked()
}

func jeanReflectArmLocked() {
	d := jeanReflectLater
	if jeanReflect.pending >= jeanReflectBatch {
		d = jeanReflectSoon
	}
	if jeanReflect.orphan != nil && jeanReflectSoon < d {
		d = jeanReflectSoon // fil vidé en attente : ne pas le laisser traîner
	}
	if (jeanReflect.corr != jeanCorrNone || jeanReflect.work >= jeanWorkCalls) && jeanReflectUrgent < d {
		d = jeanReflectUrgent // une correction n'attend pas
	}
	if jeanReflect.timer != nil {
		jeanReflect.timer.Stop()
	}
	jeanReflect.timer = time.AfterFunc(d, runJeanReflect)
}

// jeanReflectAbort : un tour (utilisateur ou tâche) démarre. On annule une
// révision en cours (elle libère le moteur) et on repousse celle programmée :
// repousser, pas oublier. Une tâche de fond qui démarre ne doit pas faire perdre
// la révision des échanges d'avant (le timer, en tombant pendant la tâche, verra
// le moteur occupé et se reprogrammera).
func jeanReflectAbort() {
	jeanReflect.mu.Lock()
	defer jeanReflect.mu.Unlock()
	if jeanReflect.cancel != nil {
		jeanReflect.cancel()
		jeanReflect.cancel = nil
	}
	if jeanReflect.pending > 0 || jeanReflect.orphan != nil {
		jeanReflectArmLocked()
	}
}

func runJeanReflect() {
	c := conv
	// Ordre des verrous : JAMAIS c.mu sous jeanReflect.mu. StartTurn et
	// RunAutonomous appellent jeanReflectAbort en tenant c.mu ; prendre ici
	// c.mu en tenant jeanReflect.mu pouvait bloquer les deux pour toujours.
	c.mu.Lock()
	busy, isJean := c.Generating, c.ID == jeanConvID
	msgs := append([]Message(nil), c.Messages...)
	ctxUsed := c.CtxUsed
	c.mu.Unlock()
	jeanReflect.mu.Lock()
	pending, caps, temp := jeanReflect.pending, jeanReflect.caps, jeanReflect.temp
	corr, corrMsg := jeanReflect.corr, append([]string(nil), jeanReflect.corrMsg...)
	work := jeanReflect.work
	orphan := jeanReflect.orphan != nil
	if orphan {
		// Contexte vidé avant la révision : on révise l'ancien fil, gardé à part.
		msgs, pending, isJean, ctxUsed = jeanReflect.orphan, jeanReflect.orphanN, true, 0
	}
	if pending == 0 || jeanReflect.cancel != nil {
		jeanReflect.mu.Unlock()
		return
	}
	switch {
	case !isJean || len(msgs) == 0:
		// Conversation Jean plus ouverte (ou contexte vidé) : le fil n'est plus là,
		// le journal le garde. On n'y revient pas.
		jeanReflect.pending = 0
		jeanReflect.mu.Unlock()
		return
	case busy || !healthCheck() || compactWouldTrigger(msgs, ctxUsed):
		jeanReflectArmLocked() // plus tard
		jeanReflect.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	ctx = context.WithValue(ctx, jeanReflectKey{}, true)
	jeanReflect.cancel = cancel
	jeanReflect.mu.Unlock()
	defer cancel()

	// Même vue modèle que generate (prompt système + skills) : c'est ce qui garde
	// le préfixe identique, donc le cache llama.cpp.
	final := msgs
	if sp := readSysPrompt(); sp != "" {
		final = append([]Message{{Role: "system", Content: sp}}, msgs...)
	}
	// Contexte vidé entre-temps (inactivité, « vider ») : on ne parle que des
	// échanges encore sous ses yeux, les autres sont dans le journal.
	shown, users := pending, 0
	for _, m := range msgs {
		if m.Role == "user" {
			users++
		}
	}
	if shown > users {
		shown = users
	}
	final = append(final, Message{Role: "user", Content: jeanWorkBrief(work) + jeanReflectPrompt(shown, corr, corrMsg)})

	r := jeanSilentRun(ctx, cancel, InjectSkills(final, caps), caps, temp, jeanReflectMaxIter, "révision")
	used, iter, err := r.used, r.iter, r.err

	jeanReflect.mu.Lock()
	aborted := ctx.Err() == context.Canceled && iter < jeanReflectMaxIter
	if jeanReflect.cancel != nil {
		jeanReflect.cancel = nil
	}
	if aborted || (err != nil && iter == 0) {
		// Interrompue par l'utilisateur ou moteur indisponible : les échanges
		// restent à réviser, la prochaine fin de tour reprogramme.
		jeanReflect.mu.Unlock()
		return
	}
	if orphan {
		jeanReflect.orphan, jeanReflect.orphanN = nil, 0
		if jeanReflect.pending > 0 {
			jeanReflectArmLocked() // le nouveau fil a aussi des échanges à réviser
		}
	} else {
		jeanReflect.pending -= pending
	}
	jeanReflect.work = 0
	if len(jeanReflect.corrMsg) <= len(corrMsg) {
		jeanReflect.corr, jeanReflect.corrMsg = jeanCorrNone, nil
	} else { // corrections arrivées pendant la révision : gardées pour la suivante
		jeanReflect.corrMsg = jeanReflect.corrMsg[len(corrMsg):]
	}
	if jeanReflect.pending < 0 {
		jeanReflect.pending = 0
	}
	jeanReflect.mu.Unlock()

	if len(used) > 0 {
		jeanMu.Lock()
		_ = jeanAppendLocked("revision", "Révision de la mémoire ("+fmt.Sprint(pending)+" échanges) : "+strings.Join(used, " · "))
		jeanMu.Unlock()
	}
	fmt.Fprintf(os.Stderr, "[jean] révision : %d échange(s), %d écriture(s), prompt %d tokens dont %d calculés\n", pending, len(used), r.totalToks, r.newToks)
}

// jeanSilent : bilan d'un tour silencieux (révision ou consolidation).
type jeanSilent struct {
	used               []string // écritures réussies (outil + libellé)
	said               string   // réponse finale du modèle
	iter               int      // appels d'outils
	newToks, totalToks int      // tokens calculés hors cache / taille du prompt
	err                error
	notes              []string // à annoncer à Jean au prochain message (jean_notes.go)
}

// jeanSilentRun joue un tour invisible : outils limités à la mémoire de Jean
// (ctx porte jeanReflectKey), rien n'est publié dans la conversation. cancel
// coupe le tour au-delà de maxIter appels : un petit modèle qui s'emballe ne
// monopolise pas le moteur.
func jeanSilentRun(ctx context.Context, cancel context.CancelFunc, msgs []Message, caps Caps, temp float64, maxIter int, tag string) jeanSilent {
	var r jeanSilent
	var said strings.Builder
	debug := os.Getenv("AJEAN_JEAN_REFLECT_DEBUG") != ""
	_, r.err = runChat(ctx, msgs, temp, caps, func(ev StreamEvent) bool {
		if ev.Stats != nil {
			// Tokens réellement calculés (hors cache) : quelques centaines si le
			// cache du fil a été repris, tout le fil sinon.
			r.newToks += ev.Stats.PromptTokens
			if r.totalToks == 0 {
				r.totalToks = ev.Stats.PromptTokensTotal
			}
		}
		if ev.ToolUsed != nil {
			said.Reset() // seul compte le texte après le dernier outil
		}
		if ev.Content != "" {
			said.WriteString(ev.Content)
		}
		if ev.ToolUsed != nil && ev.ToolUsed.Done {
			r.iter++
			if debug {
				fmt.Fprintf(os.Stderr, "[jean] %s → %s %s : %s\n", tag, ev.ToolUsed.Name, ev.ToolUsed.Label, clipRunes(ev.ToolUsed.Result, 200))
			}
			if (jeanReflectAllowed(ev.ToolUsed.Name) || ev.ToolUsed.Name == "write" || ev.ToolUsed.Name == "edit") && ev.ToolUsed.Name != "jean_search" && ev.ToolUsed.Name != "jean_read" &&
				strings.HasPrefix(ev.ToolUsed.Result, "[ok]") {
				r.used = append(r.used, ev.ToolUsed.Name+" "+ev.ToolUsed.Label)
				if n := jeanNoteFor(ev.ToolUsed.Name, ev.ToolUsed.Label); n != "" {
					r.notes = append(r.notes, n)
				}
			}
			if r.iter >= maxIter {
				cancel()
			}
		}
		return true
	}, nil)
	r.said = strings.TrimSpace(said.String())
	jeanQueueNotes(r.notes)
	if debug && r.said != "" {
		fmt.Fprintf(os.Stderr, "[jean] %s, réponse : %s\n", tag, clipRunes(r.said, 300))
	}
	return r
}

// jeanReflectPrompt : la consigne de révision. Courte et concrète : un petit
// modèle doit savoir quoi chercher, et surtout qu'il est normal de ne rien noter.
func jeanReflectPrompt(n, corr int, corrMsg []string) string {
	return jeanCorrectionBrief(corr, corrMsg) + fmt.Sprintf(`[MEMORY REVIEW. Silent: the user will not see this message nor your answer.]
Look back at the last %d exchange(s) of this conversation and save only what will still matter in a month:
1. A lasting fact about the user that is new or changed (person, date, preference, habit, project) and not already in your profile → jean_remember.
2. A lesson: the user corrected you, a method failed before the one that worked, or you hit a quirk worth knowing → jean_lesson (with fiche=<name> if it is about a fiche).
3. A procedure: steps you worked out, or how the user wants a recurring task done (where, format, rules) → jean_save a fiche (steps + pitfalls), not a lesson. A fiche that turned out wrong or incomplete → jean_patch it.
4. A list the user keeps updating (todo, shopping list…) that lives in journal notes or nowhere durable → put its CURRENT state in one fiche (jean_save), to be updated in place from now on.
Write only what was actually said or shown, never a guess or a generalization (a sister you will visit on Saturday is not "visits her often").
Never save your own interpretation as a fact (a possible typo is not a nickname). When the user and a tool result disagree, note both as they are, never a rule that ignores the error.
A pitfall goes in the fiche of the task it is about; skip it if a fiche or lesson already says it. No password, key or token in the profile: those go in the service's fiche.
Do NOT save: small talk, one-off details, what is already in your profile, lessons or fiches (the journal already keeps every exchange). Most of the time there is nothing or one thing to save: that is fine.
Do not answer the user. When done, reply with just: OK`, n)
}

const jeanTaskReviewMaxIter = 5

// jeanTaskWorthReview : la tâche a-t-elle de quoi apprendre ? Au moins un outil
// en erreur, ou un vrai travail (3 appels et plus). Une tâche qui lit une valeur
// et la rapporte n'a rien à enseigner : pas de tour en plus.
func jeanTaskWorthReview(extra []Message) bool {
	calls := 0
	for _, m := range extra {
		calls += len(m.ToolCalls)
		if m.Role == "tool" {
			t := msgText(m)
			if strings.HasPrefix(t, "[erreur]") || strings.Contains(t, "exit: 1") || strings.Contains(t, "Traceback") {
				return true
			}
		}
	}
	return calls >= 3
}

// jeanReviewTask : révision d'une tâche de Jean, à la suite de son propre fil.
func jeanReviewTask(parent context.Context, base, extra []Message, report string, caps Caps, temp float64) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	ctx = context.WithValue(ctx, jeanReflectKey{}, true)
	msgs := append(append([]Message(nil), base...), extra...)
	if report != "" {
		msgs = append(msgs, Message{Role: "assistant", Content: report})
	}
	msgs = append(msgs, Message{Role: "user", Content: jeanTaskReviewPrompt})
	r := jeanSilentRun(ctx, cancel, msgs, caps, temp, jeanTaskReviewMaxIter, "révision de tâche")
	if len(r.used) > 0 {
		jeanMu.Lock()
		_ = jeanAppendLocked("revision", "Révision après tâche : "+strings.Join(r.used, " · "))
		jeanMu.Unlock()
	}
	fmt.Fprintf(os.Stderr, "[jean] révision de tâche : %d écriture(s), prompt %d tokens dont %d calculés\n", len(r.used), r.totalToks, r.newToks)
}

const jeanTaskReviewPrompt = `[TASK REVIEW. Silent: nobody reads this.]
Look at the task you just ran. Did something fail before you found what works, or did you discover a better or required way to do it?
- If this task follows a fiche → fix it with jean_patch, or add the pitfall with jean_lesson fiche=<name>.
- A reusable procedure you had to work out → jean_save a fiche (steps + pitfalls).
- A general rule worth remembering → jean_lesson.
Only what will make the next run faster or safer. Nothing to learn is the usual case. When done, reply with just: OK`

// jeanWorkCalls : à partir de ce nombre d'appels d'outils dans un tour, Jean a
// dû chercher son chemin. La révision part vite et doit en faire une fiche :
// la prochaine fois, la même demande se fait en deux appels au lieu de dix.
const jeanWorkCalls = 5

func jeanWorkBrief(calls int) string {
	if calls < jeanWorkCalls {
		return ""
	}
	return fmt.Sprintf("[PRIORITY] One of these requests took you %d tool calls: you had to find your way. "+
		"If the user may ask it again (or something similar), save the DIRECT path as a fiche now (jean_save, or jean_patch the existing fiche): "+
		"the exact steps, commands, URLs, file names and values that worked, in order, without the dead ends, plus a pitfalls section for what failed. Never store results that change (views, prices, counts): only how to get them. "+
		"Name it after the request (e.g. youtube-stats), with a 'when' line matching how the user asks. "+
		"If a script did the job, it must live in your SCRIPTS folder (durable; the workspace can be wiped): if it is only in the workspace or was a one-off command, copy the exact version that worked (from this conversation, unchanged) into a NEW file of your scripts folder (creating is allowed there during this review; changing an existing script is not, you cannot test it here), then give its exact command in the fiche. If an existing script needs a fix, write that fix in the fiche instead: you will apply and test it next time. "+
		"Next time you must answer it in one or two calls.\n\n", calls)
}

// jeanReflectBeforeClear : le fil de Jean va être vidé (bouton, inactivité).
// S'il reste des échanges à réviser, on garde le fil à part et on lance la
// révision tout de suite : le modèle a encore ce fil en cache, elle coûte peu.
// À appeler SANS tenir c.mu.
func jeanReflectBeforeClear(old []Message) {
	jeanReflect.mu.Lock()
	defer jeanReflect.mu.Unlock()
	if jeanReflect.pending == 0 || len(old) == 0 {
		return
	}
	if jeanReflect.orphan != nil {
		// Déjà un fil en attente : on garde le plus récent, l'ancien est au journal.
		jeanReflect.orphanN = 0
	}
	jeanReflect.orphan = append([]Message(nil), old...)
	jeanReflect.orphanN = jeanReflect.pending
	jeanReflect.pending = 0
	if jeanReflect.timer != nil {
		jeanReflect.timer.Stop()
	}
	jeanReflect.timer = time.AfterFunc(2*time.Second, runJeanReflect)
}

// jeanReflectScriptWrite : pendant une révision, écrire ou retoucher un fichier
// est permis UNIQUEMENT dans le dossier de scripts de Jean. C'est ce qui lui
// permet de ranger durablement un script qui a marché (l'espace de travail peut
// être vidé) ; le shell et tout autre chemin restent interdits.
//
// CRÉER seulement, jamais modifier ni écraser : la révision ne peut pas exécuter
// le script, donc pas le tester. Vu en vrai : elle avait réécrit de mémoire un
// script qui marchait, et la version réécrite plantait. Un script existant ne se
// touche que pendant un vrai tour, où Jean peut le lancer.
func jeanReflectScriptWrite(ctx context.Context, tool string, args map[string]any) bool {
	if tool != "write" {
		return false
	}
	f, _ := args["file"].(string)
	if strings.TrimSpace(f) == "" {
		return false
	}
	// Nom nu (« montage.py ») : pendant une révision, le seul endroit permis est
	// le dossier des scripts, c'est donc forcément là qu'il veut l'écrire. Sans
	// ça, le nom tombait dans le dossier de travail, l'écriture était refusée, et
	// la fiche finissait par noter « script non copié, bash bloqué ».
	if !strings.ContainsAny(f, `/\`) {
		args["file"] = filepath.Join(jeanScriptsDir(), f)
		f = args["file"].(string)
	}
	p := resolveSpacePath(ctx, f)
	rel, err := filepath.Rel(jeanScriptsDir(), p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	_, statErr := os.Stat(p)
	return os.IsNotExist(statErr)
}

// jeanReflectReadFile : pendant une révision, lire un fichier de SES dossiers
// (travail, scripts) avec « type » ou « cat », rien d'autre. Sans ça, elle ne
// pouvait pas garder un script qui venait de marcher : faute de pouvoir le
// relire, la fiche finissait sur « script à recréer si perdu ».
var jeanReflectReadRe = regexp.MustCompile(`^(?:type|cat)\s+"?([^"&|<>;]+?)"?\s*$`)

func jeanReflectReadFile(ctx context.Context, tool string, args map[string]any) bool {
	if tool != "bash" {
		return false
	}
	cmd, _ := args["command"].(string)
	if jeanReflectCopyOK(ctx, strings.TrimSpace(cmd)) {
		return true
	}
	m := jeanReflectReadRe.FindStringSubmatch(strings.TrimSpace(cmd))
	if m == nil {
		return false
	}
	p := resolveSpacePath(ctx, m[1])
	for _, root := range []string{jeanWorkspace(), jeanScriptsDir()} {
		if rel, err := filepath.Rel(root, p); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return true
		}
	}
	return false
}

// Copier un script du dossier de travail vers celui des scripts : c'est le
// geste naturel pour garder un script qui vient de marcher (vu en test), et le
// plus sûr (copie exacte, rien de réécrit de mémoire). Création seulement.
var jeanReflectCopyRe = regexp.MustCompile(`(?i)^(?:copy|cp)(?:\s+/y)?\s+("[^"]+"|[^\s"&|<>;]+)\s+("[^"]+"|[^\s"&|<>;]+)\s*$`)

func jeanReflectCopyOK(ctx context.Context, cmd string) bool {
	m := jeanReflectCopyRe.FindStringSubmatch(cmd)
	if m == nil {
		return false
	}
	src := resolveSpacePath(ctx, strings.Trim(m[1], `"`))
	dst := resolveSpacePath(ctx, strings.Trim(m[2], `"`))
	in := func(root, p string) bool {
		rel, err := filepath.Rel(root, p)
		return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
	}
	if !in(jeanWorkspace(), src) || !in(jeanScriptsDir(), dst) {
		return false
	}
	_, err := os.Stat(dst)
	return os.IsNotExist(err)
}
