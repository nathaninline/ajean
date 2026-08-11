// Package nodeclient est le CLIENT léger « poste distant ». Il tourne sur un PC
// secondaire, s'appaire une fois avec le serveur ajean, puis ouvre une connexion
// SORTANTE et exécute — sous contrôle local — les demandes de l'IA.
//
// Volontairement autonome : dépend seulement de nodewire (protocole partagé,
// zéro dépendance) et de coder/websocket. AUCUN import d'ajean → binaire de
// quelques Mo, sans UI ni moteur.
//
// Sécurité côté poste : allowlist locale des capacités, confinement des fichiers
// à un dossier racine, confirmation des actions à effet de bord (sauf --yes).
package nodeclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/nathaninline/ajean/internal/nodewire"
)

const (
	defaultTimeoutSec = 30
	maxTimeoutSec     = 300
	maxOutput         = 8000   // caractères de stdout/stderr renvoyés
	readMax           = 100000 // caractères max d'une lecture de fichier
)

// Config persiste l'appairage et les préférences locales du poste.
type Config struct {
	ServerURL string   `json:"server_url"`
	ID        string   `json:"id"`
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	Caps      []string `json:"caps"`
	Root      string   `json:"root"`
	AutoYes   bool     `json:"auto_yes"`
}

// dataDir est le dossier de données du poste. Sur Windows : C:\ProgramData\
// ajean-remote — MACHINE-WIDE, pour que le service (qui tourne en LocalSystem) lise
// la MÊME config que celle écrite par `install` (le profil utilisateur de
// LocalSystem est différent du tien). Ailleurs : dossier de config utilisateur.
func dataDir() string {
	if runtime.GOOS == "windows" {
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "ajean-remote")
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir, _ = os.Getwd()
	}
	return filepath.Join(dir, "ajean-remote")
}

// ConfigPath renvoie le chemin du fichier de configuration.
func ConfigPath() string { return filepath.Join(dataDir(), "config.json") }

// InstalledExePath est l'emplacement stable où `install` copie le binaire, pour
// qu'il survive à la suppression du téléchargement.
func InstalledExePath() string {
	name := "ajean-remote"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dataDir(), name)
}

// DataDir est exporté pour les installateurs par plateforme.
func DataDir() string { return dataDir() }

// SanitizeCaps filtre une liste de capacités (réexport de nodewire pour la CLI).
func SanitizeCaps(caps []string) []string { return nodewire.SanitizeCaps(caps) }

func LoadConfig() (Config, error) {
	var c Config
	b, err := os.ReadFile(ConfigPath())
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func SaveConfig(c Config) error {
	p := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0o600) // 0600 : la clé d'appareil est un secret
}

// Enroll échange un code d'appairage contre une clé d'appareil et complète cfg.
func Enroll(cfg *Config, code string) error {
	name, _ := os.Hostname()
	body, _ := json.Marshal(map[string]string{
		"code": code, "name": name, "os": runtime.GOOS + "/" + runtime.GOARCH,
	})
	endpoint := strings.TrimRight(cfg.ServerURL, "/") + "/api/node/enroll"
	req, _ := http.NewRequest("POST", endpoint, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("appairage: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		OK   bool     `json:"ok"`
		ID   string   `json:"id"`
		Key  string   `json:"key"`
		Caps []string `json:"caps"`
		Root string   `json:"root"`
		Name string   `json:"name"`
		Err  string   `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if !out.OK || out.Key == "" {
		if out.Err == "" {
			out.Err = "réponse inattendue du serveur (" + resp.Status + ")"
		}
		return errors.New(out.Err)
	}
	cfg.ID, cfg.Key, cfg.Caps = out.ID, out.Key, out.Caps
	if out.Root != "" {
		cfg.Root = out.Root
	}
	if out.Name != "" {
		cfg.Name = out.Name
	}
	return nil
}

// Run maintient la connexion pour toujours (reconnexion auto). quiet coupe les
// impressions (mode service). Bloque jusqu'à ctx.Done().
func Run(ctx context.Context, cfg Config, quiet bool) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := session(ctx, cfg, quiet)
		if err != nil && !quiet {
			fmt.Printf("[node] session terminée: %v — reconnexion dans %s\n", err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func wsURL(server string) string {
	s := strings.TrimRight(server, "/")
	switch {
	case strings.HasPrefix(s, "https://"):
		s = "wss://" + strings.TrimPrefix(s, "https://")
	case strings.HasPrefix(s, "http://"):
		s = "ws://" + strings.TrimPrefix(s, "http://")
	case !strings.Contains(s, "://"):
		s = "wss://" + s
	}
	return s + "/api/node/ws"
}

func session(ctx context.Context, cfg Config, quiet bool) error {
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dialCtx, wsURL(cfg.ServerURL), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + cfg.Key}},
	})
	if err != nil {
		return fmt.Errorf("connexion: %w", err)
	}
	c.SetReadLimit(-1)
	defer c.CloseNow()

	hello := nodewire.Msg{Type: "hello", Name: cfg.Name, OS: runtime.GOOS + "/" + runtime.GOARCH, Caps: cfg.Caps}
	if err := wsjson.Write(dialCtx, c, hello); err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if !quiet {
		fmt.Printf("[node] connecté ✓ (en attente des demandes de l'IA)\n")
	}
	for {
		var m nodewire.Msg
		if err := wsjson.Read(ctx, c, &m); err != nil {
			return err
		}
		if m.Type != "call" {
			continue
		}
		result := execute(ctx, cfg, m, quiet)
		wc, wcancel := context.WithTimeout(ctx, 15*time.Second)
		err := wsjson.Write(wc, c, nodewire.Msg{Type: "result", ID: m.ID, Result: result})
		wcancel()
		if err != nil {
			return err
		}
	}
}

// execute applique les gardes locales puis exécute la demande.
func execute(ctx context.Context, cfg Config, m nodewire.Msg, quiet bool) string {
	if !nodewire.CapAllowed(cfg.Caps, m.Cap) {
		return "[refusé] la capacité « " + m.Cap + " » n'est pas activée sur ce poste"
	}
	if !cfg.AutoYes && (m.Cap == nodewire.CapShell || m.Cap == nodewire.CapWrite) {
		if !confirm(m, quiet) {
			return "[refusé] l'utilisateur du poste a refusé cette action"
		}
	}
	switch m.Cap {
	case nodewire.CapShell:
		command, _ := m.Args["command"].(string)
		to := 0
		if v, ok := m.Args["timeout"].(float64); ok {
			to = int(v)
		}
		return runShell(ctx, command, to, cfg.Root)
	case nodewire.CapRead:
		path, _ := m.Args["path"].(string)
		return readFile(cfg.Root, path)
	case nodewire.CapWrite:
		path, _ := m.Args["path"].(string)
		content, _ := m.Args["content"].(string)
		return writeFile(cfg.Root, path, content)
	case nodewire.CapList:
		path, _ := m.Args["path"].(string)
		return listDir(cfg.Root, path)
	}
	return "[erreur] capacité inconnue: " + m.Cap
}

// confirm demande validation sur le terminal du poste. Sans terminal (service)
// et sans --yes → REFUSE : mieux vaut bloquer qu'exécuter sans surveillance.
func confirm(m nodewire.Msg, quiet bool) bool {
	fi, _ := os.Stdin.Stat()
	interactive := fi != nil && (fi.Mode()&os.ModeCharDevice) != 0
	if !interactive || quiet {
		return false
	}
	label := ""
	if c, ok := m.Args["command"].(string); ok {
		label = c
	} else if p, ok := m.Args["path"].(string); ok {
		label = p
	}
	fmt.Printf("\n[node] l'IA veut %s sur ce poste :\n    %s\nAutoriser ? [y/N] ", nodewire.CapLabel(m.Cap), label)
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(sc.Text())), "y")
	}
	return false
}

func runShell(parent context.Context, command string, timeoutSec int, dir string) string {
	if strings.TrimSpace(command) == "" {
		return "[erreur] commande vide"
	}
	if timeoutSec <= 0 {
		timeoutSec = defaultTimeoutSec
	}
	if timeoutSec > maxTimeoutSec {
		timeoutSec = maxTimeoutSec
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := shellCmd(ctx, command)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			cmd.Dir = dir
		}
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("[timeout après %ds]", timeoutSec)
	}
	exit := 0
	if err != nil {
		if ee, ok := err.(interface{ ExitCode() int }); ok {
			exit = ee.ExitCode()
		} else {
			return fmt.Sprintf("[erreur: %v]", err)
		}
	}
	parts := []string{fmt.Sprintf("exit: %d", exit)}
	if o := tail(stdout.String(), maxOutput); o != "" {
		parts = append(parts, "stdout:\n"+o)
	}
	if e := tail(stderr.String(), maxOutput); e != "" {
		parts = append(parts, "stderr:\n"+e)
	}
	return strings.Join(parts, "\n\n")
}

func readFile(root, path string) string {
	abs, err := nodewire.ResolvePath(root, path)
	if err != nil {
		return "[erreur] " + err.Error()
	}
	f, err := os.Open(abs)
	if err != nil {
		return "[erreur] " + err.Error()
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, readMax+1))
	if err != nil {
		return "[erreur] " + err.Error()
	}
	if len(b) > readMax {
		return string(b[:readMax]) + "\n…[tronqué]"
	}
	return string(b)
}

func writeFile(root, path, content string) string {
	abs, err := nodewire.ResolvePath(root, path)
	if err != nil {
		return "[erreur] " + err.Error()
	}
	if d := filepath.Dir(abs); d != "" {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "[erreur] " + err.Error()
		}
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "[erreur] " + err.Error()
	}
	return fmt.Sprintf("[ok] %s écrit (%d octets)", abs, len(content))
}

func listDir(root, path string) string {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	abs, err := nodewire.ResolvePath(root, path)
	if err != nil {
		return "[erreur] " + err.Error()
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "[erreur] " + err.Error()
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			n += "/"
		}
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "[vide]"
	}
	return strings.Join(names, "\n")
}

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
