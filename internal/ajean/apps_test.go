package ajean

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestAppLifecycle(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	ctx := context.Background()
	out := toolApp(ctx, map[string]any{"action": "create", "name": "Mes Factures", "icon": "🧾",
		"html": `<h1>Factures</h1><div id="l"></div>`, "css": "h1{margin:0}", "js": "document.getElementById('l').textContent='ok';"})
	if !regexp.MustCompile(`^App « Mes Factures » créée : version 1 \(id mes-factures\)\. \[app:mes-factures:1\]$`).MatchString(strings.SplitN(out, "\n", 2)[0]) {
		t.Fatalf("create: %s", out)
	}
	// Nom identique : id unique.
	out = toolApp(ctx, map[string]any{"action": "create", "name": "Mes Factures", "html": "<p>x</p>"})
	if !strings.Contains(out, "[app:mes-factures-2:1]") {
		t.Fatalf("id unique: %s", out)
	}
	// Retouche ciblée → version 2.
	out = toolApp(ctx, map[string]any{"action": "update", "id": "mes-factures", "note": "titre",
		"edits": []any{map[string]any{"part": "html", "old": "<h1>Factures</h1>", "new": "<h1>Mes factures</h1>"}}})
	if !strings.Contains(out, "[app:mes-factures:2]") {
		t.Fatalf("update: %s", out)
	}
	c, _ := readAppCode("mes-factures", 2)
	if !strings.Contains(c.HTML, "Mes factures") {
		t.Fatalf("edit non appliqué: %q", c.HTML)
	}
	// Extrait absent → erreur lisible, pas de nouvelle version.
	out = toolApp(ctx, map[string]any{"action": "update", "id": "mes-factures", "edits": []any{map[string]any{"part": "js", "old": "zzz", "new": "y"}}})
	if !strings.HasPrefix(out, "[erreur]") {
		t.Fatalf("edit absent: %s", out)
	}
	// Retour à la v1 = nouvelle version 3 au contenu de la 1.
	m, err := restoreAppVersion("mes-factures", 1)
	if err != nil || m.Version != 3 {
		t.Fatalf("restore: %v %+v", err, m)
	}
	// Contrôles du HTML.
	for _, bad := range []string{`<script>alert(1)</script>`, `<img src=x onerror="x()">`, `<meta http-equiv="refresh">`} {
		if r := toolApp(ctx, map[string]any{"action": "create", "name": "bad", "html": bad}); !strings.HasPrefix(r, "[erreur]") {
			t.Fatalf("html %q accepté: %s", bad, r)
		}
	}
	if r := toolApp(ctx, map[string]any{"action": "create", "name": "bad", "html": "<p>x</p>", "js": "var s='</script>';"}); !strings.HasPrefix(r, "[erreur]") {
		t.Fatalf("</script> dans js accepté")
	}
	// Storage.
	if _, err := appStorageOp("mes-factures", "set", "k1", json.RawMessage(`{"a":1}`), ""); err != nil {
		t.Fatal(err)
	}
	v, _ := appStorageOp("mes-factures", "get", "k1", nil, "")
	if string(v.(json.RawMessage)) != `{"a":1}` {
		t.Fatalf("get: %v", v)
	}
	keys, _ := appStorageOp("mes-factures", "list", "", nil, "k")
	if len(keys.([]string)) != 1 {
		t.Fatalf("list: %v", keys)
	}
	// Noms de scripts : pas d'évasion.
	for _, bad := range []string{"../x.py", "a/b.py", "x.exe", ".py"} {
		if err := writeAppScript("mes-factures", bad, "print(1)"); err == nil {
			t.Fatalf("script %q accepté", bad)
		}
	}
	// Le dossier apps est réservé à l'outil.
	if msg := guardToolOnlyCommand("type " + appsDir() + "\\mes-factures\\app.json"); msg == "" && runtime.GOOS == "windows" {
		t.Fatalf("apps/ accessible au shell")
	}
	if err := deleteApp("mes-factures-2"); err != nil {
		t.Fatal(err)
	}
	if len(listApps()) != 1 {
		t.Fatalf("delete")
	}
}

func TestAppScriptRun(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	py := "python3"
	if runtime.GOOS == "windows" {
		py = "python"
	}
	if _, err := exec.LookPath(py); err != nil {
		t.Skip("python absent")
	}
	if _, err := createApp("Test", "", "", &AppCode{HTML: "<p>x</p>"}, ""); err != nil {
		t.Fatal(err)
	}
	// L'entrée arrive sur stdin, intacte, même avec des caractères de shell.
	src := "import sys,json,os\nd=json.load(sys.stdin)\nprint(json.dumps({'echo':d,'cwd_ok':os.path.realpath(os.getcwd())==os.path.realpath(os.environ['AJEAN_APP_DATA'])}))\n"
	if err := writeAppScript("test", "echo.py", src); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"q": `"; rm -rf / & echo $(x) é`}
	r := runAppScript(context.Background(), "test", "echo.py", in, 0)
	if !r.OK {
		t.Fatalf("run: %+v", r)
	}
	d := r.Data.(map[string]any)
	if d["echo"].(map[string]any)["q"] != in["q"] || d["cwd_ok"] != true {
		t.Fatalf("data: %+v", d)
	}
	_ = writeAppScript("test", "fail.py", "import sys\nprint('oops',file=sys.stderr)\nsys.exit(3)\n")
	r = runAppScript(context.Background(), "test", "fail.py", nil, 0)
	if r.OK || r.Exit != 3 || !strings.Contains(r.Stderr, "oops") {
		t.Fatalf("fail: %+v", r)
	}
	if r := runAppScript(context.Background(), "test", "absent.py", nil, 0); r.OK || r.Error == "" {
		t.Fatalf("absent: %+v", r)
	}
}

// Un seul create porte l'app ET son script : le harnais écrit le script, le
// lance, contrôle le JS et rend l'app dans Chrome avec le vrai script.
func TestAppCreateRendersWithScripts(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	py := "python3"
	if runtime.GOOS == "windows" {
		py = "python"
	}
	if _, err := exec.LookPath(py); err != nil || chromePath() == "" {
		t.Skip("python ou Chrome absent")
	}
	out := toolApp(context.Background(), map[string]any{"action": "create", "name": "Compteur",
		"html":    `<h1 id="t">…</h1>`,
		"js":      `ajean.scripts.run('n.py',{a:2}).then(function(r){ document.getElementById('t').textContent='Total '+r.data.v; document.getElementById('absent').x=1; });`,
		"scripts": []any{map[string]any{"name": "n.py", "content": "import sys,json\nd=json.load(sys.stdin)\nprint(json.dumps({'v':(d or {}).get('a',0)*21}))\n", "input": map[string]any{"a": 2}}},
	})
	for _, want := range []string{"[app:compteur:1]", "Script n.py écrit et lancé", `{"v": 42}`, "#absent", "scripts.run(n.py) → ok=true", "Total 42", "TypeError"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rapport sans %q :\n%s", want, out)
		}
	}
}

// Le modèle voit les outils SÉPARÉS ; un appel regroupé (conversation de la
// période 0.18.2 rejouée) revient quand même à l'outil interne.
func TestToolsSeparatesEtAppelsRegroupes(t *testing.T) {
	names := map[string]bool{}
	for _, tl := range EnabledTools(Caps{Agent: true, Internet: true, Mem: MemAlways}) {
		if names[tl.Function.Name] {
			t.Fatalf("doublon %s", tl.Function.Name)
		}
		names[tl.Function.Name] = true
	}
	for _, want := range []string{"bash", "write", "edit", "mem_read", "task_create", "web_grep", "app", "tracker"} {
		if !names[want] {
			t.Fatalf("outil %s absent : %v", want, names)
		}
	}
	if names["memory"] || names["web"] || names["file"] {
		t.Fatalf("outil regroupé exposé au modèle : %v", names)
	}
	cases := []struct {
		name string
		args map[string]any
		want string
		key  string
	}{
		{"memory", map[string]any{"action": "read", "file": "x.md"}, "mem_read", "file"},
		{"web", map[string]any{"action": "grep", "url": "u", "pattern": "p"}, "web_grep", "pattern"},
		{"file", map[string]any{"action": "edit", "file": "a", "old": "o", "new": "n"}, "edit", "old"},
		{"recall", map[string]any{"id": "r3"}, "recall", "id"},
		{"see", map[string]any{"file": "clip.mp4"}, "see_video", "file"},
		{"jean", map[string]any{"action": "rule", "kind": "replace", "rule_action": "remove"}, "jean_rule", "action"},
		{"bash", map[string]any{"command": "ls"}, "bash", "command"},
	}
	for _, c := range cases {
		n, a, e := expandToolCall(c.name, c.args)
		if e != "" || n != c.want || a[c.key] == nil {
			t.Fatalf("%s %v → %s %v %q", c.name, c.args, n, a, e)
		}
		if c.want != "jean_rule" && c.name != "bash" && a["action"] != nil {
			t.Fatalf("action laissée dans %v", a)
		}
	}
	if _, _, e := expandToolCall("memory", map[string]any{"action": "zap"}); !strings.Contains(e, "search, read, add, edit, delete") {
		t.Fatalf("action inconnue : %q", e)
	}
}

// Fichiers d'une app : morceaux dans l'ordre, relecture, noms piégés refusés,
// et le script les voit dans AJEAN_APP_FILES.
func TestAppFiles(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if _, err := createApp("Scan", "", "", &AppCode{HTML: "<p>x</p>"}, ""); err != nil {
		t.Fatal(err)
	}
	a := bytes.Repeat([]byte("A"), appFileChunk)
	b := []byte("fin")
	if _, err := writeAppFileChunk("scan", "page 1.jpg", 0, a); err != nil {
		t.Fatal(err)
	}
	if _, err := writeAppFileChunk("scan", "page 1.jpg", 5, b); err == nil {
		t.Fatal("morceau hors séquence accepté")
	}
	if n, err := writeAppFileChunk("scan", "page 1.jpg", int64(len(a)), b); err != nil || n != int64(len(a)+len(b)) {
		t.Fatalf("2e morceau : %d %v", n, err)
	}
	got, size, err := readAppFileChunk("scan", "page 1.jpg", int64(len(a)), 0)
	if err != nil || string(got) != "fin" || size != int64(len(a)+len(b)) {
		t.Fatalf("relecture : %q %d %v", got, size, err)
	}
	for _, bad := range []string{"../x", "a/b.pdf", "..", "", ".cache", `x\y`} {
		if _, err := writeAppFileChunk("scan", bad, 0, b); err == nil {
			t.Fatalf("nom %q accepté", bad)
		}
	}
	if fs := listAppFiles("scan"); len(fs) != 1 || fs[0].Name != "page 1.jpg" {
		t.Fatalf("liste : %+v", fs)
	}
	py := "python3"
	if runtime.GOOS == "windows" {
		py = "python"
	}
	if _, err := exec.LookPath(py); err != nil {
		t.Skip("python absent")
	}
	// Entrée de 300 Ko (au-delà de la limite d'une variable d'environnement
	// Linux) : elle arrive par stdin, et le script écrit son résultat en fichier.
	src := "import sys,json,os\nd=json.load(sys.stdin)\nf=os.environ['AJEAN_APP_FILES']\nn=os.path.getsize(os.path.join(f,d['page']))\nopen(os.path.join(f,'out.pdf'),'wb').write(b'%PDF-1.4')\nprint(json.dumps({'ok':True,'in':len(d['big']),'page':n}))\n"
	if err := writeAppScript("scan", "pdf.py", src); err != nil {
		t.Fatal(err)
	}
	r := runAppScript(context.Background(), "scan", "pdf.py", map[string]any{"page": "page 1.jpg", "big": strings.Repeat("x", 300<<10)}, 0)
	if !r.OK {
		t.Fatalf("script : %+v", r)
	}
	d := r.Data.(map[string]any)
	if d["in"].(float64) != 300<<10 || d["page"].(float64) != float64(len(a)+len(b)) {
		t.Fatalf("data : %v", d)
	}
	if out, _, err := readAppFileChunk("scan", "out.pdf", 0, 0); err != nil || string(out) != "%PDF-1.4" {
		t.Fatalf("out.pdf : %q %v", out, err)
	}
}

// export → édition des fichiers → update from_dir ; script_edit ; rappel de livrer.
func TestAppExportFromDirAndScriptEdit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AJEAN_HOME", home)
	ctx := context.Background()
	if r := toolApp(ctx, map[string]any{"action": "create", "name": "Dev", "html": `<p id="p">a</p>`, "js": "document.getElementById('p').textContent='v1';"}); !strings.Contains(r, "[app:dev:1]") {
		t.Fatal(r)
	}
	out := toolApp(ctx, map[string]any{"action": "export", "id": "dev"})
	dir := appDevDir(ctx, "dev")
	if !strings.Contains(out, dir) {
		t.Fatalf("export : %s", out)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("document.getElementById('p').textContent='v2';"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "scripts", "s.sh"), []byte("echo '{\"ok\":true}'\n"), 0o755)
	out = toolApp(ctx, map[string]any{"action": "update", "id": "dev", "from_dir": true, "note": "v2"})
	if !strings.Contains(out, "[app:dev:2]") {
		t.Fatalf("from_dir : %s", out)
	}
	c, _ := readAppCode("dev", 2)
	if !strings.Contains(c.JS, "v2") || c.HTML != `<p id="p">a</p>` {
		t.Fatalf("code : %+v", c)
	}
	if runtime.GOOS != "windows" && len(listAppScripts("dev")) != 1 {
		t.Fatalf("script du dossier non repris : %v", listAppScripts("dev"))
	}
	// script_edit
	_ = writeAppScript("dev", "e.py", "print('{\"n\": 1}')\n")
	out = toolApp(ctx, map[string]any{"action": "script_edit", "id": "dev", "script": "e.py", "edits": []any{map[string]any{"old": `"n": 1`, "new": `"n": 2`}}})
	if !strings.HasPrefix(out, "[ok] script e.py retouché") {
		t.Fatalf("script_edit : %s", out)
	}
	if p, _ := appScriptPath("dev", "e.py"); true {
		b, _ := os.ReadFile(p)
		if !strings.Contains(string(b), `"n": 2`) {
			t.Fatalf("script non retouché : %s", b)
		}
	}
	// Rappel de livrer à partir de la 5e version dans l'heure.
	last := ""
	for i := 0; i < 4; i++ {
		last = toolApp(ctx, map[string]any{"action": "update", "id": "dev", "js": fmt.Sprintf("var x=%d;", i)})
	}
	if !strings.Contains(last, "présente-la à l'utilisateur") {
		t.Fatalf("pas de rappel : %s", last)
	}
}

// check avec steps : l'IA clique, tape et choisit dans l'app comme un
// utilisateur, puis lit le texte visible APRÈS ces actions.
func TestAppCheckSteps(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	if chromePath() == "" {
		t.Skip("Chrome absent")
	}
	out := toolApp(context.Background(), map[string]any{"action": "create", "name": "Liste",
		"html": `<form id="f"><input id="name"><select id="cat"><option value="a">A</option><option value="b">B</option></select><button id="add">Ajouter</button></form><ul id="l"></ul>`,
		"js":   `document.getElementById('f').addEventListener('submit',function(e){e.preventDefault();var li=document.createElement('li');li.textContent='Nom '+document.getElementById('name').value+' cat '+document.getElementById('cat').value;document.getElementById('l').appendChild(li);});`,
	})
	if !strings.Contains(out, "[app:liste:1]") {
		t.Fatalf("création :\n%s", out)
	}
	out, _ = toolAppCheck(context.Background(), map[string]any{"action": "check", "id": "liste", "steps": []any{
		map[string]any{"type": "#name", "text": "Ana"},
		map[string]any{"select": "#cat", "value": "b"},
		map[string]any{"click": "#add"},
		map[string]any{"click": "#absent"},
		map[string]any{"select": "#cat", "value": "zz"},
		map[string]any{"wait": float64(100)},
	}})
	for _, want := range []string{"saisie dans #name : ok", "choix dans #cat : ok", "clic #add : ok", "clic #absent : introuvable", "valeur absente de la liste", "attente 100 ms : ok", "Nom Ana cat b"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rapport sans %q :\n%s", want, out)
		}
	}
}

// data_get / data_set : l'IA lit et remplit les données de l'app sans toucher
// à son code (ajean.storage).
func TestAppDataGetSet(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	m, err := createApp("Agenda", "", "", &AppCode{HTML: "<p>x</p>"}, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	call := func(a map[string]any) string { a["id"] = m.ID; return toolApp(ctx, a) }
	if out := call(map[string]any{"action": "data_get"}); !strings.Contains(out, "aucune donnée") {
		t.Fatalf("vide : %s", out)
	}
	// Valeur en objet, puis en texte JSON (le modèle passe souvent une chaîne).
	if out := call(map[string]any{"action": "data_set", "key": "state", "input": map[string]any{"events": []any{map[string]any{"date": "2026-11-16", "titre": "Tournage"}}}}); !strings.HasPrefix(out, "[ok]") {
		t.Fatalf("set objet : %s", out)
	}
	if out := call(map[string]any{"action": "data_set", "key": "liste", "input": `["a","b"]`}); !strings.HasPrefix(out, "[ok]") {
		t.Fatalf("set texte : %s", out)
	}
	out := call(map[string]any{"action": "data_get"})
	for _, want := range []string{`"titre":"Tournage"`, `"liste":["a","b"]`} {
		if !strings.Contains(out, want) {
			t.Fatalf("get sans %s : %s", want, out)
		}
	}
	// L'app lit bien la même chose par son pont.
	v, err := appStorageOp(m.ID, "get", "liste", nil, "")
	if err != nil || string(v.(json.RawMessage)) != `["a","b"]` {
		t.Fatalf("pont : %v %v", v, err)
	}
	if out := call(map[string]any{"action": "data_set", "key": "liste", "input": nil}); !strings.Contains(out, "supprimée") {
		t.Fatalf("suppression : %s", out)
	}
	if out := call(map[string]any{"action": "data_get", "key": "liste"}); !strings.Contains(out, "absente") {
		t.Fatalf("après suppression : %s", out)
	}
}
