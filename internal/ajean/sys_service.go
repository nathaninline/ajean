package ajean

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// sys_service.go holds the platform-neutral pieces of service management. The
// actual start/stop/restart/status/logs implementation is platform-specific:
//   - sys_service_linux.go   → systemd (systemctl/journalctl)
//   - sys_service_darwin.go  → launchd (launchctl)
//   - sys_service_windows.go → PID-file background process supervisor
//
// editConfig and showVram live here because they work the same everywhere.

// preflightEngine vérifie ce sans quoi le moteur ne PEUT pas démarrer, avant de
// lancer le service. Sinon llama-server sortait en erreur, systemd le relançait
// toutes les 3 s, et `ajean start` affichait un « activating » rassurant pendant
// que `ajean test` répondait « /health ne répond pas ». Le diagnostic n'était
// visible que dans le journal.
func preflightEngine() error {
	cfg := ReadConfig()
	bin := strings.TrimSpace(cfg["BIN"])
	if bin == "" {
		return fmt.Errorf("BIN non défini — installe un moteur : %s (ou renseigne BIN avec %s)",
			bold("ajean llamacpp install"), bold("ajean edit"))
	}
	if !filepath.IsAbs(bin) {
		bin = filepath.Join(AjeanHome(), bin)
	}
	if _, err := os.Stat(prebuiltResolveBin(bin)); err != nil {
		return fmt.Errorf("moteur introuvable : %s — relance %s", bin, bold("ajean llamacpp install"))
	}
	model := strings.TrimSpace(cfg["MODEL"])
	if model == "" {
		return fmt.Errorf("MODEL non défini — indique un .gguf avec %s (dossier des modèles : %s)",
			bold("ajean edit"), modelsDir())
	}
	p, err := resolveServeModelPath(model)
	if err != nil {
		return fmt.Errorf("MODEL=%s : %w", model, err)
	}
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("modèle introuvable : %s — corrige MODEL avec %s", p, bold("ajean edit"))
	}
	// Modèle en plusieurs fichiers : une tranche manquante ne se voit qu'au moment
	// où llama-server réclame un tenseur absent, dans le journal du service.
	if missing := shardFamilyMissing(filepath.Dir(p), filepath.Base(p)); len(missing) > 0 {
		return fmt.Errorf("modèle incomplet : il manque %s dans %s — ce modèle tient en %d fichiers, télécharge-les tous",
			strings.Join(missing, ", "), filepath.Dir(p), len(shardFamily(filepath.Base(p))))
	}
	return nil
}

// configTemplate est le squelette commenté proposé par `ajean edit` : la
// configuration vit en base, donc sur une installation neuve le fichier
// temporaire était QUASI VIDE — impossible de deviner quoi écrire. On y déroule
// donc les clés utiles avec leur rôle, les valeurs déjà définies telles quelles,
// les autres commentées.
var configTemplate = []struct{ key, help string }{
	{"BIN", "chemin de llama-server (posé par « ajean llamacpp install »)"},
	{"MODEL", "nom de fichier .gguf ou chemin complet"},
	{"HOST", "adresse d'écoute du moteur (défaut 0.0.0.0)"},
	{"PORT", "port du moteur (défaut 8080)"},
	{"CTX", "taille du contexte (défaut 32768)"},
	{"NGL", "couches déportées sur le GPU (défaut 999 = tout)"},
	{"BATCH", "batch (défaut 2048)"},
	{"UBATCH", "micro-batch (défaut 512)"},
	{"THREADS", "threads CPU, 0 = auto"},
	{"THREADS_BATCH", "threads CPU du prefill, 0 = auto"},
	{"KV_TYPE", "quantization du cache KV (q8_0, q4_0…) ; KV_TYPE_K / KV_TYPE_V pour les séparer"},
	{"REASONING", "passthrough du mode raisonnement (on/auto/deepseek)"},
	{"REASONING_BUDGET", "plafond de tokens de réflexion ; -1 = illimité"},
	{"COMPACT", "compactage automatique du contexte (off pour couper)"},
	{"MEM_MODE", "mémoire de l'IA : off / ondemand / always"},
	{"EXTRA_ARGS", "ajouté tel quel à la ligne de commande de llama-server"},
}

// configEditorText rend la configuration au format présenté dans $EDITOR.
func configEditorText(cfg map[string]string) string {
	var b strings.Builder
	b.WriteString("# Configuration du moteur AJEAN (ajean-engine).\n")
	b.WriteString("# Une clé par ligne : CLE=valeur. Les lignes commentées (#) sont ignorées :\n")
	b.WriteString("# décommente celles dont tu as besoin. « ajean restart » applique.\n\n")
	seen := map[string]bool{}
	for _, f := range configTemplate {
		seen[f.key] = true
		fmt.Fprintf(&b, "# %s\n", f.help)
		if v, ok := cfg[f.key]; ok && v != "" {
			fmt.Fprintf(&b, "%s=%s\n\n", f.key, quoteValue(v))
		} else {
			fmt.Fprintf(&b, "#%s=\n\n", f.key)
		}
	}
	// Tout ce que le squelette ne connaît pas (clés d'une version plus récente,
	// réglages posés par l'UI) : conservé tel quel, en fin de fichier.
	rest := map[string]string{}
	for k, v := range cfg {
		if !seen[k] {
			rest[k] = v
		}
	}
	if len(rest) > 0 {
		b.WriteString("# --- autres clés déjà définies ---\n")
		b.WriteString(formatEnv(rest))
	}
	return b.String()
}

// editConfig ouvre la configuration dans $EDITOR. La configuration vit en base
// (voir store.go) : on la déroule dans un fichier temporaire au format clé=valeur,
// on laisse l'éditeur faire son travail, puis on relit. Le contenu n'est réécrit
// que si l'éditeur sort proprement — un éditeur avorté ne doit rien effacer.
func editConfig() error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = defaultEditor()
	}
	tmp, err := os.CreateTemp("", "ajean-config-*.env")
	if err != nil {
		return err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.WriteString(configEditorText(ReadConfig())); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	cmd := exec.Command(editor, path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := WriteConfig(parseEnv(string(b))); err != nil {
		return err
	}
	fmt.Println(dim("[info] ajean restart pour appliquer"))
	return nil
}

// showVram parses `nvidia-smi --query-gpu=...` and renders a colored bar.
// nvidia-smi is available on both Linux and Windows when an NVIDIA driver is
// installed, so this is platform-neutral.
func showVram() error {
	out, err := hideCmd(exec.Command("nvidia-smi",
		"--query-gpu=name,memory.used,memory.total,utilization.gpu,temperature.gpu",
		"--format=csv,noheader,nounits")).Output()
	if err != nil {
		return fmt.Errorf("nvidia-smi indisponible: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) != 5 {
			continue
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		name := parts[0]
		used, _ := strconv.Atoi(parts[1])
		total, _ := strconv.Atoi(parts[2])
		util, _ := strconv.Atoi(parts[3])
		temp, _ := strconv.Atoi(parts[4])
		pct := 0
		if total > 0 {
			pct = used * 100 / total
		}
		full := pct / 5
		bar := strings.Repeat("█", full) + strings.Repeat("░", 20-full)
		fmt.Printf("\n  %s\n", cyan(name))
		fmt.Printf("  VRAM  %s  %3d%%   %.1f / %.1f GiB\n", green(bar), pct, float64(used)/1024, float64(total)/1024)
		fmt.Printf("  GPU   %3d%%      Temp  %d°C\n\n", util, temp)
	}
	return nil
}
