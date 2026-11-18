// Package tool définit ce qu'est un outil pour AJEAN : un nom, une description
// et des paramètres (ce que voit le modèle), et une fonction (ce qui s'exécute).
// Chaque domaine (mémoire, fichiers, rappels, navigateur…) expose ses outils
// sous cette forme ; l'agent les rassemble.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ajean/internal/llm"
)

// Call : un appel d'outil par le modèle.
type Call struct {
	Args map[string]any
	Conv string // conversation d'où vient l'appel ("" pour un travail de fond)
	Ctx  context.Context
	// Unattended : personne ne regarde (tâche planifiée).
	Unattended bool
}

// Context : le contexte de l'appel, annulé quand l'utilisateur arrête le tour.
func (c Call) Context() context.Context {
	if c.Ctx != nil {
		return c.Ctx
	}
	return context.Background()
}

// Def : un outil.
type Def struct {
	Name        string
	Description string         // pour le modèle, en anglais
	Params      map[string]any // schéma JSON des paramètres (voir Params)
	Run         func(Call) string
}

// Spec : la déclaration de l'outil au format de l'API.
func (d Def) Spec() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.Function{Name: d.Name, Description: d.Description, Parameters: d.Params}}
}

// ---- construire un schéma -------------------------------------------------------

// Params : un schéma d'objet. Pas de « required » vide : les API strictes le refusent.
func Params(props map[string]any, required ...string) map[string]any {
	p := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		p["required"] = required
	}
	return p
}

func String(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func Integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func Boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func Object(desc string) map[string]any { return map[string]any{"type": "object", "description": desc} }

// Enum : une chaîne limitée à quelques valeurs.
func Enum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

// ---- lire les arguments ---------------------------------------------------------

// Str : un argument texte, espaces de bord ôtés ("" s'il manque).
func (c Call) Str(key string) string { return strings.TrimSpace(c.Text(key)) }

// Text : un argument texte tel quel (contenu de fichier, passage exact à remplacer).
func (c Call) Text(key string) string {
	v, _ := c.Args[key].(string)
	return v
}

// Int : un argument entier. Les modèles l'envoient parfois comme texte.
func (c Call) Int(key string) (int, bool) {
	switch v := c.Args[key].(type) {
	case float64:
		return int(v), true
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil {
			return n, true
		}
	}
	return 0, false
}

// Bool : un argument booléen (false s'il manque).
func (c Call) Bool(key string) bool {
	switch v := c.Args[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// Obj : un argument objet. Les modèles l'envoient parfois comme texte JSON.
func (c Call) Obj(key string) map[string]any {
	switch v := c.Args[key].(type) {
	case map[string]any:
		return v
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(v), &m) == nil {
			return m
		}
	}
	return nil
}

// ---- écrire un résultat ---------------------------------------------------------

// Les résultats sont du texte. Deux préfixes ont un sens pour le reste du
// programme : « [ok] » (écriture réussie) et « [erreur] ».
const (
	okPrefix  = "[ok] "
	errPrefix = "[erreur] "
)

func OK(format string, a ...any) string  { return okPrefix + fmt.Sprintf(format, a...) }
func Err(format string, a ...any) string { return errPrefix + fmt.Sprintf(format, a...) }

// Result : le résultat d'une opération qui renvoie (texte, erreur).
func Result(out string, err error) string {
	if err != nil {
		return errPrefix + err.Error()
	}
	return out
}

// Done : le résultat d'une opération qui ne renvoie qu'une erreur.
func Done(err error, format string, a ...any) string {
	if err != nil {
		return errPrefix + err.Error()
	}
	return OK(format, a...)
}

func IsOK(result string) bool  { return strings.HasPrefix(result, okPrefix) }
func IsErr(result string) bool { return strings.HasPrefix(result, errPrefix) }
