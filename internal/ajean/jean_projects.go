package ajean

// jean_projects.go — accès en LECTURE SEULE du mode Jean aux projets AJEAN.
//
// Jean a son propre espace (chat_space.go) et ne peut rien modifier chez les
// projets. Il doit pourtant pouvoir regarder comment un projet fonctionne (« va
// voir comment marchait la gestion des mails ») pour s'en inspirer ou refaire
// pareil chez lui. Ces outils lisent directement les fichiers, sans jamais écrire
// ni basculer le projet actif : aucune incidence sur les projets.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const jeanProjectFileMax = 24 << 10 // 24 Kio lus au plus par fichier

func jeanProjectTools() []Tool {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	obj := func(props map[string]any, req ...string) map[string]any {
		o := map[string]any{"type": "object", "properties": props}
		// Pas de « required: null » : les API OpenAI strictes (DeepSeek…) le
		// refusent (issue #106) ; llama.cpp seul le tolérait.
		if len(req) > 0 {
			o["required"] = req
		}
		return o
	}
	mk := func(name, desc string, params map[string]any) Tool {
		return Tool{Type: "function", Function: ToolFunction{Name: name, Description: desc, Parameters: params}}
	}
	// Un seul outil pour les trois lectures : elles servent rarement, trois
	// schémas coûtaient ~300 tokens à chaque requête.
	return []Tool{
		mk("jean_projects", "READ-ONLY look at the user's AJEAN projects. No argument: list them with their shared scripts. project (+ optional page): that project's memory index, or one page in full. path (scripts/... or workspace/...): read a file or list a folder of the projects' space. To reuse something, copy it into your own scripts folder.",
			obj(map[string]any{"project": str("Project slug or name"), "page": str("Memory page, e.g. mail-setup.md"), "path": str("scripts/... or workspace/...")})),
	}
}

func isJeanProjectTool(name string) bool {
	return name == "jean_projects" || name == "jean_project_mem" || name == "jean_project_file"
}

func jeanProjectToolCall(name string, args map[string]any) string {
	s := func(k string) string { v, _ := args[k].(string); return strings.TrimSpace(v) }
	switch name {
	case "jean_projects":
		// Les anciens noms restent compris : des fils déjà enregistrés les appellent.
		if s("path") != "" {
			return jeanProjectFile(s("path"))
		}
		if s("project") != "" {
			return jeanProjectMem(s("project"), s("page"))
		}
		return jeanListProjects()
	case "jean_project_mem":
		return jeanProjectMem(s("project"), s("page"))
	case "jean_project_file":
		return jeanProjectFile(s("path"))
	}
	return "[erreur] outil inconnu"
}

func jeanListProjects() string {
	var b strings.Builder
	b.WriteString("Projects (read-only):\n")
	for _, p := range listProjects() {
		fmt.Fprintf(&b, "- %s (%s)", p.Name, p.Slug)
		if p.Desc != "" {
			b.WriteString(": " + clipRunes(p.Desc, 200))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nShared scripts (scripts/):\n")
	list, _ := listScripts()
	if len(list) == 0 {
		b.WriteString("(none)\n")
	}
	for _, s := range list {
		fmt.Fprintf(&b, "- scripts/%s (%s)\n", s.Name, humanBytes(s.Size))
	}
	return b.String()
}

// jeanResolveProject accepte un slug ou un nom (insensible à la casse).
func jeanResolveProject(q string) (Project, bool) {
	lq := strings.ToLower(q)
	for _, p := range listProjects() {
		if strings.ToLower(p.Slug) == lq || strings.ToLower(p.Name) == lq || p.Slug == slugify(q) {
			return p, true
		}
	}
	return Project{}, false
}

func jeanProjectMem(project, page string) string {
	p, ok := jeanResolveProject(project)
	if !ok || p.Slug == jeanDirName {
		return "[erreur] projet introuvable : " + project + " (voir jean_projects)"
	}
	root := projectMemoryDir(p.Slug)
	if page == "" {
		var names []string
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
				return nil
			}
			if rel, e := filepath.Rel(root, path); e == nil {
				names = append(names, filepath.ToSlash(rel))
			}
			return nil
		})
		sort.Strings(names)
		var b strings.Builder
		fmt.Fprintf(&b, "Memory of project %s (read-only)\n", p.Name)
		if txt, err := jeanReadMemFile(filepath.Join(root, "MEMORY.md")); err == nil {
			b.WriteString("\nIndex:\n" + txt + "\n")
		}
		b.WriteString("\nPages:\n")
		if len(names) == 0 {
			b.WriteString("(none)\n")
		}
		for _, n := range names {
			b.WriteString("- " + n + "\n")
		}
		return b.String()
	}
	full, err := jeanSafeJoin(root, page)
	if err != nil {
		return "[erreur] " + err.Error()
	}
	if !strings.HasSuffix(strings.ToLower(full), ".md") {
		full += ".md"
	}
	txt, err := jeanReadMemFile(full)
	if err != nil {
		if err == errMemLocked {
			return "[erreur] mémoire verrouillée (chiffrée)"
		}
		return "[erreur] page introuvable : " + page
	}
	return txt
}

func jeanReadMemFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	b, err := decodeMemContent(raw)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// jeanSafeJoin joint rel sous root en refusant toute évasion.
func jeanSafeJoin(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return "", fmt.Errorf("chemin invalide : %s", rel)
	}
	full := filepath.Join(root, filepath.Clean(filepath.FromSlash(rel)))
	if !underDir(full, root) {
		return "", fmt.Errorf("chemin hors du dossier : %s", rel)
	}
	return full, nil
}

func jeanProjectFile(path string) string {
	p := strings.TrimPrefix(filepath.ToSlash(path), "./")
	var root, rel string
	switch {
	case p == "scripts" || strings.HasPrefix(p, "scripts/"):
		root, rel = scriptsDir(), strings.TrimPrefix(strings.TrimPrefix(p, "scripts"), "/")
	case p == "workspace" || strings.HasPrefix(p, "workspace/"):
		root, rel = agentWorkspace(), strings.TrimPrefix(strings.TrimPrefix(p, "workspace"), "/")
	default:
		return "[erreur] path doit commencer par scripts/ ou workspace/"
	}
	full := root
	if rel != "" {
		var err error
		if full, err = jeanSafeJoin(root, rel); err != nil {
			return "[erreur] " + err.Error()
		}
	}
	st, err := os.Stat(full)
	if err != nil {
		return "[erreur] introuvable : " + path
	}
	if st.IsDir() {
		ents, err := os.ReadDir(full)
		if err != nil {
			return "[erreur] " + err.Error()
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s (read-only):\n", p)
		for _, e := range ents {
			if e.IsDir() {
				b.WriteString("- " + e.Name() + "/\n")
			} else if fi, err := e.Info(); err == nil {
				fmt.Fprintf(&b, "- %s (%s)\n", e.Name(), humanBytes(fi.Size()))
			}
		}
		if len(ents) == 0 {
			b.WriteString("(vide)\n")
		}
		return b.String()
	}
	f, err := os.Open(full)
	if err != nil {
		return "[erreur] " + err.Error()
	}
	defer f.Close()
	buf := make([]byte, jeanProjectFileMax)
	n, _ := f.Read(buf)
	buf = buf[:n]
	if !utf8.Valid(buf) && n < jeanProjectFileMax {
		return fmt.Sprintf("[fichier binaire, %s, non affiché]", humanBytes(st.Size()))
	}
	out := string(buf)
	if st.Size() > jeanProjectFileMax {
		out += fmt.Sprintf("\n[... tronqué : %s au total]", humanBytes(st.Size()))
	}
	return out
}
