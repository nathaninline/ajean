// Package apps gère les petites applications web que l'assistant crée pour
// l'utilisateur (« crée-moi une app qui… »). Une app, c'est trois fichiers :
// un fragment HTML, une feuille de style et un script.
//
//	APPS/<id>/
//	  app.json            nom, description, icône, version courante, outils
//	                      déclarés, dates, historique des versions
//	  versions/<n>/       index.html, style.css, app.js (jamais modifiés ensuite)
//	  data/storage.json   les données de l'app
//	  data/errors.json    ses dernières erreurs JavaScript
//
// Une app est enfermée : la page d'AJEAN l'affiche dans une iframe sans origine,
// à partir du document que fabrique Page, dont la politique de sécurité coupe
// tout accès au réseau. Ce qu'elle fait d'autre (garder des données, lancer un
// outil de l'assistant, interroger le modèle) passe par un pont que Bridge
// contrôle demande par demande.
//
// Rien ne se perd : chaque enregistrement crée une nouvelle version et les
// précédentes restent. Le modèle ne passe que par l'outil « app » (Tools), qui
// ne sait pas supprimer : c'est un geste de l'utilisateur.
package apps

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ajean/internal/home"
	"ajean/internal/icons"
	"ajean/internal/scripts"
)

const (
	maxVersions    = 20        // versions gardées par app
	maxFile        = 200 << 10 // octets par fichier d'une app
	maxName        = 60        // caractères
	maxDescription = 300
	maxNote        = 200
	maxTools       = 20 // outils déclarés par app
	maxSlug        = 40 // longueur d'un identifiant dérivé d'un nom
)

var (
	idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	// Sous Windows, un dossier portant un de ces noms désigne un périphérique.
	deviceRe = regexp.MustCompile(`^(con|prn|aux|nul|com[0-9]|lpt[0-9])$`)

	// mu protège tout ce qui vit sous APPS/. Il n'est jamais tenu pendant qu'un
	// outil ou le modèle travaille.
	mu sync.Mutex

	// now : l'horloge, remplaçable pour dater des versions sans attendre.
	now = time.Now
)

// Changed est appelée quand la liste des apps ou une app change (l'interface
// se rafraîchit). Posée par le paquet agent au démarrage.
var Changed func()

func notify() {
	if Changed != nil {
		Changed()
	}
}

// App : une application, telle que la voient l'interface et le modèle.
type App struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Icon        string    `json:"icon"`  // nom d'une icône du jeu commun (paquet icons)
	Color       string    `json:"color"` // couleur choisie par l'assistant, #rrggbb ("" : teinte neutre)
	Version     int       `json:"version"`
	Tools       []string  `json:"tools"` // outils de l'assistant que l'app a le droit de lancer
	Updated     time.Time `json:"updated"`
	Errors      int       `json:"errors"` // erreurs JavaScript en attente de correction
}

// Version : un enregistrement passé d'une app.
type Version struct {
	N    int       `json:"version"`
	At   time.Time `json:"at"`
	Note string    `json:"note"` // ce qui a changé, en une ligne (donné par l'assistant)
}

// manifest : le contenu d'app.json.
type manifest struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Icon        string    `json:"icon"`  // nom d'une icône du jeu commun (paquet icons)
	Color       string    `json:"color"` // couleur choisie par l'assistant, #rrggbb ("" : teinte neutre)
	Version     int       `json:"version"`
	Tools       []string  `json:"tools"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
	History     []Version `json:"history"` // versions gardées, la plus ancienne d'abord
}

// source : les trois fichiers d'une version.
type source struct{ html, css, js string }

// change : ce qu'un enregistrement apporte. Un texte vide garde l'existant.
type change struct {
	name, description, icon, color, note string
	html, css, js                        string
	tools                                []string // nil : garder ceux de l'app
}

// ---- emplacements ---------------------------------------------------------------

// validID : l'identifiant peut-il servir de nom de dossier ? Les identifiants
// viennent du modèle et de l'interface : aucun chemin n'est construit avant ce
// contrôle, qui exclut d'office « .. » et les séparateurs.
func validID(id string) bool { return idRe.MatchString(id) && !deviceRe.MatchString(id) }

func dir(id string) string { return filepath.Join(home.Apps(), id) }

func versionDir(id string, n int) string {
	return filepath.Join(dir(id), "versions", strconv.Itoa(n))
}

func dataFile(id, name string) string { return filepath.Join(dir(id), "data", name) }

// folds ramène les lettres accentuées courantes à leur lettre de base.
var folds = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "á", "a", "ã", "a", "å", "a", "æ", "ae",
	"ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "í", "i", "ì", "i", "ñ", "n",
	"ô", "o", "ö", "o", "ó", "o", "ò", "o", "õ", "o", "œ", "oe",
	"ù", "u", "û", "u", "ü", "u", "ú", "u", "ÿ", "y", "ý", "y", "ß", "ss",
)

// slug dérive un identifiant du nom d'une app : minuscules, sans accents, un
// tiret à la place de tout le reste.
func slug(name string) string {
	var b strings.Builder
	gap := false
	for _, r := range folds.Replace(strings.ToLower(name)) {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteByte('-')
		}
		gap = false
		b.WriteRune(r)
	}
	id := b.String()
	if len(id) > maxSlug {
		id = strings.TrimRight(id[:maxSlug], "-")
	}
	if id == "" {
		return "app"
	}
	if deviceRe.MatchString(id) {
		return "app-" + id
	}
	return id
}

// freeID : l'identifiant dérivé du nom, suivi d'un numéro s'il est déjà pris.
func freeID(name string) string {
	base := slug(name)
	id := base
	for n := 2; ; n++ {
		if _, err := os.Lstat(dir(id)); errors.Is(err, os.ErrNotExist) {
			return id
		}
		id = base + "-" + strconv.Itoa(n)
	}
}

// ---- lecture et écriture ----------------------------------------------------------

func load(id string) (manifest, error) {
	var m manifest
	if !validID(id) {
		return m, fmt.Errorf("identifiant d'app invalide : %q", id)
	}
	raw, err := os.ReadFile(filepath.Join(dir(id), "app.json"))
	if errors.Is(err, os.ErrNotExist) {
		return m, fmt.Errorf("app « %s » introuvable", id)
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("app « %s » : app.json illisible (%w)", id, err)
	}
	return m, nil
}

func (m manifest) app(id string) App {
	return App{
		ID: id, Name: m.Name, Description: m.Description, Icon: icons.Icon(m.Icon), Color: m.Color, Version: m.Version,
		Tools: append([]string{}, m.Tools...), Updated: m.Updated, Errors: len(loadErrors(id)),
	}
}

func readSource(id string, n int) (source, error) {
	var s source
	for name, dst := range map[string]*string{"index.html": &s.html, "style.css": &s.css, "app.js": &s.js} {
		raw, err := os.ReadFile(filepath.Join(versionDir(id, n), name))
		if err != nil {
			return s, fmt.Errorf("app « %s », version %d : %s illisible (%w)", id, n, name, err)
		}
		*dst = string(raw)
	}
	return s, nil
}

// commit écrit une nouvelle version, puis app.json : c'est cette dernière
// écriture qui la rend courante, une coupure avant elle ne change rien à l'app.
func commit(id string, m manifest, s source, note string) (manifest, error) {
	m.Version++
	m.Updated = now()
	if m.Created.IsZero() {
		m.Created = m.Updated
	}
	m.History = append(m.History, Version{N: m.Version, At: m.Updated, Note: note})
	var dropped []Version
	if extra := len(m.History) - maxVersions; extra > 0 {
		dropped, m.History = m.History[:extra], m.History[extra:]
	}
	for name, text := range map[string]string{"index.html": s.html, "style.css": s.css, "app.js": s.js} {
		if err := home.WriteFile(filepath.Join(versionDir(id, m.Version), name), []byte(text)); err != nil {
			return m, err
		}
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	if err := home.WriteFile(filepath.Join(dir(id), "app.json"), raw); err != nil {
		return m, err
	}
	for _, v := range dropped {
		// Un dossier qui résiste n'est plus référencé nulle part : il ne gêne
		// que par la place qu'il occupe.
		_ = os.RemoveAll(versionDir(id, v.N))
	}
	return m, nil
}

// ---- ce que voit l'interface ------------------------------------------------------

// List : toutes les apps, les plus récemment modifiées d'abord.
func List() []App {
	mu.Lock()
	defer mu.Unlock()
	entries, _ := os.ReadDir(home.Apps())
	out := []App{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if m, err := load(e.Name()); err == nil {
			out = append(out, m.app(e.Name()))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Updated.Equal(out[j].Updated) {
			return out[i].Updated.After(out[j].Updated)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Delete supprime une app, ses versions et ses données.
func Delete(id string) (err error) {
	mu.Lock()
	defer func() {
		mu.Unlock()
		if err == nil {
			notify()
		}
	}()
	if _, err := load(id); err != nil {
		return err
	}
	return os.RemoveAll(dir(id))
}

// Versions : les versions gardées d'une app, la plus récente d'abord.
func Versions(id string) ([]Version, error) {
	mu.Lock()
	defer mu.Unlock()
	m, err := load(id)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(m.History)
	slices.Reverse(out)
	return out, nil
}

// Rollback refait d'une ancienne version la version courante, en en créant une
// nouvelle qui la recopie : celle qu'on quitte reste dans l'historique.
func Rollback(id string, version int) (err error) {
	mu.Lock()
	defer func() {
		mu.Unlock()
		if err == nil {
			notify()
		}
	}()
	m, err := load(id)
	if err != nil {
		return err
	}
	if version == m.Version {
		return fmt.Errorf("la version %d est déjà la version courante", version)
	}
	if !slices.ContainsFunc(m.History, func(v Version) bool { return v.N == version }) {
		return fmt.Errorf("version %d introuvable pour l'app « %s »", version, id)
	}
	s, err := readSource(id, version)
	if err != nil {
		return err
	}
	_, err = commit(id, m, s, fmt.Sprintf("retour à la version %d", version))
	return err
}

// ---- ce que fait l'outil du modèle ------------------------------------------------

// save crée une app (id vide) ou en enregistre une nouvelle version. Rend
// l'app et les avertissements des vérifications.
func save(id string, c change) (app App, warnings []string, err error) {
	mu.Lock()
	defer func() {
		mu.Unlock()
		if err == nil {
			notify()
		}
	}()
	var m manifest
	var s source
	if id == "" {
		if c.name == "" {
			return app, nil, errors.New("'name' obligatoire pour créer une app ('id' pour en modifier une)")
		}
		if strings.TrimSpace(c.html) == "" {
			return app, nil, errors.New("'html' obligatoire pour créer une app")
		}
		id = freeID(c.name)
		m.Icon = icons.Default
	} else {
		if m, err = load(id); err != nil {
			return app, nil, err
		}
		if s, err = readSource(id, m.Version); err != nil {
			return app, nil, err
		}
	}
	if c.name != "" {
		m.Name = c.name
	}
	if c.description != "" {
		m.Description = c.description
	}
	if len([]rune(m.Name)) > maxName || len([]rune(m.Description)) > maxDescription {
		return app, nil, fmt.Errorf("nom (max %d caractères) ou description (max %d) trop longs", maxName, maxDescription)
	}
	if c.icon != "" {
		if m.Icon = icons.Icon(c.icon); m.Icon != strings.ToLower(c.icon) {
			warnings = append(warnings, "'icon' inconnu : choisis un nom de la liste (voir la description de l'outil).")
		}
	}
	if c.color != "" {
		if m.Color = icons.Color(c.color); m.Color == "" {
			warnings = append(warnings, "'color' ignoré : une couleur s'écrit #rrggbb.")
		}
	}
	if c.tools != nil {
		if len(c.tools) > maxTools {
			return app, nil, fmt.Errorf("trop d'outils déclarés (%d, max %d)", len(c.tools), maxTools)
		}
		if missing := missingTools(c.tools); len(missing) > 0 {
			return app, nil, fmt.Errorf("outil déclaré inexistant : %s. Crée-le et teste-le d'abord avec l'outil tools, puis enregistre l'app", strings.Join(missing, ", "))
		}
		m.Tools = c.tools
	}
	replace(&s.html, c.html)
	replace(&s.css, c.css)
	replace(&s.js, c.js)
	app, more, err := store(id, m, s, c.note)
	return app, append(warnings, more...), err
}

// replace : un fichier fourni remplace celui de la version précédente, un
// fichier absent le garde.
func replace(dst *string, text string) {
	if strings.TrimSpace(text) != "" {
		*dst = text
	}
}

// edit remplace un passage exact et unique d'un fichier de la version courante.
func edit(id, file, old, replacement, note string) (app App, warnings []string, err error) {
	mu.Lock()
	defer func() {
		mu.Unlock()
		if err == nil {
			notify()
		}
	}()
	m, err := load(id)
	if err != nil {
		return app, nil, err
	}
	s, err := readSource(id, m.Version)
	if err != nil {
		return app, nil, err
	}
	target, ok := map[string]*string{"html": &s.html, "css": &s.css, "js": &s.js}[file]
	if !ok {
		return app, nil, errors.New("'file' doit valoir html, css ou js")
	}
	if old == "" {
		return app, nil, errors.New("'old' obligatoire : le passage exact à remplacer")
	}
	switch n := strings.Count(*target, old); {
	case n == 0:
		return app, nil, errors.New("passage introuvable : 'old' doit reprendre le texte du fichier à l'identique, espaces compris (action=read pour le relire)")
	case n > 1:
		return app, nil, fmt.Errorf("passage présent %d fois : allonge 'old' pour qu'il soit unique", n)
	case old == replacement:
		return app, nil, errors.New("'old' et 'new' sont identiques : rien à changer")
	}
	*target = strings.Replace(*target, old, replacement, 1)
	return store(id, m, s, note)
}

// store vérifie les fichiers puis les enregistre comme nouvelle version.
func store(id string, m manifest, s source, note string) (App, []string, error) {
	if strings.TrimSpace(s.html) == "" {
		return App{}, nil, errors.New("index.html serait vide")
	}
	warnings, err := check(s)
	if err != nil {
		return App{}, nil, err
	}
	// Un outil supprimé depuis sa déclaration ne bloque pas l'enregistrement :
	// l'app n'aurait plus aucun moyen d'être corrigée.
	for _, name := range missingTools(m.Tools) {
		warnings = append(warnings, fmt.Sprintf("L'outil déclaré « %s » n'existe plus : ajean.tools.run(%q) échouera. Recrée-le, ou enregistre l'app avec une liste 'tools' à jour.", name, name))
	}
	m, err = commit(id, m, s, note)
	if err != nil {
		return App{}, nil, err
	}
	return m.app(id), warnings, nil
}

// missingTools : ceux des noms donnés qui ne sont pas des outils de l'assistant.
func missingTools(names []string) []string {
	known := map[string]bool{}
	for _, s := range scripts.List() {
		known[s.Name] = true
	}
	var missing []string
	for _, name := range names {
		if !known[name] {
			missing = append(missing, name)
		}
	}
	return missing
}
