package apps

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"ajean/internal/home"
	"ajean/internal/icons"
	"ajean/internal/tool"
)

// setup installe un dossier d'AJEAN vide et une horloge qui avance d'une
// minute à chaque lecture ; rend ce dossier.
func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AJEAN_HOME", dir)
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now = func() time.Time {
		clock = clock.Add(time.Minute)
		return clock
	}
	t.Cleanup(func() {
		now = time.Now
		Changed = nil
	})
	return dir
}

// call appelle l'outil « app » comme le ferait le modèle.
func call(args map[string]any) string {
	return Tools()[0].Run(tool.Call{Args: args})
}

var linkRe = regexp.MustCompile(`\]\(app:([a-z0-9-]+)\)`)

// mustSave enregistre une app et rend son identifiant, lu dans le lien que le
// résultat dicte au modèle.
func mustSave(t *testing.T, args map[string]any) string {
	t.Helper()
	args["action"] = "save"
	got := call(args)
	if !tool.IsOK(got) {
		t.Fatalf("save : %s", got)
	}
	m := linkRe.FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("pas de lien app: dans le résultat : %s", got)
	}
	return m[1]
}

// files lit les trois fichiers d'une version, directement sur le disque.
func files(t *testing.T, id string, n int) source {
	t.Helper()
	s, err := readSource(id, n)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSingleTool(t *testing.T) {
	defs := Tools()
	if len(defs) != 1 || defs[0].Name != "app" {
		t.Fatalf("un seul outil « app » attendu, reçu %d", len(defs))
	}
	for _, word := range []string{"ajean.storage.get", "ajean.tools.run", "ajean.ai.ask", "localStorage", "prefers-color-scheme", "Promise", "CDN", "errors"} {
		if !strings.Contains(defs[0].Description, word) {
			t.Errorf("la description ne parle pas de %q", word)
		}
	}
}

func TestCreate(t *testing.T) {
	dir := setup(t)
	if got := List(); got == nil || len(got) != 0 {
		t.Fatalf("liste vide attendue (non nil), reçu %#v", got)
	}
	got := call(map[string]any{
		"action": "save", "name": "Suivi  d'Été [2026]", "description": "Compte les\nverres d'eau", "icon": "Heart", "color": "#2F7D5B",
		"html": "<main id=\"app\"></main>\r\n", "css": "main { padding: 1rem }", "js": "document.getElementById('app').textContent = 'ok'",
		"note": "première version",
	})
	if !tool.IsOK(got) || !strings.Contains(got, "[Suivi d'Été (2026)](app:suivi-d-ete-2026)") || strings.Contains(got, "Avertissements") {
		t.Fatalf("save : %s", got)
	}
	all := List()
	if len(all) != 1 {
		t.Fatalf("List = %+v", all)
	}
	a := all[0]
	if a.ID != "suivi-d-ete-2026" || a.Name != "Suivi d'Été [2026]" || a.Description != "Compte les verres d'eau" || a.Icon != "heart" || a.Color != "#2f7d5b" || a.Version != 1 || a.Tools == nil || len(a.Tools) != 0 || a.Errors != 0 || a.Updated.IsZero() {
		t.Errorf("app = %+v", a)
	}
	s := files(t, a.ID, 1)
	if s.html != "<main id=\"app\"></main>\n" || s.css != "main { padding: 1rem }" || !strings.Contains(s.js, "textContent") {
		t.Errorf("fichiers = %+v", s)
	}
	for _, p := range []string{"APPS/suivi-d-ete-2026/app.json", "APPS/suivi-d-ete-2026/versions/1/index.html", "APPS/suivi-d-ete-2026/versions/1/style.css", "APPS/suivi-d-ete-2026/versions/1/app.js"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s manque : %v", p, err)
		}
	}

	// Sans css ni js, et un nom déjà pris : l'identifiant reste unique.
	if id := mustSave(t, map[string]any{"name": "Suivi d'été 2026", "html": "<p>x</p>"}); id != "suivi-d-ete-2026-2" {
		t.Errorf("second identifiant = %q", id)
	}
	if all := List(); len(all) != 2 || all[0].ID != "suivi-d-ete-2026-2" || all[0].Icon != icons.Default || all[0].Color != "" {
		t.Errorf("la plus récente d'abord, avec l'icône par défaut : %+v", all)
	}
	list := call(map[string]any{"action": "list"})
	if !strings.Contains(list, "- suivi-d-ete-2026 : Suivi d'Été [2026] (version 1) — Compte les verres d'eau") {
		t.Errorf("list : %s", list)
	}

	for label, args := range map[string]map[string]any{
		"sans nom":       {"html": "<p>x</p>"},
		"sans html":      {"name": "Vide", "css": "p{}"},
		"nom trop long":  {"name": strings.Repeat("n", maxName+1), "html": "<p>x</p>"},
		"app inexistant": {"id": "absente", "html": "<p>x</p>"},
	} {
		args["action"] = "save"
		if got := call(args); !tool.IsErr(got) {
			t.Errorf("%s : %q, refus attendu", label, got)
		}
	}
	if n := len(List()); n != 2 {
		t.Errorf("%d apps après des refus, 2 attendues", n)
	}
}

func TestSlug(t *testing.T) {
	for name, want := range map[string]string{
		"Météo à Besançon":         "meteo-a-besancon",
		"  --Œuvres & Cie!!  ":     "oeuvres-cie",
		"日本語":                      "app",
		"":                         "app",
		"NUL":                      "app-nul",
		"../../etc":                "etc",
		strings.Repeat("ab ", 30):  "ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-a",
		strings.Repeat("abc ", 10): "abc-abc-abc-abc-abc-abc-abc-abc-abc-abc",
	} {
		got := slug(name)
		if got != want || !validID(got) {
			t.Errorf("slug(%q) = %q, attendu %q (valide : %v)", name, got, want, validID(got))
		}
	}
}

func TestNewVersionKeepsMissingFiles(t *testing.T) {
	setup(t)
	id := mustSave(t, map[string]any{"name": "Notes", "description": "d", "icon": "note", "html": "<p>un</p>", "css": "p{color:red}", "js": "var a = 1"})

	// Seul le script change : le reste est repris de la version précédente.
	got := call(map[string]any{"action": "save", "id": id, "js": "var a = 2", "css": "  ", "note": "a vaut 2"})
	if !tool.IsOK(got) || !strings.Contains(got, "version 2") {
		t.Fatalf("save : %s", got)
	}
	if s := files(t, id, 2); s.html != "<p>un</p>" || s.css != "p{color:red}" || s.js != "var a = 2" {
		t.Errorf("version 2 = %+v", s)
	}
	if s := files(t, id, 1); s.js != "var a = 1" {
		t.Errorf("la version 1 a été modifiée : %+v", s)
	}
	a := List()[0]
	if a.Version != 2 || a.Name != "Notes" || a.Description != "d" || a.Icon != "note" {
		t.Errorf("app = %+v", a)
	}
	// Les champs fournis remplacent ; une icône hors de la liste (un emoji, du balisage) retombe sur l'icône par défaut.
	got = call(map[string]any{"action": "save", "id": id, "name": "Carnet", "icon": "📝", "color": "rouge"})
	if !tool.IsOK(got) || !strings.Contains(got, "'icon' inconnu") || !strings.Contains(got, "'color' ignoré") {
		t.Errorf("save : %s", got)
	}
	if a := List()[0]; a.ID != id || a.Name != "Carnet" || a.Icon != icons.Default || a.Version != 3 {
		t.Errorf("app = %+v", a)
	}

	read := call(map[string]any{"action": "read", "id": id})
	for _, want := range []string{"# Carnet (id notes, version 3, icon app, color )", "===== index.html =====\n<p>un</p>", "===== style.css =====\np{color:red}", "===== app.js =====\nvar a = 2"} {
		if !strings.Contains(read, want) {
			t.Errorf("read sans %q :\n%s", want, read)
		}
	}
}

func TestEdit(t *testing.T) {
	setup(t)
	id := mustSave(t, map[string]any{"name": "Compteur", "html": "<p id=\"n\">0</p><p>fin</p>", "css": "p{margin:0}", "js": "var n = 0;\nvar step = 1;"})

	got := call(map[string]any{"action": "edit", "id": id, "file": "js", "old": "var step = 1;", "new": "var step = 2;", "note": "pas de 2"})
	if !tool.IsOK(got) || !strings.Contains(got, "version 2") || !strings.Contains(got, "(app:compteur)") {
		t.Fatalf("edit : %s", got)
	}
	if s := files(t, id, 2); s.js != "var n = 0;\nvar step = 2;" || s.html != "<p id=\"n\">0</p><p>fin</p>" {
		t.Errorf("version 2 = %+v", s)
	}
	// Les fins de ligne du modèle ne font pas rater le passage.
	if got := call(map[string]any{"action": "edit", "id": id, "file": "js", "old": "var n = 0;\r\nvar step", "new": "var n = 5;\nvar step"}); !tool.IsOK(got) {
		t.Errorf("edit avec \\r\\n : %s", got)
	}
	for label, args := range map[string]map[string]any{
		"introuvable":       {"file": "js", "old": "var absent", "new": "x"},
		"multiple":          {"file": "html", "old": "<p", "new": "<div"},
		"sans old":          {"file": "css", "new": "x"},
		"identique":         {"file": "css", "old": "margin", "new": "margin"},
		"fichier inconnu":   {"file": "json", "old": "a", "new": "b"},
		"casse le script":   {"file": "js", "old": "var n = 5;", "new": "var n = ;"},
		"vide index.html":   {"file": "html", "old": "<p id=\"n\">0</p><p>fin</p>", "new": " "},
		"app introuvable":   {"id": "absente", "file": "js", "old": "a", "new": "b"},
		"identifiant vide":  {"id": "", "file": "js", "old": "a", "new": "b"},
		"identifiant piège": {"id": "../compteur", "file": "js", "old": "var", "new": "let"},
	} {
		if _, ok := args["id"]; !ok {
			args["id"] = id
		}
		args["action"] = "edit"
		if got := call(args); !tool.IsErr(got) {
			t.Errorf("%s : %q, refus attendu", label, got)
		}
	}
	if got := call(map[string]any{"action": "edit", "id": id, "file": "html", "old": "<p", "new": "<div"}); !strings.Contains(got, "2 fois") {
		t.Errorf("le refus ne dit pas combien de fois : %s", got)
	}
	if a := List()[0]; a.Version != 3 {
		t.Errorf("version %d après des refus, 3 attendue", a.Version)
	}
}

func TestVersionsAndRollback(t *testing.T) {
	setup(t)
	id := mustSave(t, map[string]any{"name": "Liste", "html": "<p>v1</p>", "js": "var v = 1", "note": "création"})
	mustSave(t, map[string]any{"id": id, "html": "<p>v2</p>", "note": "titre"})
	mustSave(t, map[string]any{"id": id, "js": "var v = 3"})

	versions, err := Versions(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 || versions[0].N != 3 || versions[1].N != 2 || versions[1].Note != "titre" || versions[2].Note != "création" || !versions[0].At.After(versions[2].At) {
		t.Fatalf("versions = %+v", versions)
	}

	if err := Rollback(id, 1); err != nil {
		t.Fatal(err)
	}
	versions, _ = Versions(id)
	if len(versions) != 4 || versions[0].N != 4 || versions[0].Note != "retour à la version 1" {
		t.Fatalf("après retour : %+v", versions)
	}
	if s := files(t, id, 4); s.html != "<p>v1</p>" || s.js != "var v = 1" {
		t.Errorf("version 4 = %+v", s)
	}
	// La version quittée reste là : on peut y revenir aussi.
	if s := files(t, id, 3); s.html != "<p>v2</p>" || s.js != "var v = 3" {
		t.Errorf("version 3 = %+v", s)
	}
	app, doc, err := Page(id)
	if err != nil || app.Version != 4 || !strings.Contains(doc, "<p>v1</p>") {
		t.Errorf("Page après retour : version %d, %v", app.Version, err)
	}

	for _, n := range []int{4, 0, 5, -1} {
		if err := Rollback(id, n); err == nil {
			t.Errorf("Rollback(%d) aurait dû échouer", n)
		}
	}
	if err := Rollback("absente", 1); err == nil {
		t.Error("Rollback d'une app absente aurait dû échouer")
	}
	if _, err := Versions("absente"); err == nil {
		t.Error("Versions d'une app absente aurait dû échouer")
	}
}

func TestOldVersionsPruned(t *testing.T) {
	dir := setup(t)
	id := mustSave(t, map[string]any{"name": "Usure", "html": "<p>1</p>"})
	for n := 2; n <= maxVersions+5; n++ {
		mustSave(t, map[string]any{"id": id, "html": "<p>" + strconv.Itoa(n) + "</p>"})
	}
	versions, err := Versions(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != maxVersions || versions[0].N != maxVersions+5 || versions[maxVersions-1].N != 6 {
		t.Fatalf("%d versions gardées, de %d à %d", len(versions), versions[len(versions)-1].N, versions[0].N)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "APPS", id, "versions"))
	if err != nil || len(entries) != maxVersions {
		t.Errorf("%d dossiers de versions sur le disque (%v), %d attendus", len(entries), err, maxVersions)
	}
	if err := Rollback(id, 5); err == nil {
		t.Error("retour à une version élaguée : refus attendu")
	}
	if err := Rollback(id, 6); err != nil {
		t.Errorf("retour à la plus ancienne version gardée : %v", err)
	}
	if s := files(t, id, maxVersions+6); s.html != "<p>6</p>" {
		t.Errorf("contenu après retour : %+v", s)
	}
}

func TestDelete(t *testing.T) {
	dir := setup(t)
	id := mustSave(t, map[string]any{"name": "Jetable", "html": "<p>x</p>"})
	if _, err := Bridge(context.Background(), id, "storage.set", map[string]any{"key": "k", "value": 1.0}); err != nil {
		t.Fatal(err)
	}
	if err := Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "APPS", id)); !os.IsNotExist(err) {
		t.Errorf("le dossier de l'app est resté : %v", err)
	}
	if len(List()) != 0 {
		t.Error("l'app supprimée est encore listée")
	}
	if err := Delete(id); err == nil {
		t.Error("supprimer une app absente aurait dû échouer")
	}
	// L'outil du modèle ne sait pas supprimer.
	if got := call(map[string]any{"action": "delete", "id": id}); !tool.IsErr(got) {
		t.Errorf("action=delete : %q", got)
	}
}

func TestHostileIDsRefused(t *testing.T) {
	dir := setup(t)
	mustSave(t, map[string]any{"name": "Vraie", "html": "<p>x</p>"})
	// Un dossier qui ressemble à une app, hors d'APPS : rien ne doit l'atteindre.
	outside := filepath.Join(dir, "x")
	for name, text := range map[string]string{"app.json": `{"name":"Dehors","version":1}`, "versions/1/index.html": "<p>dehors</p>", "versions/1/style.css": "", "versions/1/app.js": ""} {
		if err := home.WriteFile(filepath.Join(outside, filepath.FromSlash(name)), []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	for _, id := range []string{"../x", "..", "a/b", `..\x`, "", " ", "Vraie", "-vraie", "vraie.", "nul", "vraie/../vraie", "C:", strings.Repeat("a", 49)} {
		if _, _, err := Page(id); err == nil {
			t.Errorf("Page(%q) acceptée", id)
		}
		if err := Delete(id); err == nil {
			t.Errorf("Delete(%q) acceptée", id)
		}
		if _, err := Versions(id); err == nil {
			t.Errorf("Versions(%q) acceptée", id)
		}
		if err := Rollback(id, 1); err == nil {
			t.Errorf("Rollback(%q) acceptée", id)
		}
		for _, op := range []string{"storage.get", "storage.set", "storage.remove", "storage.keys", "tools.run", "ai.ask", "error"} {
			if _, err := Bridge(ctx, id, op, map[string]any{"key": "k", "value": "v", "name": "t", "prompt": "p", "message": "m"}); err == nil {
				t.Errorf("Bridge(%q, %s) acceptée", id, op)
			}
		}
		for _, args := range []map[string]any{
			{"action": "read"},
			{"action": "errors", "clear": true},
			{"action": "edit", "file": "html", "old": "dehors", "new": "dedans"},
		} {
			args["id"] = id
			if got := call(args); !tool.IsErr(got) {
				t.Errorf("%s id=%q : %q, refus attendu", args["action"], id, got)
			}
		}
		// Pour save, un identifiant vide veut dire « créer ».
		if strings.TrimSpace(id) != "" {
			if got := call(map[string]any{"action": "save", "id": id, "html": "<p>y</p>"}); !tool.IsErr(got) {
				t.Errorf("save id=%q : %q, refus attendu", id, got)
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(outside, "versions", "1", "index.html")); err != nil || string(raw) != "<p>dehors</p>" {
		t.Errorf("le dossier hors d'APPS a été touché : %q, %v", raw, err)
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 2 {
		t.Errorf("des fichiers ont été écrits hors d'APPS : %v", entries)
	}
	if all := List(); len(all) != 1 || all[0].ID != "vraie" || all[0].Version != 1 {
		t.Errorf("List = %+v", all)
	}
}

func TestChangedCalled(t *testing.T) {
	setup(t)
	calls := 0
	// La liste doit être lisible depuis le rappel : le verrou est déjà rendu.
	Changed = func() {
		calls++
		List()
	}
	expect := func(label string, want int) {
		t.Helper()
		if calls != want {
			t.Errorf("%s : Changed appelée %d fois, %d attendues", label, calls, want)
		}
	}
	ctx := context.Background()
	id := mustSave(t, map[string]any{"name": "Vivante", "html": "<p>x</p>"})
	expect("création", 1)
	call(map[string]any{"action": "edit", "id": id, "file": "html", "old": "x", "new": "y"})
	expect("edit", 2)
	call(map[string]any{"action": "edit", "id": id, "file": "html", "old": "absent", "new": "y"})
	call(map[string]any{"action": "save", "id": id, "js": "var = ;"})
	call(map[string]any{"action": "read", "id": id})
	expect("refus et lecture", 2)
	if err := Rollback(id, 1); err != nil {
		t.Fatal(err)
	}
	expect("retour", 3)
	if _, err := Bridge(ctx, id, "storage.set", map[string]any{"key": "k", "value": "v"}); err != nil {
		t.Fatal(err)
	}
	expect("stockage", 3)
	if _, err := Bridge(ctx, id, "error", map[string]any{"message": "boom"}); err != nil {
		t.Fatal(err)
	}
	expect("erreur", 4)
	call(map[string]any{"action": "errors", "id": id})
	expect("lecture des erreurs", 4)
	call(map[string]any{"action": "errors", "id": id, "clear": true})
	expect("effacement des erreurs", 5)
	if err := Delete(id); err != nil {
		t.Fatal(err)
	}
	expect("suppression", 6)
	if err := Delete(id); err == nil {
		t.Error("seconde suppression acceptée")
	}
	expect("suppression refusée", 6)
}
