package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"ajean/internal/home"
	"ajean/internal/tool"
)

// Les fiches : procédures, recettes, notices, guides gardés en entier. Le nom
// et une ligne « quand s'en servir » sont toujours sous les yeux du modèle
// (voir que la fiche existe est ce qui lui fait penser à l'ouvrir) ; le contenu
// n'entre dans le contexte que quand elle sert.

const (
	ficheMaxChars = 20000
	// Une fusion peut dépasser la taille d'une fiche ordinaire : sinon une
	// grosse fiche de référence ne pourrait recevoir aucune de ses doublures.
	mergeMaxChars = 32000
	whenMaxChars  = 160
	ficheNameMax  = 48
	// Le « quand » d'une fiche fusionnée cumule les déclencheurs des deux.
	mergeWhenMaxChars = 400
	pitfallsHead      = "## Pièges"
)

var ficheNameRe = regexp.MustCompile(fmt.Sprintf(`^[a-z0-9][a-z0-9-]{0,%d}$`, ficheNameMax-1))

// Fiche : une fiche, pour l'interface.
type Fiche struct {
	Name     string `json:"name"`
	When     string `json:"when"`
	Content  string `json:"content,omitempty"`
	Archived bool   `json:"archived,omitempty"`
}

// fiche : une fiche et la date de sa dernière écriture.
type fiche struct {
	Fiche
	mod time.Time
}

// normFicheName : « Déployer le site » → « deployer-le-site ».
func normFicheName(n string) string {
	var b strings.Builder
	dash := false
	for _, r := range fold(n) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func ficheRel(name string, archived bool) string {
	if archived {
		return archivesDir + "/" + name + ".md"
	}
	return fichesDir + "/" + name + ".md"
}

func fichePath(name string, archived bool) string { return memPath(ficheRel(name, archived)) }

func parseFiche(name, s string) Fiche {
	f := Fiche{Name: name}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	i := 0
	if i < len(lines) && strings.HasPrefix(lines[i], "# ") {
		i++
	}
	if i < len(lines) && strings.HasPrefix(lines[i], "when: ") {
		f.When = strings.TrimSpace(strings.TrimPrefix(lines[i], "when: "))
		i++
	}
	f.Content = strings.TrimSpace(strings.Join(lines[i:], "\n"))
	return f
}

func renderFiche(f Fiche) string {
	return "# " + f.Name + "\nwhen: " + f.When + "\n\n" + strings.TrimSpace(f.Content) + "\n"
}

// show : la fiche telle qu'on la donne au modèle.
func (f Fiche) show() string { return "# " + f.Name + "\n(when: " + f.When + ")\n\n" + f.Content }

func writeFicheLocked(f Fiche) error {
	path := fichePath(f.Name, f.Archived)
	// Le cache reconnaît un fichier à sa date et à sa taille : deux écritures de
	// même longueur dans le même tic d'horloge lui échapperaient.
	delete(ficheCache[filepath.Dir(path)], f.Name)
	return home.WriteFile(path, []byte(renderFiche(f)))
}

// loadFicheLocked lit une fiche (ok=false si elle n'existe pas).
func loadFicheLocked(name string, archived bool) (Fiche, bool) {
	s, err := readFile(fichePath(name, archived))
	if err != nil || s == "" {
		return Fiche{}, false
	}
	f := parseFiche(name, s)
	f.Archived = archived
	return f, true
}

// findFicheLocked : la fiche de ce nom, dans la liste ou à défaut aux archives.
func findFicheLocked(name string) (Fiche, bool) {
	for _, archived := range []bool{false, true} {
		if f, ok := loadFicheLocked(name, archived); ok {
			return f, true
		}
	}
	return Fiche{}, false
}

// cachedFiche : une fiche déjà lue, valable tant que son fichier n'a pas changé.
type cachedFiche struct {
	fiche
	size int64
}

// ficheCache : les fiches déjà lues, par dossier puis par nom (protégé par mu).
// La liste des fiches est demandée à chaque appel au modèle : sans lui, chacun
// rouvrirait tous les fichiers.
var ficheCache = map[string]map[string]cachedFiche{}

// fichesLocked : les fiches (ou les archives), contenu compris, les plus
// récemment écrites d'abord. Seuls les fichiers qui ont changé sont relus.
func fichesLocked(archived bool) []fiche {
	dir := memPath(fichesDir)
	if archived {
		dir = memPath(archivesDir)
	}
	ents, _ := os.ReadDir(dir)
	// Le cache est refait à chaque passage : une fiche disparue n'y reste pas.
	seen := map[string]cachedFiche{}
	var out []fiche
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // supprimée depuis la lecture du dossier
		}
		c, ok := ficheCache[dir][name]
		if !ok || !c.mod.Equal(info.ModTime()) || c.size != info.Size() {
			f, ok := loadFicheLocked(name, archived)
			if !ok {
				continue
			}
			c = cachedFiche{fiche{f, info.ModTime()}, info.Size()}
		}
		seen[name] = c
		out = append(out, c.fiche)
	}
	ficheCache[dir] = seen
	sort.SliceStable(out, func(i, j int) bool { return out[i].mod.After(out[j].mod) })
	return out
}

func ficheNames(fs []fiche) []string {
	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = f.Name
	}
	return names
}

// Fiches : toutes les fiches sans leur contenu, par nom, les archivées à la suite.
func Fiches() []Fiche {
	mu.Lock()
	defer mu.Unlock()
	out := []Fiche{}
	for _, archived := range []bool{false, true} {
		fs := fichesLocked(archived)
		sort.Slice(fs, func(i, j int) bool { return fs[i].Name < fs[j].Name })
		for _, f := range fs {
			f.Content = ""
			out = append(out, f.Fiche)
		}
	}
	return out
}

// GetFiche : une fiche avec son contenu. Une archive est lue sans être ressortie.
func GetFiche(name string) (Fiche, error) {
	name = normFicheName(name)
	mu.Lock()
	defer mu.Unlock()
	if f, ok := findFicheLocked(name); ok {
		return f, nil
	}
	return Fiche{}, fmt.Errorf("fiche « %s » introuvable", name)
}

// ReadFicheQuietly : lecture pendant un travail de fond. Elle ne compte pas
// comme un usage (les compteurs disent quelles fiches servent vraiment) et ne
// sort rien des archives.
func ReadFicheQuietly(name string) (string, error) {
	f, err := GetFiche(name)
	if err != nil {
		return "", err
	}
	if f.Archived {
		return "(fiche archivée)\n" + f.show(), nil
	}
	return f.show(), nil
}

// newVersion valide ce qu'on veut enregistrer sous name (déjà normalisé).
// prev : la version que ça remplace (vide pour une fiche neuve). Une fiche
// déjà longue (fruit d'une fusion) peut être réécrite tant qu'elle ne grossit
// pas : sinon même la raccourcir serait refusé.
func newVersion(name, when, content string, prev Fiche) (Fiche, error) {
	if !ficheNameRe.MatchString(name) {
		return Fiche{}, errors.New("nom invalide : court, ex. « deployer-le-site »")
	}
	when = squash(when)
	if when == "" {
		return Fiche{}, errors.New("'when' obligatoire : une ligne qui dit quand se servir de cette fiche")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return Fiche{}, errors.New("contenu vide")
	}
	if limit := min(max(whenMaxChars, runeLen(prev.When)), mergeWhenMaxChars); runeLen(when) > limit {
		return Fiche{}, fmt.Errorf("'when' trop long (%d car. max)", limit)
	}
	if n, limit := runeLen(content), ficheLimit(prev); n > limit {
		return Fiche{}, fmt.Errorf("fiche trop longue (%d car., max %d) : découpe-la en plusieurs fiches", n, limit)
	}
	return Fiche{Name: name, When: when, Content: content}, nil
}

// putFicheLocked écrit une fiche dans la liste. fromArchive : une archive
// porte ce nom. Elle est d'abord ressortie, pour que la nouvelle version la
// remplace : laissée en place, la fiche existerait deux fois, et la prochaine
// à être archivée sous ce nom écraserait l'ancienne.
func putFicheLocked(f Fiche, fromArchive bool) error {
	if fromArchive {
		if err := os.Rename(fichePath(f.Name, true), fichePath(f.Name, false)); err != nil {
			return err
		}
	}
	return writeFicheLocked(f)
}

// saveFiche (outil fiche_save) crée ou remplace une fiche (même nom = nouvelle
// version, y compris d'une fiche archivée, qui revient dans la liste). Une
// NOUVELLE fiche qui couvre la même demande qu'une existante est refusée, sinon
// la mémoire se remplit de deux procédures pour la même chose et le modèle
// ouvre la mauvaise. Renvoie ce qui a été fait : sans « [ok] » si la fiche
// disait déjà ça, car rien n'est écrit.
func saveFiche(name, when, content string) (string, error) {
	name = normFicheName(name)
	mu.Lock()
	defer mu.Unlock()
	prev, exists := findFicheLocked(name)
	f, err := newVersion(name, when, content, prev)
	if err != nil {
		return "", err
	}
	if !exists {
		if dup, ok := similarFiche(name, f.When, fichesLocked(false)); ok {
			return "", fmt.Errorf("une fiche couvre déjà ça : « %s » (when: %s). Ne crée pas de doublon : complète-la (fiche_read puis fiche_patch, ou fiche_save sous le nom « %s »)", dup.Name, dup.When, dup.Name)
		}
	}
	if exists && !prev.Archived && prev.When == f.When && prev.Content == f.Content {
		return fmt.Sprintf("fiche « %s » déjà à jour : rien changé", name), nil
	}
	if err := putFicheLocked(f, prev.Archived); err != nil {
		return "", err
	}
	switch {
	case prev.Archived:
		logLocked(kindFiche, fmt.Sprintf("Fiche « %s » ressortie des archives et mise à jour (%s)", name, f.When))
		return tool.OK("fiche « %s » mise à jour (elle était archivée : elle est revenue dans ta liste)", name), nil
	case exists:
		logLocked(kindFiche, fmt.Sprintf("Fiche « %s » mise à jour (%s)", name, f.When))
		return tool.OK("fiche « %s » mise à jour", name), nil
	}
	logLocked(kindFiche, fmt.Sprintf("Fiche « %s » créée (%s)", name, f.When))
	return tool.OK("fiche « %s » enregistrée", name), nil
}

// UpdateFiche enregistre une fiche depuis l'interface : l'utilisateur décide,
// même d'une fiche proche d'une autre. oldName non vide et différent de name
// (une fois normalisés) = renommage : l'ancienne fiche n'est retirée qu'après
// l'écriture de la nouvelle, donc reste si celle-ci est refusée.
func UpdateFiche(oldName, name, when, content string) error {
	oldName, name = normFicheName(oldName), normFicheName(name)
	mu.Lock()
	defer mu.Unlock()
	prev, exists := findFicheLocked(name)
	renamed := false
	if oldName != "" && oldName != name {
		var old Fiche
		if old, renamed = findFicheLocked(oldName); renamed {
			if exists {
				return fmt.Errorf("une fiche « %s » existe déjà : choisis un autre nom", name)
			}
			prev = old
		}
	}
	f, err := newVersion(name, when, content, prev)
	if err != nil {
		return err
	}
	if err := putFicheLocked(f, exists && prev.Archived); err != nil {
		return err
	}
	switch {
	case renamed:
		if err := os.Remove(fichePath(prev.Name, prev.Archived)); err != nil {
			return err
		}
		renameUseLocked(prev.Name, name)
		noteRemovedLocked(ficheMark(prev.Name))
		logLocked(kindFiche, fmt.Sprintf("Fiche « %s » renommée « %s » (%s)", prev.Name, name, f.When))
	case exists:
		// Ce que l'utilisateur a ôté de sa main est ôté exprès : un sommeil en
		// cours n'a pas à le remettre.
		noteDroppedLocked(name, prev.Content)
		logLocked(kindFiche, fmt.Sprintf("Fiche « %s » mise à jour (%s)", name, f.When))
	default:
		logLocked(kindFiche, fmt.Sprintf("Fiche « %s » créée (%s)", name, f.When))
	}
	return nil
}

// ficheLimit : taille permise au contenu d'une fiche qui remplace old.
func ficheLimit(old Fiche) int { return min(max(ficheMaxChars, runeLen(old.Content)), mergeMaxChars) }

// DeleteFiche supprime pour de bon une fiche, ou à défaut son archive.
func DeleteFiche(name string) error {
	name = normFicheName(name)
	mu.Lock()
	defer mu.Unlock()
	f, ok := findFicheLocked(name)
	if !ok {
		return fmt.Errorf("fiche « %s » introuvable", name)
	}
	if err := os.Remove(fichePath(name, f.Archived)); err != nil {
		return err
	}
	noteRemovedLocked(ficheMark(name))
	logLocked(kindFiche, fmt.Sprintf("Fiche « %s » supprimée", name))
	return nil
}

// readFiche (outil fiche_read) : la fiche entière. Une fiche archivée qu'on
// rouvre revient dans la liste : si on en a de nouveau besoin, c'est qu'elle sert.
func readFiche(name string) (string, error) {
	name = normFicheName(name)
	mu.Lock()
	defer mu.Unlock()
	f, ok := loadFicheLocked(name, false)
	revived := false
	if !ok {
		if _, archived := loadFicheLocked(name, true); archived && os.Rename(fichePath(name, true), fichePath(name, false)) == nil {
			f, ok = loadFicheLocked(name, false)
			revived = ok
		}
	}
	if !ok {
		names := ficheNames(fichesLocked(false))
		if len(names) == 0 {
			return "", fmt.Errorf("fiche « %s » introuvable : aucune fiche enregistrée", name)
		}
		return "", fmt.Errorf("fiche « %s » introuvable (memory_search peut la retrouver sous un autre nom). Fiches : %s", name, strings.Join(names, ", "))
	}
	noteUseLocked(name)
	if revived {
		logLocked(kindFiche, fmt.Sprintf("Fiche « %s » ressortie des archives", name))
		return "(cette fiche était archivée faute de servir ; elle est revenue dans ta liste)\n" + f.show(), nil
	}
	return f.show(), nil
}

// patchFiche (outil fiche_patch) : remplacement exact dans une fiche. old doit
// y figurer une seule fois ; repl vide = suppression du passage. Retoucher
// évite de réécrire toute la fiche, où un modèle perd des lignes en recopiant.
func patchFiche(name, old, repl string) error {
	name = normFicheName(name)
	if old == "" {
		return errors.New("'old' vide : donne le passage exact à remplacer (pour ajouter un piège : memory_lesson avec fiche)")
	}
	if old == repl {
		return errors.New("'new' est identique à 'old' : rien à changer")
	}
	mu.Lock()
	defer mu.Unlock()
	f, ok := loadFicheLocked(name, false)
	if !ok {
		return fmt.Errorf("fiche « %s » introuvable", name)
	}
	limit := ficheLimit(f)
	switch n := strings.Count(f.Content, old); {
	case n == 0:
		return fmt.Errorf("passage introuvable dans « %s » : relis la fiche (fiche_read) et recopie-le exactement", name)
	case n > 1:
		return fmt.Errorf("passage présent %d fois dans « %s » : allonge 'old' pour qu'il soit unique", n, name)
	}
	f.Content = strings.TrimSpace(strings.Replace(f.Content, old, repl, 1))
	if f.Content == "" {
		return errors.New("la fiche serait vide : pour la retirer, memory_forget")
	}
	if n := runeLen(f.Content); n > limit {
		// Sans piste, le modèle raccourcit et rallonge la même fiche en boucle :
		// on lui dit de combien elle déborde et quoi faire à la place.
		return fmt.Errorf("fiche trop longue après modification (%d car., max %d, %d de trop) : n'essaie pas de la raccourcir pour faire de la place ; "+
			"mets ce nouveau contenu dans une fiche à part (fiche_save) et ajoute ici une ligne qui y renvoie", n, limit, n-limit)
	}
	if err := writeFicheLocked(f); err != nil {
		return err
	}
	noteDroppedLocked(name, old)
	logLocked(kindFiche, fmt.Sprintf("Fiche « %s » retouchée", name))
	return nil
}

// demoteHeadings descend d'un niveau les titres Markdown d'un texte. Les blocs
// de code sont laissés tels quels : « # … » y est un commentaire shell ou
// Python, pas un titre.
func demoteHeadings(content string) string {
	lines := strings.Split(content, "\n")
	fence := "" // la clôture qui a ouvert le bloc de code en cours
	for i, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case fence != "":
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
		case strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~"):
			fence = t[:3]
		case strings.HasPrefix(l, "#"):
			lines[i] = "#" + l
		}
	}
	return strings.Join(lines, "\n")
}

// mergeFiches (outil fiche_merge) : verse une fiche ENTIÈRE dans une autre,
// comme nouvelle section, puis archive la source. Rien ne se perd.
func mergeFiches(from, into string) error {
	from, into = normFicheName(from), normFicheName(into)
	if from == "" || into == "" || from == into {
		return errors.New("donne deux fiches différentes : from (absorbée) et into (référence)")
	}
	mu.Lock()
	defer mu.Unlock()
	src, ok := loadFicheLocked(from, false)
	if !ok {
		return fmt.Errorf("fiche « %s » introuvable", from)
	}
	dst, ok := loadFicheLocked(into, false)
	if !ok {
		return fmt.Errorf("fiche « %s » introuvable", into)
	}
	section := "\n\n## " + strings.ReplaceAll(from, "-", " ") + " (ex-fiche " + from + ")\n"
	if src.When != "" {
		section += "_Quand : " + src.When + "_\n\n"
	}
	// Les titres de la source deviennent des sous-sections de la nouvelle section.
	dst.Content += section + demoteHeadings(src.Content)
	// Les déclencheurs de la fiche absorbée rejoignent ceux de la référence :
	// sinon la question qui menait à elle ne mène plus nulle part.
	if src.When != "" && !strings.Contains(dst.When, src.When) {
		dst.When = clip(dst.When+" ; aussi : "+src.When, mergeWhenMaxChars)
	}
	if n := runeLen(dst.Content); n > mergeMaxChars {
		return fmt.Errorf("fusion trop longue (%d car., max %d) : resserre d'abord une des deux fiches", n, mergeMaxChars)
	}
	if err := writeFicheLocked(dst); err != nil {
		return err
	}
	logLocked(kindFiche, fmt.Sprintf("Fiche « %s » fusionnée dans « %s »", from, into))
	return archiveFicheLocked(from)
}

// archiveFiche sort une fiche de la liste sans la perdre : la recherche la
// trouve encore, et la rouvrir la ramène.
func archiveFiche(name string) error {
	mu.Lock()
	defer mu.Unlock()
	return archiveFicheLocked(normFicheName(name))
}

func archiveFicheLocked(name string) error {
	if _, ok := loadFicheLocked(name, false); !ok {
		return fmt.Errorf("fiche « %s » introuvable", name)
	}
	if err := os.MkdirAll(memPath(archivesDir), 0o755); err != nil {
		return err
	}
	as := freeArchiveNameLocked(name)
	if err := os.Rename(fichePath(name, false), fichePath(as, true)); err != nil {
		return err
	}
	noteRemovedLocked(ficheMark(name))
	if as != name {
		logLocked(kindFiche, fmt.Sprintf("Fiche « %s » archivée sous le nom « %s » (une archive « %s » existait déjà)", name, as, name))
	} else {
		logLocked(kindFiche, fmt.Sprintf("Fiche « %s » archivée", name))
	}
	return nil
}

// freeArchiveNameLocked : le nom sous lequel archiver une fiche sans rien
// écraser. Une archive qui porte déjà ce nom (posée à la main, ou revenue par
// une restauration) est une autre fiche, promise elle aussi à ne pas se
// perdre : la nouvelle prend alors un nom numéroté, libre aussi dans la liste
// pour que la rouvrir ne heurte rien.
func freeArchiveNameLocked(name string) string {
	exists := func(n string, archived bool) bool {
		_, err := os.Stat(fichePath(n, archived))
		return err == nil
	}
	as := name
	for n := 2; exists(as, true) || as != name && exists(as, false); n++ {
		suffix := fmt.Sprintf("-%d", n)
		as = strings.TrimRight(name[:min(len(name), ficheNameMax-len(suffix))], "-") + suffix
	}
	return as
}

// similarFiche : une fiche existante couvre-t-elle déjà la même demande ?
// Compare les mots importants du nom et du « quand ». Les mots présents dans la
// plupart des fiches (le prénom de l'utilisateur, « demande »…) ne comptent
// pas : ils ne distinguent rien.
func similarFiche(name, when string, fs []fiche) (Fiche, bool) {
	purpose := func(name, when string) map[string]bool {
		return words(strings.ReplaceAll(name, "-", " ") + " " + when)
	}
	sets := make([]map[string]bool, len(fs))
	freq := map[string]int{}
	for i, f := range fs {
		sets[i] = purpose(f.Name, f.When)
		for w := range sets[i] {
			freq[w]++
		}
	}
	distinctive := func(set map[string]bool) map[string]bool {
		out := map[string]bool{}
		for w := range set {
			if len(fs) < 3 || freq[w]*2 <= len(fs) {
				out[w] = true
			}
		}
		return out
	}
	mine := distinctive(purpose(name, when))
	for i, f := range fs {
		theirs := distinctive(sets[i])
		small, big := mine, theirs
		if len(small) > len(big) {
			small, big = big, small
		}
		if f.Name != name && len(small) >= 2 && overlap(small, big) >= 0.6 {
			return f.Fiche, true
		}
	}
	return Fiche{}, false
}
