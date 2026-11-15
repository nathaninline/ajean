package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"ajean/internal/config"
	"ajean/internal/embedder"
	"ajean/internal/home"
	"ajean/internal/llm"
	"ajean/internal/memory"
)

const maxAudioBytes = 25 << 20 // limite courante des API de transcription

// ---- ce que l'interface voit ----------------------------------------------------

// providerView : un fournisseur tel que l'interface le reçoit. La clé ne sort
// jamais du serveur.
type providerView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"api_url"`
	HasKey bool   `json:"has_key"`
}

// presetView : une configuration dans le sélecteur de la zone de saisie.
type presetView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
	Provider string `json:"provider"` // nom du fournisseur
}

func viewPreset(c config.Config, p config.Preset) presetView {
	provider, _ := c.Provider(p.Provider)
	return presetView{ID: p.ID, Name: p.Name, Model: p.Model, Effort: p.Effort, Provider: provider.Name}
}

// chatState : ce que /api/state dit de l'API de discussion.
func chatState(c config.Config) map[string]any {
	presets := []presetView{}
	for _, p := range c.Presets {
		presets = append(presets, viewPreset(c, p))
	}
	active, _ := c.Preset()
	return map[string]any{
		"configured": c.Chat().Ready(),
		"preset":     viewPreset(c, active),
		"presets":    presets,
		"transcribe": c.Transcription().Ready(),
		"language":   c.Language,
	}
}

func handleConfigGet(w http.ResponseWriter, r *http.Request) {
	c := config.Get()
	providers := []providerView{}
	for _, p := range c.Providers {
		providers = append(providers, providerView{ID: p.ID, Name: p.Name, URL: p.URL, HasKey: p.Key != ""})
	}
	active, _ := c.Preset()
	reply(w, map[string]any{
		"providers": providers, "presets": orEmpty(c.Presets), "active": active.ID,
		"embed": c.Embed, "transcribe": c.Transcribe,
		"language": c.Language, "env": config.FromEnv(), "review": !c.ReviewOff, "sleep": !c.SleepOff,
		"dirs": map[string]string{"memory": home.Memory(), "workspace": home.Workspace(), "tools": home.Tools()},
	})
}

// handleConfigSave (POST {embed?, transcribe?, review?, sleep?, language?}) : les réglages
// qui ne sont ni un fournisseur ni une configuration. Un champ absent ne change pas.
func handleConfigSave(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Embed, Transcribe *config.ModelRef
		Review, Sleep     *bool
		Language          *string
	}
	if !body(w, r, &q) {
		return
	}
	// Le moteur local se choisit comme un fournisseur, sans modèle : c'est celui
	// qu'il a installé.
	local := q.Embed != nil && strings.TrimSpace(q.Embed.Provider) == config.LocalEmbed
	if local && !embedder.Installed() {
		fail(w, http.StatusBadRequest, errors.New("le moteur local n'est pas installé"))
		return
	}
	err := config.Update(func(c *config.Config) error {
		if q.Language != nil {
			if !config.KnownLanguage(*q.Language) {
				return errors.New("langue inconnue")
			}
			c.Language = *q.Language
		}
		for _, ref := range []struct {
			in  *config.ModelRef
			dst *config.ModelRef
		}{{q.Embed, &c.Embed}, {q.Transcribe, &c.Transcribe}} {
			if ref.in == nil {
				continue
			}
			if local && ref.dst == &c.Embed {
				c.Embed = config.ModelRef{Provider: config.LocalEmbed}
				continue
			}
			clean := config.ModelRef{Provider: strings.TrimSpace(ref.in.Provider), Model: strings.TrimSpace(ref.in.Model)}
			if _, ok := c.Provider(clean.Provider); !ok && clean.Provider != "" {
				return errors.New("fournisseur introuvable")
			}
			if clean.Provider == "" || clean.Model == "" {
				clean = config.ModelRef{} // l'un sans l'autre ne veut rien dire : désactivé
			}
			*ref.dst = clean
		}
		if q.Review != nil {
			c.ReviewOff = !*q.Review
		}
		if q.Sleep != nil {
			c.SleepOff = !*q.Sleep
		}
		return nil
	})
	if err == nil && q.Embed != nil {
		memory.WakeIndexer() // nouvelle source de vecteurs : l'index se reconstruit sans attendre
	}
	done(w, err, http.StatusBadRequest)
}

// ---- fournisseurs ---------------------------------------------------------------

// providerInput : un fournisseur tel que l'interface l'envoie.
type providerInput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"api_url"`
	Key  string `json:"api_key"` // vide = garder la clé enregistrée (si l'URL ne change pas)
}

// resolve donne le fournisseur que décrit la saisie, par-dessus celui déjà
// enregistré sous cet identifiant. Une clé n'est jamais gardée pour une autre
// URL que la sienne : sinon il suffirait de changer l'URL d'un fournisseur pour
// se faire envoyer sa clé.
func (in providerInput) resolve(c config.Config) config.Provider {
	saved, _ := c.Provider(in.ID)
	p := config.Provider{ID: saved.ID, Name: strings.TrimSpace(in.Name), URL: strings.TrimSpace(in.URL), Key: strings.TrimSpace(in.Key)}
	if p.URL == "" {
		p.URL = saved.URL
	}
	if p.Key == "" && p.URL == saved.URL {
		p.Key = saved.Key
	}
	if p.Name == "" {
		p.Name = saved.Name
	}
	return p
}

func handleProviderSave(w http.ResponseWriter, r *http.Request) {
	var in providerInput
	if !body(w, r, &in) {
		return
	}
	var id string
	err := config.Update(func(c *config.Config) error {
		p := in.resolve(*c)
		if p.URL == "" {
			return errors.New("l'URL de l'API est obligatoire")
		}
		if p.Name == "" {
			p.Name = config.HostName(p.URL)
		}
		if i := slices.IndexFunc(c.Providers, func(x config.Provider) bool { return x.ID == p.ID }); p.ID != "" && i >= 0 {
			c.Providers[i] = p
		} else {
			p.ID = config.NewID()
			c.Providers = append(c.Providers, p)
		}
		id = p.ID
		return nil
	})
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	reply(w, map[string]string{"id": id})
}

func handleProviderDelete(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID string }
	if !body(w, r, &q) {
		return
	}
	err := config.Update(func(c *config.Config) error {
		i := slices.IndexFunc(c.Providers, func(p config.Provider) bool { return p.ID == q.ID })
		if i < 0 {
			return errors.New("fournisseur introuvable")
		}
		var users []string
		for _, p := range c.Presets {
			if p.Provider == q.ID {
				users = append(users, "le modèle « "+p.Name+" »")
			}
		}
		if c.Transcribe.Provider == q.ID {
			users = append(users, "la transcription")
		}
		if c.Embed.Provider == q.ID {
			users = append(users, "la recherche par le sens")
		}
		if len(users) > 0 {
			return fmt.Errorf("ce fournisseur sert encore à %s : change-les d'abord", strings.Join(users, ", "))
		}
		c.Providers = slices.Delete(c.Providers, i, i+1)
		return nil
	})
	done(w, err, http.StatusConflict)
}

// handleProviderModels : les modèles qu'un fournisseur annonce (enregistré, ou
// en cours de saisie).
func handleProviderModels(w http.ResponseWriter, r *http.Request) {
	var in providerInput
	if !body(w, r, &in) {
		return
	}
	p := in.resolve(config.Get())
	if p.URL == "" {
		fail(w, http.StatusBadRequest, errors.New("l'URL de l'API est obligatoire"))
		return
	}
	models, err := llm.Models(r.Context(), llm.Endpoint{URL: p.URL, Key: p.Key})
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	reply(w, map[string][]string{"models": orEmpty(models)})
}

// handleProviderTest : un appel minimal à un modèle du fournisseur, pour
// valider URL, clé et modèle avant d'enregistrer.
func handleProviderTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		providerInput
		Model string `json:"model"`
	}
	if !body(w, r, &in) {
		return
	}
	p := in.resolve(config.Get())
	ep := llm.Endpoint{URL: p.URL, Key: p.Key, Model: strings.TrimSpace(in.Model)}
	if !ep.Ready() {
		fail(w, http.StatusBadRequest, errors.New("il faut une URL et un modèle"))
		return
	}
	answer, err := llm.Test(r.Context(), ep)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	reply(w, map[string]string{"answer": answer})
}

// ---- configurations -------------------------------------------------------------

// cleanEffort valide un effort de réflexion. Les valeurs admises dépendent de
// l'API : on ne vérifie que la forme, c'est elle qui tranche au prochain message.
func cleanEffort(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 16 || strings.ContainsFunc(s, func(c rune) bool { return c < 'a' || c > 'z' }) {
		return "", errors.New("effort invalide")
	}
	return s, nil
}

func handlePresetSave(w http.ResponseWriter, r *http.Request) {
	var in config.Preset
	if !body(w, r, &in) {
		return
	}
	var id string
	err := config.Update(func(c *config.Config) error {
		p := config.Preset{ID: in.ID, Name: strings.TrimSpace(in.Name), Provider: in.Provider, Model: strings.TrimSpace(in.Model)}
		var err error
		if p.Effort, err = cleanEffort(in.Effort); err != nil {
			return err
		}
		if _, ok := c.Provider(p.Provider); !ok {
			return errors.New("choisis un fournisseur")
		}
		if p.Model == "" {
			return errors.New("l'identifiant du modèle est obligatoire")
		}
		if p.Name == "" {
			p.Name = p.Model
		}
		if i := slices.IndexFunc(c.Presets, func(x config.Preset) bool { return x.ID == p.ID }); p.ID != "" && i >= 0 {
			c.Presets[i] = p
		} else {
			p.ID = config.NewID()
			c.Presets = append(c.Presets, p)
		}
		if len(c.Presets) == 1 {
			c.Active = p.ID // la première devient l'active
		}
		id = p.ID
		return nil
	})
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	reply(w, map[string]string{"id": id})
}

func handlePresetDelete(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID string }
	if !body(w, r, &q) {
		return
	}
	err := config.Update(func(c *config.Config) error {
		i := slices.IndexFunc(c.Presets, func(p config.Preset) bool { return p.ID == q.ID })
		if i < 0 {
			return errors.New("modèle introuvable")
		}
		c.Presets = slices.Delete(c.Presets, i, i+1)
		if c.Active == q.ID {
			next, _ := c.Preset() // l'active disparaît : la première restante prend sa place
			c.Active = next.ID
		}
		return nil
	})
	done(w, err, http.StatusNotFound)
}

// handlePresetSelect fait d'une configuration l'active : le prochain message
// part avec elle, dans toutes les conversations.
func handlePresetSelect(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID string }
	if !body(w, r, &q) {
		return
	}
	err := config.Update(func(c *config.Config) error {
		if !slices.ContainsFunc(c.Presets, func(p config.Preset) bool { return p.ID == q.ID }) {
			return errors.New("modèle introuvable")
		}
		c.Active = q.ID
		return nil
	})
	done(w, err, http.StatusNotFound)
}

// handleEffortChoose (POST {effort}) : l'effort de réflexion de la configuration
// active ; vide = laisser le modèle décider.
func handleEffortChoose(w http.ResponseWriter, r *http.Request) {
	var q struct{ Effort string }
	if !body(w, r, &q) {
		return
	}
	effort, err := cleanEffort(q.Effort)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	err = config.Update(func(c *config.Config) error {
		active, ok := c.Preset()
		if !ok {
			return errors.New("aucun modèle")
		}
		i := slices.IndexFunc(c.Presets, func(p config.Preset) bool { return p.ID == active.ID })
		c.Presets[i].Effort = effort
		return nil
	})
	done(w, err, http.StatusBadRequest)
}

// ---- dictée ---------------------------------------------------------------------

// handleTranscribe (multipart, champ « file ») : le texte d'un enregistrement du
// micro, par l'API de transcription réglée.
func handleTranscribe(w http.ResponseWriter, r *http.Request) {
	ep := config.Get().Transcription()
	if !ep.Ready() {
		fail(w, http.StatusBadRequest, errors.New("aucune API de transcription n'est réglée"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAudioBytes)
	f, head, err := r.FormFile("file")
	if err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("enregistrement illisible ou trop long (%d Mo max)", maxAudioBytes>>20))
		return
	}
	defer f.Close()
	audio, err := io.ReadAll(f)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	text, err := llm.Transcribe(r.Context(), ep, safeName(head.Filename), audio)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	reply(w, map[string]string{"text": text})
}
