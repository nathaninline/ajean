package server

import (
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"

	"ajean/internal/config"
	"ajean/internal/embedder"
	"ajean/internal/home"
)

// engineVersion : la version de llama.cpp que le paquet embedder installe, donc
// le dossier où il cherche son serveur.
const engineVersion = "b12257"

func TestLocalEmbedRoutes(t *testing.T) {
	fresh(t)
	t.Cleanup(embedder.Shutdown)
	state := func() string {
		return strings.TrimSpace(do(t, http.MethodGet, "/api/embed/local", local, "", "").Body.String())
	}
	const absent = `{"state":"absent","model":"","size":0,"progress":null,"error":"","active":false}`
	if got := state(); got != absent {
		t.Errorf("état sans moteur = %s", got)
	}

	// Rien d'installé : le moteur local ne peut être ni choisi ni essayé, et
	// seul un GGUF de Hugging Face peut être demandé.
	if code := post(t, "/api/config", `{"embed":{"provider":"local","model":""}}`, nil); code != http.StatusBadRequest {
		t.Errorf("choix d'un moteur absent : statut %d, attendu 400", code)
	}
	if code := post(t, "/api/embed/test", `{"provider":"local"}`, nil); code != http.StatusBadGateway {
		t.Errorf("essai d'un moteur absent : statut %d, attendu 502", code)
	}
	if code := post(t, "/api/embed/local/install", `{"url":"https://example.com/modele.gguf"}`, nil); code != http.StatusBadRequest {
		t.Errorf("modèle hors de Hugging Face : statut %d, attendu 400", code)
	}
	if code := post(t, "/api/embed/local/cancel", `{}`, nil); code != http.StatusOK {
		t.Errorf("annulation sans installation : statut %d", code)
	}

	// Un moteur en place se choisit comme un fournisseur, sans modèle.
	server := "llama-server"
	if runtime.GOOS == "windows" {
		server += ".exe"
	}
	for _, path := range []string{home.State("embedder", "engine", engineVersion, server), home.State("embedder", "models", "essai.gguf")} {
		if err := home.WriteFile(path, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if code := post(t, "/api/config", `{"embed":{"provider":" local ","model":"ignoré"}}`, nil); code != http.StatusOK {
		t.Fatalf("choix du moteur local : statut %d", code)
	}
	if got := config.Get().Embed; got != (config.ModelRef{Provider: config.LocalEmbed}) {
		t.Errorf("réglage = %+v", got)
	}
	if body := do(t, http.MethodGet, "/api/config", local, "", "").Body.String(); !strings.Contains(body, `"embed":{"provider":"local","model":""}`) {
		t.Errorf("/api/config ne rend pas le réglage tel quel : %s", body)
	}
	if got := state(); got != `{"state":"ready","model":"essai.gguf","size":1,"progress":null,"error":"","active":true}` {
		t.Errorf("état avec moteur = %s", got)
	}

	// Le supprimer coupe la recherche par le sens qui passait par lui.
	if code := post(t, "/api/embed/local/remove", `{}`, nil); code != http.StatusOK {
		t.Fatalf("suppression : statut %d", code)
	}
	if got := state(); got != absent || config.Get().Embed != (config.ModelRef{}) {
		t.Errorf("après suppression : état %s, réglage %+v", got, config.Get().Embed)
	}
	if _, err := os.Stat(home.State("embedder")); err == nil {
		t.Error("le dossier du moteur est resté")
	}
}
