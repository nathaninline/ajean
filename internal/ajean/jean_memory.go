package ajean

// jean_memory.go — mémoire du mode « Jean » (assistant personnel qui se souvient
// de tout). Trois niveaux, pour que le contexte reste léger même avec un petit
// modèle local et des années d'historique :
//
//   - PROFIL : profile.md, des lignes `- clé: valeur` TOUJOURS injectées en tête de
//     conversation. Budget dur (jeanProfileMaxChars) : plein = refus d'écrire, l'IA
//     doit remplacer ou oublier une ligne. Écrire une clé existante la REMPLACE
//     (jamais deux versions d'un même fait) ; l'ancienne valeur part au journal.
//   - FICHES : procédures et guides gardés en entier ; seuls leur nom et leur
//     « quand s'en servir » sont injectés. Voir jean_fiches.go.
//   - JOURNAL : journal-AAAA-MM.md, tout ce qui s'est passé (chaque échange y est
//     consigné automatiquement, plus les notes de l'IA). Jamais chargé : on n'y
//     accède que par jean_search, qui ne renvoie que quelques extraits.
//
// Pas d'oubli par l'âge : l'anniversaire d'un proche doit remonter dans deux ans.
// La fraîcheur ne sert qu'à départager deux résultats équivalents. Chaque note
// renvoyée par une recherche est comptée (recalls.md) ; une note souvent rappelée
// sur plusieurs jours est signalée à l'IA comme candidate au profil.
//
// Rangé sous memory/_jean/ en fichiers .md : chiffrement, déchiffrement et
// sauvegarde de la mémoire les prennent donc en charge sans code dédié.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	jeanDirName         = "_jean"
	jeanProfileFile     = "profile.md"
	jeanRecallsFile     = "recalls.md"
	jeanProfileMaxChars = 4000 // ~1000 tokens : le prix fixe du mode, à ne pas gonfler
	jeanValueMaxChars   = 300
	jeanNoteMaxChars    = 2000
	jeanAutoExcerpt     = 400 // longueur gardée de chaque côté d'un échange consigné
	jeanPromoteRecalls  = 3   // rappels (sur jeanPromoteDays jours distincts) avant de suggérer le profil
	jeanPromoteDays     = 2
	jeanContextPrefix   = "Jean memory"
)

var (
	jeanMu     sync.Mutex
	jeanKeyRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,47}$`)
	jeanLineRe = regexp.MustCompile(`^- ([a-z0-9][a-z0-9._-]{0,47}): (.*)$`)
	jeanHeadRe = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2} \d{2}:\d{2}(?: [+-]\d{4})?) · ([a-z0-9]+)(?: · (\w+))?$`)
)

func jeanDir() string { return filepath.Join(projectsRoot(), jeanDirName) }

// jeanRead lit un fichier du dossier Jean (déchiffré si besoin). Absent = vide.
func jeanRead(name string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(jeanDir(), name))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	b, err := decodeMemContent(raw)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// jeanWrite écrit un fichier du dossier Jean (chiffré si la mémoire l'est).
func jeanWrite(name, content string) error {
	out, err := encodeMemContent([]byte(content))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(jeanDir(), 0o755); err != nil {
		return err
	}
	if name == jeanProfileFile || name == jeanLessonsFile || strings.HasPrefix(name, jeanFichePrefix) {
		jeanMemDirty.Store(true) // à consolider (jean_consolidate.go)
	}
	return memWriteFileVerified(filepath.Join(jeanDir(), name), out, 0o600)
}

// ---- le profil ---------------------------------------------------------------

type jeanFact struct{ Key, Value string }

func parseJeanProfile(s string) []jeanFact {
	var out []jeanFact
	for _, l := range strings.Split(s, "\n") {
		if m := jeanLineRe.FindStringSubmatch(strings.TrimRight(l, "\r")); m != nil {
			out = append(out, jeanFact{m[1], m[2]})
		}
	}
	return out
}

func renderJeanProfile(facts []jeanFact) string {
	var b strings.Builder
	b.WriteString("# Profil Jean\n\n")
	for _, f := range facts {
		b.WriteString("- " + f.Key + ": " + f.Value + "\n")
	}
	return b.String()
}

func jeanProfile() ([]jeanFact, error) {
	s, err := jeanRead(jeanProfileFile)
	if err != nil {
		return nil, err
	}
	return parseJeanProfile(s), nil
}

// normJeanKey ramène une clé à sa forme canonique (minuscules, espaces → points).
func normJeanKey(k string) string {
	k = foldSearch(k)
	k = strings.NewReplacer(" ", ".", "/", ".", ":", ".").Replace(k)
	return strings.Trim(k, ".-_")
}

// JeanRemember pose ou REMPLACE un fait du profil. Refuse au-delà du budget.
func JeanRemember(key, value string) (string, error) {
	key = normJeanKey(key)
	if !jeanKeyRe.MatchString(key) {
		return "", fmt.Errorf("clé invalide : courte, en minuscules, ex. « famille.mere.anniversaire »")
	}
	value = strings.Join(strings.Fields(value), " ") // une seule ligne
	if value == "" {
		return "", fmt.Errorf("valeur vide (pour retirer une ligne : jean_forget)")
	}
	if jeanLooksSecret(value) {
		return "", fmt.Errorf("refusé : le profil est relu à chaque message, aucun mot de passe, clé ni jeton dedans. Mets l'identifiant dans la fiche du service (jean_save / jean_patch) et, ici, seulement un renvoi (ex. « identifiants dans la fiche media-server »)")
	}
	if len([]rune(value)) > jeanValueMaxChars {
		return "", fmt.Errorf("valeur trop longue (%d car. max) : garde l'essentiel, le détail va dans jean_note", jeanValueMaxChars)
	}
	jeanMu.Lock()
	defer jeanMu.Unlock()
	facts, err := jeanProfile()
	if err != nil {
		return "", err
	}
	replaced := ""
	found := false
	for i := range facts {
		if facts[i].Key == key {
			replaced, facts[i].Value, found = facts[i].Value, value, true
			break
		}
	}
	if !found {
		facts = append(facts, jeanFact{key, value})
	}
	if out := renderJeanProfile(facts); len(out) > jeanProfileMaxChars {
		keys := make([]string, 0, len(facts))
		for _, f := range facts {
			keys = append(keys, f.Key)
		}
		return "", fmt.Errorf("profil plein (%d car. max). Fusionne ou raccourcis une ligne existante, ou jean_forget une ligne moins utile (elle reste dans le journal). Clés : %s",
			jeanProfileMaxChars, strings.Join(keys, ", "))
	}
	if err := jeanWrite(jeanProfileFile, renderJeanProfile(facts)); err != nil {
		return "", err
	}
	// L'ancienne valeur n'est pas perdue : elle part dans le journal (froid).
	if found && replaced != value {
		_ = jeanAppendLocked("profil", fmt.Sprintf("Profil « %s » remplacé. Avant : %s. Maintenant : %s", key, replaced, value))
		return fmt.Sprintf("[ok] « %s » mis à jour (ancienne valeur archivée dans le journal)", key), nil
	}
	return fmt.Sprintf("[ok] « %s » retenu", key), nil
}

// jeanForgetFact retire une ligne du profil ; sa valeur est archivée dans le journal.
func jeanForgetFact(key string) (string, error) {
	key = normJeanKey(key)
	jeanMu.Lock()
	defer jeanMu.Unlock()
	facts, err := jeanProfile()
	if err != nil {
		return "", err
	}
	for i, f := range facts {
		if f.Key != key {
			continue
		}
		facts = append(facts[:i], facts[i+1:]...)
		if err := jeanWrite(jeanProfileFile, renderJeanProfile(facts)); err != nil {
			return "", err
		}
		_ = jeanAppendLocked("profil", fmt.Sprintf("Retiré du profil « %s » : %s", f.Key, f.Value))
		return fmt.Sprintf("[ok] « %s » retiré du profil (toujours retrouvable par jean_search)", key), nil
	}
	return "", errJeanNoFact
}

var errJeanNoFact = errors.New("clé absente du profil")

// JeanForget (outil jean_forget) : une ligne du profil, à défaut une fiche du même nom.
func JeanForget(key string) (string, error) {
	if out, isLesson, err := jeanForgetLesson(key); isLesson {
		return out, err
	}
	out, err := jeanForgetFact(key)
	if !errors.Is(err, errJeanNoFact) {
		return out, err
	}
	if jeanArchiveFiche(key) == nil {
		return fmt.Sprintf("[ok] fiche « %s » archivée : hors de ta liste, jean_search la retrouve et la relire la ramène", normFicheName(key)), nil
	}
	return "", fmt.Errorf("« %s » n'est ni une clé du profil ni une fiche", key)
}

// jeanContextMessage : le profil et la liste des fiches, injectés UNE FOIS en tête
// de conversation (et après un compactage). Toujours renvoyé, même vide : l'IA
// doit savoir que le profil existe pour penser à le remplir.
func jeanContextMessage() Message {
	facts, err := jeanProfile()
	var b strings.Builder
	b.WriteString(jeanContextPrefix + " — what you know about the user for sure (profile, kept short; edit with jean_remember / jean_forget). Everything else is in your journal: jean_search.\n")
	b.WriteString("Each user message starts with the current date and time [in brackets]: rely on it, never on an older one. Task schedules use that same time zone.\n\n")
	switch {
	case err != nil:
		b.WriteString("(profile unavailable: " + err.Error() + ")")
	case len(facts) == 0:
		b.WriteString("(empty profile: learn about the user and save the lasting facts)")
	default:
		for _, f := range facts {
			b.WriteString("- " + f.Key + ": " + f.Value + "\n")
		}
	}
	if err == nil {
		b.WriteString(jeanLessonsBlock())
		b.WriteString(jeanRulesBlock())
		b.WriteString(jeanFicheIndex())
	}
	return Message{Role: "system", Content: strings.TrimRight(b.String(), "\n")}
}

func hasJeanContext(msgs []Message) bool {
	for _, m := range msgs {
		if s, ok := m.Content.(string); ok && m.Role == "system" && strings.HasPrefix(s, jeanContextPrefix) {
			return true
		}
	}
	return false
}

func ensureJeanContextFront(msgs []Message) []Message {
	if hasJeanContext(msgs) {
		return msgs
	}
	return append([]Message{jeanContextMessage()}, msgs...)
}

// ---- le journal --------------------------------------------------------------

type jeanEntry struct {
	ID   string
	When time.Time
	Kind string // echange / note / profil / fiche / message
	Text string
}

func jeanJournalFile(t time.Time) string { return "journal-" + t.Format("2006-01") + ".md" }

// JeanNote ajoute une note au journal (appelée par l'outil jean_note).
func JeanNote(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("note vide")
	}
	if n := len([]rune(text)); n > jeanNoteMaxChars {
		return "", fmt.Errorf("note trop longue (%d car., max %d) : une procédure ou un long texte à garder va dans une fiche (jean_save)", n, jeanNoteMaxChars)
	}
	jeanMu.Lock()
	defer jeanMu.Unlock()
	if err := jeanAppendLocked("note", text); err != nil {
		return "", err
	}
	return "[ok] noté dans le journal", nil
}

// jeanLogExchange consigne un échange (fin de tour) : c'est ce qui permet à Jean
// de « se souvenir de tout » sans rien coûter au modèle. Extraits bornés.
func jeanLogExchange(user, answer string) {
	// L'entrée est déjà datée : le préfixe « [date heure (fuseau)] » ajouté au
	// message pour le modèle (voir jeanNow) ferait doublon.
	user = jeanCleanUserText(user) // ni la date, ni la note d'arrière-plan, ni celle des pièces jointes
	user, answer = clipRunes(strings.TrimSpace(user), jeanAutoExcerpt), clipRunes(strings.TrimSpace(answer), jeanAutoExcerpt)
	if user == "" {
		return
	}
	text := "Utilisateur : " + user
	if answer != "" {
		text += "\nJean : " + answer
	}
	jeanMu.Lock()
	defer jeanMu.Unlock()
	if err := jeanAppendLocked("echange", text); err != nil {
		fmt.Fprintf(os.Stderr, "[jean] journal non écrit : %v\n", err)
	}
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func jeanAppendLocked(kind, text string) error {
	now := time.Now()
	name := jeanJournalFile(now)
	cur, err := jeanRead(name)
	if err != nil {
		return err
	}
	if cur == "" {
		cur = "# Journal " + now.Format("2006-01") + "\n"
	}
	id := fmt.Sprintf("%x", now.UnixNano()/1e6)
	// Heure de l'utilisateur, décalage explicite : sans lui l'heure du serveur
	// (UTC) passait pour l'heure française (« vers 13h30 » au lieu de 15h30).
	cur += "\n## " + now.In(loc(defaultTaskTZ())).Format("2006-01-02 15:04 -0700") + " · " + id + " · " + kind + "\n" + text + "\n"
	return jeanWrite(name, cur)
}

func parseJeanJournal(s string) []jeanEntry {
	var out []jeanEntry
	var cur *jeanEntry
	var body []string
	flush := func() {
		if cur != nil {
			cur.Text = strings.TrimSpace(strings.Join(body, "\n"))
			out = append(out, *cur)
		}
	}
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, "\r")
		if m := jeanHeadRe.FindStringSubmatch(l); m != nil {
			flush()
			// Anciennes entrées sans décalage : écrites à l'heure du serveur.
			t, err := time.Parse("2006-01-02 15:04 -0700", m[1])
			if err != nil {
				t, _ = time.ParseInLocation("2006-01-02 15:04", m[1], time.Local)
			}
			cur, body = &jeanEntry{ID: m[2], When: t, Kind: m[3]}, nil
			continue
		}
		if cur != nil {
			body = append(body, l)
		}
	}
	flush()
	return out
}

func jeanJournalAll() []jeanEntry {
	ents, _ := os.ReadDir(jeanDir())
	var out []jeanEntry
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, "journal-") || !strings.HasSuffix(n, ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		jeanJournalCacheMu.Lock()
		c, ok := jeanJournalCache[filepath.Join(jeanDir(), n)]
		jeanJournalCacheMu.Unlock()
		if !ok || !c.mod.Equal(info.ModTime()) || c.size != info.Size() {
			s, err := jeanRead(n)
			if err != nil {
				continue // mois chiffré et mémoire verrouillée : ignoré
			}
			c = jeanJournalMonth{info.ModTime(), info.Size(), parseJeanJournal(s)}
			jeanJournalCacheMu.Lock()
			jeanJournalCache[filepath.Join(jeanDir(), n)] = c
			jeanJournalCacheMu.Unlock()
		}
		out = append(out, c.ents...)
	}
	return out
}

// Cache des mois du journal déjà lus : seul le mois en cours change, les autres
// ne sont déchiffrés et analysés qu'une fois par démarrage. Sans lui, chaque
// recherche relisait toutes les années de journal (lent au bout de quelques ans).
type jeanJournalMonth struct {
	mod  time.Time
	size int64
	ents []jeanEntry
}

var (
	jeanJournalCacheMu sync.Mutex
	jeanJournalCache   = map[string]jeanJournalMonth{}
)

// jeanRecall : compteur de rappels d'une note (nombre + jours distincts).
type jeanRecall struct {
	N    int      `json:"n"`
	Days []string `json:"days"`
}

// JeanSearch cherche dans le journal. Même classement que MemSearch (couverture
// des termes puis rareté), fraîcheur en simple départage. Chaque note renvoyée
// voit son compteur de rappels augmenter.
func JeanSearch(query string, limit int) string {
	if limit <= 0 || limit > 15 {
		limit = 6
	}
	terms := uniqueTerms(foldSearch(query))
	if len(terms) == 0 {
		return "[erreur] requête vide"
	}
	jeanMu.Lock()
	defer jeanMu.Unlock()
	all := jeanJournalAll()
	hays := make([]string, len(all))
	df := map[string]int{}
	for i, e := range all {
		hays[i] = foldSearch(e.Text)
		for _, t := range terms {
			if strings.Contains(hays[i], t) {
				df[t]++
			}
		}
	}
	type hit struct {
		e       jeanEntry
		matched int
		score   float64
	}
	var hits []hit
	n := float64(len(all))
	now := time.Now()
	for i, e := range all {
		matched, score := 0, 0.0
		for _, t := range terms {
			c := strings.Count(hays[i], t)
			if c == 0 {
				continue
			}
			matched++
			score += (math.Log((n+1)/float64(df[t]+1)) + 1) * (1 + math.Log(float64(c)))
		}
		if matched == 0 {
			continue
		}
		if e.Kind == "note" {
			score *= 1.3 // une note voulue par l'IA vaut plus qu'un échange brut
		}
		// Départage seulement : au plus +10 % pour une note toute fraîche.
		score *= 1 + 0.1*math.Exp(-now.Sub(e.When).Hours()/(24*90))
		hits = append(hits, hit{e, matched, score})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].matched != hits[j].matched {
			return hits[i].matched > hits[j].matched
		}
		return hits[i].score > hits[j].score
	})
	fiches := jeanFicheHits(terms)
	if len(hits) == 0 && len(fiches) == 0 {
		return "[aucun résultat] (essaie d'autres mots, un synonyme, ou un seul mot-clé)"
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	recalls := map[string]*jeanRecall{}
	if s, err := jeanRead(jeanRecallsFile); err == nil && s != "" {
		_ = json.Unmarshal([]byte(s), &recalls)
	}
	today := now.Format("2006-01-02")
	userLoc := loc(defaultTaskTZ())
	var b strings.Builder
	// Fiches d'abord : une procédure qui correspond vaut mieux qu'un vieil échange.
	for _, f := range fiches {
		b.WriteString(f + "\n")
	}
	for _, h := range hits {
		r := recalls[h.e.ID]
		if r == nil {
			r = &jeanRecall{}
			recalls[h.e.ID] = r
		}
		r.N++
		if len(r.Days) == 0 || r.Days[len(r.Days)-1] != today {
			r.Days = append(r.Days, today)
			if len(r.Days) > 10 {
				r.Days = r.Days[len(r.Days)-10:]
			}
		}
		fmt.Fprintf(&b, "- [%s · %s] %s\n", h.e.When.In(userLoc).Format("2006-01-02 15:04"), h.e.Kind, clipRunes(h.e.Text, 600))
		if h.e.Kind != "profil" && r.N >= jeanPromoteRecalls && len(r.Days) >= jeanPromoteDays {
			fmt.Fprintf(&b, "  (recalled %d times on %d days: if it's a lasting fact, put it in the profile with jean_remember)\n", r.N, len(r.Days))
		}
	}
	if raw, err := json.Marshal(recalls); err == nil {
		_ = jeanWrite(jeanRecallsFile, string(raw))
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---- outils exposés au modèle (mode Jean) ------------------------------------

// Schémas volontairement minuscules : un petit modèle local les lit à chaque tour.
func jeanTools() []Tool {
	obj := func(props map[string]any, req ...string) map[string]any {
		o := map[string]any{"type": "object", "properties": props}
		// Pas de « required: null » : les API OpenAI strictes (DeepSeek…) le
		// refusent (issue #106) ; llama.cpp seul le tolérait.
		if len(req) > 0 {
			o["required"] = req
		}
		return o
	}
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	mk := func(name, desc string, params map[string]any) Tool {
		return Tool{Type: "function", Function: ToolFunction{Name: name, Description: desc, Parameters: params}}
	}
	return append([]Tool{
		mk("jean_search", "Search your journal (all past conversations and notes) by keywords.",
			obj(map[string]any{"query": str("Keywords"), "limit": map[string]any{"type": "integer", "description": "Default 6"}}, "query")),
		mk("jean_remember", "Save or REPLACE a lasting fact in the user's profile (always visible to you). Same key = replaces the old value.",
			obj(map[string]any{"key": str("Short dotted key, e.g. family.mother.birthday"), "value": str("One short line")}, "key", "value")),
		mk("jean_forget", "Remove a profile line or a lesson (lecon-N); they stay findable in the journal. Or archive a fiche no longer useful (still findable, reading it brings it back).",
			obj(map[string]any{"key": str("Profile key, lecon-N or fiche name")}, "key")),
		mk("jean_save", "Save or REPLACE a fiche: a procedure, how-to, recipe or guide to keep in full. Same name = new version.",
			obj(map[string]any{"name": str("Short name, e.g. deploy-server"), "when": str("One line: when to use it"), "content": str("Full content, Markdown")}, "name", "when", "content")),
		mk("jean_read", "Open a fiche in full.",
			obj(map[string]any{"name": str("Fiche name")}, "name")),
		mk("jean_lesson", "Save a lesson: a correction from the user, or what failed before what worked. One short rule (a full procedure goes in a fiche). Give fiche to add it to that fiche's pitfalls.",
			obj(map[string]any{"text": str("One sentence: the rule to follow and why"), "fiche": str("Optional fiche name")}, "text")),
		mk("jean_rule", "Have your harness enforce a mechanical writing rule on all you write: kind=replace (banned text 'from' becomes 'to'), no_numbered_lists, or max_list_items (max). action=remove, from=rule-N drops one. Behavior rules go in jean_lesson.",
			obj(map[string]any{"kind": str("replace | no_numbered_lists | max_list_items"), "from": str("replace: the exact banned text; remove: rule-N"), "to": str("replace: what to write instead"), "max": map[string]any{"type": "integer", "description": "max_list_items: the limit"}, "action": str("add (default) or remove"), "why": str("Short reason")}, "kind")),
		mk("jean_patch", "Fix part of a fiche without rewriting it: replace an exact passage.",
			obj(map[string]any{"name": str("Fiche name"), "old": str("Exact passage to replace"), "new": str("Replacement (empty = delete)")}, "name", "old", "new")),
		mk("jean_note", "Write a note in your journal: details or events to find later (not changing lists, those live in a fiche).",
			obj(map[string]any{"text": str("The note")}, "text")),
	}, jeanProjectTools()...)
}

// jeanToolCall exécute un outil jean_*. ok=false si le nom n'en est pas un.
func jeanToolCall(name string, args map[string]any) (result string, ok bool) {
	s := func(k string) string { v, _ := args[k].(string); return v }
	var err error
	switch name {
	case "jean_search":
		lim := 0
		if v, ok := args["limit"].(float64); ok {
			lim = int(v)
		}
		return JeanSearch(s("query"), lim), true
	case "jean_remember":
		result, err = JeanRemember(s("key"), s("value"))
	case "jean_forget":
		result, err = JeanForget(s("key"))
	case "jean_note":
		result, err = JeanNote(s("text"))
	case "jean_save":
		result, err = JeanSave(s("name"), s("when"), s("content"))
	case "jean_rule":
		mx := 0
		if v, ok := args["max"].(float64); ok {
			mx = int(v)
		}
		result, err = JeanRule(s("action"), s("kind"), s("from"), s("to"), mx, s("why"))
	case "jean_read":
		if silent, _ := args["_silent"].(bool); silent {
			result, err = jeanReadSilent(s("name"))
		} else {
			result, err = JeanRead(s("name"))
		}
	case "jean_lesson":
		result, err = JeanLesson(s("text"), s("fiche"))
	case "jean_patch":
		result, err = JeanPatch(s("name"), s("old"), s("new"))
	default:
		if isJeanProjectTool(name) {
			return jeanProjectToolCall(name, args), true
		}
		return "", false
	}
	if err != nil {
		return "[erreur] " + err.Error(), true
	}
	return result, true
}

// jeanToolLabel : libellé affiché dans la bulle d'outil.
func jeanToolLabel(name string, args map[string]any) string {
	for _, k := range []string{"path", "page", "project", "query", "key", "name", "fiche", "text", "from", "kind"} {
		if v, ok := args[k].(string); ok && v != "" {
			return clipRunes(v, 80)
		}
	}
	return ""
}

// jeanPromptSection : consignes du mode Jean, ajoutées au préambule système.
// Courtes à dessein (voir baseSystemPrompt) : un petit modèle doit les suivre.
func jeanPromptSection() string {
	return "\nYou are the user's personal assistant and you remember everything.\n" +
		"Memory (the \"Jean memory\" block is always in front of you):\n" +
		"- Profile: lasting facts about the user (name, family, dates, preferences, habits, places, projects). Save each new one at once with jean_remember, without asking. Only what they said or confirmed: never your guess, a nickname you assumed, or a secret (passwords, keys, tokens live in the fiche of the service, the profile at most points to it).\n" +
		"- Fiches: procedures and long things to keep in full. When a request matches one, jean_read it first and follow it. Name a fiche after the real thing.\n" +
		"- Journal: every exchange is logged. When the user refers to the past or to something you don't recognize, jean_search before answering; never say you don't know before searching.\n" +
		"- Lessons: when the user corrects you or something fails before what works, save it (jean_lesson, with fiche=<name> if it is about that fiche; fix a wrong step with jean_patch), confirm in one short sentence what you will do from now on, and redo it right. A mechanical writing rule (banned word or character, list limits) is also enforced with jean_rule. Don't mention your memory or rules unless asked.\n" +
		"- Living lists the user updates (todo, shopping, collection) are kept in ONE fiche with the current state, edited in place; never as journal notes. jean_note is for details worth finding later.\n" +
		"How you work:\n" +
		"- State only what you checked; mark a guess as a guess. Follow the user's instructions literally. When a word looks like a typo or a name you don't know, use the obvious meaning, then ask in one line which it was.\n" +
		"- When a tool result contradicts the user (an error, a missing file), tell them what you saw; never note their claim as fact or blame a bug without proof. Your own reading of an image or your memory is not proof: when the user corrects it, above all about their own photo or life, believe them.\n" +
		"- Ask before changing the system (system files, network settings, services, installs) or anything that isn't the user's.\n" +
		"- When a request took many steps or trial and error, save the direct path in a fiche BEFORE answering: exact commands, URLs and values that worked, plus pitfalls, and its script (robust, kept in your scripts folder) if one did the job, so next time takes one or two calls. When a saved script fails, fix the script. A fiche keeps HOW to get a changing value, never the value. Drafts and tests stay in your workspace.\n" +
		"- Your answer: in the user's language (every visible word, notes before tool calls too); first answer the question actually asked, in one line, then only what helps; never refer to something you didn't show. Save to memory before answering, then stop: nothing after the answer.\n" +
		"- Reminders are tasks: their result reaches the user as your message here, so write the prompt as what you will do then (\"Remind the user to call their mother\"). One-time: in_minutes or at (\"HH:MM\"), never a cron. Times are the user's, in brackets at the start of their messages; never the server clock.\n" +
		"- The user's AJEAN projects are read-only for you (jean_projects); to reuse something, make your own copy.\n"
}

// lastUserText : texte du dernier message utilisateur (parties texte seulement
// pour un message multimodal ; images ignorées).
func lastUserText(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		switch c := msgs[i].Content.(type) {
		case string:
			return c
		case []map[string]any:
			var parts []string
			for _, p := range c {
				if t, ok := p["text"].(string); ok {
					parts = append(parts, t)
				}
			}
			return strings.Join(parts, "\n")
		case []any:
			var parts []string
			for _, p := range c {
				if m, ok := p.(map[string]any); ok {
					if t, ok := m["text"].(string); ok {
						parts = append(parts, t)
					}
				}
			}
			return strings.Join(parts, "\n")
		}
		return ""
	}
	return ""
}

// jeanNow : date et heure actuelles dans le fuseau de l'utilisateur (celui de son
// navigateur, voir defaultTaskTZ), pas celui du serveur. Préfixées à chaque message
// en mode Jean : dans un fil sans fin, une heure donnée une seule fois au début
// devient fausse, et une heure UTC décalait les rappels de deux heures.
func jeanNow() string {
	tz := defaultTaskTZ()
	now := time.Now().In(loc(tz))
	return now.Format("Monday 2006-01-02 15:04") + " (" + tzLabel(tz) + ")"
}
