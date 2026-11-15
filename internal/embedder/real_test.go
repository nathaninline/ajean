package embedder

import (
	"math"
	"os"
	"testing"
	"time"
)

// realEnv : dossier où faire l'essai réel. Sans elle, l'essai est sauté : il
// télécharge le vrai moteur et le vrai modèle (environ 360 Mo) depuis GitHub et
// Hugging Face, puis les supprime.
const realEnv = "AJEAN_EMBED_REAL"

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}

// L'installation réelle, de bout en bout : archive officielle de la version
// épinglée (empreinte vérifiée), modèle proposé d'office, lancement, et des
// vecteurs qui rapprochent bien deux phrases de même sens.
func TestRealEngine(t *testing.T) {
	dir := os.Getenv(realEnv)
	if dir == "" {
		t.Skip(realEnv + " n'est pas définie")
	}
	setup(t)
	t.Setenv("AJEAN_HOME", dir)
	began := time.Now()
	if err := Install(""); err != nil {
		t.Fatal(err)
	}
	var info Info
	for deadline := began.Add(time.Hour); ; time.Sleep(time.Second) {
		if info = Status(); info.State != Downloading {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("téléchargement trop long : %+v", *info.Progress)
		}
	}
	if info.State != Ready || !info.Active || info.Size != defaultModelSize {
		t.Fatalf("état après installation = %+v", info)
	}
	t.Logf("installé en %s : %v", time.Since(began).Round(time.Second), names(t, engineDir()))

	began = time.Now()
	question, err := Embed(t.Context(), []string{"où dort le chat ?"}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("démarrage et première demande : %s, %d dimensions", time.Since(began).Round(time.Millisecond), len(question[0]))
	began = time.Now()
	texts, err := Embed(t.Context(), []string{
		"Le chat dort sur le canapé du salon.",
		"Un félin fait la sieste sur le sofa.",
		"La bourse de Paris a clôturé en hausse de deux pour cent.",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("trois textes : %s", time.Since(began).Round(time.Millisecond))
	began = time.Now()
	if _, err := Embed(t.Context(), []string{"et une question de plus"}, true); err != nil {
		t.Fatal(err)
	}
	t.Logf("une question, moteur chaud : %s", time.Since(began).Round(time.Millisecond))

	near, far := cosine(texts[0], texts[1]), cosine(texts[0], texts[2])
	t.Logf("phrases proches : %.3f ; sans rapport : %.3f", near, far)
	t.Logf("question ↔ chat : %.3f ; question ↔ bourse : %.3f", cosine(question[0], texts[0]), cosine(question[0], texts[2]))
	if near < far+0.2 {
		t.Errorf("deux phrases de même sens (%.3f) ne sont pas nettement plus proches que deux phrases sans rapport (%.3f)", near, far)
	}
	if got := Status().State; got != Running {
		t.Errorf("état en marche = %q", got)
	}
	// Le tout premier lancement paie la découverte des fichiers par le système
	// (antivirus, cache du disque) : le suivant donne le temps habituel.
	stop()
	began = time.Now()
	if _, err := Embed(t.Context(), []string{"après un arrêt"}, true); err != nil {
		t.Fatal(err)
	}
	t.Logf("relance et demande : %s", time.Since(began).Round(time.Millisecond))
	if err := Remove(); err != nil {
		t.Fatal(err)
	}
}
