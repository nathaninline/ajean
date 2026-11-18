package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"ajean/internal/config"
	"ajean/internal/sandbox"
)

// Le mode test : l'instance vierge tourne à part, sur 127.0.0.1, et se montre sous
// /sandbox/ à travers ce proxy. Elle ne reçoit ni les réglages ni les données de
// la vraie instance ; la clé de l'API ne lui parvient que par son environnement.

type sandboxRun struct {
	cmd   *exec.Cmd
	stdin io.Closer
	dir   string
	model string
	proxy *httputil.ReverseProxy
	ended chan struct{}
}

var (
	sandboxMu  sync.Mutex
	sandboxCur *sandboxRun
)

// sandboxEnv : l'environnement de l'instance, sans aucun réglage de la vraie.
func sandboxEnv(dir, port, apiURL, key, model string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "AJEAN_") {
			env = append(env, kv)
		}
	}
	return append(env, sandbox.Env+"=1", "AJEAN_HOME="+dir, "AJEAN_HOST=127.0.0.1", "AJEAN_PORT="+port,
		"AJEAN_NO_BROWSER=1", "AJEAN_API_URL="+apiURL, "AJEAN_API_KEY="+key, "AJEAN_MODEL="+model)
}

// sandboxSeed : les réglages de départ de l'instance. Seuls le modèle à essayer, son
// effort de réflexion, la langue et le fuseau sont repris. La clé n'est jamais écrite
// sur le disque : elle passe par l'environnement.
func sandboxSeed(conf config.Config, preset config.Preset, prov config.Provider) ([]byte, error) {
	const id = "test"
	return json.Marshal(config.Config{
		Providers: []config.Provider{{ID: id, Name: prov.Name, URL: prov.URL}},
		Presets:   []config.Preset{{ID: id, Name: preset.Name, Provider: id, Model: preset.Model, Effort: preset.Effort}},
		Active:    id, Language: conf.Language, TZ: conf.TZ,
	})
}

// startSandbox lance une instance vierge sur ce modèle des réglages.
func startSandbox(presetID string) (*sandboxRun, error) {
	conf := config.Get()
	i := slices.IndexFunc(conf.Presets, func(p config.Preset) bool { return p.ID == presetID })
	if i < 0 {
		return nil, errors.New("modèle introuvable")
	}
	preset := conf.Presets[i]
	prov, found := conf.Provider(preset.Provider)
	if !found || prov.URL == "" {
		return nil, errors.New("ce modèle n'a pas de fournisseur valide")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "ajean-sandbox-")
	if err != nil {
		return nil, err
	}
	run, err := launchSandbox(exe, dir, conf, preset, prov)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return run, nil
}

func launchSandbox(exe, dir string, conf config.Config, preset config.Preset, prov config.Provider) (*sandboxRun, error) {
	seed, err := sandboxSeed(conf, preset, prov)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), seed, 0o600); err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()

	cmd := exec.Command(exe)
	cmd.Dir = os.TempDir()
	cmd.Env = sandboxEnv(dir, port, prov.URL, prov.Key, preset.Model)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	run := &sandboxRun{cmd: cmd, stdin: stdin, dir: dir, model: preset.Name, ended: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(run.ended) }()
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", port)}
	run.proxy = sandboxProxy(target)
	if err := sandboxWait(target.String(), run.ended); err != nil {
		run.stop()
		return nil, err
	}
	return run, nil
}

func sandboxWait(base string, ended <-chan struct{}) error {
	c := &http.Client{Timeout: time.Second}
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		select {
		case <-ended:
			return errors.New("l'environnement de test s'est arrêté au démarrage")
		default:
		}
		if resp, err := c.Get(base + "/api/auth"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
	return errors.New("l'environnement de test n'a pas démarré à temps")
}

// stop arrête l'instance et efface tout ce qu'elle a produit.
func (s *sandboxRun) stop() {
	_ = s.stdin.Close()
	select {
	case <-s.ended:
	case <-time.After(3 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.ended
	}
	// Sous Windows, les fichiers restent verrouillés un court instant.
	for i := 0; i < 20 && os.RemoveAll(s.dir) != nil; i++ {
		time.Sleep(150 * time.Millisecond)
	}
}

// sandboxPage adapte les adresses de la page, écrites pour la racine du site.
func sandboxPage(b []byte) []byte {
	for _, p := range []string{"/api/", "/inter-var.woff2", "/favicon.svg", "/icon.png"} {
		b = bytes.ReplaceAll(b, []byte(p), []byte("/sandbox"+p))
	}
	return b
}

func sandboxProxy(target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		FlushInterval: -1, // le flux d'événements
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			pr.Out.URL.Path = "/" + strings.TrimPrefix(pr.In.URL.Path, "/sandbox/")
			pr.Out.URL.RawPath = ""
			// La vraie instance a contrôlé l'accès : ni sa session ni l'identité du
			// visiteur ne descendent. L'instance voit un appel de sa propre machine.
			for _, h := range append([]string{"Cookie", "Accept-Encoding", "Referer"}, forwardHeaders...) {
				pr.Out.Header.Del(h)
			}
			if pr.Out.Header.Get("Origin") != "" {
				pr.Out.Header.Set("Origin", target.String())
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
				return nil
			}
			b, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return err
			}
			b = sandboxPage(b)
			resp.Body = io.NopCloser(bytes.NewReader(b))
			resp.ContentLength = int64(len(b))
			resp.Header.Set("Content-Length", strconv.Itoa(len(b)))
			resp.Header.Del("Etag")
			resp.Header.Del("Last-Modified")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			fail(w, http.StatusBadGateway, errors.New("environnement de test arrêté"))
		},
	}
}

// handleSandboxProxy sert l'instance de test sous /sandbox/.
func handleSandboxProxy(w http.ResponseWriter, r *http.Request) {
	sandboxMu.Lock()
	cur := sandboxCur
	sandboxMu.Unlock()
	if cur == nil {
		http.Error(w, "Aucun test en cours.", http.StatusNotFound)
		return
	}
	cur.proxy.ServeHTTP(w, r)
}

// handleSandboxStart remplace le test en cours par un environnement neuf.
func handleSandboxStart(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Preset string `json:"preset"`
	}
	if !body(w, r, &q) {
		return
	}
	sandboxMu.Lock()
	defer sandboxMu.Unlock()
	if sandboxCur != nil {
		sandboxCur.stop()
		sandboxCur = nil
	}
	run, err := startSandbox(q.Preset)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	sandboxCur = run
	reply(w, map[string]string{"model": run.model})
}

// handleSandboxStop termine le test.
func handleSandboxStop(w http.ResponseWriter, r *http.Request) {
	sandboxMu.Lock()
	defer sandboxMu.Unlock()
	if sandboxCur != nil {
		sandboxCur.stop()
		sandboxCur = nil
	}
	ok(w)
}
