package ajean

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestExternalModels(t *testing.T) {
	var gotAuth, gotPath string
	body := `{"data":[{"id":"qwen-max"},{"id":"deepseek-v3"},{"id":"qwen-max"},{"id":""}]}`
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := externalModels(context.Background(), srv.URL+"/compatible-mode/v1", " sk-test ")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"deepseek-v3", "qwen-max"}; !reflect.DeepEqual(got, want) {
		t.Errorf("modèles = %v, attendu %v", got, want)
	}
	if gotPath != "/compatible-mode/v1/models" || gotAuth != "Bearer sk-test" {
		t.Errorf("requête = %s (%q)", gotPath, gotAuth)
	}

	// Tableau nu et forme {"models":[{"name":…}]}.
	body = `[{"id":"b"},{"id":"a"}]`
	if got, _ := externalModels(context.Background(), srv.URL+"/v1", ""); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("tableau nu = %v", got)
	}
	body = `{"models":[{"name":"llama3"}]}`
	if got, _ := externalModels(context.Background(), srv.URL+"/v1", ""); !reflect.DeepEqual(got, []string{"llama3"}) {
		t.Errorf("forme models = %v", got)
	}

	// Refus du fournisseur : l'erreur remonte, pas une liste vide.
	status, body = 401, `{"error":{"message":"clé invalide"}}`
	if _, err := externalModels(context.Background(), srv.URL+"/v1", "x"); err == nil {
		t.Error("401 : erreur attendue")
	}
	if _, err := externalModels(context.Background(), "", ""); err == nil {
		t.Error("URL vide : erreur attendue")
	}
}
