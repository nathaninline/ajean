package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ajean/internal/config"
	"ajean/internal/home"
)

// Le journal : tout ce qui s'est passé, un fichier par mois. Chaque échange y
// est consigné d'office ; c'est ce qui permet de « se souvenir de tout » sans
// rien coûter au contexte.

const (
	noteMaxChars    = 2000
	exchangeExcerpt = 400 // longueur gardée de chaque côté d'un échange
	headLayout      = "2006-01-02 15:04 -0700"
	entryTimeLayout = "2006-01-02 15:04"

	kindExchange = "echange"
	kindNote     = "note"
	kindProfile  = "profil"
	kindLesson   = "lecon"
	kindRule     = "regle"
	kindFiche    = "fiche"

	userLabel      = "Utilisateur : "
	assistantLabel = "AJEAN : "
	// Ce qui tient lieu de texte à un message fait seulement de pièces jointes.
	attachmentsOnly = "(pièces jointes seulement)"
)

var (
	headRe = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2} \d{2}:\d{2} [+-]\d{4}) · ([0-9a-z]+) · (\w+)$`)
	kindRe = regexp.MustCompile(`[^a-z0-9_]+`)
)

// entry : une entrée du journal, telle que le paquet la manipule.
type entry struct {
	id     string
	at     time.Time
	kind   string
	text   string
	folded string // texte replié, calculé une fois : chaque recherche le parcourt
	month  string // AAAA-MM du fichier qui la porte (c'est aussi celui de son vecteur)
}

// Entry : une entrée du journal, pour l'interface.
type Entry struct {
	ID   string `json:"id"`
	When string `json:"when"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// monthCache : un mois déjà lu. Seul le mois en cours change ; sans ce cache,
// chaque recherche relirait des années de journal.
type monthCache struct {
	mod     time.Time
	size    int64
	entries []entry
}

var (
	journalCache = map[string]monthCache{} // par chemin de fichier
	lastEntryMS  int64                     // deux entrées dans la même milliseconde auraient le même id
)

func journalPath(month string) string { return memPath(journalDir + "/" + month + ".md") }

// Log consigne une entrée quelconque (« message », « revision », « sommeil »…).
func Log(kind, text string) {
	kind = kindRe.ReplaceAllString(fold(kind), "")
	if kind == "" {
		kind = kindNote
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	logLocked(kind, clip(text, noteMaxChars))
}

// LogExchange consigne un échange, en extraits bornés. Un message sans texte
// (des pièces jointes seulement) est consigné aussi : la réponse qu'il a reçue
// vaut d'être retrouvée. Seul un échange vide des deux côtés ne laisse rien.
func LogExchange(user, answer string) {
	answer = clip(strings.TrimSpace(answer), exchangeExcerpt)
	if strings.TrimSpace(user) == "" && answer == "" {
		return
	}
	text := userLabel + exchangeUserPart(user)
	if answer != "" {
		text += "\n" + assistantLabel + answer
	}
	mu.Lock()
	defer mu.Unlock()
	logLocked(kindExchange, text)
}

// exchangeUserPart : ce qu'un échange consigné garde du message de l'utilisateur.
func exchangeUserPart(user string) string {
	if user = strings.TrimSpace(user); user != "" {
		return clip(user, exchangeExcerpt)
	}
	return attachmentsOnly
}

// exchangeKey : la part « utilisateur » d'un échange consigné, sous une forme
// comparable ("" si l'entrée n'est pas un échange). Sert à reconnaître un
// échange encore présent dans le contexte du modèle.
func exchangeKey(e entry) string {
	rest, ok := strings.CutPrefix(e.text, userLabel)
	if !ok || e.kind != kindExchange {
		return ""
	}
	user, _, _ := strings.Cut(rest, "\n"+assistantLabel)
	return fold(squash(user))
}

// logLocked consigne sans jamais faire échouer l'opération qui l'appelle : une
// trace manquée ne vaut pas de refuser l'écriture qu'elle accompagne.
func logLocked(kind, text string) {
	if err := appendLocked(kind, text); err != nil {
		fmt.Fprintf(os.Stderr, "[memory] journal non écrit : %v\n", err)
	}
}

// appendLocked ajoute une entrée au bout du fichier du mois, sans le relire ni
// le réécrire : en fin de mois il porte des milliers d'entrées, et il s'en
// ajoute une à chaque échange. Chaque entrée commence par une ligne vide : une
// écriture coupée net (coupure de courant) abîme au pire sa propre entrée,
// jamais l'en-tête de la suivante.
func appendLocked(kind, text string) error {
	now := time.Now().In(config.Location())
	month := now.Format("2006-01")
	path := journalPath(month)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	lastEntryMS = max(now.UnixMilli(), lastEntryMS+1)
	e := entry{id: strconv.FormatInt(lastEntryMS, 16), at: now, kind: kind, text: text}
	block := renderEntry(e)
	if info.Size() == 0 {
		block = journalHead(month) + block
	}
	_, err = f.WriteString(block)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	WakeIndexer()
	return nil
}

func journalHead(month string) string { return "# Journal " + month + "\n" }

// renderEntry : une entrée telle qu'elle est écrite. Le décalage horaire y
// figure : sans lui, l'heure d'un serveur en UTC passerait pour celle de
// l'utilisateur.
func renderEntry(e entry) string {
	return "\n## " + e.at.Format(headLayout) + " · " + e.id + " · " + e.kind + "\n" + escapeHeads(e.text) + "\n"
}

// escapeHeads décale d'une espace toute ligne du texte qui passerait pour
// l'en-tête d'une entrée : à la relecture, elle couperait l'entrée en deux.
func escapeHeads(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, l := range lines {
		if headRe.MatchString(l) {
			lines[i] = " " + l
		}
	}
	return strings.Join(lines, "\n")
}

func parseJournal(month, s string) []entry {
	var out []entry
	var body []string
	open := false
	flush := func() {
		if open {
			e := &out[len(out)-1]
			e.text = strings.TrimSpace(strings.Join(body, "\n"))
			e.folded = fold(e.text)
		}
	}
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, "\r")
		if m := headRe.FindStringSubmatch(l); m != nil {
			if t, err := time.Parse(headLayout, m[1]); err == nil {
				flush()
				out = append(out, entry{id: m[2], at: t, kind: m[3], month: month})
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

func renderJournal(month string, entries []entry) string {
	var b strings.Builder
	b.WriteString(journalHead(month))
	for _, e := range entries {
		b.WriteString(renderEntry(e))
	}
	return b.String()
}

// journalMonths : les mois du journal, du plus ancien au plus récent.
func journalMonths() []string {
	ents, _ := os.ReadDir(memPath(journalDir))
	var out []string
	for _, e := range ents {
		if name, ok := strings.CutSuffix(e.Name(), ".md"); ok && !e.IsDir() {
			out = append(out, name)
		}
	}
	return out
}

// monthLocked : les entrées d'un mois (relues seulement si le fichier a changé).
func monthLocked(month string) []entry {
	path := journalPath(month)
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	c, ok := journalCache[path]
	if !ok || !c.mod.Equal(info.ModTime()) || c.size != info.Size() {
		s, err := readFile(path)
		if err != nil {
			return nil
		}
		c = monthCache{info.ModTime(), info.Size(), parseJournal(month, s)}
		journalCache[path] = c
	}
	return c.entries
}

// journalLocked : tout le journal, du plus ancien au plus récent. Les tranches
// renvoyées sont partagées avec le cache : à lire, jamais à modifier.
func journalLocked() []entry {
	var out []entry
	for _, m := range journalMonths() {
		out = append(out, monthLocked(m)...)
	}
	return out
}

// Journal : les entrées les plus récentes d'abord. query filtre sans casse ni
// accents (texte ou genre) ; limit borne la liste (0 = tout), total compte tout
// ce qui correspond.
func Journal(query string, limit int) (entries []Entry, total int) {
	q := fold(query)
	loc := config.Location()
	mu.Lock()
	defer mu.Unlock()
	all := journalLocked()
	entries = []Entry{}
	for i := len(all) - 1; i >= 0; i-- {
		e := all[i]
		if q != "" && !strings.Contains(e.folded, q) && !strings.Contains(e.kind, q) {
			continue
		}
		total++
		if limit <= 0 || len(entries) < limit {
			entries = append(entries, Entry{ID: e.id, When: e.at.In(loc).Format(entryTimeLayout), Kind: e.kind, Text: e.text})
		}
	}
	return entries, total
}

// DeleteEntry retire une entrée du journal, pour de bon.
func DeleteEntry(id string) error {
	mu.Lock()
	defer mu.Unlock()
	for _, month := range journalMonths() {
		entries := monthLocked(month)
		for i, e := range entries {
			if e.id != id {
				continue
			}
			rest := append(append([]entry{}, entries[:i]...), entries[i+1:]...)
			if len(rest) == 0 {
				return os.Remove(journalPath(month))
			}
			return home.WriteFile(journalPath(month), []byte(renderJournal(month, rest)))
		}
	}
	return errors.New("entrée introuvable dans le journal")
}

// note (outil memory_note) : une note voulue par l'assistant.
func note(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("note vide")
	}
	if n := runeLen(text); n > noteMaxChars {
		return fmt.Errorf("note trop longue (%d car., max %d) : une procédure ou un long texte à garder va dans une fiche (fiche_save)", n, noteMaxChars)
	}
	mu.Lock()
	defer mu.Unlock()
	return appendLocked(kindNote, text)
}
