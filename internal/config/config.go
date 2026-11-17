// Package config tient les réglages d'AJEAN, rangés dans config.json à côté de
// l'exécutable et réglables depuis l'interface.
//
// Trois notions :
//   - un FOURNISSEUR est une API compatible OpenAI : un nom, une URL, une clé.
//     On peut en avoir plusieurs (OpenAI, Groq, un serveur local…) ;
//   - une CONFIGURATION est ce que l'on choisit pour discuter : un fournisseur,
//     un modèle, un effort de réflexion. Une seule est active à la fois ;
//   - la transcription (dictée) et la recherche par le sens (embeddings) ont
//     chacune leur fournisseur et leur modèle, indépendants de la discussion.
//     La recherche par le sens peut aussi se passer d'API : son « fournisseur »
//     est alors le moteur local (LocalEmbed), qu'AJEAN installe lui-même.
//
// Les variables d'environnement AJEAN_API_URL, AJEAN_API_KEY et AJEAN_MODEL
// passent devant la configuration active.
package config

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"ajean/internal/home"
	"ajean/internal/llm"
)

const defaultMaxContextChars = 200_000 // ~50 000 tokens

// LocalEmbed : à la place d'un identifiant de fournisseur dans Embed, désigne
// le moteur local d'embeddings (paquet embedder). Le modèle est alors vide :
// c'est celui que le moteur a installé.
const LocalEmbed = "local"

// Provider : une API compatible OpenAI.
type Provider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"api_url"` // base de l'API, ex. https://api.openai.com/v1
	Key  string `json:"api_key"` // jamais renvoyée à l'interface
}

// Preset : une configuration de discussion.
type Preset struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"` // identifiant du fournisseur
	Model    string `json:"model"`
	Effort   string `json:"effort"` // effort de réflexion demandé au modèle ("" : son réglage par défaut)
}

// ModelRef : un modèle chez un fournisseur (transcription, embeddings).
type ModelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type Config struct {
	Providers       []Provider `json:"providers"`
	Presets         []Preset   `json:"presets"`
	Active          string     `json:"active"`            // identifiant de la configuration active
	Transcribe      ModelRef   `json:"transcribe"`        // facultatif : dictée par API
	Embed           ModelRef   `json:"embed"`             // facultatif : recherche par le sens
	Language        string     `json:"language"`          // langue de l'interface : fr, en, ou "" (celle du navigateur)
	TZ              string     `json:"tz"`                // fuseau de l'utilisateur (posé par son navigateur)
	MaxContextChars int        `json:"max_context_chars"` // au-delà, les plus vieux messages sortent du contexte
	ReviewOff       bool       `json:"review_off"`        // couper la révision automatique de la mémoire
	SleepOff        bool       `json:"sleep_off"`         // couper le sommeil nocturne (consolidation)
	Services        []string   `json:"services,omitempty"` // services systemd (utilisateur) suivis dans l'écran Machine
}

var (
	mu  sync.RWMutex
	cfg = Config{MaxContextChars: defaultMaxContextChars}
)

// legacy : les réglages d'avant les fournisseurs (une seule API). Lus une fois
// pour être convertis, jamais réécrits.
type legacy struct {
	APIURL     string `json:"api_url"`
	APIKey     string `json:"api_key"`
	Model      string `json:"model"`
	EmbedModel string `json:"embed_model"`
	Effort     string `json:"effort"`
}

// Load lit config.json (absent = réglages par défaut). Un fichier illisible est
// une erreur : continuer avec des réglages vides l'écraserait à la première
// modification, clés d'API comprises.
func Load() error {
	mu.Lock()
	defer mu.Unlock()
	raw, err := os.ReadFile(home.Config())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var old legacy
	if err := errors.Join(json.Unmarshal(raw, &cfg), json.Unmarshal(raw, &old)); err != nil {
		return fmt.Errorf("%s est illisible (%w) : corrige-le ou supprime-le", home.Config(), err)
	}
	if len(cfg.Providers) == 0 && old.APIURL != "" {
		cfg.adopt(old)
	}
	if cfg.MaxContextChars <= 0 {
		cfg.MaxContextChars = defaultMaxContextChars
	}
	return nil
}

// adopt convertit des réglages à une seule API en un fournisseur et une
// configuration.
func (c *Config) adopt(old legacy) {
	p := Provider{ID: NewID(), Name: HostName(old.APIURL), URL: old.APIURL, Key: old.APIKey}
	c.Providers = []Provider{p}
	if old.Model != "" {
		preset := Preset{ID: NewID(), Name: old.Model, Provider: p.ID, Model: old.Model, Effort: old.Effort}
		c.Presets, c.Active = []Preset{preset}, preset.ID
	}
	if old.EmbedModel != "" {
		c.Embed = ModelRef{Provider: p.ID, Model: old.EmbedModel}
	}
}

// clone : une copie dont les listes sont indépendantes de l'original.
func (c Config) clone() Config {
	c.Providers, c.Presets, c.Services = slices.Clone(c.Providers), slices.Clone(c.Presets), slices.Clone(c.Services)
	return c
}

// Get : une copie des réglages.
func Get() Config {
	mu.RLock()
	defer mu.RUnlock()
	return cfg.clone()
}

// Update modifie les réglages puis les écrit sur le disque. Si fn renvoie une
// erreur, rien n'est changé.
func Update(fn func(*Config) error) error {
	mu.Lock()
	defer mu.Unlock()
	next := cfg.clone()
	if err := fn(&next); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := home.WriteFile(home.Config(), raw); err != nil {
		return err
	}
	cfg = next
	return nil
}

// NewID : un identifiant court pour un fournisseur ou une configuration.
func NewID() string { return strings.ToLower(rand.Text()[:8]) }

// HostName : un nom lisible tiré d'une URL d'API (« api.openai.com »), pour un
// fournisseur que l'utilisateur n'a pas nommé.
func HostName(raw string) string {
	if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.Host != "" {
		return u.Host
	}
	return "API"
}

// Provider : le fournisseur d'identifiant id.
func (c Config) Provider(id string) (Provider, bool) {
	i := slices.IndexFunc(c.Providers, func(p Provider) bool { return p.ID == id })
	if i < 0 {
		return Provider{}, false
	}
	return c.Providers[i], true
}

// Preset : la configuration active (à défaut la première ; ok=false s'il n'y en a aucune).
func (c Config) Preset() (Preset, bool) {
	if i := slices.IndexFunc(c.Presets, func(p Preset) bool { return p.ID == c.Active }); i >= 0 {
		return c.Presets[i], true
	}
	if len(c.Presets) > 0 {
		return c.Presets[0], true
	}
	return Preset{}, false
}

// endpoint : le point d'accès d'un modèle chez un fournisseur.
func (c Config) endpoint(ref ModelRef) llm.Endpoint {
	p, _ := c.Provider(ref.Provider)
	return llm.Endpoint{URL: p.URL, Key: p.Key, Model: ref.Model}
}

// Chat : le point d'accès de la discussion, c'est-à-dire la configuration
// active, les variables d'environnement passant devant.
func (c Config) Chat() llm.Endpoint {
	preset, _ := c.Preset()
	ep := c.endpoint(ModelRef{Provider: preset.Provider, Model: preset.Model})
	ep.Effort = preset.Effort
	if v := strings.TrimSpace(os.Getenv("AJEAN_API_URL")); v != "" {
		ep.URL, ep.Key = v, strings.TrimSpace(os.Getenv("AJEAN_API_KEY"))
	}
	if v := strings.TrimSpace(os.Getenv("AJEAN_MODEL")); v != "" {
		ep.Model = v
	}
	return ep
}

// Embeddings : le point d'accès de l'API d'embeddings (pas prêt si aucune n'est
// réglée, ou si c'est le moteur local qui est choisi).
func (c Config) Embeddings() llm.Endpoint { return c.endpoint(c.Embed) }

// Transcription : le point d'accès de la dictée (pas prêt si aucun n'est réglé).
func (c Config) Transcription() llm.Endpoint { return c.endpoint(c.Transcribe) }

// FromEnv : des variables d'environnement passent-elles devant la configuration active ?
func FromEnv() bool {
	return os.Getenv("AJEAN_API_URL") != "" || os.Getenv("AJEAN_MODEL") != ""
}

// Location : le fuseau de l'utilisateur. Inconnu ou introuvable → celui de la machine.
func Location() *time.Location {
	if tz := Get().TZ; tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			return l
		}
	}
	return time.Local
}

// Stamp : un instant tel qu'on le donne au modèle, dans le fuseau de
// l'utilisateur et avec le nom de ce fuseau.
func Stamp(t time.Time) string {
	label := Get().TZ
	if label == "" {
		label = "local time"
	}
	return t.In(Location()).Format("Monday 2006-01-02 15:04") + " (" + label + ")"
}

// KnownLanguage : l'interface existe-t-elle dans cette langue ? (vide = celle
// du navigateur). L'assistant, lui, répond dans la langue des messages : ce
// réglage ne le concerne pas.
func KnownLanguage(code string) bool { return slices.Contains([]string{"", "fr", "en", "vi"}, code) }
