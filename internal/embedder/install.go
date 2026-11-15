package embedder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"ajean/internal/config"
	"ajean/internal/home"
)

// Les deux étapes d'une installation, telles que l'interface les nomme.
const (
	stepEngine = "engine"
	stepModel  = "model"
)

// job : une installation en cours.
type job struct {
	cancel   context.CancelFunc
	done     chan struct{} // fermé quand l'installation a rendu la main
	model    model
	progress Progress
	notified time.Time // dernier avis d'avancement : pas plus d'un par seconde
}

// Install lance en arrière-plan l'installation du moteur local : le serveur,
// s'il manque, puis le modèle désigné par modelURL (vide : celui proposé
// d'office). Ce qui est déjà là n'est pas retéléchargé. À la fin, la recherche
// par le sens passe par le moteur local. Status dit où elle en est.
func Install(modelURL string) error {
	m, err := parseModelURL(modelURL)
	if err != nil {
		return err
	}
	a, ok := engineAssets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return fmt.Errorf("le moteur local n'existe pas pour ce système (%s, %s)", runtime.GOOS, runtime.GOARCH)
	}
	return start(releaseURL+a.name, a, m)
}

func start(engineURL string, a asset, m model) error {
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{cancel: cancel, done: make(chan struct{}), model: m, progress: Progress{What: stepEngine, Total: a.size}}
	st.Lock()
	if st.job != nil {
		st.Unlock()
		cancel()
		return errors.New("une installation est déjà en cours")
	}
	st.job, st.err, st.retryAt = j, "", time.Time{}
	st.Unlock()
	notify()
	go func() {
		err := j.run(ctx, engineURL, a)
		st.Lock()
		st.job = nil
		if err != nil && ctx.Err() == nil { // une annulation n'est pas un échec
			st.err = err.Error()
		}
		st.Unlock()
		cancel()
		close(j.done)
		notify()
	}()
	return nil
}

func (j *job) run(ctx context.Context, engineURL string, a asset) error {
	if engineBinary() == "" {
		archive := filepath.Join(enginesDir(), a.name)
		if err := fetch(ctx, engineURL, archive, a.size, a.sha256, j.report(stepEngine)); err != nil {
			return fmt.Errorf("moteur : %w", err)
		}
		if err := unpackEngine(archive); err != nil {
			return fmt.Errorf("moteur : %w", err)
		}
	}
	if err := j.installModel(ctx); err != nil {
		return fmt.Errorf("modèle : %w", err)
	}
	return config.Update(func(c *config.Config) error {
		c.Embed = config.ModelRef{Provider: config.LocalEmbed}
		return nil
	})
}

// installModel met le modèle en place, seul dans son dossier : les vecteurs de
// deux modèles ne se comparent pas, garder l'ancien ne servirait à rien.
func (j *job) installModel(ctx context.Context) error {
	m := j.model
	dest := filepath.Join(modelsDir(), m.file)
	if fi, err := os.Stat(dest); err != nil || (m.size > 0 && fi.Size() != m.size) {
		if err == nil {
			// Un fichier du même nom mais d'une autre taille : le moteur qui le
			// tient ouvert empêcherait de le remplacer.
			stop()
			if err := os.Remove(dest); err != nil {
				return err
			}
		}
		if err := fetch(ctx, m.url, dest, m.size, "", j.report(stepModel)); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(modelsDir())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == m.file {
			continue
		}
		if filepath.Ext(e.Name()) == modelExt {
			stop() // il tourne peut-être avec le modèle qu'on retire
		}
		if err := os.Remove(filepath.Join(modelsDir(), e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// report : de quoi noter l'avancement d'une étape. L'interface est prévenue
// une fois par seconde au plus.
func (j *job) report(what string) func(done, total int64) {
	return func(done, total int64) {
		st.Lock()
		j.progress = Progress{What: what, Done: done, Total: total}
		due := time.Since(j.notified) >= time.Second
		if due {
			j.notified = time.Now()
		}
		st.Unlock()
		if due {
			notify()
		}
	}
}

// Cancel arrête l'installation en cours, s'il y en a une. Ce qui est déjà
// téléchargé est gardé : la prochaine installation reprendra là.
func Cancel() {
	st.Lock()
	j := st.job
	st.Unlock()
	if j != nil {
		j.cancel()
		<-j.done
	}
}

// Remove arrête le moteur local et supprime tout ce qu'il a installé. Si la
// recherche par le sens passait par lui, elle est coupée.
func Remove() error {
	Cancel()
	stop()
	if err := os.RemoveAll(home.State("embedder")); err != nil {
		return err
	}
	st.Lock()
	st.err, st.retryAt = "", time.Time{}
	st.Unlock()
	err := config.Update(func(c *config.Config) error {
		if c.Embed.Provider == config.LocalEmbed {
			c.Embed = config.ModelRef{}
		}
		return nil
	})
	notify()
	return err
}

// Shutdown arrête le moteur et toute installation en cours (fin du programme).
func Shutdown() {
	Cancel()
	stop()
}
