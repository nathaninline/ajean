package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ajean/internal/config"
)

const local = "127.0.0.1:8090"

// post envoie du JSON à une route et décode la réponse dans out (s'il est donné).
func post(t *testing.T, path, body string, out any) int {
	t.Helper()
	rec := do(t, http.MethodPost, path, local, "", body)
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s : réponse illisible (%v) : %s", path, err, rec.Body)
		}
	}
	return rec.Code
}

// fresh repart de réglages vides, dans un dossier à part.
func fresh(t *testing.T) {
	t.Helper()
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := config.Update(func(c *config.Config) error { *c = config.Config{}; return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestProvidersAndPresets(t *testing.T) {
	fresh(t)
	var a, b, fast, deep struct{ ID string }
	if code := post(t, "/api/providers/save", `{"name":"Alpha","api_url":"https://alpha.example/v1","api_key":"sk-alpha"}`, &a); code != http.StatusOK || a.ID == "" {
		t.Fatalf("création du fournisseur : statut %d, id %q", code, a.ID)
	}
	post(t, "/api/providers/save", `{"api_url":"https://beta.example/v1"}`, &b)
	if p, _ := config.Get().Provider(b.ID); p.Name != "beta.example" {
		t.Errorf("un fournisseur sans nom prend celui de son hôte, pas %q", p.Name)
	}

	// La première configuration devient l'active ; on en change à la demande.
	post(t, "/api/presets/save", `{"name":"Rapide","provider":"`+a.ID+`","model":"petit"}`, &fast)
	post(t, "/api/presets/save", `{"provider":"`+b.ID+`","model":"gros","effort":"High"}`, &deep)
	if ep := config.Get().Chat(); ep.URL != "https://alpha.example/v1" || ep.Key != "sk-alpha" || ep.Model != "petit" {
		t.Errorf("discussion après la première configuration = %+v", ep)
	}
	if code := post(t, "/api/presets/select", `{"id":"`+deep.ID+`"}`, nil); code != http.StatusOK {
		t.Fatalf("sélection : statut %d", code)
	}
	if ep := config.Get().Chat(); ep.URL != "https://beta.example/v1" || ep.Model != "gros" || ep.Effort != "high" {
		t.Errorf("discussion après sélection = %+v", ep)
	}

	// L'effort se règle sur la configuration active, et elle seule.
	post(t, "/api/effort", `{"effort":""}`, nil)
	if code := post(t, "/api/effort", `{"effort":"high\", \"x\":1"}`, nil); code != http.StatusBadRequest {
		t.Errorf("effort douteux : statut %d, attendu 400", code)
	}
	c := config.Get()
	if c.Chat().Effort != "" || c.Presets[0].Effort != "" || c.Presets[1].Name != "gros" {
		t.Errorf("configurations après réglage de l'effort = %+v", c.Presets)
	}

	// Un fournisseur encore utilisé ne se supprime pas ; supprimer l'active rend la main à une autre.
	if code := post(t, "/api/providers/delete", `{"id":"`+b.ID+`"}`, nil); code != http.StatusConflict {
		t.Errorf("suppression d'un fournisseur utilisé : statut %d, attendu 409", code)
	}
	post(t, "/api/presets/delete", `{"id":"`+deep.ID+`"}`, nil)
	if active, _ := config.Get().Preset(); active.ID != fast.ID {
		t.Errorf("configuration active après suppression = %+v", active)
	}
	if code := post(t, "/api/providers/delete", `{"id":"`+b.ID+`"}`, nil); code != http.StatusOK {
		t.Errorf("suppression d'un fournisseur libre : statut %d", code)
	}
}

// Changer l'URL d'un fournisseur sans redonner sa clé ne doit jamais envoyer la
// clé enregistrée à la nouvelle adresse, ni pour un essai ni à l'enregistrement.
func TestKeyIsNotKeptForAnotherURL(t *testing.T) {
	fresh(t)
	var p struct{ ID string }
	post(t, "/api/providers/save", `{"name":"Alpha","api_url":"https://alpha.example/v1","api_key":"sk-secret"}`, &p)

	same := providerInput{ID: p.ID, URL: "https://alpha.example/v1"}.resolve(config.Get())
	if same.Key != "sk-secret" {
		t.Errorf("même URL : clé = %q, la clé enregistrée était attendue", same.Key)
	}
	other := providerInput{ID: p.ID, URL: "https://evil.example/v1"}.resolve(config.Get())
	if other.Key != "" {
		t.Errorf("autre URL : la clé enregistrée a été gardée (%q)", other.Key)
	}

	post(t, "/api/providers/save", `{"id":"`+p.ID+`","api_url":"https://evil.example/v1"}`, nil)
	if saved, _ := config.Get().Provider(p.ID); saved.Key != "" || saved.Name != "Alpha" {
		t.Errorf("après changement d'URL : %+v", saved)
	}
}

func TestConfigNeverShowsKeys(t *testing.T) {
	fresh(t)
	post(t, "/api/providers/save", `{"api_url":"https://alpha.example/v1","api_key":"sk-secret"}`, nil)
	for _, path := range []string{"/api/config", "/api/state"} {
		if body := do(t, http.MethodGet, path, local, "", "").Body.String(); strings.Contains(body, "sk-secret") {
			t.Errorf("%s laisse sortir la clé : %s", path, body)
		}
	}
}

func TestTranscribeAndEmbedSettings(t *testing.T) {
	fresh(t)
	var heard struct{ model, name string }
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, head, _ := r.FormFile("file")
		heard.model, heard.name = r.FormValue("model"), head.Filename
		_, _ = w.Write([]byte(`{"text":" bonjour AJEAN "}`))
	}))
	defer api.Close()
	var p struct{ ID string }
	post(t, "/api/providers/save", `{"api_url":"`+api.URL+`/v1"}`, &p)

	audio := func() *httptest.ResponseRecorder {
		var form bytes.Buffer
		w := multipart.NewWriter(&form)
		part, _ := w.CreateFormFile("file", "dictee.webm")
		_, _ = part.Write([]byte("son"))
		_ = w.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/transcribe", &form)
		req.Host, req.RemoteAddr = local, "127.0.0.1:50000"
		req.Header.Set("Origin", "http://"+local)
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		New().ServeHTTP(rec, req)
		return rec
	}
	if got := audio().Code; got != http.StatusBadRequest {
		t.Errorf("sans API de transcription : statut %d, attendu 400", got)
	}
	if code := post(t, "/api/config", `{"transcribe":{"provider":"`+p.ID+`","model":"whisper"},"embed":{"provider":"inconnu","model":"x"}}`, nil); code != http.StatusBadRequest {
		t.Errorf("fournisseur inconnu : statut %d, attendu 400", code)
	}
	post(t, "/api/config", `{"transcribe":{"provider":"`+p.ID+`","model":"whisper"},"sleep":false,"language":"fr"}`, nil)
	if c := config.Get(); c.Transcribe.Model != "whisper" || !c.SleepOff || c.ReviewOff || c.Language != "fr" {
		t.Fatalf("réglages = %+v", c)
	}
	rec := audio()
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"text":"bonjour AJEAN"`) {
		t.Fatalf("transcription : statut %d, %s", rec.Code, rec.Body)
	}
	if heard.model != "whisper" || heard.name != "dictee.webm" {
		t.Errorf("l'API a reçu %+v", heard)
	}
	// Un modèle sans fournisseur ne veut rien dire : la dictée est désactivée.
	post(t, "/api/config", `{"transcribe":{"provider":"","model":"whisper"}}`, nil)
	if config.Get().Transcription().Ready() {
		t.Error("la transcription aurait dû être désactivée")
	}
	if code := post(t, "/api/config", `{"language":"klingon!"}`, nil); code != http.StatusBadRequest {
		t.Errorf("langue douteuse : statut %d, attendu 400", code)
	}
}
