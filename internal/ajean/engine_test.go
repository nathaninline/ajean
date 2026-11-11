package ajean

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Le manifeste embarqué doit correspondre à la version épinglée, et chaque
// machine prise en charge doit y trouver de quoi s'installer. C'est le test qui
// casse si un « bump » a été fait à moitié, ou si llama.cpp renomme ses archives.
func TestEngineManifestCouvreChaqueMachine(t *testing.T) {
	m, err := engineAssets()
	if err != nil {
		t.Fatal(err)
	}
	if m.Tag == "" || m.Tag != strings.TrimSpace(llamaCppVersionFile) {
		t.Fatalf("manifeste %q ≠ LLAMA_CPP_VERSION %q", m.Tag, llamaCppVersionFile)
	}
	for _, a := range m.Assets {
		if len(a.SHA256) != 64 || a.Size <= 0 {
			t.Errorf("%s : empreinte ou taille manquante", a.Name)
		}
	}
	ids := func(vs []engineVariant) string {
		var s []string
		for _, v := range vs {
			s = append(s, v.ID)
		}
		return strings.Join(s, ",")
	}
	cases := []struct {
		name string
		hw   engineHW
		want string // suite des variantes attendue (préfixe)
	}{
		{"windows nvidia", engineHW{OS: "windows", Arch: "amd64", Nvidia: true, DriverCuda: 99, Vulkan: true}, "cuda,"},
		{"windows amd/intel", engineHW{OS: "windows", Arch: "amd64", Vulkan: true}, "vulkan,cpu"},
		{"windows arm", engineHW{OS: "windows", Arch: "arm64", Vulkan: true}, "cpu"},
		{"mac arm", engineHW{OS: "darwin", Arch: "arm64"}, "metal"},
		{"mac intel", engineHW{OS: "darwin", Arch: "amd64"}, "metal"},
		{"linux nvidia", engineHW{OS: "linux", Arch: "amd64", Nvidia: true, DriverCuda: 99, Vulkan: true}, "cuda,"},
		{"linux rocm", engineHW{OS: "linux", Arch: "amd64", ROCm: true, Vulkan: true}, "rocm,vulkan,cpu"},
		{"linux sans gpu", engineHW{OS: "linux", Arch: "amd64"}, "cpu"},
		{"linux arm", engineHW{OS: "linux", Arch: "arm64", Vulkan: true}, "vulkan,cpu"},
	}
	for _, c := range cases {
		vs, err := engineCandidates(m.Assets, c.hw, "")
		if err != nil {
			t.Errorf("%s : %v", c.name, err)
			continue
		}
		got := ids(vs)
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("%s : variantes %q, attendu %q…", c.name, got, c.want)
		}
		if !strings.HasSuffix(got, "cpu") && c.hw.OS != "darwin" {
			t.Errorf("%s : le CPU doit toujours fermer la liste (%q)", c.name, got)
		}
		for _, v := range vs {
			if v.ID == "cuda" && v.Extra == nil {
				t.Errorf("%s : %s sans ses bibliothèques CUDA", c.name, v.Label)
			}
		}
	}
}

// Les builds CUDA plus récents que le pilote sont écartés ; parmi les autres,
// le plus récent d'abord. Pilote de version inconnue : le plus ancien d'abord.
func TestEngineCandidatesCuda(t *testing.T) {
	assets := []engineAsset{
		{Name: "llama-b1-bin-win-cuda-12.4-x64.zip"}, {Name: "llama-b1-bin-win-cuda-13.5-x64.zip"},
		{Name: "cudart-llama-bin-win-cuda-12.4-x64.zip"}, {Name: "cudart-llama-bin-win-cuda-13.5-x64.zip"},
		{Name: "llama-b1-bin-win-vulkan-x64.zip"}, {Name: "llama-b1-bin-win-cpu-x64.zip"},
	}
	labels := func(hw engineHW, forced string) string {
		vs, err := engineCandidates(assets, hw, forced)
		if err != nil {
			return "ERR " + err.Error()
		}
		var s []string
		for _, v := range vs {
			s = append(s, v.Main.Name[len("llama-b1-bin-win-"):])
		}
		return strings.Join(s, " ")
	}
	hw := engineHW{OS: "windows", Arch: "amd64", Nvidia: true, Vulkan: true}
	hw.DriverCuda = 13.6
	if got := labels(hw, ""); got != "cuda-13.5-x64.zip cuda-12.4-x64.zip vulkan-x64.zip cpu-x64.zip" {
		t.Errorf("pilote 13.6 : %s", got)
	}
	hw.DriverCuda = 12.9
	if got := labels(hw, ""); got != "cuda-12.4-x64.zip vulkan-x64.zip cpu-x64.zip" {
		t.Errorf("pilote 12.9 : %s", got)
	}
	hw.DriverCuda = 11.8 // pilote trop ancien pour tout build CUDA
	if got := labels(hw, ""); got != "vulkan-x64.zip cpu-x64.zip" {
		t.Errorf("pilote 11.8 : %s", got)
	}
	hw.DriverCuda = 0
	if got := labels(hw, ""); got != "cuda-12.4-x64.zip cuda-13.5-x64.zip vulkan-x64.zip cpu-x64.zip" {
		t.Errorf("pilote inconnu : %s", got)
	}
	if got := labels(hw, "vulkan"); got != "vulkan-x64.zip" {
		t.Errorf("variante imposée : %s", got)
	}
	if got := labels(hw, "rocm"); !strings.HasPrefix(got, "ERR") {
		t.Errorf("variante imposée inexistante acceptée : %s", got)
	}
}

// --- installation de bout en bout, contre un faux dépôt de releases ---------

type fakeRelease struct {
	mu    sync.Mutex
	files map[string][]byte // nom → contenu
	hits  map[string]int
	onReq func(*http.Request)
	srv   *httptest.Server
}

func newFakeRelease(t *testing.T) *fakeRelease {
	t.Helper()
	f := &fakeRelease{files: map[string][]byte{}, hits: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		f.mu.Lock()
		b, ok := f.files[name]
		f.hits[name]++
		hook := f.onReq
		f.mu.Unlock()
		if hook != nil {
			hook(r)
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
	}))
	t.Cleanup(f.srv.Close)
	old := engineDownloadBase
	engineDownloadBase = f.srv.URL
	t.Cleanup(func() { engineDownloadBase = old })
	return f
}

// add fabrique une archive zip et renvoie son entrée de manifeste.
func (f *fakeRelease) add(t *testing.T, name string, entries map[string]string) engineAsset {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// Ordre fixe : deux archives de même contenu doivent avoir la même empreinte.
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(entries[n]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	f.mu.Lock()
	f.files[name] = buf.Bytes()
	f.mu.Unlock()
	return engineAsset{Name: name, Size: int64(buf.Len()), SHA256: hex.EncodeToString(sum[:])}
}

func (f *fakeRelease) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[name]
}

// engineTestSetup : faux manifeste (machine Windows NVIDIA), sonde remplaçable.
func engineTestSetup(t *testing.T, tag string, f *fakeRelease, probe func(variant string) error) *engineManifest {
	t.Helper()
	exe := engineExeName()
	m := &engineManifest{Tag: tag, Assets: []engineAsset{
		f.add(t, "llama-"+tag+"-bin-win-cuda-12.4-x64.zip", map[string]string{exe: "cuda " + tag, "ggml-cuda.dll": "x"}),
		f.add(t, "cudart-llama-bin-win-cuda-12.4-x64.zip", map[string]string{"cudart64_12.dll": "runtime", "cublas64_12.dll": "runtime"}),
		f.add(t, "llama-"+tag+"-bin-win-vulkan-x64.zip", map[string]string{exe: "vulkan " + tag}),
		f.add(t, "llama-"+tag+"-bin-win-cpu-x64.zip", map[string]string{exe: "cpu " + tag}),
	}}
	hw := &engineHW{OS: "windows", Arch: "amd64", Nvidia: true, DriverCuda: 12.9, Vulkan: true}
	oldM, oldHW, oldProbe := engineManifestTest, engineHWTest, engineProbe
	engineManifestTest, engineHWTest = m, hw
	engineProbe = func(bin string, libDirs []string, variant string) error {
		if !isFile(bin) {
			return errors.New("binaire absent au moment de la sonde")
		}
		return probe(variant)
	}
	t.Cleanup(func() { engineManifestTest, engineHWTest, engineProbe = oldM, oldHW, oldProbe })
	return m
}

func TestEngineInstallVarianteRetenue(t *testing.T) {
	home := testHome(t)
	f := newFakeRelease(t)
	engineTestSetup(t, "b100", f, func(string) error { return nil })
	// Reste d'une version d'avant la 0.20 : géré par AJEAN, donc nettoyé.
	old := filepath.Join(home, "backends", "llama.cpp-prebuilt", "llama-b1")
	_ = os.MkdirAll(old, 0o755)
	// Moteur compilé par l'utilisateur : signalé, jamais supprimé.
	compiled := filepath.Join(home, "backends", "llama.cpp")
	_ = os.MkdirAll(compiled, 0o755)

	if engineBin() != "" {
		t.Fatal("moteur annoncé installé sur une machine vierge")
	}
	bin, err := engineEnsure(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := engineInstalled()
	if e == nil || e.Variant != "cuda" || e.Tag != "b100" || !samePath(bin, e.binPath()) || !samePath(bin, engineBin()) {
		t.Fatalf("installé : %+v, bin=%s", e, bin)
	}
	if b, _ := os.ReadFile(bin); string(b) != "cuda b100" {
		t.Errorf("mauvais binaire installé : %q", b)
	}
	// Les bibliothèques CUDA livrées sont dans le chemin de recherche du moteur.
	libs := engineLibDirsFor(filepath.Dir(bin))
	if len(libs) != 1 || !isFile(filepath.Join(libs[0], "cudart64_12.dll")) {
		t.Errorf("bibliothèques GPU introuvables : %v", libs)
	}
	if engineLibDirsFor(t.TempDir()) != nil {
		t.Error("bibliothèques du moteur ajoutées à un binaire qui n'est pas le moteur")
	}
	for _, p := range []string{filepath.Join(engineRoot(), "dl"), filepath.Join(engineRoot(), "b100.tmp"), filepath.Join(engineRoot(), ".lock"), filepath.Dir(old)} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("devrait avoir disparu après l'installation : %s", p)
		}
	}
	if !isDir(compiled) {
		t.Error("le moteur compilé par l'utilisateur a été supprimé")
	}
	if l := engineLegacyDirs(); len(l) != 1 || !samePath(l[0].Path, compiled) {
		t.Errorf("restes signalés : %+v", l)
	}
	// Déjà installé : aucun nouveau téléchargement.
	n := f.count("llama-b100-bin-win-cuda-12.4-x64.zip")
	if _, err := engineEnsure(nil, nil); err != nil || f.count("llama-b100-bin-win-cuda-12.4-x64.zip") != n {
		t.Errorf("réinstallation inutile (err=%v)", err)
	}
}

// Une variante GPU qui ne passe pas la sonde est écartée au profit de la suivante.
func TestEngineInstallRepliSurVarianteSuivante(t *testing.T) {
	testHome(t)
	f := newFakeRelease(t)
	engineTestSetup(t, "b100", f, func(v string) error {
		if v == "cuda" {
			return errors.New("le moteur ne voit aucune carte graphique")
		}
		return nil
	})
	var log []string
	if _, err := engineInstall(false, func(s string) { log = append(log, s) }, nil); err != nil {
		t.Fatal(err)
	}
	e := engineInstalled()
	if e == nil || e.Variant != "vulkan" || len(e.LibDirs) != 0 {
		t.Fatalf("attendu Vulkan après l'échec de CUDA : %+v", e)
	}
	if !strings.Contains(strings.Join(log, "\n"), "écartée") {
		t.Errorf("le repli n'est pas expliqué dans le journal : %v", log)
	}
	// Variante imposée : pas de repli, et l'erreur dit pourquoi.
	if err := setEngineForcedVariant("cuda"); err != nil {
		t.Fatal(err)
	}
	if _, err := engineInstall(true, nil, nil); err == nil || !strings.Contains(err.Error(), "aucune carte") {
		t.Errorf("variante imposée en échec : %v", err)
	}
	if e := engineInstalled(); e == nil || e.Variant != "vulkan" {
		t.Errorf("un échec de réparation ne doit pas retirer le moteur en place : %+v", e)
	}
	if err := setEngineForcedVariant("n'importe quoi"); err == nil {
		t.Error("variante invalide acceptée")
	}
	if err := setEngineForcedVariant("auto"); err != nil || engineForcedVariant() != "" {
		t.Errorf("retour à l'automatique : %v %q", err, engineForcedVariant())
	}
}

// Une archive qui ne correspond pas à l'empreinte épinglée n'est jamais installée.
func TestEngineInstallRefuseEmpreinteInattendue(t *testing.T) {
	testHome(t)
	f := newFakeRelease(t)
	m := engineTestSetup(t, "b100", f, func(string) error { return nil })
	for i := range m.Assets {
		m.Assets[i].SHA256 = strings.Repeat("0", 64)
	}
	_, err := engineInstall(false, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("erreur d'empreinte attendue, reçu : %v", err)
	}
	if engineInstalled() != nil || engineBin() != "" {
		t.Error("moteur installé malgré une empreinte inattendue")
	}
}

// Mise à jour d'AJEAN = nouvelle version épinglée : le nouveau moteur remplace
// l'ancien, et les bibliothèques CUDA identiques ne sont pas retéléchargées.
func TestEngineMiseAJourReutiliseLesBibliothequesGPU(t *testing.T) {
	testHome(t)
	f := newFakeRelease(t)
	engineTestSetup(t, "b100", f, func(string) error { return nil })
	if _, err := engineEnsure(nil, nil); err != nil {
		t.Fatal(err)
	}
	if f.count("cudart-llama-bin-win-cuda-12.4-x64.zip") != 1 {
		t.Fatal("les bibliothèques CUDA auraient dû être téléchargées une fois")
	}
	engineTestSetup(t, "b200", f, func(string) error { return nil })
	if engineInstalled() != nil {
		t.Fatal("l'ancien moteur ne doit pas passer pour celui de la nouvelle version")
	}
	bin, err := engineEnsure(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "cuda b200" {
		t.Errorf("binaire après mise à jour : %q", b)
	}
	if n := f.count("cudart-llama-bin-win-cuda-12.4-x64.zip"); n != 1 {
		t.Errorf("bibliothèques CUDA retéléchargées (%d fois)", n)
	}
	if libs := engineLibDirsFor(filepath.Dir(bin)); len(libs) != 1 || !isFile(filepath.Join(libs[0], "cublas64_12.dll")) {
		t.Errorf("bibliothèques GPU absentes après mise à jour : %v", libs)
	}
	if isDir(engineDir("b100")) {
		t.Error("l'ancienne version du moteur n'a pas été supprimée")
	}
}

// Un téléchargement coupé reprend où il s'était arrêté.
func TestEngineFetchReprise(t *testing.T) {
	f := newFakeRelease(t)
	body := bytes.Repeat([]byte("0123456789abcdef"), 4096)
	sum := sha256.Sum256(body)
	f.files["gros.bin"] = body
	dest := filepath.Join(t.TempDir(), "gros.bin")
	if err := os.WriteFile(dest+".part", body[:10000], 0o644); err != nil {
		t.Fatal(err)
	}
	var ranged bool
	f.onReq = func(r *http.Request) { ranged = r.Header.Get("Range") == "bytes=10000-" }
	err := engineFetch(context.Background(), f.srv.URL+"/b1/gros.bin", dest, int64(len(body)), hex.EncodeToString(sum[:]), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !ranged || !bytes.Equal(got, body) {
		t.Errorf("reprise : range=%v, contenu identique=%v", ranged, bytes.Equal(got, body))
	}
}

func TestEngineErrorFromLog(t *testing.T) {
	log := strings.Join([]string{
		"oct 01 host ajean[12]: [ajean serve] C:/x/llama-server.exe  model=a.gguf  port=8080",
		"oct 01 host ajean[12]: ggml_backend_cuda_buffer_type_alloc_buffer: allocating 9000.00 MiB on device 0: cudaMalloc failed: out of memory",
		"[ajean serve] /x/llama-server  model=b.gguf  port=8080",
		"llama_model_load_from_file_impl: using device CUDA0",
		"E srv    load_model: error loading model: tensor 'blk.3' data is not within the file bounds",
		"main: exiting due to model loading error",
	}, "\n")
	got := engineErrorFromLog(log)
	if strings.Contains(got, "cudaMalloc") {
		t.Errorf("erreur d'un lancement précédent conservée : %q", got)
	}
	if !strings.Contains(got, "error loading model") {
		t.Errorf("erreur de chargement non relevée : %q", got)
	}
	if got := engineErrorFromLog("srv  main: server is listening on http://0.0.0.0:8080\nslot update_slots: all slots are idle"); got != "" {
		t.Errorf("faux positif sur un journal sain : %q", got)
	}
	if got := engineErrorFromLog("error: unknown argument: --mlock"); got == "" {
		t.Error("argument inconnu non relevé")
	}
	if got := engineErrorFromLog("[err] moteur llama.cpp indisponible : téléchargement de x.zip : HTTP 404"); !strings.Contains(got, "HTTP 404") {
		t.Errorf("échec d'ajean serve non relevé : %q", got)
	}
}

// Les presets des moteurs retirés ne démarrent plus llama.cpp à l'aveugle.
func TestRemovedEngine(t *testing.T) {
	testHome(t)
	for cfg, want := range map[string]string{"moe": "MoE", "exl3": "ExLlama", "": "", "llamacpp": ""} {
		if got := removedEngine(map[string]string{"ENGINE": cfg}); got != want {
			t.Errorf("ENGINE=%q : %q, attendu %q", cfg, got, want)
		}
	}
	if err := WriteConfig(map[string]string{"ENGINE": "exl3", "EXL3_MODEL": "x"}); err != nil {
		t.Fatal(err)
	}
	if err := preflightEngine(); err == nil || !strings.Contains(err.Error(), "ExLlama") {
		t.Errorf("pré-vol d'un preset ExLlama : %v", err)
	}
	if err := cmdServe(nil); err == nil || !strings.Contains(err.Error(), "ExLlama") {
		t.Errorf("serve d'un preset ExLlama : %v", err)
	}
}

// Après une mise à jour d'AJEAN, un modèle qui tourne encore sur l'ancien moteur
// est relancé sur le nouveau ; un modèle déjà sur le bon moteur n'est pas touché.
func TestEngineCmdlineIsStale(t *testing.T) {
	dir := `C:\ProgramData\ajean\backends\engine\b12257`
	for cmd, want := range map[string]bool{
		`C:\ProgramData\ajean\backends\llama.cpp-prebuilt\llama-b9000\llama-server.exe -m x.gguf`: true,
		`c:/programdata/ajean/backends/engine/b12257/llama-server.exe -m x.gguf`:                  false,
		`"C:\ProgramData\ajean\backends\engine\b12000\llama-server.exe" -m x.gguf`:                true,
		``: false,
	} {
		if got := engineCmdlineIsStale(cmd, dir); got != want {
			t.Errorf("%q : %v, attendu %v", cmd, got, want)
		}
	}
}
