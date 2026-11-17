package scripts

import (
	"encoding/json"
	"fmt"
	"strings"

	"ajean/internal/tool"
)

// Tools : le seul outil que voit le modèle, par lequel passent tous les scripts.
func Tools() []tool.Def {
	return []tool.Def{{
		Name: "tools",
		Description: "Your own tools: scripts you write once and reuse. action=list (names and what they do), load (description, params and code of one), run (execute it with args), save (create or replace), delete. " +
			"A tool is JavaScript defining function run(args) that returns a string or an object. Synchronous code only: no require, import, fetch, async or setTimeout. " +
			"Available inside: ws.read(path), ws.write(path, text), ws.list(path), ws.exists(path), ws.remove(path), ws.move(from, to) (your workspace only); " +
			"http.get(url, headers), http.post(url, body, headers), http.request({method, url, headers, body}), each returning {status, body, type}; " +
			"sh.run(command, seconds) runs a real shell command (sh -c, PowerShell on Windows) starting in your workspace and returns {code, out} (stdout and stderr together): the command must be written in the tool's code: parameters may only insert simple values (no spaces or shell characters), and a parameter named cmd/command/shell is refused; JSON, Math, Date; console.log. " +
			"When a task needs commands, make one tool per domain (e.g. nas with action=disk|status|…, each action mapped to a command written in the code) and extend it with new actions, rather than improvising.",
		Params: tool.Params(map[string]any{
			"action":      tool.Enum("What to do", "list", "load", "run", "save", "delete"),
			"name":        tool.String("Tool name, e.g. meteo_ville (all actions except list)"),
			"args":        tool.Object("run: the tool's parameters"),
			"description": tool.String("save: one line, what it does and when to use it"),
			"params":      tool.Object("save: each parameter name with its description, e.g. {\"city\": \"City name\"}"),
			"code":        tool.String("save: the full JavaScript source"),
		}, "action"),
		Run: dispatch,
	}}
}

func dispatch(c tool.Call) string {
	switch strings.ToLower(c.Str("action")) {
	case "list", "":
		return listAll()
	case "load":
		return show(c.Str("name"))
	case "run":
		return execute(c.Str("name"), c.Obj("args"))
	case "save":
		params := map[string]string{}
		for k, v := range c.Obj("params") {
			params[k] = fmt.Sprint(v)
		}
		name, err := save(Script{Name: c.Str("name"), Description: c.Str("description"), Params: params, Code: c.Text("code")})
		return tool.Done(err, "outil « %s » enregistré. Teste-le maintenant : tools action=run name=%s", name, name)
	case "delete":
		return tool.Done(Delete(c.Str("name")), "outil supprimé")
	}
	return tool.Err("action inconnue : list, load, run, save ou delete")
}

func listAll() string {
	all := List()
	if len(all) == 0 {
		return "(aucun outil pour l'instant : action=save pour en créer un)"
	}
	lines := make([]string, len(all))
	for i, s := range all {
		lines[i] = "- " + s.Name + ": " + s.Description
	}
	return strings.Join(lines, "\n")
}

func show(name string) string {
	s, err := load(name)
	if err != nil {
		return tool.Err("%v", err)
	}
	params, err := json.Marshal(s.Params)
	if err != nil {
		return tool.Err("%v", err)
	}
	return "# " + s.Name + "\n" + s.Description + "\nparams: " + string(params) + "\n\n" + s.Code
}

func execute(name string, args map[string]any) string {
	s, err := load(name)
	if err != nil {
		return tool.Err("%v", err)
	}
	out, err := run(s, args)
	if err != nil {
		msg := tool.Err("%v\n(corrige le script : action=load pour le relire, action=save pour le remplacer)", err)
		if out != "" {
			msg += "\nÉcrit avant l'erreur :\n" + out
		}
		return msg
	}
	if out == "" {
		return "(l'outil n'a rien renvoyé : run(args) doit faire return)"
	}
	return out
}
