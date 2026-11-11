package ajean

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServiceValidate(t *testing.T) {
	s := Service{ID: "basalt", URL: "http://127.0.0.1:8777/v1/"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.URL != "http://127.0.0.1:8777/v1" || s.Name != "basalt" {
		t.Fatalf("normalisation : %+v", s)
	}
	for _, bad := range []Service{{ID: "Bad ID", URL: "http://x/v1"}, {ID: "ok", URL: "ftp://x"}, {ID: "ok", URL: ""}} {
		if bad.Validate() == nil {
			t.Fatalf("aurait dû refuser %+v", bad)
		}
	}
}

func TestServiceStoreAndPeak(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := PutService(Service{ID: "a", URL: "http://127.0.0.1:1/v1", Need: ServiceNeed{RAMGB: 4}}); err != nil {
		t.Fatal(err)
	}
	if err := PutService(Service{ID: "a", Name: "A2", URL: "http://127.0.0.1:1/v1", Need: ServiceNeed{RAMGB: 4}}); err != nil {
		t.Fatal(err)
	}
	list, _ := LoadServices()
	if len(list) != 1 || list[0].Name != "A2" {
		t.Fatalf("remplacement : %+v", list)
	}
	updateServiceState("a", func(r *serviceRuntime) { r.Peak = ServiceNeed{RAMGB: 9, VRAMGB: map[string]float64{"0": 7}} })
	s, _ := GetService("a")
	n := effectiveNeed(&s)
	if n.RAMGB != 9 || n.VRAMGB["0"] != 7 {
		t.Fatalf("besoin effectif : %+v", n)
	}
	if err := DeleteService("a"); err != nil {
		t.Fatal(err)
	}
	if list, _ := LoadServices(); len(list) != 0 {
		t.Fatal("non supprimé")
	}
}

func TestLocalPort(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:8777/v1": "8777",
		"http://localhost/v1":      "80",
		"https://api.example.com":  "",
	}
	for u, want := range cases {
		if got := localPort(&Service{URL: u}); got != want {
			t.Errorf("%s : %q, attendu %q", u, got, want)
		}
	}
}

func TestPreflightRefusals(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	s := Service{ID: "x", Exec: "/introuvable/moteur", URL: "http://127.0.0.1:1/v1"}
	if err := preflightService(&s, false); err == nil || !strings.Contains(err.Error(), "introuvable") {
		t.Fatalf("programme absent : %v", err)
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	s = Service{ID: "y", URL: srv.URL + "/v1"}
	if err := preflightService(&s, true); err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("port occupé : %v", err)
	}
	s = Service{ID: "z", URL: "http://127.0.0.1:1/v1", Need: ServiceNeed{RAMGB: 1 << 20}}
	if _, total := ramUsageMB(); total > 0 {
		if err := preflightService(&s, false); err == nil || !strings.Contains(err.Error(), "RAM") {
			t.Fatalf("RAM : %v", err)
		}
	}
}

func TestProbeReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	if err := probeReady(&Service{URL: srv.URL + "/v1", APIKey: "k"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if probeReady(&Service{URL: srv.URL + "/v1"}, time.Second) == nil {
		t.Fatal("sans clé : aurait dû échouer")
	}
}

func TestServiceSlug(t *testing.T) {
	cases := map[string]string{
		"QWEN 3.8 27B EXL3 SC4": "qwen-3-8-27b-exl3-sc4",
		"Modèle été":            "modele-ete",
		"***":                   "modele",
	}
	for in, want := range cases {
		if got := serviceSlug(in); got != want {
			t.Errorf("%q : %q, attendu %q", in, got, want)
		}
	}
}

func TestStrikeFor(t *testing.T) {
	at := time.Date(2026, 11, 9, 14, 32, 10, 0, time.Local)
	kills := []GuardKill{
		{Name: "ajean-svc-a", Bytes: 20 << 30, At: at.Add(-time.Hour).UnixMilli()},
		{Name: "autre", Bytes: 1 << 30, At: at.UnixMilli()},
		{Name: "ajean-svc-a", Bytes: 21 << 30, At: at.UnixMilli()},
	}
	k, ok := strikeFor(kills, "ajean-svc-a.service", at.Add(-15*time.Second), at.Add(2*time.Second))
	if !ok || k.Bytes != 21<<30 {
		t.Fatalf("frappe attendue sur ajean-svc-a : %+v %v", k, ok)
	}
	if _, ok := strikeFor(kills, "ajean-svc-a", at.Add(time.Second), at.Add(time.Minute)); ok {
		t.Fatal("aucune frappe après l'instant demandé")
	}
	if _, ok := strikeFor(kills, "ajean-svc-b", time.Time{}, at.Add(time.Minute)); ok {
		t.Fatal("une autre unité ne compte pas")
	}
	if txt := guardStrikeText(k); !strings.Contains(txt, "21.0 Go") || !strings.Contains(txt, "14:32") {
		t.Fatalf("texte inattendu : %s", txt)
	}
}

func TestParseUnitTime(t *testing.T) {
	want := time.Date(2026, 11, 9, 14, 32, 10, 0, time.Local)
	for _, in := range []string{"Mon 2026-11-09 14:32:10 CET", "lun. 2026-11-09 14:32:10 CET", "2026-11-09 14:32:10"} {
		if got := parseUnitTime(in); !got.Equal(want) {
			t.Errorf("%q : %v, attendu %v", in, got, want)
		}
	}
	for _, in := range []string{"", "n/a", "0"} {
		if got := parseUnitTime(in); !got.IsZero() {
			t.Errorf("%q : %v, attendu zéro", in, got)
		}
	}
}
