package ajean

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// La mémoire de ajean = des fichiers Markdown plats sous memory/<nom>.md.
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
//
// Classement en deux temps, pour ne PAS laisser une grosse page bourrée d'un mot
// fréquent écraser une petite page qui matche VRAIMENT la requête :
//  1. Couverture d'abord : les pages qui contiennent le PLUS de termes distincts
//     de la requête passent devant (chercher « copine Alice » doit remonter la
//     page qui a les deux mots, pas celle qui répète « Alice » 40 fois).
//  2. À couverture égale, score pondéré par la RARETÉ du terme (IDF) : un mot
//     rare et discriminant (« copine ») pèse bien plus qu'un mot omniprésent
//     (« Alice »). La fréquence est amortie (log) pour que le bourrage ne gagne
//     pas, et un match dans le NOM ou le TITRE est fortement bonifié.
func MemSearch(query string, limit int) []MemHit {
	if limit <= 0 || limit > 30 {
		limit = 8
	}
	terms := uniqueTerms(strings.ToLower(query))

	type doc struct {
		p       MemPage
		name    string // minuscules
		title   string // minuscules
		content string // brut (pour l'extrait)
		hay     string // minuscules : nom + contenu
	}
	docs := make([]doc, 0)
	df := make(map[string]int) // nb de pages contenant chaque terme
	for _, p := range MemList() {
		b, _ := os.ReadFile(filepath.Join(memoryDir(), p.Name))
		content := string(b)
		d := doc{
			p:       p,
			name:    strings.ToLower(p.Name),
			title:   strings.ToLower(p.Title),
			content: content,
			hay:     strings.ToLower(p.Name + "\n" + content),
		}
		docs = append(docs, d)
		for _, t := range terms {
			if strings.Contains(d.hay, t) {
				df[t]++
			}
		}
	}
	n := len(docs)

	type scored struct {
		hit     MemHit
		matched int     // termes distincts trouvés (clé de tri primaire)
		score   float64 // pertinence pondérée (clé secondaire)
	}
	var ranked []scored
	for _, d := range docs {
		matched := 0
		score := 0.0
		for _, t := range terms {
			cnt := strings.Count(d.hay, t)
			if cnt == 0 {
				continue
			}
			matched++
			// IDF : rare = discriminant. +1 pour qu'un terme fréquent compte encore.
			idf := math.Log(float64(n+1)/float64(df[t]+1)) + 1
			// Fréquence amortie : 10 occurrences ne valent pas 10x une occurrence.
			tf := 1.0 + math.Log(float64(cnt))
			field := 1.0
			if strings.Contains(d.name, t) {
				field += 4 // le terme est dans le NOM de la page
			}
			if strings.Contains(d.title, t) {
				field += 2 // ... ou dans son TITRE
			}
			score += idf * tf * field
		}
		if matched == 0 && len(terms) > 0 {
			continue
		}
		ranked = append(ranked, scored{
			hit:     MemHit{File: d.p.Name, Title: d.p.Title, Snippet: snippetAround(d.content, terms)},
			matched: matched,
			score:   score,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].matched != ranked[j].matched {
			return ranked[i].matched > ranked[j].matched // couverture d'abord
		}
		return ranked[i].score > ranked[j].score
	})
	out := []MemHit{}
	for i, r := range ranked {
		if i >= limit {
			break
		}
		out = append(out, r.hit)
	}
	return out
}

// uniqueTerms découpe une requête en mots distincts (dédupliqués, ordre gardé).
func uniqueTerms(q string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Fields(q) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
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
// errAlreadyApplied signale une édition dont le résultat est DÉJÀ en place :
// ce n'est pas un échec, la page est dans l'état demandé.
var errAlreadyApplied = errors.New("déjà à jour — la page contient déjà cette modification")

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
		// Déjà remplacé (le modèle rejoue souvent la même édition) : ce n'est pas
		// une erreur, la page est dans l'état demandé.
		if newText != "" && strings.Contains(content, newText) {
			return errAlreadyApplied
		}
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
	case "off":
		return MemOff
	case "ondemand":
		return MemOnDemand
	default: // "always", "auto", "" et inconnus
		return MemAlways
	}
}

// setMemMode persiste le mode mémoire dans config.env.
func setMemMode(m MemMode) error {
	return SetConfigKey("MEM_MODE", string(m))
}

// cmdMemory : ajean memory [off|ondemand|always|status]
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
	case "off":
		if err := setMemMode(MemOff); err != nil {
			return err
		}
	case "ondemand", "on-demand", "demand", "manual", "manuel":
		if err := setMemMode(MemOnDemand); err != nil {
			return err
		}
	case "always":
		if err := setMemMode(MemAlways); err != nil {
			return err
		}
	case "", "status":
		m := memMode()
		fmt.Printf("%s  mode: %s — %s\n", cyan("Mémoire"), bold(string(m)), label[m])
		pages := MemList()
		fmt.Printf("  %d page(s) sous %s\n", len(pages), memoryDir())
		return nil
	default:
		return fmt.Errorf("usage: ajean memory [off|ondemand|always|status]")
	}
	m := memMode()
	fmt.Printf("%s mémoire : %s — %s\n", green("[ok]"), bold(string(m)), label[m])
	return nil
}
