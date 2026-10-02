package ajean

// chat_space.go — l'ESPACE de travail de l'agent : workspace + dossier de scripts.
//
// Le mode Jean a le sien, séparé de celui des projets. Sans ça, Jean modifiait les
// scripts d'un projet (ex. le script de la boîte mail) et la mémoire du projet ne
// correspondait plus au code quand on y revenait. Chaque espace est cloisonné dans
// les deux sens : Jean n'a pas accès au workspace ni aux scripts des projets, et
// les projets n'ont pas accès à ceux de Jean.
//
// L'espace voyage dans le contexte du tour (withJeanSpace dans runChat) : les
// outils write/edit/bash/see_image le lisent sans changer leur logique.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

type jeanSpaceKey struct{}

// withJeanSpace marque le contexte comme appartenant au mode Jean.
func withJeanSpace(ctx context.Context) context.Context {
	return context.WithValue(ctx, jeanSpaceKey{}, true)
}

func isJeanSpace(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(jeanSpaceKey{}).(bool)
	return v
}

// jeanWorkspace est voisin du workspace général (même parent, donc inscriptible
// là où celui-ci l'est).
func jeanWorkspace() string {
	ws := agentWorkspace()
	if ws == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(ws), "jean-workspace")
}

func jeanScriptsDir() string { return filepath.Join(AjeanHome(), "jean-scripts") }

func spaceWorkspace(ctx context.Context) string {
	if isJeanSpace(ctx) {
		return jeanWorkspace()
	}
	return agentWorkspace()
}

func spaceScripts(ctx context.Context) string {
	if isJeanSpace(ctx) {
		return jeanScriptsDir()
	}
	return scriptsDir()
}

// scriptsDirFor : dossier de scripts d'une tâche (Jean ou projets).
func scriptsDirFor(jean bool) string {
	if jean {
		return jeanScriptsDir()
	}
	return scriptsDir()
}

// otherSpaceDirs : les dossiers de l'AUTRE espace, interdits à celui-ci. Les
// pièces jointes (workspace/uploads) restent lisibles depuis Jean : c'est là que
// l'UI dépose les fichiers envoyés dans le chat, quel que soit le mode.
func otherSpaceDirs(ctx context.Context) []string {
	if isJeanSpace(ctx) {
		return []string{agentWorkspace(), scriptsDir()}
	}
	return []string{jeanWorkspace(), jeanScriptsDir()}
}

func spaceExempt(path string) bool {
	return underDir(path, filepath.Join(agentWorkspace(), "uploads"))
}

func spaceRefusal(ctx context.Context, d string) string {
	if isJeanSpace(ctx) {
		return fmt.Sprintf("[refusé] %s appartient aux projets AJEAN, pas au mode Jean. Travaille dans ton propre espace (%s, scripts dans %s).", d, jeanWorkspace(), jeanScriptsDir())
	}
	return fmt.Sprintf("[refusé] %s appartient au mode Jean, pas à ce projet.", d)
}

// guardSpacePath refuse un chemin (déjà résolu) situé dans l'autre espace.
func guardSpacePath(ctx context.Context, path string) string {
	if spaceExempt(path) {
		return ""
	}
	for _, d := range otherSpaceDirs(ctx) {
		if d != "" && underDir(path, d) {
			return spaceRefusal(ctx, d)
		}
	}
	return ""
}

// guardSpaceCommand refuse une commande shell qui cite un dossier de l'autre
// espace (même heuristique textuelle que guardToolOnlyCommand).
func guardSpaceCommand(ctx context.Context, command string) string {
	lc := strings.ToLower(command)
	// Jean : pas de remontée relative. Son workspace est voisin de celui des
	// projets, un « ../workspace » y menait tout droit sans citer le chemin
	// absolu. Il n'en a pas besoin : son espace s'adresse en chemins absolus.
	if isJeanSpace(ctx) && hasParentRef(command) {
		return fmt.Sprintf("[refusé] pas de « .. » dans tes commandes : utilise des chemins absolus dans ton espace (%s, scripts dans %s).", jeanWorkspace(), jeanScriptsDir())
	}
	up := strings.ToLower(normPath(filepath.Join(agentWorkspace(), "uploads")))
	for _, d := range otherSpaceDirs(ctx) {
		if d == "" {
			continue
		}
		nd := strings.ToLower(normPath(d))
		// Retire les mentions autorisées (pièces jointes) avant de chercher.
		if strings.Contains(strings.ReplaceAll(lc, up, ""), nd) {
			return spaceRefusal(ctx, d)
		}
	}
	return ""
}

// hasParentRef : la commande contient-elle un segment de chemin « .. » ?
func hasParentRef(command string) bool {
	f := func(r rune) bool {
		return r == '/' || r == '\\' || r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\'' || r == ';' || r == '&' || r == '|' || r == '=' || r == '(' || r == ')' || r == '<' || r == '>'
	}
	for _, seg := range strings.FieldsFunc(command, f) {
		if seg == ".." {
			return true
		}
	}
	return false
}
