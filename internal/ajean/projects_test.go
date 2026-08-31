package ajean

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// La mémoire est cloisonnée par projet : une page écrite dans un projet n'est pas
// visible depuis un autre, et memoryDir suit le projet actif.
func TestProjectMemoryIsolation(t *testing.T) {
	testHome(t)
	ensureDefaultProject()

	if got := activeProjectSlug(); got != defaultProjectSlug {
		t.Fatalf("projet actif par défaut = %q, veut %q", got, defaultProjectSlug)
	}
	// Page dans Générale.
	if err := MemAdd("note-gen.md", "# note générale\n"); err != nil {
		t.Fatalf("MemAdd générale : %v", err)
	}

	// Nouveau projet + bascule.
	p, err := createProject("Site Vitrine")
	if err != nil {
		t.Fatalf("createProject : %v", err)
	}
	if err := setActiveProject(p.Slug); err != nil {
		t.Fatalf("setActiveProject : %v", err)
	}
	// La page de Générale ne doit pas être visible ici.
	for _, m := range MemList() {
		if m.Name == "note-gen.md" {
			t.Fatalf("page de Générale visible dans le projet %q", p.Slug)
		}
	}
	if err := MemAdd("note-site.md", "# note site\n"); err != nil {
		t.Fatalf("MemAdd site : %v", err)
	}
	// Les deux pages vivent dans des dossiers distincts sur disque.
	if _, err := os.Stat(filepath.Join(projectMemoryDir(defaultProjectSlug), "note-gen.md")); err != nil {
		t.Fatalf("note-gen.md absente du dossier Générale : %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectMemoryDir(p.Slug), "note-site.md")); err != nil {
		t.Fatalf("note-site.md absente du dossier %q : %v", p.Slug, err)
	}

	// Retour sur Générale : on retrouve sa page, pas celle du site.
	if err := setActiveProject(defaultProjectSlug); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, m := range MemList() {
		names[m.Name] = true
	}
	if !names["note-gen.md"] || names["note-site.md"] {
		t.Fatalf("mémoire de Générale incorrecte : %v", names)
	}
}

// Migration : une mémoire plate historique (memory/*.md) et un keyvault sont
// déplacés dans le projet Générale / à la racine.
func TestProjectMigrationFlatMemory(t *testing.T) {
	home := testHome(t)
	// Simule l'ancienne disposition : pages à plat sous memory/.
	oldMem := filepath.Join(home, "memory")
	if err := os.MkdirAll(oldMem, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldMem, "vieille.md"), []byte("# vieille note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldMem, ".keyvault"), []byte("kv"), 0o600); err != nil {
		t.Fatal(err)
	}

	ensureDefaultProject()

	// La page est passée dans memory/generale/.
	if _, err := os.Stat(filepath.Join(projectMemoryDir(defaultProjectSlug), "vieille.md")); err != nil {
		t.Fatalf("page non migrée dans Générale : %v", err)
	}
	// Le keyvault est passé à la racine.
	if _, err := os.Stat(filepath.Join(home, ".keyvault")); err != nil {
		t.Fatalf("keyvault non migré à la racine : %v", err)
	}
	// L'ancienne page ne traîne plus à la racine de memory/.
	if _, err := os.Stat(filepath.Join(oldMem, "vieille.md")); err == nil {
		t.Fatalf("page toujours à la racine de memory/")
	}
	// Elle est lisible via l'API mémoire (projet actif = Générale).
	if MemContent("vieille.md") == "" {
		t.Fatalf("page migrée illisible via MemContent")
	}
}

// deleteProject refuse le dernier projet, et supprime dossier + sessions sinon.
func TestProjectDeleteGuards(t *testing.T) {
	testHome(t)
	ensureDefaultProject()
	if err := deleteProject(defaultProjectSlug); err == nil {
		t.Fatalf("suppression du dernier projet acceptée")
	}
	p, err := createProject("Jetable")
	if err != nil {
		t.Fatal(err)
	}
	if err := deleteProject(p.Slug); err != nil {
		t.Fatalf("suppression projet : %v", err)
	}
	if projectExists(p.Slug) {
		t.Fatalf("projet toujours présent après suppression")
	}
	if _, err := os.Stat(projectMemoryDir(p.Slug)); err == nil {
		t.Fatalf("dossier du projet toujours présent")
	}
}

// L'index MEMORY.md est maintenu par le CODE : mem_add ajoute la ligne, mem_delete
// la retire, sans intervention du modèle.
func TestMemIndexAutoMaintained(t *testing.T) {
	testHome(t)
	ensureDefaultProject()
	if err := MemAdd("docker-notes.md", "# Notes Docker\ncontenu\n"); err != nil {
		t.Fatal(err)
	}
	if err := MemAdd("gpu.md", "# GPU\nx\n"); err != nil {
		t.Fatal(err)
	}
	idx := MemContent("MEMORY.md")
	if !strings.Contains(idx, "](docker-notes.md)") || !strings.Contains(idx, "Notes Docker") {
		t.Fatalf("index sans la page docker : %q", idx)
	}
	if !strings.Contains(idx, "](gpu.md)") {
		t.Fatalf("index sans la page gpu : %q", idx)
	}
	// MEMORY.md ne s'auto-référence pas.
	if strings.Contains(idx, "](MEMORY.md)") {
		t.Fatalf("index s'auto-référence")
	}
	// Suppression → ligne retirée.
	if err := MemDelete("docker-notes.md"); err != nil {
		t.Fatal(err)
	}
	idx = MemContent("MEMORY.md")
	if strings.Contains(idx, "](docker-notes.md)") {
		t.Fatalf("ligne docker toujours dans l'index après suppression : %q", idx)
	}
	if !strings.Contains(idx, "](gpu.md)") {
		t.Fatalf("suppression a retiré la mauvaise ligne : %q", idx)
	}
	// MEMORY.md n'apparaît pas comme une note.
	for _, p := range MemList() {
		if strings.EqualFold(p.Name, "MEMORY.md") {
			t.Fatalf("MEMORY.md listée comme note")
		}
	}
}

// L'index mémoire est un MESSAGE injecté (début de conv + après compactage), plus
// un bloc reconstruit à chaque tour. On vérifie le helper.
func TestMemIndexMessageInjection(t *testing.T) {
	testHome(t)
	ensureDefaultProject()
	SetConfigKey("MEM_MODE", "always")
	if err := MemAdd("gpu.md", "# GPU\nx\n"); err != nil {
		t.Fatal(err)
	}
	m, ok := memIndexMessage()
	if !ok {
		t.Fatal("index message attendu en mode always")
	}
	s, _ := m.Content.(string)
	if !strings.HasPrefix(s, memIndexPrefix) || !strings.Contains(s, "](gpu.md)") {
		t.Fatalf("message d'index incorrect : %q", s)
	}
	// ensureMemIndexFront : ajoute si absent, ne double pas si présent.
	base := []Message{{Role: "user", Content: "salut"}}
	withIdx := ensureMemIndexFront(base)
	if len(withIdx) != 2 || !hasMemIndex(withIdx) {
		t.Fatalf("index non préfixé : %+v", withIdx)
	}
	again := ensureMemIndexFront(withIdx)
	if len(again) != 2 {
		t.Fatalf("index dupliqué : %d messages", len(again))
	}
	// Hors mode always : pas d'injection.
	SetConfigKey("MEM_MODE", "ondemand")
	if _, ok := memIndexMessage(); ok {
		t.Fatal("pas d'index hors mode always")
	}
}
