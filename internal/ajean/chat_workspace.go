package ajean

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Dossier de travail du mode agent.
//
// Sans ça, les outils write/edit/bash héritent du répertoire courant du PROCESSUS :
// le modèle écrit "meteo.json", et le fichier atterrit là d'où l'utilisateur a
// lancé jean — le Bureau quand on double-clique l'installateur, C:\ProgramData\jean\bin
// quand on lance le binaire installé. Personne ne s'attend à voir un chat déposer
// des fichiers sur son Bureau. On donne donc à l'agent un dossier à lui : les
// chemins relatifs y sont résolus, et le shell y démarre. Les chemins ABSOLUS
// restent honorés tels quels — quand l'utilisateur demande d'écrire dans un
// dossier précis, ça doit marcher.

const workspaceEnv = "JEAN_WORKSPACE"

var (
	workspaceOnce sync.Once
	workspacePath string
)

// agentWorkspace renvoie le dossier de travail de l'agent, créé au besoin. Il
// essaie plusieurs emplacements car AjeanHome() vaut %ProgramData%\jean sous
// Windows, qui n'est pas inscriptible par un utilisateur non administrateur.
func agentWorkspace() string {
	workspaceOnce.Do(func() {
		for _, dir := range workspaceCandidates() {
			if dir == "" {
				continue
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				continue
			}
			// MkdirAll réussit sur un dossier existant même non inscriptible :
			// on vérifie l'écriture réelle.
			f, err := os.CreateTemp(dir, ".probe-*")
			if err != nil {
				continue
			}
			name := f.Name()
			f.Close()
			os.Remove(name)
			workspacePath = dir
			return
		}
		// Dernier recours : le répertoire courant, comportement historique.
		workspacePath, _ = os.Getwd()
	})
	return workspacePath
}

func workspaceCandidates() []string {
	var c []string
	if v := strings.TrimSpace(os.Getenv(workspaceEnv)); v != "" {
		c = append(c, v)
	}
	c = append(c, filepath.Join(AjeanHome(), "workspace"))
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		c = append(c, filepath.Join(home, "jean", "workspace"))
	}
	c = append(c, filepath.Join(os.TempDir(), "jean-workspace"))
	return c
}

// resolveAgentPath résout un chemin fourni par le modèle. Absolu → inchangé ;
// "~/x" → dans le home de l'utilisateur ; relatif → dans le workspace.
func resolveAgentPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(p[1:], "/")))
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(agentWorkspace(), filepath.FromSlash(p))
}
