package embedder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ajean/internal/llm"
)

const (
	idleMax     = 20 * time.Minute // sans demande pendant ce temps, le moteur rend sa mémoire vive
	idleCheck   = time.Minute
	startMax    = 90 * time.Second // chargement du modèle compris, sur une machine lente
	startPoll   = 200 * time.Millisecond
	launchRetry = 2 * time.Minute // après un lancement raté, on ne réessaie pas avant
	minFreeMB   = 1500            // mémoire vive libre exigée pour lancer le moteur
	contextSize = "2048"          // jetons par texte : ce que les petits modèles d'embeddings acceptent
	logTail     = 2 << 10
)

// errStopped : le moteur a été arrêté pendant son démarrage (suppression, fin
// du programme). Ce n'est pas une panne.
var errStopped = errors.New("le moteur local a été arrêté")

// process : un llama-server lancé par AJEAN.
type process struct {
	cmd     *exec.Cmd
	exited  chan struct{} // fermé quand le programme s'est terminé
	port    int
	model   string
	log     *tail
	ready   bool // le modèle est chargé : il répond
	stopped bool // arrêté par nous, pas tombé tout seul
	lastUse time.Time
}

// launching : un seul lancement à la fois. Tenu pendant tout le démarrage,
// donc jamais pris avec le verrou de st.
var launching sync.Mutex

// Embed demande au moteur local un vecteur par texte, en le lançant s'il est à
// l'arrêt. query dit si les textes sont des questions ou des textes à
// retrouver. Si ctx expire pendant le démarrage, celui-ci continue en fond :
// la demande suivante trouvera le moteur prêt.
func Embed(ctx context.Context, texts []string, query bool) ([][]float32, error) {
	p, err := serve(ctx)
	if err != nil {
		return nil, err
	}
	prefix, document := prefixes(p.model)
	if !query {
		prefix = document
	}
	inputs := make([]string, len(texts))
	for i, text := range texts {
		inputs[i] = prefix + text
	}
	return llm.Embed(ctx, llm.Endpoint{URL: "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(p.port)) + "/v1", Model: p.model}, inputs)
}

// running : le moteur s'il est en marche, en notant qu'il vient de servir.
func running() *process {
	st.Lock()
	defer st.Unlock()
	if st.proc == nil || !st.proc.ready {
		return nil
	}
	st.proc.lastUse = time.Now()
	return st.proc
}

// serve : le moteur en marche, lancé au besoin.
func serve(ctx context.Context) (*process, error) {
	if p := running(); p != nil {
		return p, nil
	}
	type result struct {
		p   *process
		err error
	}
	ch := make(chan result, 1)
	go func() {
		p, err := launch()
		ch <- result{p, err}
	}()
	select {
	case r := <-ch:
		return r.p, r.err
	case <-ctx.Done():
		return nil, errors.New("le moteur local est en train de démarrer")
	}
}

// launch lance le moteur, sauf si un lancement vient d'échouer.
func launch() (*process, error) {
	launching.Lock()
	defer launching.Unlock()
	if p := running(); p != nil {
		return p, nil
	}
	st.Lock()
	wait, last := time.Until(st.retryAt), st.err
	st.Unlock()
	if wait > 0 {
		return nil, errors.New(last)
	}
	file, _, ok := installed()
	if !ok {
		return nil, errors.New("le moteur local n'est pas installé")
	}
	p, err := spawn(file)
	if err != nil && !errors.Is(err, errStopped) {
		st.Lock()
		st.err, st.retryAt = err.Error(), time.Now().Add(launchRetry)
		st.Unlock()
	}
	notify()
	return p, err
}

// spawn démarre llama-server avec ce modèle, sur un port libre de la machine,
// et attend que le modèle soit chargé.
func spawn(file string) (*process, error) {
	if mb, known := freeRAM(); known && mb < minFreeMB {
		return nil, fmt.Errorf("mémoire vive insuffisante pour le moteur local (%d Mo libres, %d nécessaires)", mb, minFreeMB)
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	bin := engineBinary()
	// Peu de fils d'exécution : le moteur travaille en fond, il ne doit pas
	// ralentir la machine. « -ngl 0 » le garde sur le processeur même là où
	// l'archive sait se servir d'une carte.
	threads := max(1, min(4, runtime.NumCPU()/4))
	cmd := exec.Command(bin, "-m", filepath.Join(modelsDir(), file), "--embedding", "-ngl", "0",
		"-c", contextSize, "-b", contextSize, "-ub", contextSize, "-np", "1", "-t", strconv.Itoa(threads),
		"--host", "127.0.0.1", "--port", strconv.Itoa(port))
	cmd.Dir = filepath.Dir(bin)
	p := &process{cmd: cmd, exited: make(chan struct{}), port: port, model: file, log: &tail{}}
	cmd.Stderr = p.log
	prepare(cmd)
	// Enregistré dès le lancement, avant même d'être prêt : un arrêt demandé
	// pendant le démarrage doit le trouver.
	st.Lock()
	if err := cmd.Start(); err != nil {
		st.Unlock()
		return nil, fmt.Errorf("lancement du moteur local impossible : %w", err)
	}
	st.proc = p
	st.Unlock()
	adopt(cmd)
	go p.wait()

	health := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/health"
	for deadline := time.Now().Add(startMax); !healthy(health); {
		select {
		case <-p.exited:
			st.Lock()
			stopped := p.stopped
			st.Unlock()
			if stopped {
				return nil, errStopped
			}
			return nil, errors.New("le moteur local s'est arrêté au démarrage : " + p.log.last())
		case <-time.After(startPoll):
		}
		if time.Now().After(deadline) {
			stop()
			return nil, errors.New("le moteur local ne répond pas")
		}
	}
	st.Lock()
	p.ready, p.lastUse, st.err = true, time.Now(), ""
	st.Unlock()
	go p.watch()
	return p, nil
}

// wait attend la fin du programme. S'il tombe tout seul, il est oublié : la
// demande suivante le relancera.
func (p *process) wait() {
	_ = p.cmd.Wait()
	close(p.exited)
	st.Lock()
	fell := st.proc == p && p.ready
	if st.proc == p {
		st.proc = nil
	}
	st.Unlock()
	if fell {
		notify()
	}
}

// watch arrête le moteur quand il ne sert plus.
func (p *process) watch() {
	tick := time.NewTicker(idleCheck)
	defer tick.Stop()
	for {
		select {
		case <-p.exited:
			return
		case <-tick.C:
		}
		st.Lock()
		idle := st.proc == p && time.Since(p.lastUse) > idleMax
		st.Unlock()
		if idle {
			stop()
			return
		}
	}
}

// stop arrête le moteur s'il tourne, et attend qu'il ait rendu ses fichiers.
func stop() {
	st.Lock()
	p := st.proc
	st.proc = nil
	if p != nil {
		p.stopped = true
	}
	st.Unlock()
	if p == nil {
		return
	}
	_ = p.cmd.Process.Kill()
	<-p.exited
	notify()
}

var healthClient = &http.Client{Timeout: 2 * time.Second}

// healthy : le serveur répond-il, modèle chargé ? (il répond 503 tant qu'il charge)
func healthy(url string) bool {
	resp, err := healthClient.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// tail garde la fin de ce que le moteur écrit : de quoi dire pourquoi il n'a
// pas démarré. Lu seulement après la fin du programme.
type tail struct{ buf []byte }

func (t *tail) Write(b []byte) (int, error) {
	t.buf = append(t.buf, b...)
	if len(t.buf) > logTail {
		t.buf = t.buf[len(t.buf)-logTail:]
	}
	return len(b), nil
}

// last : la dernière ligne écrite.
func (t *tail) last() string {
	lines := strings.Split(string(bytes.TrimSpace(t.buf)), "\n")
	if line := strings.TrimSpace(lines[len(lines)-1]); line != "" {
		return line
	}
	return "aucun message"
}
