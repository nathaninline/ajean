package ajean

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failIfEngineDownloads : le test échoue si quoi que ce soit tente de
// télécharger le moteur épinglé. La machine est factice (aucune sonde du
// matériel réel) et le manifeste aussi.
func failIfEngineDownloads(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("téléchargement du moteur épinglé déclenché : %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	oldBase, oldM, oldHW := engineDownloadBase, engineManifestTest, engineHWTest
	engineDownloadBase = srv.URL
	if engineManifestTest == nil {
		engineManifestTest = &engineManifest{Tag: "b100", Assets: []engineAsset{{Name: "llama-b100-bin-win-cpu-x64.zip", Size: 1, SHA256: strings.Repeat("0", 64)}}}
	}
	if engineHWTest == nil {
		engineHWTest = &engineHW{OS: "windows", Arch: "amd64"}
	}
	t.Cleanup(func() {
		srv.Close()
		engineDownloadBase, engineManifestTest, engineHWTest = oldBase, oldM, oldHW
	})
}

// fakePinnedEngine pose un moteur épinglé « installé » pour le tag donné, et
// épingle ce tag. Renvoie le chemin de son llama-server.
func fakePinnedEngine(t *testing.T, tag string) string {
	t.Helper()
	old := engineManifestTest
	engineManifestTest = &engineManifest{Tag: tag}
	t.Cleanup(func() { engineManifestTest = old })
	return fakeEngineDir(t, tag)
}

// fakeEngineDir écrit backends/engine/<tag>/ avec son marqueur, sans toucher à
// la version épinglée.
func fakeEngineDir(t *testing.T, tag string) string {
	t.Helper()
	dir := engineDir(tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, engineExeName())
	if err := os.WriteFile(bin, []byte("moteur "+tag), 0o755); err != nil {
		t.Fatal(err)
	}
	// "ajean" : signé par la 0.21. Un marqueur sans ce champ est celui d'un
	// moteur installé par la 0.20 (voir fakeEngine020, migrate_020_test.go).
	marker := `{"tag":"` + tag + `","variant":"cpu","label":"CPU","asset":"x.zip","bin":"` + engineExeName() + `","installed_at":"2026-01-01T00:00:00Z","ajean":"test"}`
	if err := os.WriteFile(filepath.Join(dir, engineMarker), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	return bin
}

// fakeCompiledEngine pose un llama.cpp « compilé » (backends/llama.cpp) et
// renvoie le chemin de son binaire.
func fakeCompiledEngine(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(backendsDir(), "llama.cpp", "build", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(bin, []byte("compilé"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// Cas 1 : BIN désigne un moteur de la 0.18. Rien ne change : le chemin est rendu
// tel quel (rendu absolu, comme le faisait « ajean serve »), le précompilé suit
// sa release courante, et le moteur épinglé n'est jamais installé.
func TestResolveBinMoteur018Inchange(t *testing.T) {
	prebuilt := fakePrebuilt(t, "b10280", "b10280") // pose aussi le dossier de données de test
	failIfEngineDownloads(t)
	fakePinnedEngine(t, "b100") // même installé, il ne doit pas prendre la place d'un BIN de la 0.18

	compiled := fakeCompiledEngine(t)
	ext := filepath.Join(t.TempDir(), "mon-fork", "llama-server")
	for name, bin := range map[string]string{"compilé": compiled, "chemin au choix": ext} {
		got, pinned := resolveBin(map[string]string{"BIN": bin})
		if pinned || got != bin {
			t.Errorf("%s : BIN=%s résolu en %s (épinglé=%v)", name, bin, got, pinned)
		}
	}
	// Chemin relatif : rapporté au dossier de données, comme en 0.18.
	if got, pinned := resolveBin(map[string]string{"BIN": "backends/llama.cpp/build/bin/llama-server"}); pinned || !samePath(got, compiled) {
		t.Errorf("BIN relatif résolu en %s (épinglé=%v)", got, pinned)
	}
	// Précompilé « dernière release » : un chemin d'une release remplacée suit la
	// release installée (prebuiltResolveBin).
	stale := filepath.Join(prebuilt, "llama-b10088", "llama-server")
	got, pinned := resolveBin(map[string]string{"BIN": stale})
	if pinned || !strings.Contains(filepath.ToSlash(got), "/llama-b10280/") {
		t.Errorf("BIN précompilé périmé résolu en %s (épinglé=%v)", got, pinned)
	}
	// Le service lance ce binaire sans rien installer.
	if bin, err := serveBin(map[string]string{"BIN": compiled}); err != nil || bin != compiled {
		t.Errorf("serveBin d'un moteur compilé : %s, %v", bin, err)
	}
	// Un fork que l'utilisateur a nommé « engine » avant la 0.21 n'est pas le
	// moteur épinglé, bien qu'il vive dans backends/engine/.
	fork := filepath.Join(engineRoot(), "build", "bin", "llama-server")
	if got, pinned := resolveBin(map[string]string{"BIN": fork}); pinned || got != fork {
		t.Errorf("fork nommé « engine » pris pour le moteur épinglé : %s (épinglé=%v)", got, pinned)
	}
}

// Cas 2 : BIN pointe dans backends/engine/. C'est le moteur épinglé de la
// version COURANTE, même si le chemin enregistré est celui d'un ancien tag.
func TestResolveBinEpingleSuitLaVersionCourante(t *testing.T) {
	testHome(t)
	failIfEngineDownloads(t)
	cur := fakePinnedEngine(t, "b200")
	oldTag := filepath.Join(engineDir("b100"), engineExeName()) // n'existe plus
	for _, bin := range []string{oldTag, cur, filepath.Join("backends", "engine", "b100", engineExeName())} {
		got, pinned := resolveBin(map[string]string{"BIN": bin})
		if !pinned || !samePath(got, cur) {
			t.Errorf("BIN=%s résolu en %s (épinglé=%v), attendu %s", bin, got, pinned, cur)
		}
	}
	if !engineOwns(oldTag) || engineOwns(filepath.Join(backendsDir(), "llama.cpp", "llama-server")) || engineOwns("") {
		t.Error("engineOwns ne reconnaît pas le dossier du moteur épinglé")
	}
	if bin, err := serveBin(map[string]string{"BIN": oldTag}); err != nil || !samePath(bin, cur) {
		t.Errorf("serveBin d'un BIN d'ancien tag : %s, %v", bin, err)
	}
}

// Cas 3 : BIN vide (preset créé en 0.20, installation neuve). C'est le moteur
// épinglé, installé par le service s'il manque.
func TestResolveBinVideUtiliseLeMoteurEpingle(t *testing.T) {
	testHome(t)
	f := newFakeRelease(t)
	engineTestSetup(t, "b100", f, func(string) error { return nil })

	if bin, pinned := resolveBin(map[string]string{"MODEL": "m.gguf"}); !pinned || bin != "" {
		t.Fatalf("BIN vide, moteur absent : %q (épinglé=%v)", bin, pinned)
	}
	if !usesPinnedEngine(map[string]string{"MODEL": "m.gguf"}) {
		t.Error("un preset local sans BIN doit tourner sur le moteur épinglé")
	}
	// « ajean serve » l'installe au besoin…
	bin, err := serveBin(map[string]string{"MODEL": "m.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	e := engineInstalled()
	if e == nil || !samePath(bin, e.binPath()) {
		t.Fatalf("moteur installé par le service : %+v, bin=%s", e, bin)
	}
	// … et ne le retélécharge pas la fois suivante.
	n := f.count(e.Asset)
	if again, err := serveBin(map[string]string{}); err != nil || !samePath(again, bin) || f.count(e.Asset) != n {
		t.Errorf("second démarrage : %s, %v, téléchargements %d → %d", again, err, n, f.count(e.Asset))
	}
	if got, pinned := resolveBin(map[string]string{"BIN": "  "}); !pinned || !samePath(got, bin) {
		t.Errorf("BIN blanc : %s (épinglé=%v)", got, pinned)
	}
}

// Le moteur épinglé ne se télécharge tout seul que là où il sert : la
// configuration active tourne dessus, ou aucun moteur n'est installé.
func TestEngineWanted(t *testing.T) {
	type tc struct {
		name  string
		setup func(t *testing.T) string // renvoie le corps de la config active
		want  bool
	}
	cases := []tc{
		{"installation neuve, rien de configuré", func(t *testing.T) string { return "" }, true},
		{"preset local sans BIN (venu de la 0.20)", func(t *testing.T) string {
			fakeCompiledEngine(t) // un autre moteur existe : la config a quand même besoin de l'épinglé
			return "MODEL=m.gguf\n"
		}, true},
		{"preset local dont le BIN est dans backends/engine/", func(t *testing.T) string {
			return "MODEL=m.gguf\nBIN=" + filepath.Join(engineDir("b90"), engineExeName()) + "\n"
		}, true},
		{"0.18 : BIN vers le moteur compilé", func(t *testing.T) string {
			return "MODEL=m.gguf\nBIN=" + fakeCompiledEngine(t) + "\n"
		}, false},
		{"0.18 : BIN vers un moteur disparu (c'est à l'utilisateur de choisir)", func(t *testing.T) string {
			return "MODEL=m.gguf\nBIN=/opt/llama/absent/llama-server\n"
		}, false},
		{"0.18 : preset ExLlama actif, llama.cpp compilé installé", func(t *testing.T) string {
			fakeCompiledEngine(t)
			return "ENGINE=exl3\nEXL3_MODEL=x\n"
		}, false},
		{"0.18 : preset API actif, précompilé installé", func(t *testing.T) string {
			dir := filepath.Join(prebuiltDir(), "llama-b10280")
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, engineExeName()), []byte("bin"), 0o755)
			return "EXTERNAL=1\nEXTERNAL_URL=https://api.exemple.com/v1\nEXTERNAL_MODEL=m\n"
		}, false},
		{"preset API actif, aucun moteur sur la machine", func(t *testing.T) string {
			return "EXTERNAL=1\nEXTERNAL_URL=https://api.exemple.com/v1\nEXTERNAL_MODEL=m\n"
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testHome(t)
			failIfEngineDownloads(t)
			setConfig(t, c.setup(t))
			if got := engineWanted(); got != c.want {
				t.Errorf("engineWanted() = %v, attendu %v", got, c.want)
			}
		})
	}
	t.Run("déjà installé", func(t *testing.T) {
		testHome(t)
		fakePinnedEngine(t, "b100")
		if engineWanted() {
			t.Error("moteur épinglé déjà en place : rien à installer")
		}
	})
}

// Le dossier du moteur épinglé n'est pas un backend personnalisé : on ne peut ni
// y installer un fork ni l'emporter en désinstallant un backend.
func TestEngineReservedDir(t *testing.T) {
	testHome(t)
	if !engineReservedDir(engineRoot()) {
		t.Error("backends/engine doit être réservé au moteur épinglé")
	}
	if _, err := installCustomBackend("https://github.com/exemple/engine.git", "", "", nil); err == nil || !strings.Contains(err.Error(), "réservé") {
		t.Errorf("un fork nommé « engine » aurait dû être refusé : %v", err)
	}
	// Le clone d'un fork nommé « engine » avant la 0.21 reste celui de l'utilisateur.
	if err := os.MkdirAll(filepath.Join(engineRoot(), ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if engineReservedDir(engineRoot()) {
		t.Error("un fork déjà cloné dans backends/engine doit rester gérable")
	}
	if engineReservedDir(filepath.Join(backendsDir(), "mon-fork")) {
		t.Error("un autre backend pris pour le dossier du moteur épinglé")
	}
}
