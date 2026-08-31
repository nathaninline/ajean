package ajean

// projects.go — les PROJETS d'AJEAN. Un projet cloisonne une mémoire (un dossier
// de pages .md + un index MEMORY.md, façon Claude Code) ET son propre jeu de
// sessions de chat. Fini la mémoire globale fourre-tout : on crée un projet par
// chantier, chacun avec sa mémoire et ses conversations indépendantes.
//
// Sur disque : memory/<slug>/*.md (+ MEMORY.md). Le `slug` est l'identité STABLE
// (il ne bouge jamais, même à un renommage) ; le `Name` est le libellé affiché.
// memoryDir() (run.go) pointe sur le dossier du projet ACTIF, donc tout le
// code mémoire existant (mem_io, chat_memory, snapshots) hérite du cloisonnement
// sans le savoir. Les sessions sont tagguées d'un champ Project et filtrées à
// l'affichage (chat_history.go).
//
// Le registre (liste + projet actif) vit dans le bucket state, EN CLAIR : ce sont
// des noms de projets et un slug, pas des données de conversation.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Project décrit un projet. CreatedAt sert au tri (plus ancien d'abord : le projet
// « Générale » historique reste en tête).
type Project struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

const (
	defaultProjectSlug = "generale"
	defaultProjectName = "Générale"
	stProjectsKey      = "projects"       // bkState : []Project (JSON, clair)
	stActiveProjectKey = "active_project" // bkState : slug du projet actif
)

// projectsRoot est le dossier racine de la mémoire, qui contient un sous-dossier
// par projet (memory/<slug>/...). C'est l'ancien dossier `memory` plat, promu en
// racine de projets.
func projectsRoot() string { return filepath.Join(AjeanHome(), "memory") }

// projectMemoryDir renvoie le dossier mémoire d'un projet donné (par slug).
func projectMemoryDir(slug string) string {
	if slug == "" {
		slug = defaultProjectSlug
	}
	return filepath.Join(projectsRoot(), slug)
}

// --- projet actif (avec cache RAM + override pour les tâches) ------------------

var (
	activeProjMu    sync.Mutex
	activeProjCache string // "" = pas encore lu ; sinon slug mémorisé

	projOverrideMu sync.Mutex
	projOverride   string // non vide = force le projet actif (exécution d'une tâche)
)

// activeProjectSlug renvoie le slug du projet actif. Priorité : override d'exécution
// de tâche (RAM), puis cache, puis la valeur persistée, puis le défaut. Lecture PURE :
// ne crée jamais de dossier (l'amorçage se fait dans ensureDefaultProject au boot).
func activeProjectSlug() string {
	projOverrideMu.Lock()
	o := projOverride
	projOverrideMu.Unlock()
	if o != "" {
		return o
	}
	activeProjMu.Lock()
	defer activeProjMu.Unlock()
	if activeProjCache != "" {
		return activeProjCache
	}
	s := strings.TrimSpace(getStr(bkState, stActiveProjectKey))
	if s == "" {
		s = defaultProjectSlug
	}
	activeProjCache = s
	return s
}

// setActiveProject persiste le projet actif et rafraîchit le cache.
func setActiveProject(slug string) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return fmt.Errorf("slug vide")
	}
	if err := putStr(bkState, stActiveProjectKey, slug); err != nil {
		return err
	}
	activeProjMu.Lock()
	activeProjCache = slug
	activeProjMu.Unlock()
	return nil
}

// setProjectOverride force (ou libère avec "") le projet actif VU PAR memoryDir(),
// le temps de l'exécution d'une tâche planifiée sur un projet précis. Sans effet
// sur le projet actif persisté / l'UI. Sûr car une seule inférence tourne à la
// fois (gate de génération), donc pas de course avec un tour utilisateur.
func setProjectOverride(slug string) {
	projOverrideMu.Lock()
	projOverride = slug
	projOverrideMu.Unlock()
}

// --- registre -----------------------------------------------------------------

// listProjects renvoie les projets connus, le plus ancien d'abord. Garantit au
// moins le projet par défaut (base neuve).
func listProjects() []Project {
	var out []Project
	if b := getBytes(bkState, stProjectsKey); len(b) > 0 {
		_ = json.Unmarshal(b, &out)
	}
	if len(out) == 0 {
		out = []Project{{Slug: defaultProjectSlug, Name: defaultProjectName, CreatedAt: time.Now().UnixMilli()}}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

func saveProjects(list []Project) error {
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return putBytes(bkState, stProjectsKey, b)
}

// projectExists indique si un slug correspond à un projet connu.
func projectExists(slug string) bool {
	for _, p := range listProjects() {
		if p.Slug == slug {
			return true
		}
	}
	return false
}

// projectName renvoie le libellé affiché d'un slug (le slug lui-même à défaut).
func projectName(slug string) string {
	for _, p := range listProjects() {
		if p.Slug == slug {
			return p.Name
		}
	}
	return slug
}

var slugStripRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify réduit un nom à un slug de fichier sûr (minuscules, alphanum + tirets).
// Les accents courants sont translittérés pour ne pas tout perdre.
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	repl := map[rune]string{'à': "a", 'â': "a", 'ä': "a", 'á': "a", 'ã': "a",
		'é': "e", 'è': "e", 'ê': "e", 'ë': "e", 'î': "i", 'ï': "i", 'í': "i",
		'ô': "o", 'ö': "o", 'ó': "o", 'õ': "o", 'ù': "u", 'û': "u", 'ü': "u", 'ú': "u",
		'ç': "c", 'ñ': "n"}
	var b strings.Builder
	for _, r := range s {
		if v, ok := repl[r]; ok {
			b.WriteString(v)
		} else {
			b.WriteRune(r)
		}
	}
	slug := slugStripRe.ReplaceAllString(b.String(), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	return slug
}

// memorySeed est le contenu initial de l'index MEMORY.md d'un nouveau projet.
func memorySeed(name string) string {
	return "# Index mémoire — " + name + "\n\n" +
		"Une ligne par page de ce projet (`- [titre](fichier.md) — accroche`). " +
		"Tiens cet index à jour quand tu ajoutes, modifies ou supprimes une page.\n"
}

// createProject crée un projet à partir d'un nom d'affichage : slug unique, dossier
// memory, index MEMORY.md amorcé. Renvoie le projet créé.
func createProject(name string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, fmt.Errorf("nom vide")
	}
	if len([]rune(name)) > 60 {
		name = string([]rune(name)[:60])
	}
	base := slugify(name)
	if base == "" {
		base = "projet"
	}
	// Slug unique : suffixe -2, -3… en cas de collision.
	list := listProjects()
	taken := map[string]bool{}
	for _, p := range list {
		taken[p.Slug] = true
	}
	slug := base
	for i := 2; taken[slug]; i++ {
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	dir := projectMemoryDir(slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Project{}, err
	}
	// Index MEMORY.md (respecte le chiffrement via writeMemFile, qui écrit dans le
	// projet actif) : on écrit directement le fichier, en clair — un nouveau projet
	// n'a pas encore de raison d'être chiffré page par page, et le chiffrement
	// global le reprendra au besoin.
	idx := filepath.Join(dir, "MEMORY.md")
	if _, err := os.Stat(idx); os.IsNotExist(err) {
		_ = os.WriteFile(idx, []byte(memorySeed(name)), 0o600)
	}
	p := Project{Slug: slug, Name: name, CreatedAt: time.Now().UnixMilli()}
	list = append(list, p)
	if err := saveProjects(list); err != nil {
		return Project{}, err
	}
	return p, nil
}

// renameProject change le LIBELLÉ d'un projet (le slug, les chemins et les sessions
// ne bougent pas).
func renameProject(slug, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("nom vide")
	}
	if len([]rune(name)) > 60 {
		name = string([]rune(name)[:60])
	}
	list := listProjects()
	found := false
	for i := range list {
		if list[i].Slug == slug {
			list[i].Name = name
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("projet introuvable")
	}
	return saveProjects(list)
}

// deleteProject supprime un projet : son dossier mémoire ET ses sessions. Refuse
// de supprimer le dernier projet restant. Si c'était le projet actif, on rebascule
// sur un autre (voir web_projects.go pour la conversation).
func deleteProject(slug string) error {
	list := listProjects()
	if len(list) <= 1 {
		return fmt.Errorf("impossible de supprimer le dernier projet")
	}
	kept := make([]Project, 0, len(list))
	found := false
	for _, p := range list {
		if p.Slug == slug {
			found = true
			continue
		}
		kept = append(kept, p)
	}
	if !found {
		return fmt.Errorf("projet introuvable")
	}
	// Sessions du projet.
	for _, m := range listArchivesForProject(slug) {
		_ = deleteArchive(m.ID)
	}
	// Dossier disque du projet.
	_ = os.RemoveAll(projectMemoryDir(slug))
	if err := saveProjects(kept); err != nil {
		return err
	}
	// Réassigne l'actif si nécessaire.
	if activeProjectSlug() == slug {
		_ = setActiveProject(kept[0].Slug)
	}
	return nil
}

// ensureDefaultProject amorce le projet « Générale » et MIGRE l'existant (mémoire
// plate + sessions + keyvault) au premier démarrage de cette version. Idempotent.
func ensureDefaultProject() {
	// 1. Le registre PERSISTÉ contient-il le projet par défaut ? On lit le brut (pas
	//    listProjects, qui synthétise toujours un défaut) pour savoir s'il faut écrire.
	var stored []Project
	if b := getBytes(bkState, stProjectsKey); len(b) > 0 {
		_ = json.Unmarshal(b, &stored)
	}
	hasDefault := false
	for _, p := range stored {
		if p.Slug == defaultProjectSlug {
			hasDefault = true
			break
		}
	}
	if !hasDefault {
		// Base neuve OU migration : on inscrit « Générale » en tête et on persiste.
		stored = append([]Project{{Slug: defaultProjectSlug, Name: defaultProjectName, CreatedAt: time.Now().UnixMilli()}}, stored...)
		_ = saveProjects(stored)
	}

	genMem := projectMemoryDir(defaultProjectSlug)
	_ = os.MkdirAll(genMem, 0o755)

	// 2. Migration de la mémoire plate historique (AJEAN_HOME/memory/*) vers le
	//    dossier du projet Générale. Les pages plates vivaient à la RACINE de memory/,
	//    qui accueille désormais les sous-dossiers de projet : on ne déplace donc que
	//    les FICHIERS restés à la racine (les sous-dossiers, déjà des projets, sont
	//    ignorés par le `e.IsDir()` ci-dessous).
	oldMem := projectsRoot()
	if oldMem != genMem {
		if entries, err := os.ReadDir(oldMem); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				src := filepath.Join(oldMem, name)
				// Le keyvault mémoire devient GLOBAL (AJEAN_HOME/.keyvault) : la DEK est
				// unique pour tous les projets.
				if name == ".keyvault" {
					dst := filepath.Join(AjeanHome(), ".keyvault")
					if _, err := os.Stat(dst); os.IsNotExist(err) {
						_ = os.Rename(src, dst)
					}
					continue
				}
				dst := filepath.Join(genMem, name)
				if _, err := os.Stat(dst); os.IsNotExist(err) {
					_ = os.Rename(src, dst)
				}
			}
		}
	}

	// 3. Index MEMORY.md du projet Générale s'il manque.
	idx := filepath.Join(genMem, "MEMORY.md")
	if _, err := os.Stat(idx); os.IsNotExist(err) {
		_ = os.WriteFile(idx, []byte(memorySeed(defaultProjectName)), 0o600)
	}

	// 4. Tag des sessions historiques sans projet → Générale.
	tagOrphanArchives(defaultProjectSlug)

	// 5. Projet actif par défaut.
	if strings.TrimSpace(getStr(bkState, stActiveProjectKey)) == "" {
		_ = setActiveProject(defaultProjectSlug)
	}
}
