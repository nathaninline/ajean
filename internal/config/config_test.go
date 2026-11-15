package config

import (
	"os"
	"testing"

	"ajean/internal/home"
)

// Des réglages d'avant les fournisseurs (une seule API) sont repris tels quels :
// l'utilisateur ne doit pas avoir à ressaisir sa clé.
func TestLoadAdoptsSingleAPISettings(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	old := `{"api_url":"https://api.example/v1","api_key":"sk-old","model":"m1","embed_model":"e1","effort":"high","tz":"Europe/Paris","review_off":true}`
	if err := os.WriteFile(home.Config(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Load(); err != nil {
		t.Fatal(err)
	}
	c := Get()
	if len(c.Providers) != 1 || len(c.Presets) != 1 || c.Providers[0].Name != "api.example" {
		t.Fatalf("réglages repris = %+v", c)
	}
	if ep := c.Chat(); ep.URL != "https://api.example/v1" || ep.Key != "sk-old" || ep.Model != "m1" || ep.Effort != "high" {
		t.Errorf("discussion = %+v", ep)
	}
	if ep := c.Embeddings(); ep.Model != "e1" || ep.Key != "sk-old" {
		t.Errorf("embeddings = %+v", ep)
	}
	if c.TZ != "Europe/Paris" || !c.ReviewOff || c.Transcription().Ready() {
		t.Errorf("le reste des réglages = %+v", c)
	}
}

func TestLoadRefusesABrokenFile(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := os.WriteFile(home.Config(), []byte(`{"providers": [`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Load(); err == nil {
		t.Error("un fichier illisible doit être une erreur, pas des réglages vides")
	}
}

// Une modification refusée ne laisse aucune trace.
func TestUpdateIsAllOrNothing(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	_ = Update(func(c *Config) error { *c = Config{Providers: []Provider{{ID: "a"}}}; return nil })
	err := Update(func(c *Config) error {
		c.Providers[0].Name = "changé"
		c.Providers = append(c.Providers, Provider{ID: "b"})
		return os.ErrInvalid
	})
	if err == nil {
		t.Fatal("erreur attendue")
	}
	if c := Get(); len(c.Providers) != 1 || c.Providers[0].Name != "" {
		t.Errorf("réglages après une modification refusée = %+v", c.Providers)
	}
}

func TestEnvironmentGoesFirst(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	_ = Update(func(c *Config) error {
		*c = Config{Providers: []Provider{{ID: "a", URL: "https://a.example/v1", Key: "sk-a"}}, Presets: []Preset{{ID: "p", Provider: "a", Model: "m"}}}
		return nil
	})
	t.Setenv("AJEAN_API_URL", "https://env.example/v1")
	t.Setenv("AJEAN_MODEL", "env-model")
	if ep := Get().Chat(); ep.URL != "https://env.example/v1" || ep.Key != "" || ep.Model != "env-model" {
		t.Errorf("discussion = %+v : l'URL de l'environnement ne doit pas recevoir la clé enregistrée", ep)
	}
}
