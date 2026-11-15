package scripts

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ajean/internal/home"
	"ajean/internal/tool"
	"ajean/internal/workspace"
)

// setup installe un dossier d'AJEAN vide (workspace ouvert) et rend ce dossier.
func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AJEAN_HOME", dir)
	if err := workspace.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := workspace.Close(); err != nil {
			t.Error(err)
		}
	})
	return dir
}

// call appelle l'outil « tools » comme le ferait le modèle.
func call(args map[string]any) string {
	return Tools()[0].Run(tool.Call{Args: args})
}

// saveAndRun enregistre un script puis le lance.
func saveAndRun(t *testing.T, name, code string, args map[string]any) string {
	t.Helper()
	got := call(map[string]any{"action": "save", "name": name, "description": "outil de test", "code": code})
	if !tool.IsOK(got) {
		t.Fatalf("save %s : %s", name, got)
	}
	return call(map[string]any{"action": "run", "name": name, "args": args})
}

func TestSingleTool(t *testing.T) {
	defs := Tools()
	if len(defs) != 1 || defs[0].Name != "tools" {
		t.Fatalf("un seul outil « tools » attendu, reçu %d", len(defs))
	}
}

func TestRunReturnsString(t *testing.T) {
	setup(t)
	got := saveAndRun(t, "salut", `function run(args) { return "bonjour " + args.name }`, map[string]any{"name": "Alice"})
	if got != "bonjour Alice" {
		t.Errorf("reçu %q", got)
	}
}

func TestRunReturnsObjectAsJSON(t *testing.T) {
	setup(t)
	got := saveAndRun(t, "objet", `function run(args) { return {total: args.a + args.b, tags: ["x", "y"]} }`, map[string]any{"a": 2.0, "b": 3.0})
	want := "{\n  \"tags\": [\n    \"x\",\n    \"y\"\n  ],\n  \"total\": 5\n}"
	if got != want {
		t.Errorf("reçu %q\nattendu %q", got, want)
	}
	// Les arguments arrivent parfois comme texte JSON.
	if got := call(map[string]any{"action": "run", "name": "objet", "args": `{"a": 1, "b": 1}`}); !strings.Contains(got, `"total": 2`) {
		t.Errorf("args en texte : %q", got)
	}
}

func TestConsoleLogAndConstRun(t *testing.T) {
	setup(t)
	got := saveAndRun(t, "bavard", `const run = (args) => { console.log("étape", 1, {ok: true}); return 42 }`, nil)
	if want := "étape 1 {\n  \"ok\": true\n}\n42"; got != want {
		t.Errorf("reçu %q\nattendu %q", got, want)
	}
	if got := saveAndRun(t, "muet", `function run() {}`, nil); !strings.Contains(got, "rien renvoyé") {
		t.Errorf("script sans return : %q", got)
	}
}

func TestWorkspaceAccess(t *testing.T) {
	dir := setup(t)
	code := `function run(args) {
		ws.write("notes/a.txt", args.text)
		ws.move("notes/a.txt", "notes/b.txt")
		var seen = ws.exists("notes/b.txt") && !ws.exists("notes/a.txt")
		var listed = ws.list("notes").map(function (e) { return e }).join(",")
		var text = ws.read("notes/b.txt")
		ws.remove("notes/b.txt")
		return text + "|" + seen + "|" + listed + "|" + ws.exists("notes/b.txt")
	}`
	got := saveAndRun(t, "fichiers", code, map[string]any{"text": "contenu"})
	if want := "contenu|true|b.txt (7 octets)|false"; got != want {
		t.Errorf("reçu %q, attendu %q", got, want)
	}

	if err := os.WriteFile(filepath.Join(dir, "secret"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	got = saveAndRun(t, "evasion", `function run() { return ws.read("../secret") }`, nil)
	if !tool.IsErr(got) || strings.Contains(got, "secret\n") || !strings.Contains(got, "chemin refusé") {
		t.Errorf("lecture hors du workspace : %q", got)
	}
	got = saveAndRun(t, "evasion2", `function run() { ws.write("C:\\Windows\\x.txt", "x"); return "écrit" }`, nil)
	if !tool.IsErr(got) {
		t.Errorf("écriture hors du workspace : %q", got)
	}
	// Une erreur du workspace est une exception ordinaire pour le script.
	got = saveAndRun(t, "attrape", `function run() { try { ws.read("absent.txt") } catch (e) { return "attrapé" } }`, nil)
	if got != "attrapé" {
		t.Errorf("exception attrapée : %q", got)
	}
}

func TestInfiniteLoopInterrupted(t *testing.T) {
	setup(t)
	old := timeout
	timeout = 200 * time.Millisecond
	t.Cleanup(func() { timeout = old })

	start := time.Now()
	got := saveAndRun(t, "boucle", `function run() { console.log("avant"); while (true) {} }`, nil)
	if !tool.IsErr(got) || !strings.Contains(got, "temps dépassé") {
		t.Errorf("boucle infinie : %q", got)
	}
	if !strings.Contains(got, "avant") {
		t.Errorf("ce qui a été écrit avant l'arrêt est perdu : %q", got)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("interruption trop lente : %s", d)
	}
	got = saveAndRun(t, "recursion", `function run() { return run() }`, nil)
	if !tool.IsErr(got) {
		t.Errorf("récursion sans fin : %q", got)
	}
}

func TestLocalNetworkRefused(t *testing.T) {
	setup(t)
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer srv.Close()

	for name, code := range map[string]string{
		"port_ferme": `function run() { return http.get("http://127.0.0.1:1/").body }`,
		"interface":  `function run(args) { return http.get(args.url).body }`,
		"par_nom":    `function run() { return http.post("http://localhost:1/", "x").body }`,
		"fichier":    `function run() { return http.get("file:///C:/Windows/win.ini").body }`,
		"sans_url":   `function run() { return http.request({method: "GET"}).body }`,
	} {
		got := saveAndRun(t, name, code, map[string]any{"url": srv.URL})
		if !tool.IsErr(got) || !strings.Contains(got, "refusée") {
			t.Errorf("%s : %q, refus attendu", name, got)
		}
	}
	if reached {
		t.Error("un script a joint la machine locale")
	}
}

// Le contrôle des adresses est neutralisé ici pour vérifier le reste du
// chemin : méthode, en-têtes, corps, forme de la réponse, taille bornée.
func TestHTTPPlumbing(t *testing.T) {
	setup(t)
	guarded := client.Transport
	client.Transport = http.DefaultTransport
	t.Cleanup(func() { client.Transport = guarded })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gros" {
			w.Write(make([]byte, maxResponse+1000))
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, r.Method+" "+r.Header.Get("X-Test")+" "+r.Header.Get("User-Agent")+" "+string(body))
	}))
	defer srv.Close()

	code := `function run(args) {
		var a = http.get(args.url, {"X-Test": "un"})
		var b = http.post(args.url, "corps", {"X-Test": "deux"})
		var c = http.request({method: "put", url: args.url, headers: {"X-Test": "trois"}, body: "autre"})
		var big = http.get(args.url + "/gros")
		return [a.status, a.type, a.body, b.body, c.body, big.body.length].join("|")
	}`
	got := saveAndRun(t, "web", code, map[string]any{"url": srv.URL})
	want := "201|text/plain|GET un ajean-tool/1.0 |POST deux ajean-tool/1.0 corps|PUT trois ajean-tool/1.0 autre|2097152"
	if got != want {
		t.Errorf("reçu %q\nattendu %q", got, want)
	}
}

func TestNoSystemAccess(t *testing.T) {
	setup(t)
	code := `function run() {
		return ["require", "process", "fetch", "module", "setTimeout", "XMLHttpRequest", "Deno", "os", "fs"]
			.filter(function (n) { return typeof globalThis[n] !== "undefined" }).join(",") || "rien"
	}`
	if got := saveAndRun(t, "globales", code, nil); got != "rien" {
		t.Errorf("globales inattendues : %q", got)
	}
	got := saveAndRun(t, "require", `function run() { return require("fs").readFileSync("C:/Windows/win.ini") }`, nil)
	if !tool.IsErr(got) || !strings.Contains(got, "require") {
		t.Errorf("require : %q", got)
	}
}

func TestSaveRefused(t *testing.T) {
	setup(t)
	cases := map[string]map[string]any{
		"ne compile pas":   {"name": "casse", "description": "d", "code": "function run( { return 1"},
		"sans run":         {"name": "sans_run", "description": "d", "code": "var x = 1"},
		"sans description": {"name": "sans_desc", "code": "function run() { return 1 }"},
		"sans code":        {"name": "sans_code", "description": "d"},
		"nom invalide":     {"name": "../evasion", "description": "d", "code": "function run() { return 1 }"},
		"nom vide":         {"description": "d", "code": "function run() { return 1 }"},
		"trop long":        {"name": "long", "description": "d", "code": "function run() { return 1 }\n//" + strings.Repeat("x", maxCode)},
	}
	for label, args := range cases {
		args["action"] = "save"
		if got := call(args); !tool.IsErr(got) {
			t.Errorf("%s : %q, refus attendu", label, got)
		}
	}
	if got := List(); len(got) != 0 {
		t.Errorf("des scripts refusés ont été enregistrés : %v", got)
	}
}

func TestListLoadDelete(t *testing.T) {
	setup(t)
	if got := call(map[string]any{"action": "list"}); !strings.Contains(got, "aucun outil") {
		t.Errorf("liste vide : %q", got)
	}
	got := call(map[string]any{
		"action": "save", "name": "Meteo Ville", "description": "Donne la météo */ d'une\nville",
		"params": map[string]any{"city": "Nom de la ville"}, "code": "function run(args) {\r\n  return args.city\r\n}",
	})
	if !tool.IsOK(got) || !strings.Contains(got, "meteo_ville") {
		t.Fatalf("save : %q", got)
	}
	call(map[string]any{"action": "save", "name": "alpha", "description": "premier", "code": "function run() { return 1 }"})

	all := List()
	if len(all) != 2 || all[0].Name != "alpha" || all[1].Name != "meteo_ville" {
		t.Fatalf("List = %+v", all)
	}
	s := all[1]
	if s.Description != "Donne la météo * / d'une ville" || s.Params["city"] != "Nom de la ville" || s.Code != "function run(args) {\n  return args.city\n}\n" {
		t.Errorf("script relu : %+v", s)
	}
	if got := call(map[string]any{"action": "list"}); got != "- alpha: premier\n- meteo_ville: Donne la météo * / d'une ville" {
		t.Errorf("list : %q", got)
	}
	got = call(map[string]any{"action": "load", "name": "meteo-ville"})
	if !strings.HasPrefix(got, "# meteo_ville\n") || !strings.Contains(got, `params: {"city":"Nom de la ville"}`) || !strings.Contains(got, "return args.city") {
		t.Errorf("load : %q", got)
	}
	if got := call(map[string]any{"action": "run", "name": "meteo_ville", "args": map[string]any{"city": "Lyon"}}); got != "Lyon" {
		t.Errorf("run : %q", got)
	}

	// Remplacer un script existant.
	call(map[string]any{"action": "save", "name": "alpha", "description": "second", "code": "function run() { return 2 }"})
	if got := call(map[string]any{"action": "run", "name": "alpha"}); got != "2" {
		t.Errorf("après remplacement : %q", got)
	}

	if got := call(map[string]any{"action": "delete", "name": "alpha"}); !tool.IsOK(got) {
		t.Errorf("delete : %q", got)
	}
	if err := Delete("alpha"); err == nil {
		t.Error("supprimer un script absent aurait dû échouer")
	}
	for _, action := range []string{"load", "run", "delete"} {
		if got := call(map[string]any{"action": action, "name": "alpha"}); !tool.IsErr(got) {
			t.Errorf("%s d'un script supprimé : %q", action, got)
		}
	}
	if got := call(map[string]any{"action": "explose"}); !tool.IsErr(got) {
		t.Errorf("action inconnue : %q", got)
	}
}

func TestHandWrittenFile(t *testing.T) {
	setup(t)
	if err := home.WriteFile(filepath.Join(home.Tools(), "main.js"), []byte("function run() { return 'à la main' }")); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteFile(filepath.Join(home.Tools(), "Pas Un Outil.js"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	all := List()
	if len(all) != 1 || all[0].Name != "main" || all[0].Description != "" {
		t.Fatalf("List = %+v", all)
	}
	if got := call(map[string]any{"action": "run", "name": "main"}); got != "à la main" {
		t.Errorf("run : %q", got)
	}
}

func TestOutputBounded(t *testing.T) {
	setup(t)
	got := saveAndRun(t, "bavard", `function run() { for (var i = 0; i < 5000; i++) console.log("ligne " + i); return "x".repeat(100000) }`, nil)
	if n := len([]rune(got)); n > maxOutput+100 {
		t.Errorf("sortie de %d caractères, max %d", n, maxOutput)
	}
	if !strings.Contains(got, "tronqué") {
		t.Error("la troncature n'est pas signalée")
	}
}

func TestRuntimeErrorReported(t *testing.T) {
	setup(t)
	got := saveAndRun(t, "plante", `function run() { console.log("début"); throw new Error("ça casse") }`, nil)
	if !tool.IsErr(got) || !strings.Contains(got, "ça casse") || !strings.Contains(got, "début") || !strings.Contains(got, "action=load") {
		t.Errorf("erreur d'exécution : %q", got)
	}
}
