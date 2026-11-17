package scripts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"ajean/internal/home"
)

const maxShellOutput = 1 << 20 // octets gardés de la sortie d'une commande

// shell lance une commande pour un script : sh -c sur Linux et macOS,
// PowerShell sur Windows. Elle part du WORKSPACE (son dossier de travail), mais
// rien ne l'y enferme : c'est un vrai shell, avec les droits du programme.
// Elle s'arrête avec le script (contexte) ou au bout de secs secondes.
func shell(ctx context.Context, command string, secs int) (map[string]any, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, errors.New("commande vide")
	}
	if secs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(secs)*time.Second)
		defer cancel()
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command)
	}
	dir := home.Workspace()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	cmd.Dir = dir
	cmd.Stdin = nil
	cmd.WaitDelay = 2 * time.Second // un enfant qui garde la sortie ouverte ne bloque pas la fin
	var out capped
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		switch {
		case ctx.Err() != nil:
			return map[string]any{"code": -1, "out": out.String() + "\n[arrêtée : temps dépassé]"}, nil
		case errors.As(err, &ee):
			code = ee.ExitCode()
		default:
			return nil, err
		}
	}
	return map[string]any{"code": code, "out": out.String()}, nil
}

// capped : un tampon qui cesse de grandir au-delà de maxShellOutput.
type capped struct{ bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxShellOutput - c.Len(); room > 0 {
		if len(p) > room {
			c.Buffer.Write(p[:room])
		} else {
			c.Buffer.Write(p)
		}
	}
	return len(p), nil
}

// Un outil lance des commandes PRÉDÉFINIES : écrites dans son code. Ses
// paramètres peuvent y glisser une valeur simple (un nom, un chemin, un nombre),
// jamais un morceau de commande. Sinon un outil « cmd » deviendrait un shell
// libre par lequel tout passerait.

var simpleValue = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}._@:+=,/-]{0,127}$`)

// checkArgs refuse une commande qui contient une valeur reçue en paramètre
// autre qu'une valeur simple (espaces, ; | & $ ` ' " > <, option commençant par -).
func checkArgs(command string, args map[string]any) error {
	for _, v := range flatten(args) {
		v = strings.TrimSpace(v)
		if v == "" || simpleValue.MatchString(v) {
			continue
		}
		if strings.Contains(command, v) {
			return fmt.Errorf("commande refusée : le paramètre %q y entre tel quel. Un outil lance des commandes écrites dans son code ; un paramètre ne peut y apporter qu'une valeur simple (nom, chemin, nombre, sans espace ni caractère spécial)", v)
		}
	}
	return nil
}

func flatten(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case map[string]any:
		var out []string
		for _, e := range x {
			out = append(out, flatten(e)...)
		}
		return out
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, flatten(e)...)
		}
		return out
	}
	return nil
}

// commandParam : un nom de paramètre qui annonce une commande libre.
func commandParam(name string) bool {
	switch strings.ToLower(strings.Trim(name, "_- ")) {
	case "cmd", "command", "commande", "commandline", "command_line", "shell", "sh", "bash", "script", "exec", "execute", "run", "powershell", "code", "raw":
		return true
	}
	return false
}
