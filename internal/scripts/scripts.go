// Package scripts gère les outils que l'assistant se fabrique lui-même. Chacun
// est un script JavaScript rangé dans TOOLS/<nom>.js : une première ligne
// « /* TOOL {json} */ » (description et paramètres), puis le code, qui définit
// function run(args).
//
// Ces scripts ne sont PAS envoyés au modèle à chaque tour : seul l'outil
// « tools » est toujours là, et c'est par lui que l'assistant liste, charge,
// lance, enregistre ou supprime un script. Mille outils ne coûtent donc rien
// au contexte.
//
// Ils tournent dans un interpréteur intégré au programme (goja) : rien à
// installer sur la machine, et surtout aucun accès au système. Un script ne
// peut toucher QUE ce qu'on lui donne :
//
//	args                                              les paramètres reçus
//	ws.read / write / list / exists / remove / move   les fichiers du workspace
//	http.get / post / request                         le web, pas la machine locale
//	console.log                                       texte ajouté au résultat
package scripts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/dop251/goja"

	"ajean/internal/home"
)

const (
	maxCode = 40000 // caractères d'un script
	header  = "/* TOOL "
)

var (
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,39}$`)
	runRe  = regexp.MustCompile(`\brun\b`)

	// mu : home.WriteFile passe par un fichier temporaire au nom fixe, deux
	// enregistrements simultanés du même script s'y marcheraient dessus.
	mu sync.Mutex
)

// Script : un outil fabriqué par l'assistant.
type Script struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Params      map[string]string `json:"params"` // nom du paramètre → ce qu'il veut dire
	Code        string            `json:"code"`
}

// meta : ce que porte la première ligne du fichier.
type meta struct {
	Description string            `json:"description"`
	Params      map[string]string `json:"params"`
}

// normalize rapproche un nom donné par le modèle de la forme attendue
// (« Météo-Ville » ne passera pas, « meteo ville » si).
func normalize(name string) (string, error) {
	name = strings.NewReplacer(" ", "_", "-", "_", ".", "_").Replace(strings.ToLower(strings.TrimSpace(name)))
	name = strings.Trim(name, "_")
	if !nameRe.MatchString(name) {
		return "", errors.New("nom invalide : court, en minuscules sans accent, chiffres et _ (ex. meteo_ville)")
	}
	return name, nil
}

func file(name string) string { return filepath.Join(home.Tools(), name+".js") }

func parse(name, src string) Script {
	s := Script{Name: name, Params: map[string]string{}}
	src = strings.ReplaceAll(src, "\r\n", "\n")
	head, rest, _ := strings.Cut(src, "\n")
	raw, ok := strings.CutPrefix(head, header)
	if !ok {
		s.Code = src // fichier posé à la main, sans en-tête
		return s
	}
	var m meta
	if json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimSpace(raw), "*/")), &m) == nil {
		s.Description = m.Description
		if m.Params != nil {
			s.Params = m.Params
		}
	}
	s.Code = strings.TrimLeft(rest, "\n")
	return s
}

// List : tous les scripts, par ordre alphabétique.
func List() []Script {
	entries, _ := os.ReadDir(home.Tools())
	var out []Script
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".js")
		if e.IsDir() || !ok || !nameRe.MatchString(name) {
			continue
		}
		if raw, err := os.ReadFile(file(name)); err == nil {
			out = append(out, parse(name, string(raw)))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func load(name string) (Script, error) {
	name, err := normalize(name)
	if err != nil {
		return Script{}, err
	}
	raw, err := os.ReadFile(file(name))
	if err != nil {
		return Script{}, fmt.Errorf("outil « %s » introuvable (action=list donne les noms)", name)
	}
	return parse(name, string(raw)), nil
}

// oneLine tient un texte sur la ligne d'en-tête sans pouvoir fermer son commentaire.
func oneLine(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "*/", "* /")
}

// save enregistre un script (création ou remplacement) et rend son nom
// normalisé. Un script qui ne compile pas est refusé : mieux vaut l'apprendre
// à l'écriture qu'au premier lancement, des semaines plus tard.
func save(s Script) (string, error) {
	name, err := normalize(s.Name)
	if err != nil {
		return "", err
	}
	m := meta{Description: oneLine(s.Description), Params: map[string]string{}}
	if m.Description == "" {
		return "", errors.New("'description' obligatoire : une ligne qui dit ce que fait l'outil et quand s'en servir")
	}
	for k, v := range s.Params {
		if commandParam(k) {
			return "", fmt.Errorf("paramètre « %s » refusé : un outil lance des commandes écrites dans son code, jamais une commande reçue en paramètre. Fais un outil par tâche précise", k)
		}
		m.Params[k] = oneLine(v)
	}
	code := strings.TrimSpace(strings.ReplaceAll(s.Code, "\r\n", "\n"))
	if code == "" {
		return "", errors.New("'code' vide")
	}
	if n := len([]rune(code)); n > maxCode {
		return "", fmt.Errorf("script trop long (%d caractères, max %d)", n, maxCode)
	}
	if _, err := goja.Compile(name+".js", code, false); err != nil {
		return "", fmt.Errorf("le script ne se compile pas : %v", err)
	}
	if !runRe.MatchString(code) {
		return "", errors.New("le script doit définir function run(args) { … return résultat }")
	}
	// Le même code sous un autre nom : un doublon, qui encombrerait la liste. On
	// renvoie vers l'outil existant (à modifier avec save sous son nom).
	for _, other := range List() {
		if other.Name != name && strings.TrimSpace(strings.ReplaceAll(other.Code, "\r\n", "\n")) == code {
			return "", fmt.Errorf("cet outil existe déjà sous le nom « %s » : modifie celui-là (même nom) au lieu d'en créer un second", other.Name)
		}
	}
	head, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	mu.Lock()
	defer mu.Unlock()
	return name, home.WriteFile(file(name), []byte(header+string(head)+" */\n"+code+"\n"))
}

// Delete supprime un script.
func Delete(name string) error {
	name, err := normalize(name)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	if err := os.Remove(file(name)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("outil « %s » introuvable", name)
		}
		return err
	}
	return nil
}

// Run exécute l'outil name hors d'un tour du modèle : une tâche planifiée ou
// une app s'en servent pour travailler sans consommer un seul jeton.
func Run(name string, args map[string]any) (string, error) {
	s, err := load(name)
	if err != nil {
		return "", err
	}
	return run(s, args)
}
