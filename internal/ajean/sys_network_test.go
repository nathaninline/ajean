package ajean

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEngineHostDefautEtBascule(t *testing.T) {
	testHome(t)
	// Clé absente : c'est le défaut de backend_serve.go qui s'applique, soit toutes
	// les interfaces. Annoncer « fermé » ici serait un mensonge.
	if h := engineHost(); h != hostAllInterfaces {
		t.Fatalf("HOST absent → %q ; attendu %q", h, hostAllInterfaces)
	}
	if !lanExposed() {
		t.Fatal("HOST absent devrait compter comme exposé")
	}
	if _, err := setLANExposure(false); err != nil {
		t.Fatal(err)
	}
	if lanExposed() || ReadConfig()["HOST"] != hostLocalOnly {
		t.Fatalf("fermeture sans effet : HOST=%q", ReadConfig()["HOST"])
	}
	if _, err := setLANExposure(true); err != nil {
		t.Fatal(err)
	}
	if !lanExposed() || ReadConfig()["HOST"] != hostAllInterfaces {
		t.Fatalf("ouverture sans effet : HOST=%q", ReadConfig()["HOST"])
	}
}

// « localhost » écrit à la main vaut boucle locale, au même titre que 127.0.0.1.
func TestLanExposedReconnaitLocalhost(t *testing.T) {
	testHome(t)
	for _, h := range []string{"127.0.0.1", "localhost", "::1", "LOCALHOST"} {
		if err := SetConfigKey("HOST", h); err != nil {
			t.Fatal(err)
		}
		if lanExposed() {
			t.Errorf("HOST=%q compté comme exposé", h)
		}
	}
	for _, h := range []string{"0.0.0.0", "192.168.1.25", "::"} {
		if err := SetConfigKey("HOST", h); err != nil {
			t.Fatal(err)
		}
		if !lanExposed() {
			t.Errorf("HOST=%q compté comme fermé", h)
		}
	}
}

// Une machine volontairement fermée ne doit pas se rouvrir toute seule parce
// qu'on bascule sur un preset écrit avant que HOST existe.
func TestHostSurvitAuChangementDePreset(t *testing.T) {
	testHome(t)
	if _, err := setLANExposure(false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "vieux.env")
	if err := os.WriteFile(p, []byte("MODEL=\"x.gguf\"\nCTX=4096\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyPresetFile(p); err != nil {
		t.Fatal(err)
	}
	if ReadConfig()["HOST"] != hostLocalOnly {
		t.Fatalf("HOST perdu à la bascule : %q", ReadConfig()["HOST"])
	}
	// Mais un preset qui revendique HOST garde le dernier mot.
	if err := os.WriteFile(p, []byte("MODEL=\"x.gguf\"\nHOST=\"0.0.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyPresetFile(p); err != nil {
		t.Fatal(err)
	}
	if ReadConfig()["HOST"] != hostAllInterfaces {
		t.Fatalf("le preset n'a pas pu imposer HOST : %q", ReadConfig()["HOST"])
	}
}

func TestHandleNetwork(t *testing.T) {
	testHome(t)
	rr := httptest.NewRecorder()
	handleNetwork(rr, httptest.NewRequest("POST", "/api/network", strings.NewReader(`{"exposed":false}`)))
	if rr.Code != 200 {
		t.Fatalf("HTTP %d : %s", rr.Code, rr.Body)
	}
	var out struct {
		OK     bool      `json:"ok"`
		Status netStatus `json:"status"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Status.Exposed || out.Status.Host != hostLocalOnly {
		t.Fatalf("état inattendu : %+v", out.Status)
	}
	if out.Status.Port == 0 || !strings.Contains(out.Status.URL, "/v1") {
		t.Fatalf("endpoint mal formé : %+v", out.Status)
	}
}
