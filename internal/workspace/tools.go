package workspace

import (
	"fmt"
	"strings"

	"ajean/internal/tool"
)

// maxReadChars : au-delà, ws_read tronque pour ne pas noyer le contexte.
const maxReadChars = 60000

// Tools : les outils de fichiers donnés au modèle.
func Tools() []tool.Def {
	const rel = "Path relative to your workspace, e.g. notes/todo.md"
	return []tool.Def{
		{
			Name:        "ws_list",
			Description: "List a folder of your workspace (the only place on disk you can access).",
			Params:      tool.Params(map[string]any{"path": tool.String("Folder, relative to your workspace; empty = its root")}),
			Run:         list,
		},
		{
			Name:        "ws_read",
			Description: "Read a text file of your workspace.",
			Params:      tool.Params(map[string]any{"path": tool.String(rel)}, "path"),
			Run:         read,
		},
		{
			Name:        "ws_write",
			Description: "Create or REPLACE a text file in your workspace (folders are created as needed).",
			Params:      tool.Params(map[string]any{"path": tool.String(rel), "content": tool.String("Full content of the file")}, "path", "content"),
			Run: func(c tool.Call) string {
				return tool.Done(Write(c.Str("path"), c.Text("content")), "fichier écrit")
			},
		},
		{
			Name:        "ws_move",
			Description: "Move or rename a file or folder inside your workspace.",
			Params:      tool.Params(map[string]any{"from": tool.String(rel), "to": tool.String(rel)}, "from", "to"),
			Run: func(c tool.Call) string {
				return tool.Done(Move(c.Str("from"), c.Str("to")), "déplacé")
			},
		},
		{
			Name:        "ws_delete",
			Description: "Delete a file or folder of your workspace.",
			Params:      tool.Params(map[string]any{"path": tool.String(rel)}, "path"),
			Run: func(c tool.Call) string {
				return tool.Done(Delete(c.Str("path")), "supprimé")
			},
		},
	}
}

func list(c tool.Call) string {
	entries, err := List(c.Str("path"))
	if err != nil {
		return tool.Err("%v", err)
	}
	if len(entries) == 0 {
		return "(dossier vide)"
	}
	return strings.Join(entries, "\n")
}

func read(c tool.Call) string {
	text, err := Read(c.Str("path"))
	if err != nil {
		return tool.Err("%v", err)
	}
	if text == "" {
		return "(fichier vide)"
	}
	if r := []rune(text); len(r) > maxReadChars {
		return string(r[:maxReadChars]) + fmt.Sprintf("\n[… tronqué : %d caractères sur %d]", maxReadChars, len(r))
	}
	return text
}
