package ajean

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mkHome crée un dossier de données factice contenant un fichier témoin, pour
// vérifier que la migration déplace bien le CONTENU et pas seulement le dossier.
func mkHome(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.env"), []byte("MODEL=x.gguf\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Cas nominal : seul l'ancien dossier existe → il est migré, contenu compris.
func TestMigrateHomeMovesLegacy(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "jean")
	target := filepath.Join(root, "ajean")
	mkHome(t, legacy)

	if got := migrateHome(target, legacy); got != target {
		t.Fatalf("got %q, attendu le nouveau chemin %q", got, target)
	}
	if b, err := os.ReadFile(filepath.Join(target, "config.env")); err != nil || string(b) != "MODEL=x.gguf\n" {
		t.Fatalf("contenu non migré: %q (%v)", b, err)
	}
	if isDir(legacy) {
		t.Fatalf("l'ancien dossier existe encore après un rename réussi")
	}
}

// Installation neuve : rien à migrer, on renvoie le nouveau chemin sans rien créer.
func TestMigrateHomeFreshInstall(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ajean")
	if got := migrateHome(target, filepath.Join(root, "jean")); got != target {
		t.Fatalf("got %q, attendu %q", got, target)
	}
}

// Déjà migré : le nouveau dossier existe → on ne retouche à rien, même si un
// vieux dossier « jean » traîne encore (utilisateur qui l'a recréé, restauration
// de sauvegarde…). Écraser serait perdre les données courantes.
func TestMigrateHomeKeepsExistingTarget(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "jean")
	target := filepath.Join(root, "ajean")
	mkHome(t, legacy)
	mkHome(t, target)
	if err := os.WriteFile(filepath.Join(target, "config.env"), []byte("MODEL=courant.gguf\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := migrateHome(target, legacy); got != target {
		t.Fatalf("got %q, attendu %q", got, target)
	}
	b, _ := os.ReadFile(filepath.Join(target, "config.env"))
	if string(b) != "MODEL=courant.gguf\n" {
		t.Fatalf("le dossier courant a été écrasé par l'ancien: %q", b)
	}
	if !isDir(legacy) {
		t.Fatalf("l'ancien dossier a été supprimé — la migration ne doit JAMAIS supprimer")
	}
}

// Rename impossible → on doit retomber sur l'ancien chemin, intact. C'est la
// garantie « au pire, rien n'a bougé », celle qui protège les .gguf.
//
// On reproduit le scénario réel : un service AJEAN tourne encore et tient un
// handle ouvert dans le dossier de données, ce qui fait échouer le rename du
// dossier sous Windows. Unix, lui, renomme sans broncher un dossier dont des
// fichiers sont ouverts — il n'y a donc rien à simuler là-bas.
func TestMigrateHomeFallsBackWhenRenameFails(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("un rename de dossier réussit avec des fichiers ouverts hors Windows")
	}
	root := t.TempDir()
	legacy := filepath.Join(root, "jean")
	target := filepath.Join(root, "ajean")
	mkHome(t, legacy)

	held, err := os.Open(filepath.Join(legacy, "config.env"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	if got := migrateHome(target, legacy); got != legacy {
		t.Fatalf("got %q, attendu un repli sur l'ancien chemin %q", got, legacy)
	}
	if b, err := os.ReadFile(filepath.Join(legacy, "config.env")); err != nil || string(b) != "MODEL=x.gguf\n" {
		t.Fatalf("l'ancien dossier a été abîmé par une migration ratée: %q (%v)", b, err)
	}
}

// Le cas qui a casse une vraie machine : config.env contient un chemin ABSOLU
// vers le dossier de donnees (BIN=<home>\backends\...\llama-server.exe). Apres
// migration ce chemin doit suivre, sinon llama-server ne demarre plus — et ca
// concerne tous ceux qui ont fait un `llamacpp install`, donc le cas nominal.
func TestMigrateHomeRewritesAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "jean")
	target := filepath.Join(root, "ajean")
	mkHome(t, legacy)
	if err := os.MkdirAll(filepath.Join(legacy, "configs"), 0o755); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(legacy, "backends", "llama.cpp", "build", "bin", "llama-server")
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(legacy, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.env", "BIN="+bin+"\nMODEL=\"x.gguf\"\n")
	// Les presets portent le meme BIN et doivent suivre aussi.
	write(filepath.Join("configs", "gros.env"), "BIN="+bin+"\n")
	// Ecriture avec des slashs : Windows accepte les deux formes.
	write("model_dirs.json", `{"dirs":["`+filepath.ToSlash(legacy)+`/models"]}`)

	if got := migrateHome(target, legacy); got != target {
		t.Fatalf("migration echouee: %q", got)
	}

	for _, rel := range []string{"config.env", filepath.Join("configs", "gros.env"), "model_dirs.json"} {
		b, err := os.ReadFile(filepath.Join(target, rel))
		if err != nil {
			t.Fatalf("%s illisible: %v", rel, err)
		}
		if strings.Contains(string(b), legacy) || strings.Contains(string(b), filepath.ToSlash(legacy)) {
			t.Errorf("%s reference encore l'ancien dossier:\n%s", rel, b)
		}
		if !strings.Contains(string(b), target) && !strings.Contains(string(b), filepath.ToSlash(target)) {
			t.Errorf("%s ne pointe pas vers le nouveau dossier:\n%s", rel, b)
		}
	}

	// Le BIN reecrit doit designer un chemin reellement atteignable.
	newBin := filepath.Join(target, "backends", "llama.cpp", "build", "bin", "llama-server")
	if b, _ := os.ReadFile(filepath.Join(target, "config.env")); !strings.Contains(string(b), newBin) {
		t.Errorf("BIN ne pointe pas sur %s:\n%s", newBin, b)
	}
}

// Les fichiers d'etat du service (jean.pid/jean.log) doivent etre repris : le
// PID dit si le service tourne, l'ignorer ferait demarrer un second service.
func TestMigrateHomeAdoptsServiceStateFiles(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "jean")
	target := filepath.Join(root, "ajean")
	mkHome(t, legacy)
	if err := os.WriteFile(filepath.Join(legacy, "jean.pid"), []byte("4242\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	migrateHome(target, legacy)

	b, err := os.ReadFile(filepath.Join(target, "ajean.pid"))
	if err != nil {
		t.Fatalf("ajean.pid absent apres migration: %v", err)
	}
	if strings.TrimSpace(string(b)) != "4242" {
		t.Fatalf("PID perdu: %q", b)
	}
}

// Les fichiers JSON (model_dirs.json, mcp.json, webprefs.json) sont ecrits par
// json.Marshal : sous Windows chaque antislash y est DOUBLE. Chercher la forme
// native ne les trouve donc pas, et on croirait avoir tout reecrit en laissant
// des references mortes. Constate en bac a sable.
func TestMigrateHomeRewritesJSONEscapedPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("l'echappement JSON des separateurs ne concerne que Windows")
	}
	root := t.TempDir()
	legacy := filepath.Join(root, "jean")
	target := filepath.Join(root, "ajean")
	mkHome(t, legacy)

	esc := jsonEscapePath
	body := `{"dirs":["` + esc(filepath.Join(legacy, "models")) + `"]}`
	if err := os.WriteFile(filepath.Join(legacy, "model_dirs.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	migrateHome(target, legacy)

	b, err := os.ReadFile(filepath.Join(target, "model_dirs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), esc(legacy)) {
		t.Errorf("reference JSON a l'ancien dossier non reecrite:\n%s", b)
	}
	if !strings.Contains(string(b), esc(target)) {
		t.Errorf("le JSON ne pointe pas vers le nouveau dossier:\n%s", b)
	}
	// Le fichier doit rester du JSON valide apres reecriture.
	var parsed struct {
		Dirs []string `json:"dirs"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("JSON casse par la reecriture: %v\n%s", err, b)
	}
	if len(parsed.Dirs) != 1 || !strings.HasPrefix(parsed.Dirs[0], target) {
		t.Fatalf("chemin decode inattendu: %+v", parsed.Dirs)
	}
}
