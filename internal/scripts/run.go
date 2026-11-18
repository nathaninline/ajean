package scripts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"

	"ajean/internal/secrets"
	"ajean/internal/workspace"
)

const (
	maxOutput = 20000 // caractères renvoyés au modèle
	maxDepth  = 2000  // appels imbriqués : une récursion sans fin s'arrête avant de remplir la mémoire

	shellLimit = 10 * time.Minute // un outil qui lance des commandes peut travailler plus longtemps
)

// timeout : durée maximale d'une exécution. Une variable, pour que les tests
// n'attendent pas trente secondes.
var timeout = 30 * time.Second

// run exécute un script dans un interpréteur neuf, borné dans le temps. Le
// texte rendu réunit ce que le script a écrit avec console.log et ce que
// run(args) a renvoyé ; en cas d'erreur, il garde ce qui a été écrit avant.
func run(s Script, args map[string]any) (string, error) {
	return runCtx(context.Background(), s, args)
}

// runCtx : run, interrompu avec le contexte parent.
func runCtx(parent context.Context, s Script, args map[string]any) (string, error) {
	limit := timeout
	if strings.Contains(s.Code, "sh.run") {
		limit = max(timeout, shellLimit)
	}
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	vm := goja.New()
	vm.SetMaxCallStackSize(maxDepth)
	// Le contexte coupe les requêtes web en cours ; l'interruption, elle,
	// arrête le code JavaScript lui-même (une boucle sans fin, un arrêt demandé).
	stop := context.AfterFunc(ctx, func() { vm.Interrupt("temps dépassé ou arrêt demandé") })
	defer stop()

	var logs strings.Builder
	return runIn(ctx, vm, s, args, &logs)
}

func runIn(ctx context.Context, vm *goja.Runtime, s Script, args map[string]any, logs *strings.Builder) (string, error) {
	if err := provide(ctx, vm, logs, args); err != nil {
		return "", err
	}
	if _, err := vm.RunScript(s.Name+".js", s.Code); err != nil {
		return output(logs, nil), explain(err)
	}
	// Par une expression plutôt que par l'objet global : « const run = … » ne
	// crée pas de propriété globale.
	entry, err := vm.RunString("typeof run === 'function' ? run : undefined")
	if err != nil {
		return output(logs, nil), explain(err)
	}
	fn, ok := goja.AssertFunction(entry)
	if !ok {
		return output(logs, nil), errors.New("le script ne définit pas function run(args)")
	}
	if args == nil {
		args = map[string]any{}
	}
	res, err := fn(goja.Undefined(), vm.ToValue(args))
	if err != nil {
		return output(logs, nil), explain(err)
	}
	return output(logs, res), nil
}

// provide installe dans l'interpréteur tout ce qu'un script a le droit
// d'utiliser. Ce qui n'est pas ici n'existe pas pour lui.
func provide(ctx context.Context, vm *goja.Runtime, logs *strings.Builder, args map[string]any) error {
	log := func(call goja.FunctionCall) goja.Value {
		if logs.Len() < maxOutput {
			parts := make([]string, len(call.Arguments))
			for i, a := range call.Arguments {
				parts[i] = text(a)
			}
			logs.WriteString(strings.Join(parts, " ") + "\n")
		}
		return goja.Undefined()
	}
	// Une erreur Go devient une exception JavaScript, que le script peut attraper.
	reply := func(v map[string]any, err error) goja.Value {
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(v)
	}
	object := func(v goja.Value) map[string]any {
		if absent(v) {
			return nil
		}
		m, _ := v.Export().(map[string]any)
		return m
	}
	globals := map[string]map[string]any{
		"console": {"log": log, "error": log, "warn": log, "info": log},
		"ws": {
			"read":   workspace.Read,
			"write":  workspace.Write,
			"list":   workspace.List,
			"exists": workspace.Exists,
			"remove": workspace.Delete,
			"move":   workspace.Move,
		},
		"sh": {
			"run": func(c goja.FunctionCall) goja.Value {
				secs := 0
				if !absent(c.Argument(1)) {
					secs = int(c.Argument(1).ToInteger())
				}
				command := text(c.Argument(0))
				if err := checkArgs(command, args); err != nil {
					panic(vm.NewGoError(err))
				}
				return reply(shell(ctx, command, secs))
			},
		},
		"http": {
			"get": func(c goja.FunctionCall) goja.Value {
				return reply(fetch(ctx, "GET", text(c.Argument(0)), "", object(c.Argument(1))))
			},
			"post": func(c goja.FunctionCall) goja.Value {
				return reply(fetch(ctx, "POST", text(c.Argument(0)), text(c.Argument(1)), object(c.Argument(2))))
			},
			"request": func(c goja.FunctionCall) goja.Value {
				o := object(c.Argument(0))
				method, _ := o["method"].(string)
				if method == "" {
					method = "GET"
				}
				url, _ := o["url"].(string)
				body, _ := o["body"].(string)
				headers, _ := o["headers"].(map[string]any)
				return reply(fetch(ctx, method, url, body, headers))
			},
		},
		"secrets": {
			"get": func(c goja.FunctionCall) goja.Value {
				v, err := secrets.Get(text(c.Argument(0)))
				if err != nil {
					panic(vm.NewGoError(err))
				}
				return vm.ToValue(v)
			},
		},
	}
	for name, v := range globals {
		if err := vm.Set(name, v); err != nil {
			return err
		}
	}
	return nil
}

func absent(v goja.Value) bool { return v == nil || goja.IsUndefined(v) || goja.IsNull(v) }

// text : une valeur JavaScript en texte (les objets et tableaux en JSON).
func text(v goja.Value) string {
	if absent(v) {
		return ""
	}
	switch x := v.Export().(type) {
	case string:
		return x
	case map[string]any, []any:
		if b, err := json.MarshalIndent(x, "", "  "); err == nil {
			return string(b)
		}
	}
	return v.String()
}

// output assemble les lignes de console.log et la valeur renvoyée, le tout borné.
func output(logs *strings.Builder, res goja.Value) string {
	out := strings.TrimSpace(logs.String())
	if r := text(res); r != "" {
		if out != "" {
			out += "\n"
		}
		out += r
	}
	if r := []rune(out); len(r) > maxOutput {
		out = string(r[:maxOutput]) + fmt.Sprintf("\n[… tronqué : %d caractères sur %d]", maxOutput, len(r))
	}
	return out
}

// explain rend lisible une erreur de l'interpréteur.
func explain(err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		return fmt.Errorf("arrêté : %v (max %s)", interrupted.Value(), timeout)
	}
	var thrown *goja.Exception
	if errors.As(err, &thrown) {
		return errors.New(thrown.String())
	}
	return err
}
