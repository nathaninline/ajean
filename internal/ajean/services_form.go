package ajean

// Formulaire d'un service : la commande sur une ligne, le
// dossier, l'utilisateur, la description et la relance automatique. Ces champs
// sont enregistrés dans services.json, et l'unité de l'OS est réécrite d'après
// eux.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type ServiceForm struct {
	Command     string `json:"command"`
	WorkDir     string `json:"workdir"`
	User        string `json:"user"`
	Description string `json:"description"`
	Restart     bool   `json:"restart"`
}

func singleLine(s string) bool { return !strings.ContainsAny(s, "\r\n\x00") }

func (f *ServiceForm) Validate() error {
	f.Command = strings.TrimSpace(f.Command)
	f.WorkDir = strings.TrimSpace(f.WorkDir)
	f.User = strings.TrimSpace(f.User)
	f.Description = strings.TrimSpace(f.Description)
	if f.Command == "" || !singleLine(f.Command) {
		return errors.New("La commande doit tenir sur une seule ligne.")
	}
	if !singleLine(f.Description) {
		return errors.New("La description doit tenir sur une seule ligne.")
	}
	if f.User == "root" {
		return errors.New("Un service ne peut pas tourner en root depuis AJEAN.")
	}
	if f.User != "" && !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`).MatchString(f.User) {
		return errors.New("Nom d'utilisateur invalide.")
	}
	if f.WorkDir != "" {
		if !singleLine(f.WorkDir) || !filepath.IsAbs(f.WorkDir) {
			return errors.New("Le dossier doit être un chemin absolu.")
		}
		if st, err := os.Stat(f.WorkDir); err != nil || !st.IsDir() {
			return errors.New("Ce dossier n'existe pas.")
		}
	}
	return nil
}

var (
	cmdPortRe   = regexp.MustCompile(`(?:--port[ =]|-addr [\w.]*:)(\d+)`)
	cmdConfigRe = regexp.MustCompile(`--config[ =](\S+)`)
	cfgPortRe   = regexp.MustCompile(`(?m)^\s*"?port"?\s*[:=]\s*(\d+)`)
	cmdKeyRe    = regexp.MustCompile(`--api-key[ =](\S+)`)
	engineCmdRe = regexp.MustCompile(`llama-server|basalt|vllm|serve/server\.py|main\.py --config|\bfnx\b|\bmcpm\b`)
	errLineRe   = regexp.MustCompile(`(?i)error|failed|fatal|cannot|unable|no such file|not found|denied`)
)

// portFromCommand : le port lu dans la commande, ou dans le fichier --config.
func portFromCommand(cmd, workdir string) string {
	if m := cmdPortRe.FindStringSubmatch(cmd); m != nil {
		return m[1]
	}
	if m := cmdConfigRe.FindStringSubmatch(cmd); m != nil {
		p := strings.Trim(m[1], `"'`)
		if !filepath.IsAbs(p) && workdir != "" {
			p = filepath.Join(workdir, p)
		}
		if b, err := os.ReadFile(p); err == nil {
			if c := cfgPortRe.FindStringSubmatch(string(b)); c != nil {
				return c[1]
			}
		}
	}
	if engineCmdRe.MatchString(cmd) {
		return "8080" // port par défaut des moteurs
	}
	return ""
}

func keyFromCommand(cmd string) string {
	if m := cmdKeyRe.FindStringSubmatch(cmd); m != nil {
		return strings.Trim(m[1], `"'`)
	}
	return ""
}

// failureFromLog traduit la fin du journal en une phrase compréhensible.
func failureFromLog(text, port string) (reason, detail string) {
	// Seul le dernier lancement compte.
	if i := strings.LastIndex(text, "Started "); i >= 0 {
		text = text[i:]
	}
	low := strings.ToLower(text)
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if errLineRe.MatchString(l) && !strings.Contains(l, ".service:") {
			detail = l
		}
	}
	if len(detail) > 220 {
		detail = detail[:220]
	}
	switch {
	case strings.Contains(low, "couldn't bind") || strings.Contains(low, "address already in use"):
		if port != "" {
			if _, who := osPortOwner(port); who != "" {
				return fmt.Sprintf("Le port %s est déjà utilisé par %s.", port, who), detail
			}
		}
		return "Le port du service était déjà utilisé.", detail
	case strings.Contains(low, "out of memory") || strings.Contains(low, "failed to allocate") || strings.Contains(low, "cuda_error_out") || strings.Contains(low, "oom-kill"):
		return "Mémoire insuffisante pour ce modèle.", detail
	case strings.Contains(low, "command not found") || strings.Contains(low, "no such file"):
		return "Un programme ou un fichier de la commande est introuvable.", detail
	case strings.Contains(low, "permission denied"):
		return "Droits insuffisants sur un fichier de la commande.", detail
	case strings.Contains(low, "invalid argument") || strings.Contains(low, "unknown argument") || strings.Contains(low, "error: invalid"):
		return "Une option de la commande n'est pas reconnue.", detail
	}
	if detail != "" {
		return "Le service s'est arrêté sur une erreur.", detail
	}
	return "Le service s'est arrêté. Le journal en dit plus.", ""
}
