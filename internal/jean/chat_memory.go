package jean

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// La mémoire de jean = des fichiers Markdown plats sous MEMORY/<nom>.md.
// L'IA y range ce qu'elle veut retenir entre les sessions (préférences,
// décisions, procédures, infos projet). Quatre outils : mem_search, mem_read,
// mem_add, mem_edit.

type MemPage struct {
	Name  string // nom de fichier sans le dossier (ex: "docker-notes.md")
	Title string // 1re ligne non vide, sans les #
}

type MemHit struct {
	File    string
	Title   string
	Snippet string
}

var memNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// memFileName normalise un nom de page : ajoute .md si absent et valide.
func memFileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("nom vide")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".md") {
		name += ".md"
	}
	if !memNameRe.MatchString(name) {
		return "", fmt.Errorf("nom invalide (alphanum, ._-)")
	}
	return name, nil
}

// safeMemPath valide et renvoie le chemin disque d'une page mémoire.
func safeMemPath(name string) (string, error) {
	fn, err := memFileName(name)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(memoryDir())
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filepath.Join(root, fn))
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path invalide")
	}
	return abs, nil
}

// titleOf renvoie la 1re ligne non vide d'un contenu, sans les # de tête.
func titleOf(content string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if s != "" {
			return s
		}
	}
	return ""
}

// MemList liste les pages mémoire (nom + titre), triées par nom.
func MemList() []MemPage {
	entries, err := os.ReadDir(memoryDir())
	if err != nil {
		return nil
	}
	out := []MemPage{}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(memoryDir(), e.Name()))
		out = append(out, MemPage{Name: e.Name(), Title: titleOf(string(b))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// MemSearch cherche les termes de la requête dans le nom + le contenu de chaque
// page, renvoie une liste classée {fichier, titre, extrait}. Comme un moteur de
// recherche : à compléter par mem_read sur la page la plus pertinente.
func MemSearch(query string, limit int) []MemHit {
	if limit <= 0 || limit > 30 {
		limit = 8
	}
	terms := strings.Fields(strings.ToLower(query))
	type scored struct {
		hit   MemHit
		score int
	}
	var ranked []scored
	for _, p := range MemList() {
		b, _ := os.ReadFile(filepath.Join(memoryDir(), p.Name))
		content := string(b)
		hay := strings.ToLower(p.Name + "\n" + content)
		score := 0
		for _, t := range terms {
			score += strings.Count(hay, t)
			if strings.Contains(strings.ToLower(p.Name), t) {
				score += 3 // bonus si le terme est dans le nom de la page
			}
		}
		if score == 0 && query != "" {
			continue
		}
		ranked = append(ranked, scored{
			hit:   MemHit{File: p.Name, Title: p.Title, Snippet: snippetAround(content, terms)},
			score: score,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	out := []MemHit{}
	for i, r := range ranked {
		if i >= limit {
			break
		}
		out = append(out, r.hit)
	}
	return out
}

// snippetAround renvoie un court extrait autour de la 1re occurrence d'un terme.
func snippetAround(content string, terms []string) string {
	flat := strings.Join(strings.Fields(content), " ")
	low := strings.ToLower(flat)
	idx := -1
	for _, t := range terms {
		if i := strings.Index(low, t); i >= 0 && (idx < 0 || i < idx) {
			idx = i
		}
	}
	if idx < 0 {
		if len(flat) > 160 {
			return flat[:160] + "…"
		}
		return flat
	}
	start := idx - 60
	if start < 0 {
		start = 0
	}
	end := idx + 100
	if end > len(flat) {
		end = len(flat)
	}
	s := flat[start:end]
	if start > 0 {
		s = "…" + s
	}
	if end < len(flat) {
		s += "…"
	}
	return s
}

// MemRead lit une plage de lignes d'une page (1-indexé, lignes préfixées du
// numéro). offset/limit par défaut : tout depuis la ligne 1 (cap 500).
func MemRead(name string, offset, limit int) (string, error) {
	p, err := safeMemPath(name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("page '%s' introuvable", name)
	}
	lines := strings.Split(string(b), "\n")
	if offset <= 0 {
		offset = 1
	}
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	var out strings.Builder
	for i := offset - 1; i < len(lines) && i < offset-1+limit; i++ {
		fmt.Fprintf(&out, "%d\t%s\n", i+1, lines[i])
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// MemAdd crée une nouvelle page mémoire. Refuse d'écraser une page existante
// (utiliser mem_edit pour modifier).
func MemAdd(name, content string) error {
	p, err := safeMemPath(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("la page existe déjà — utilise mem_edit pour la modifier")
	}
	if err := os.MkdirAll(memoryDir(), 0o755); err != nil {
		return err
	}
	body := strings.TrimRight(content, "\n") + "\n"
	return os.WriteFile(p, []byte(body), 0o644)
}

// MemEdit remplace oldText par newText dans une page. oldText doit apparaître
// EXACTEMENT une fois (sinon erreur), pour une édition sans ambiguïté.
func MemEdit(name, oldText, newText string) error {
	p, err := safeMemPath(name)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("page '%s' introuvable", name)
	}
	content := string(b)
	n := strings.Count(content, oldText)
	if oldText == "" {
		return fmt.Errorf("old vide")
	}
	if n == 0 {
		return fmt.Errorf("old introuvable dans la page")
	}
	if n > 1 {
		return fmt.Errorf("old apparaît %d fois — ajoute du contexte pour le rendre unique", n)
	}
	updated := strings.Replace(content, oldText, newText, 1)
	return os.WriteFile(p, []byte(updated), 0o644)
}

// MemContent renvoie le contenu brut d'une page (vide si absente). Utilisé par
// l'éditeur web.
func MemContent(name string) string {
	p, err := safeMemPath(name)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// MemSave écrit (crée ou écrase) une page mémoire. Si old est fourni et diffère
// du nouveau nom, l'ancienne page est renommée (supprimée). Utilisé par l'éditeur
// web.
func MemSave(name, old, content string) error {
	if old != "" {
		if oldFn, e1 := memFileName(old); e1 == nil {
			if newFn, e2 := memFileName(name); e2 == nil && oldFn != newFn {
				if od, e3 := safeMemPath(old); e3 == nil {
					_ = os.Remove(od)
				}
			}
		}
	}
	p, err := safeMemPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(memoryDir(), 0o755); err != nil {
		return err
	}
	body := strings.TrimRight(content, "\n") + "\n"
	return os.WriteFile(p, []byte(body), 0o644)
}

// MemDelete supprime une page mémoire.
func MemDelete(name string) error {
	p, err := safeMemPath(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("introuvable")
	}
	return os.Remove(p)
}

// migrateSkillsToMemory copie une seule fois les anciens skills
// (SKILLS/<nom>/SKILL.md) vers la mémoire (MEMORY/<nom>.md). Idempotente : ne
// touche pas une page mémoire déjà existante, et pose un drapeau .migrated pour
// ne pas reparcourir à chaque lancement. Silencieuse en cas d'absence de SKILLS.
func migrateSkillsToMemory() {
	flag := filepath.Join(memoryDir(), ".skills_migrated")
	if _, err := os.Stat(flag); err == nil {
		return // déjà fait
	}
	entries, err := os.ReadDir(skillsDir())
	if err != nil {
		return // pas d'anciens skills
	}
	migrated := 0
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		src := filepath.Join(skillsDir(), e.Name(), "SKILL.md")
		b, rerr := os.ReadFile(src)
		if rerr != nil {
			continue
		}
		dst, derr := safeMemPath(e.Name())
		if derr != nil {
			continue
		}
		if _, err := os.Stat(dst); err == nil {
			continue // page mémoire homonyme déjà présente : on ne l'écrase pas
		}
		if err := os.MkdirAll(memoryDir(), 0o755); err != nil {
			return
		}
		if os.WriteFile(dst, b, 0o644) == nil {
			migrated++
		}
	}
	_ = os.MkdirAll(memoryDir(), 0o755)
	_ = os.WriteFile(flag, []byte(fmt.Sprintf("%d skills migrés\n", migrated)), 0o644)
}

// MemMode gouverne l'accès de l'IA à sa mémoire persistante, indépendamment du
// mode agent (shell). Trois modes :
//   - MemOff      : mémoire coupée (aucun outil mem_*, aucune consigne).
//   - MemOnDemand : outils mem_* disponibles, mais l'IA ne les utilise QUE si
//     l'utilisateur le demande explicitement (pas de recherche/écriture spontanée).
//   - MemAlways   : comportement proactif historique (cherche avant de répondre, sauve d'elle-même).
type MemMode string

const (
	MemOff      MemMode = "off"
	MemOnDemand MemMode = "ondemand"
	MemAlways   MemMode = "always"
)

// memMode lit MEM_MODE dans config.env. Défaut = always (préserve le comportement
// actuel). Toute valeur inconnue retombe sur always.
func memMode() MemMode {
	switch strings.ToLower(strings.TrimSpace(ReadConfig()["MEM_MODE"])) {
	case "off", "0", "false", "none", "no", "non":
		return MemOff
	case "ondemand", "on-demand", "demand", "manual", "manuel":
		return MemOnDemand
	default: // "always", "auto", "" et inconnus
		return MemAlways
	}
}

// setMemMode persiste le mode mémoire dans config.env.
func setMemMode(m MemMode) error {
	return SetConfigKey("MEM_MODE", string(m))
}

// cmdMemory : jean memory [off|ondemand|always|status]
func cmdMemory(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	label := map[MemMode]string{
		MemOff:      "désactivée (l'IA n'a aucun accès mémoire)",
		MemOnDemand: "sur demande (outils dispo, utilisés seulement si tu le demandes)",
		MemAlways:   "auto (l'IA cherche et sauve d'elle-même)",
	}
	switch sub {
	case "off", "none", "0", "false":
		if err := setMemMode(MemOff); err != nil {
			return err
		}
	case "ondemand", "on-demand", "demand", "manual", "manuel":
		if err := setMemMode(MemOnDemand); err != nil {
			return err
		}
	case "always", "auto", "on":
		if err := setMemMode(MemAlways); err != nil {
			return err
		}
	case "", "status", "list":
		m := memMode()
		fmt.Printf("%s  mode: %s — %s\n", cyan("Mémoire"), bold(string(m)), label[m])
		pages := MemList()
		fmt.Printf("  %d page(s) sous %s\n", len(pages), memoryDir())
		return nil
	default:
		return fmt.Errorf("usage: jean memory [off|ondemand|always|status]")
	}
	m := memMode()
	fmt.Printf("%s mémoire : %s — %s\n", green("[ok]"), bold(string(m)), label[m])
	return nil
}

// memorySystemPrompt liste les pages mémoire à injecter quand le mode agent est
// actif, pour que l'IA sache ce qu'elle a déjà retenu.
func memorySystemPrompt(caps Caps) string {
	if !caps.Agent {
		return ""
	}
	list := MemList()
	var b strings.Builder
	b.WriteString("Memory: persistent Markdown pages under MEMORY/. Use mem_search FIRST whenever the user asks about something you might already know (preferences, ongoing projects, past decisions). Open the most relevant page with mem_read. Save anything worth keeping across sessions with mem_add (one topic per page, descriptive kebab-case name); update a page with mem_edit (old must match exactly once). The first content line is a short title (#).\n")
	if len(list) == 0 {
		b.WriteString("Pages: none yet.")
		return b.String()
	}
	b.WriteString("Pages:\n")
	for _, p := range list {
		fmt.Fprintf(&b, "- %s: %s\n", p.Name, p.Title)
	}
	return strings.TrimRight(b.String(), "\n")
}
