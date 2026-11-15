package embedder

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ajean/internal/config"
)

// Les essais n'ont ni réseau ni llama.cpp : le « moteur » installé est ce
// programme de test lui-même, recopié sous le nom du serveur. Lancé avec
// fakeEnv, il se comporte comme lui (voir fakeEngine).
const (
	fakeEnv      = "AJEAN_FAKE_ENGINE"   // « serve » : répondre ; « crash » : tomber au démarrage
	fakeLaunches = "AJEAN_FAKE_LAUNCHES" // fichier où chaque lancement ajoute une ligne
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		fakeEngine(mode)
		return
	}
	retryPause = 10 * time.Millisecond
	os.Exit(m.Run())
}

// fakeEngine : un faux llama-server. Le vecteur qu'il rend dit quelle consigne
// précédait le texte : question, texte à retrouver, ou rien.
func fakeEngine(mode string) {
	var port, modelPath string
	for i, arg := range os.Args[:len(os.Args)-1] {
		switch arg {
		case "--port":
			port = os.Args[i+1]
		case "-m":
			modelPath = os.Args[i+1]
		}
	}
	if path := os.Getenv(fakeLaunches); path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, modelPath)
			f.Close()
		}
	}
	if mode == fakeParent {
		abandonChild(os.Args[1])
		return
	}
	if mode == "crash" {
		fmt.Fprintln(os.Stderr, "chargement…\nmodèle illisible")
		os.Exit(1)
	}
	query, document := prefixes("embeddinggemma")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("POST /v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.Model != filepath.Base(modelPath) {
			http.Error(w, `{"error":{"message":"modèle inconnu"}}`, http.StatusBadRequest)
			return
		}
		type item struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		var resp struct {
			Data []item `json:"data"`
		}
		for i, text := range req.Input {
			v := []float32{0, 0, 1}
			switch {
			case strings.HasPrefix(text, query):
				v = []float32{1, 0, 0}
			case strings.HasPrefix(text, document):
				v = []float32{0, 1, 0}
			}
			resp.Data = append(resp.Data, item{i, v})
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	_ = http.ListenAndServe("127.0.0.1:"+port, mux)
}

// setup isole un essai : dossier à part, réglages vides, moteur arrêté à la fin.
func setup(t *testing.T) {
	t.Helper()
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := config.Update(func(c *config.Config) error { *c = config.Config{}; return nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		Shutdown()
		st.Lock()
		st.err, st.retryAt = "", time.Time{}
		st.Unlock()
		Changed = nil
	})
}

// release : une fausse page de versions. Elle sert l'archive du faux moteur et
// des modèles ; hold retient la fin d'un modèle tant qu'il est vrai.
type release struct {
	*site
	engine asset
	hold   atomic.Bool
}

func (r *release) engineURL() string { return r.URL + "/" + r.engine.name }

func (r *release) model(file string) model {
	return model{url: r.URL + "/" + file, file: file, size: int64(len(modelBytes(file)))}
}

// modelBytes : le contenu servi pour un modèle (propre à son nom).
func modelBytes(file string) []byte {
	return []byte(strings.Repeat("GGUF "+file+"\n", 4000))
}

func fakeRelease(t *testing.T) *release {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	archive := filepath.Join(dir, "moteur.zip")
	out, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	for name, src := range map[string]string{"build/bin/" + serverName(): self, "build/bin/llama-cli.exe": "", "build/bin/fake.dll": ""} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if src == "" {
			continue
		}
		in, err := os.Open(src)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(w, in)
		in.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := fileSHA256(archive)
	if err != nil {
		t.Fatal(err)
	}
	r := &release{engine: asset{name: "moteur.zip", size: fi.Size(), sha256: sum}}
	r.site = host(t, nil, func(n int, w http.ResponseWriter, req *http.Request) bool {
		file := strings.TrimPrefix(req.URL.Path, "/")
		if file == r.engine.name {
			http.ServeFile(w, req, archive)
			return true
		}
		if !r.hold.Load() {
			http.ServeContent(w, req, "", time.Time{}, strings.NewReader(string(modelBytes(file))))
			return true
		}
		data := modelBytes(file)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		_, _ = w.Write(data[:len(data)/2])
		w.(http.Flusher).Flush()
		<-req.Context().Done()
		return true
	})
	return r
}

// waitFor attend que l'état du moteur local satisfasse ok.
func waitFor(t *testing.T, what string, ok func(Info) bool) Info {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		info := Status()
		if ok(info) {
			return info
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s : jamais atteint, état = %+v", what, info)
		}
	}
}

func is(s State) func(Info) bool { return func(i Info) bool { return i.State == s } }

func TestModelURL(t *testing.T) {
	m, err := parseModelURL("  ")
	if err != nil || m.url != defaultModelURL || m.file != "embeddinggemma-300M-Q8_0.gguf" || m.size != defaultModelSize {
		t.Errorf("modèle proposé d'office = %+v, %v", m, err)
	}
	m, err = parseModelURL("https://huggingface.co/org/repo/blob/main/sous/nomic-embed.Q4_K_M.gguf?download=true")
	if err != nil || m.url != "https://huggingface.co/org/repo/resolve/main/sous/nomic-embed.Q4_K_M.gguf?download=true" || m.file != "nomic-embed.Q4_K_M.gguf" || m.size != 0 {
		t.Errorf("adresse d'une page de fichier = %+v, %v", m, err)
	}
	for _, raw := range []string{
		"http://huggingface.co/org/repo/resolve/main/m.gguf",          // pas chiffré
		"https://huggingface.co.evil.example/org/resolve/main/m.gguf", // un autre site
		"https://evil.example/huggingface.co/m.gguf",
		"https://user@huggingface.co/org/repo/resolve/main/m.gguf",
		"https://huggingface.co:8443/org/repo/resolve/main/m.gguf",
		"https://huggingface.co/org/repo/resolve/main/m.bin", // pas un GGUF
		"https://huggingface.co/org/repo/resolve/main/m.gguf/",
		"https://huggingface.co/org/repo/resolve/main/..gguf",
		"m.gguf",
	} {
		if m, err := parseModelURL(raw); err == nil {
			t.Errorf("%s accepté : %+v", raw, m)
		}
	}
	if err := Install("https://example.com/m.gguf"); err == nil {
		t.Error("Install a accepté une adresse hors de Hugging Face")
	}
}

func TestPrefixesDependOnTheModel(t *testing.T) {
	if q, d := prefixes("EmbeddingGemma-300M-Q8_0.gguf"); q != "task: search result | query: " || d != "title: none | text: " {
		t.Errorf("consignes d'EmbeddingGemma = %q, %q", q, d)
	}
	if q, d := prefixes("nomic-embed-text.gguf"); q != "" || d != "" {
		t.Errorf("un modèle inconnu ne reçoit aucune consigne, pas %q, %q", q, d)
	}
}

// Une API d'embeddings garde le nom de son modèle pour espace : les vecteurs
// déjà calculés avec elle restent valables.
func TestActiveSourceIsTheConfiguredOne(t *testing.T) {
	setup(t)
	if _, ok := Active(); ok {
		t.Error("aucune source n'est réglée")
	}
	if err := config.Update(func(c *config.Config) error {
		c.Providers = []config.Provider{{ID: "p", URL: "https://api.example/v1"}}
		c.Embed = config.ModelRef{Provider: "p", Model: "text-embed"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if src, ok := Active(); !ok || src.Space != "text-embed" {
		t.Errorf("source par API = %+v, %v", src, ok)
	}
	// Le moteur local choisi mais pas installé : pas de recherche par le sens.
	if err := config.Update(func(c *config.Config) error { c.Embed = config.ModelRef{Provider: config.LocalEmbed}; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, ok := Active(); ok || Installed() || Status().Active {
		t.Error("le moteur local n'est pas installé")
	}
}

func TestInstallEmbedReplaceRemove(t *testing.T) {
	setup(t)
	t.Setenv(fakeEnv, "serve")
	var changes atomic.Int64
	Changed = func() { changes.Add(1) }
	rel := fakeRelease(t)

	if got := Status(); got != (Info{State: Absent}) {
		t.Fatalf("état de départ = %+v", got)
	}
	if err := start(rel.engineURL(), rel.engine, rel.model("embeddinggemma-essai.gguf")); err != nil {
		t.Fatal(err)
	}
	info := waitFor(t, "installation", is(Ready))
	if info.Model != "embeddinggemma-essai.gguf" || info.Size != int64(len(modelBytes(info.Model))) || info.Progress != nil || info.Error != "" || !info.Active {
		t.Errorf("état après installation = %+v", info)
	}
	if got := config.Get().Embed; got != (config.ModelRef{Provider: config.LocalEmbed}) {
		t.Errorf("réglage après installation = %+v", got)
	}
	// Seuls le serveur et ses bibliothèques sont gardés ; l'archive est partie.
	if got := names(t, engineDir()); !slices.Equal(got, []string{"fake.dll", serverName()}) {
		t.Errorf("dossier du moteur = %v", got)
	}
	if got := names(t, enginesDir()); !slices.Equal(got, []string{engineTag}) {
		t.Errorf("dossier des versions = %v", got)
	}
	if changes.Load() == 0 {
		t.Error("Changed n'a jamais été appelée")
	}

	// Le moteur démarre à la première demande, et reçoit la consigne du modèle.
	src, ok := Active()
	if !ok || src.Space != "local:embeddinggemma-essai.gguf" {
		t.Fatalf("source = %+v, %v", src, ok)
	}
	question, err := src.Embed(t.Context(), []string{"où est la tondeuse"}, true)
	if err != nil {
		t.Fatal(err)
	}
	texts, err := src.Embed(t.Context(), []string{"la tondeuse est au cabanon", "le chat dort"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(question[0], []float32{1, 0, 0}) || len(texts) != 2 || !slices.Equal(texts[1], []float32{0, 1, 0}) {
		t.Errorf("consignes reçues par le moteur : question %v, textes %v", question, texts)
	}
	if got := Status().State; got != Running {
		t.Errorf("état après une demande = %q", got)
	}

	// Arrêté, il repart tout seul à la demande suivante.
	stop()
	if got := Status().State; got != Ready {
		t.Errorf("état après l'arrêt = %q", got)
	}
	if _, err := Embed(t.Context(), []string{"encore"}, true); err != nil {
		t.Errorf("relance à la demande : %v", err)
	}

	// Un autre modèle remplace le premier : autre espace, et aucune consigne
	// pour un modèle inconnu.
	if err := start(rel.engineURL(), rel.engine, rel.model("autre.gguf")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "remplacement du modèle", func(i Info) bool { return i.State == Ready && i.Model == "autre.gguf" })
	if got := names(t, modelsDir()); !slices.Equal(got, []string{"autre.gguf"}) {
		t.Errorf("modèles = %v", got)
	}
	if src, _ := Active(); src.Space != "local:autre.gguf" {
		t.Errorf("espace après remplacement = %q", src.Space)
	}
	if vs, err := Embed(t.Context(), []string{"question"}, true); err != nil || !slices.Equal(vs[0], []float32{0, 0, 1}) {
		t.Errorf("vecteur du nouveau modèle = %v, %v", vs, err)
	}

	if err := Remove(); err != nil {
		t.Fatal(err)
	}
	if got := Status(); got != (Info{State: Absent}) {
		t.Errorf("état après suppression = %+v", got)
	}
	if _, err := os.Stat(filepath.Dir(modelsDir())); err == nil {
		t.Error("des fichiers du moteur sont restés")
	}
	if got := config.Get().Embed; got != (config.ModelRef{}) {
		t.Errorf("réglage après suppression = %+v", got)
	}
	if _, err := Embed(t.Context(), []string{"x"}, true); err == nil {
		t.Error("un moteur supprimé a répondu")
	}
}

func TestCancelThenResume(t *testing.T) {
	setup(t)
	rel := fakeRelease(t)
	rel.hold.Store(true)
	m := rel.model("essai.gguf")
	if err := start(rel.engineURL(), rel.engine, m); err != nil {
		t.Fatal(err)
	}
	info := waitFor(t, "téléchargement du modèle", func(i Info) bool {
		return i.State == Downloading && i.Progress.What == stepModel && i.Progress.Done > 0
	})
	if info.Model != "essai.gguf" || info.Size != m.size || info.Progress.Total != m.size || info.Active {
		t.Errorf("état pendant le téléchargement = %+v, avancement %+v", info, *info.Progress)
	}
	if err := start(rel.engineURL(), rel.engine, m); err == nil {
		t.Error("une seconde installation a démarré pendant la première")
	}

	Cancel()
	if got := Status(); got != (Info{State: Absent}) {
		t.Errorf("état après annulation = %+v", got)
	}
	part := filepath.Join(modelsDir(), m.file+partExt)
	if fi, err := os.Stat(part); err != nil || fi.Size() != m.size/2 {
		t.Fatalf("la moitié reçue devait être gardée (%v)", err)
	}

	// La reprise ne redemande ni le moteur ni la moitié déjà reçue.
	rel.hold.Store(false)
	before := len(rel.seen())
	if err := start(rel.engineURL(), rel.engine, m); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "reprise", is(Ready))
	if got := rel.seen()[before:]; !slices.Equal(got, []string{fmt.Sprintf("bytes=%d-", m.size/2)}) {
		t.Errorf("requêtes de la reprise = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(modelsDir(), m.file)); string(got) != string(modelBytes(m.file)) {
		t.Error("le modèle repris n'est pas celui du serveur")
	}
}

func TestFailedInstallIsReported(t *testing.T) {
	setup(t)
	rel := fakeRelease(t)
	wrong := rel.engine
	wrong.sha256 = strings.Repeat("0", 64)
	if err := start(rel.engineURL(), wrong, rel.model("essai.gguf")); err != nil {
		t.Fatal(err)
	}
	info := waitFor(t, "échec", is(Failed))
	if !strings.Contains(info.Error, "SHA-256") || info.Active || Installed() {
		t.Errorf("état après une archive inattendue = %+v", info)
	}
	if got := names(t, enginesDir()); len(got) != 0 {
		t.Errorf("l'archive refusée a laissé %v", got)
	}
}

func TestFailedLaunchIsNotRetriedAtOnce(t *testing.T) {
	setup(t)
	launches := filepath.Join(t.TempDir(), "lancements")
	t.Setenv(fakeEnv, "crash")
	t.Setenv(fakeLaunches, launches)
	rel := fakeRelease(t)
	if err := start(rel.engineURL(), rel.engine, rel.model("essai.gguf")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "installation", is(Ready))

	_, first := Embed(t.Context(), []string{"x"}, true)
	if first == nil || !strings.HasSuffix(first.Error(), "modèle illisible") {
		t.Fatalf("erreur du lancement = %v", first)
	}
	if info := Status(); info.State != Failed || info.Error != first.Error() {
		t.Errorf("état après un lancement raté = %+v", info)
	}
	if _, again := Embed(t.Context(), []string{"x"}, true); again == nil || again.Error() != first.Error() {
		t.Errorf("seconde demande = %v", again)
	}
	if raw, _ := os.ReadFile(launches); strings.Count(string(raw), "\n") != 1 {
		t.Errorf("lancements = %q, un seul attendu", raw)
	}

	// Réinstaller efface l'échec : la demande suivante retente.
	t.Setenv(fakeEnv, "serve")
	if err := start(rel.engineURL(), rel.engine, rel.model("essai.gguf")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "réinstallation", is(Ready))
	if _, err := Embed(t.Context(), []string{"x"}, true); err != nil {
		t.Errorf("après réinstallation : %v", err)
	}
}

// fakeParent : mode du faux moteur où il joue AJEAN lui-même. Il lance un faux
// serveur comme le fait spawn, puis se termine sans l'arrêter (voir
// TestEngineDiesWithItsParent).
const fakeParent = "parent"

func abandonChild(port string) {
	cmd := exec.Command(os.Args[0], "--port", port)
	cmd.Env = append(os.Environ(), fakeEnv+"=serve")
	prepare(cmd)
	if err := cmd.Start(); err != nil {
		os.Exit(1)
	}
	adopt(cmd)
	for !healthy("http://127.0.0.1:" + port + "/health") {
		time.Sleep(20 * time.Millisecond)
	}
}
