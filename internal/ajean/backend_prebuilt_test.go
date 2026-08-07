package ajean

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestExtractArchiveSymlink : les archives macOS/Linux de llama.cpp livrent les
// bibliothèques sous leur nom versionné (libllama-common.0.0.10107.dylib) plus
// un LIEN SYMBOLIQUE portant le nom recherché par l'éditeur de liens
// (libllama-common.0.dylib). L'extracteur ignorait ces entrées : le backend
// s'installait mais llama-server mourait sur « Library not loaded ».
func TestExtractArchiveSymlink(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "backend.tar.gz")

	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("faux contenu de bibliothèque")
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "b/libllama-common.0.0.1.dylib",
		Mode: 0o644, Size: int64(len(body)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeSymlink, Name: "b/libllama-common.0.dylib",
		Linkname: "libllama-common.0.0.1.dylib", Mode: 0o777,
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []func() error{tw.Close, gz.Close, f.Close} {
		if err := c(); err != nil {
			t.Fatal(err)
		}
	}

	out := filepath.Join(dir, "out")
	if err := extractArchive(archive, out); err != nil {
		t.Fatalf("extractArchive: %v", err)
	}

	// Le lien doit être RÉSOLVABLE : symlink là où c'est permis, copie sinon
	// (Windows sans mode développeur). Dans les deux cas os.ReadFile réussit.
	got, err := os.ReadFile(filepath.Join(out, "b", "libllama-common.0.dylib"))
	if err != nil {
		t.Fatalf("lien non extrait : %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("contenu résolu = %q, attendu %q", got, body)
	}
}

// fakePrebuilt monte un dossier prebuilt contenant plusieurs releases extraites,
// comme sur une machine mise à jour plusieurs fois.
func fakePrebuilt(t *testing.T, version string, tags ...string) string {
	t.Helper()
	home := testHome(t)
	dir := filepath.Join(home, "backends", "llama.cpp-prebuilt")
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, tag := range tags {
		d := filepath.Join(dir, "llama-"+tag)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, name), []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if version != "" {
		if err := os.WriteFile(filepath.Join(dir, "VERSION"),
			[]byte(version+" - "+prebuiltFormat+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// La version notée dans VERSION gagne, même si une extraction plus ancienne
// vient avant dans l'ordre alphabétique — le bug : on servait b10088, une
// installation incomplète, alors que b10280 était la version installée.
func TestPrebuiltServerBinSuitLeMarqueurVersion(t *testing.T) {
	fakePrebuilt(t, "b10280", "b10088", "b10159", "b10280")
	got := filepath.ToSlash(prebuiltServerBin())
	if !strings.Contains(got, "/llama-b10280/") {
		t.Errorf("binaire élu = %s, attendu sous /llama-b10280/", got)
	}
}

// Sans marqueur exploitable, on prend la release la plus récente.
func TestPrebuiltServerBinSansVersionPrendLePlusRecent(t *testing.T) {
	fakePrebuilt(t, "", "b10088", "b10280")
	if got := filepath.ToSlash(prebuiltServerBin()); !strings.Contains(got, "/llama-b10280/") {
		t.Errorf("binaire élu = %s, attendu la release la plus récente", got)
	}
}

// Un BIN de preset écrit avant une mise à jour pointe une release disparue : il
// doit suivre le moteur courant au lieu d'échouer au lancement (exit 127).
func TestPrebuiltResolveBinSuitLaMiseAJour(t *testing.T) {
	dir := fakePrebuilt(t, "b10280", "b10280")
	stale := filepath.Join(dir, "llama-b10088", "llama-server")
	if got := filepath.ToSlash(prebuiltResolveBin(stale)); !strings.Contains(got, "/llama-b10280/") {
		t.Errorf("BIN périmé résolu en %s, attendu la release installée", got)
	}
	// Un binaire hors du dossier prebuilt (fork, build maison) n'est jamais touché.
	ext := filepath.Join(t.TempDir(), "llama-server")
	if got := prebuiltResolveBin(ext); got != ext {
		t.Errorf("BIN externe réécrit en %s", got)
	}
}

// L'installation fait le ménage : les autres releases sont supprimées.
func TestPrebuiltPruneGardeLaVersionCourante(t *testing.T) {
	dir := fakePrebuilt(t, "b10280", "b10088", "b10159", "b10280")
	prebuiltPrune(prebuiltServerBin(), nil)
	for _, tag := range []string{"b10088", "b10159"} {
		if _, err := os.Stat(filepath.Join(dir, "llama-"+tag)); err == nil {
			t.Errorf("%s aurait dû être supprimé", tag)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "llama-b10280")); err != nil {
		t.Errorf("la version courante a été supprimée : %v", err)
	}
}

// Un preset pointant une release remplacée désigne toujours « le moteur
// précompilé » : sans ça, l'UI repassait tous les presets en « personnalisé »
// après chaque mise à jour du moteur.
func TestPrebuiltOwns(t *testing.T) {
	dir := fakePrebuilt(t, "b10280", "b10280")
	if !prebuiltOwns(filepath.Join(dir, "llama-b10088", "llama-server")) {
		t.Error("une release périmée du dossier prebuilt doit rester reconnue")
	}
	if prebuiltOwns(filepath.Join(t.TempDir(), "llama-server")) {
		t.Error("un binaire externe ne doit pas être pris pour le moteur précompilé")
	}
	if prebuiltOwns("") {
		t.Error("BIN vide reconnu à tort")
	}
}
