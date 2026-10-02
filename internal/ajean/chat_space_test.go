package ajean

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Jean et les projets ont chacun leur espace : aucun ne touche à celui de l'autre.
func TestJeanSpaceIsolation(t *testing.T) {
	bg := context.Background()
	jc := withJeanSpace(bg)

	if got := resolveSpacePath(jc, "a.txt"); !underDir(got, jeanWorkspace()) {
		t.Fatalf("Jean : relatif résolu hors de son workspace : %s", got)
	}
	projScript := filepath.Join(scriptsDir(), "mail.py")
	if msg := guardSpacePath(jc, projScript); msg == "" {
		t.Fatal("Jean ne doit pas pouvoir écrire un script de projet")
	}
	for _, c := range []string{"cat ../workspace/x", "cd .. && ls", `type ..\scripts\a.py`} {
		if guardSpaceCommand(jc, c) == "" {
			t.Fatalf("remontée relative acceptée : %s", c)
		}
	}
	if guardSpaceCommand(jc, "echo a..b && ls ./x") != "" {
		t.Fatal("faux positif sur a..b")
	}
	if guardSpaceCommand(bg, "cd .. && ls") != "" {
		t.Fatal("les projets gardent les chemins relatifs")
	}
	if msg := guardSpaceCommand(jc, "python "+projScript); msg == "" {
		t.Fatal("Jean ne doit pas pouvoir lancer/modifier un script de projet au shell")
	}
	if msg := guardSpacePath(jc, filepath.Join(agentWorkspace(), "uploads", "x.png")); msg != "" {
		t.Fatalf("les pièces jointes doivent rester accessibles à Jean : %s", msg)
	}
	if msg := guardSpacePath(jc, filepath.Join(jeanScriptsDir(), "a.py")); msg != "" {
		t.Fatalf("Jean doit pouvoir écrire ses propres scripts : %s", msg)
	}

	if msg := guardSpacePath(bg, filepath.Join(jeanScriptsDir(), "a.py")); msg == "" {
		t.Fatal("un projet ne doit pas toucher aux scripts de Jean")
	}
	if msg := guardSpacePath(bg, projScript); msg != "" {
		t.Fatalf("un projet doit garder ses scripts : %s", msg)
	}
	if res := fileWriteIn(jc, projScript, "x"); !strings.HasPrefix(res, "[refusé]") {
		t.Fatalf("fileWriteIn devait refuser : %s", res)
	}
}

// Jean lit les projets (scripts, mémoire) sans pouvoir les modifier.
func TestJeanReadsProjectsReadOnly(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	_ = os.MkdirAll(scriptsDir(), 0o755)
	if err := os.WriteFile(filepath.Join(scriptsDir(), "mail.py"), []byte("print('mail')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := jeanProjectFile("scripts/mail.py"); !strings.Contains(out, "print('mail')") {
		t.Fatalf("lecture du script de projet : %s", out)
	}
	if out := jeanProjectFile("scripts/../../etc/passwd"); !strings.HasPrefix(out, "[erreur]") {
		t.Fatalf("évasion acceptée : %s", out)
	}
	if out := jeanProjectFile("/etc/passwd"); !strings.HasPrefix(out, "[erreur]") {
		t.Fatalf("chemin absolu accepté : %s", out)
	}
	if out := jeanListProjects(); !strings.Contains(out, "scripts/mail.py") {
		t.Fatalf("liste : %s", out)
	}
}

// La conversation de Jean n'appartient à aucun projet : jamais listée, jamais
// emportée par un « tout supprimer ».
func TestJeanHorsHistorique(t *testing.T) {
	testHome(t)
	ensureDefaultProject()
	if err := saveArchive(&convArchive{ID: jeanConvID, Project: defaultProjectSlug, Title: "Jean", Mode: "jean"}); err != nil {
		t.Fatal(err)
	}
	if err := saveArchive(&convArchive{ID: "c1", Project: defaultProjectSlug, Title: "autre"}); err != nil {
		t.Fatal(err)
	}
	for _, m := range listAllArchives() {
		if m.ID == jeanConvID {
			t.Fatal("Jean listé dans l'historique")
		}
	}
	deleteNonFavArchives("")
	if _, ok := loadArchive(jeanConvID); !ok {
		t.Fatal("« tout supprimer » a effacé la conversation de Jean")
	}
}

// Changer de projet depuis l'historique ne quitte pas la conversation de Jean.
func TestSwitchProjectGardeJean(t *testing.T) {
	testHome(t)
	ensureDefaultProject()
	p, err := createProject("Recherche")
	if err != nil {
		t.Fatal(err)
	}
	conv.mu.Lock()
	conv.ID, conv.Mode, conv.Messages = "", "", nil
	conv.mu.Unlock()
	if err := conv.OpenJean(); err != nil {
		t.Fatal(err)
	}
	if err := conv.SwitchProject(p.Slug); err != nil {
		t.Fatal(err)
	}
	if conv.currentID() != jeanConvID || activeProjectSlug() != p.Slug {
		t.Fatalf("conv=%q projet=%q", conv.currentID(), activeProjectSlug())
	}
}

// L'historique suit le mode : conversations rapides à part, projet sans elles.
func TestHistoryListParMode(t *testing.T) {
	testHome(t)
	ensureDefaultProject()
	_ = setActiveProject(defaultProjectSlug)
	for _, a := range []*convArchive{
		{ID: "p1", Project: defaultProjectSlug, Mode: "project"},
		{ID: "old", Project: defaultProjectSlug},
		{ID: "f1", Project: defaultProjectSlug, Mode: "fast"},
		{ID: "b1", Project: "autre", Mode: "base"},
	} {
		if err := saveArchive(a); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(l []convArchiveMeta) string {
		var s []string
		for _, m := range l {
			s = append(s, m.ID)
		}
		sort.Strings(s)
		return strings.Join(s, ",")
	}
	if got := ids(historyList("")); got != "old,p1" {
		t.Fatalf("projet : %s", got)
	}
	if got := ids(historyList("quick")); got != "b1,f1" {
		t.Fatalf("rapide : %s", got)
	}
}

// Plusieurs appareils : un message en mode Jean part chez Jean, même si un autre
// appareil a ouvert une conversation de projet entre-temps (et inversement).
func TestAlignConvForMode(t *testing.T) {
	testHome(t)
	ensureDefaultProject()
	conv.mu.Lock()
	conv.ID, conv.Mode, conv.Messages = "", "", nil
	conv.mu.Unlock()
	conv.NewSession() // l'autre appareil : conversation de projet
	if err := conv.alignConvForMode("jean"); err != nil || conv.currentID() != jeanConvID {
		t.Fatalf("mode Jean : conv=%q err=%v", conv.currentID(), err)
	}
	if err := conv.alignConvForMode("project"); err != nil || conv.currentID() == jeanConvID {
		t.Fatalf("mode Projet depuis Jean : conv=%q err=%v", conv.currentID(), err)
	}
}

// Supprimer un projet n'emporte pas les conversations rapides rangées dessous.
func TestDeleteProjectGardeRapides(t *testing.T) {
	testHome(t)
	ensureDefaultProject()
	p, err := createProject("Jetable")
	if err != nil {
		t.Fatal(err)
	}
	_ = saveArchive(&convArchive{ID: "q1", Project: p.Slug, Mode: "fast"})
	_ = saveArchive(&convArchive{ID: "p1", Project: p.Slug, Mode: "project"})
	if err := deleteProject(p.Slug); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadArchive("q1"); !ok {
		t.Fatal("conversation rapide effacée avec le projet")
	}
	if _, ok := loadArchive("p1"); ok {
		t.Fatal("conversation du projet pas effacée")
	}
}

// Rapide et Modèle de base ont chacun leur historique.
func TestHistoryRapideEtBaseSepares(t *testing.T) {
	testHome(t)
	ensureDefaultProject()
	_ = saveArchive(&convArchive{ID: "f1", Mode: "fast"})
	_ = saveArchive(&convArchive{ID: "b1", Mode: "base"})
	if l := historyList("fast"); len(l) != 1 || l[0].ID != "f1" {
		t.Fatalf("rapide : %v", l)
	}
	if l := historyList("base"); len(l) != 1 || l[0].ID != "b1" {
		t.Fatalf("base : %v", l)
	}
}
