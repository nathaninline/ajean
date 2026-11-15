package apps

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestPage(t *testing.T) {
	setup(t)
	id := mustSave(t, map[string]any{
		"name": "Titre <b>", "html": "<main id=\"root\">Bonjour</main>", "css": "main { color: teal }",
		"js": "document.getElementById('root').dataset.ok = 1 < 2",
	})
	app, doc, err := Page(id)
	if err != nil {
		t.Fatal(err)
	}
	if app.ID != id || app.Name != "Titre <b>" || app.Version != 1 {
		t.Errorf("app = %+v", app)
	}
	for _, want := range []string{
		"<!doctype html>", `<meta charset="utf-8">`, `<meta name="viewport"`,
		`<meta http-equiv="Content-Security-Policy" content="default-src 'none';`, "script-src 'unsafe-inline'", "img-src data: blob:",
		"<title>Titre &lt;b&gt;</title>", "color-scheme: light dark", "main { color: teal }",
		`<main id="root">Bonjour</main>`, "window.ajean = ", `parent.postMessage({ ajean: 1, id: id, op: op`,
		`"document.getElementById('root').dataset.ok = 1 \` + `u003c 2"`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("le document ne contient pas %q", want)
		}
	}
	// La politique passe avant tout ce qui peut charger quelque chose, le pont
	// avant le corps, et rien dans la politique n'ouvre le réseau.
	csp, bridge, body := strings.Index(doc, "Content-Security-Policy"), strings.Index(doc, "window.ajean"), strings.Index(doc, "<body>")
	if csp > strings.Index(doc, "<style>") || csp > bridge || bridge > body {
		t.Errorf("ordre du document : politique %d, pont %d, corps %d", csp, bridge, body)
	}
	for _, open := range []string{"http:", "https:", "connect-src", "*", "'self'", "unsafe-eval"} {
		if strings.Contains(policy, open) {
			t.Errorf("la politique laisse passer %q", open)
		}
	}
}

// lineSeparator : un caractère invisible que certains analyseurs prennent pour
// une fin de ligne : il ne doit pas arriver tel quel dans le document.
var lineSeparator = string(rune(0x2028))

func TestPageCannotBeBrokenByAppFiles(t *testing.T) {
	setup(t)
	js := "var s = \"</script><script>window.pwned = 1</script>\"; var c = '<!-- <script>';" + lineSeparator + "var t = `</SCRIPT >`"
	css := "p::after { content: \"</style><b>pwned</b>\" } q { content: '</STYLE >' }"
	id := mustSave(t, map[string]any{"name": "Piège", "html": "<p>corps</p>", "css": css, "js": js})
	_, doc, err := Page(id)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(doc)
	if n := strings.Count(lower, "<script"); n != 1 {
		t.Errorf("%d balises <script> ouvertes, 1 attendue (le pont)", n)
	}
	if n := strings.Count(lower, "</script"); n != 1 {
		t.Errorf("%d balises </script>, 1 attendue", n)
	}
	if n := strings.Count(lower, "</style"); n != 2 {
		t.Errorf("%d balises </style>, 2 attendues (base et app)", n)
	}
	if strings.Contains(doc, "<!--") || strings.Contains(doc, lineSeparator) {
		t.Error("le script de l'app apparaît en clair dans le document")
	}
	// Le script arrive intact à l'autre bout : c'est un littéral que le pont
	// reçoit en argument.
	start := strings.Index(doc, bridgeJS)
	if start < 0 {
		t.Fatal("pont introuvable")
	}
	literal, _, _ := strings.Cut(doc[start+len(bridgeJS):], ");\n</script>")
	var got string
	if err := json.Unmarshal([]byte(literal), &got); err != nil || got != js {
		t.Errorf("script relu = %q (%v)", got, err)
	}
	if !strings.Contains(doc, `content: "<\/style><b>pwned</b>"`) || !strings.Contains(doc, `'<\/STYLE >'`) {
		t.Error("la feuille de style n'est pas neutralisée comme attendu")
	}
}

func TestPageReducesFullDocument(t *testing.T) {
	setup(t)
	full := "<!DOCTYPE html>\n<html lang=\"fr\">\n<HEAD>\n<meta charset=\"utf-8\">\n<title>Mon titre</title>\n" +
		"<link rel=\"stylesheet\" href=\"https://cdn.example/x.css\">\n<style>\n.a { color: red }\n</style>\n<script src=\"lib.js\"></script>\n<STYLE media=\"print\">.b{}</STYLE>\n</HEAD>\n" +
		"<BODY class=\"page\">\n<p>Bonjour</p>\n<style>.c{}</style>\n</BODY>\n</html>\n"
	got := call(map[string]any{"action": "save", "name": "Complet", "html": full})
	if !strings.Contains(got, "fragment de corps") {
		t.Errorf("pas d'avertissement pour un document complet : %s", got)
	}
	_, doc, err := Page("complet")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<p>Bonjour</p>", ".a { color: red }", `<STYLE media="print">.b{}</STYLE>`, "<style>.c{}</style>"} {
		if !strings.Contains(doc, want) {
			t.Errorf("perdu en route : %q", want)
		}
	}
	for _, gone := range []string{"Mon titre", "cdn.example", "lib.js", "<BODY", "</BODY>", "<HEAD>", "<!DOCTYPE", `lang="fr"`} {
		if strings.Contains(doc, gone) {
			t.Errorf("resté dans le document : %q", gone)
		}
	}
	if n := strings.Count(strings.ToLower(doc), "<body"); n != 1 {
		t.Errorf("%d balises <body>", n)
	}
	// Les styles de l'en-tête restent dans l'en-tête, le corps dans le corps.
	if strings.Index(doc, ".a { color: red }") > strings.Index(doc, "<body>") || strings.Index(doc, "<p>Bonjour</p>") < strings.Index(doc, "<body>") {
		t.Error("styles ou corps au mauvais endroit")
	}

	for doc, want := range map[string][2]string{
		"<p>simple</p>": {"", "<p>simple</p>"},
		"<head><style>a{}</style></head><p>sans</p>":    {"<style>a{}</style>", "<p>sans</p>"},
		"<body><p>a</p></body><!-- </body> fin":         {"", "<p>a</p></body><!--"},
		"<html><body>\n<p>ouvert</p>":                   {"", "<p>ouvert</p>"},
		"<style>dans le corps{}</style><p>fragment</p>": {"", "<style>dans le corps{}</style><p>fragment</p>"},
	} {
		styles, body := fragment(doc)
		if styles != want[0] || body != want[1] {
			t.Errorf("fragment(%q) = %q, %q ; attendu %q, %q", doc, styles, body, want[0], want[1])
		}
	}
}

// Le pont est exécuté pour de vrai, face à une fausse page : ce qu'il envoie
// et ce qu'il attend en retour sont le contrat avec la page d'AJEAN.
func TestBridgeScriptProtocol(t *testing.T) {
	vm := goja.New()
	run := func(src string) goja.Value {
		t.Helper()
		v, err := vm.RunString(src)
		if err != nil {
			t.Fatalf("%v\n%s", err, src)
		}
		return v
	}
	run(`
		var posted = [], handlers = {}, appended = [], logged = 0;
		var parent = { postMessage: function (m, target) { posted.push(JSON.stringify(m) + " " + target); } };
		var window = globalThis;
		window.addEventListener = function (type, fn) { handlers[type] = fn; };
		var console = { error: function () { logged++; } };
		var document = {
			addEventListener: function (type, fn) { handlers["document." + type] = fn; },
			createElement: function (tag) { return { tag: tag }; },
			body: { appendChild: function (el) { appended.push(el.tag + ":" + el.textContent); } }
		};
	`)
	source, _ := json.Marshal("init('</script>')")
	run(bridgeJS + string(source) + ");")

	run(`
		var got = "rien", failed = "rien", cyclic = "rien", keys;
		ajean.storage.get("k").then(function (v) { got = v; });
		ajean.tools.run("meteo").catch(function (e) { failed = e.message; });
		ajean.storage.set("k", { n: 1, f: function () {}, d: undefined });
		ajean.storage.remove("k");
		ajean.storage.keys().then(function (v) { keys = v; });
		ajean.ai.ask("Dis bonjour");
		var loop = {}; loop.self = loop;
		ajean.storage.set("boucle", loop).catch(function (e) { cyclic = "refusé"; });
	`)
	want := []string{
		`{"ajean":1,"id":1,"op":"storage.get","args":{"key":"k"}} *`,
		`{"ajean":1,"id":2,"op":"tools.run","args":{"name":"meteo","args":{}}} *`,
		`{"ajean":1,"id":3,"op":"storage.set","args":{"key":"k","value":{"n":1}}} *`,
		`{"ajean":1,"id":4,"op":"storage.remove","args":{"key":"k"}} *`,
		`{"ajean":1,"id":5,"op":"storage.keys","args":{}} *`,
		`{"ajean":1,"id":6,"op":"ai.ask","args":{"prompt":"Dis bonjour"}} *`,
	}
	if got := run(`posted.join("\n")`).String(); got != strings.Join(want, "\n") {
		t.Errorf("messages envoyés :\n%s\nattendu :\n%s", got, strings.Join(want, "\n"))
	}
	if got := run(`cyclic`).String(); got != "refusé" {
		t.Errorf("valeur cyclique : %s", got)
	}

	// Une réponse qui ne vient pas de la page parente est ignorée.
	run(`handlers.message({ source: {}, data: { ajean: 1, id: 1, ok: true, result: "intrus" } })`)
	run(`handlers.message({ source: parent, data: { autre: 1, id: 1, ok: true, result: "intrus" } })`)
	if got := run(`got`).String(); got != "rien" {
		t.Errorf("réponse étrangère acceptée : %s", got)
	}
	run(`handlers.message({ source: parent, data: { ajean: 1, id: 1, ok: true, result: { a: 1 } } })`)
	run(`handlers.message({ source: parent, data: { ajean: 1, id: 2, ok: false, error: "outil non déclaré" } })`)
	run(`handlers.message({ source: parent, data: { ajean: 1, id: 5, ok: true, result: ["k"] } })`)
	run(`handlers.message({ source: parent, data: { ajean: 1, id: 1, ok: true, result: "rejouée" } })`)
	if got := run(`JSON.stringify([got, failed, keys])`).String(); got != `[{"a":1},"outil non déclaré",["k"]]` {
		t.Errorf("promesses dénouées : %s", got)
	}

	// Erreurs remontées sans attendre de réponse, et bornées par ouverture.
	run(`posted = [];
		handlers.error({ message: "x is not defined", lineno: 12 });
		handlers.unhandledrejection({ reason: new Error("promesse") });
		handlers["document.securitypolicyviolation"]({ violatedDirective: "img-src", blockedURI: "https://cdn.example/a.png" });
		console.error("échec", { code: 3 });
		for (var i = 0; i < 50; i++) handlers.error({ message: "boucle " + i });
	`)
	wantErrors := []string{
		`{"ajean":1,"id":0,"op":"error","args":{"message":"x is not defined (app.js line 12)"}} *`,
		`{"ajean":1,"id":0,"op":"error","args":{"message":"Unhandled promise rejection: promesse"}} *`,
		`{"ajean":1,"id":0,"op":"error","args":{"message":"Blocked by the sandbox (img-src): https://cdn.example/a.png"}} *`,
		`{"ajean":1,"id":0,"op":"error","args":{"message":"console.error: échec {\"code\":3}"}} *`,
	}
	if got := run(`posted.slice(0, 4).join("\n")`).String(); got != strings.Join(wantErrors, "\n") {
		t.Errorf("erreurs envoyées :\n%s\nattendu :\n%s", got, strings.Join(wantErrors, "\n"))
	}
	if n := run(`posted.length`).ToInteger(); n != 20 {
		t.Errorf("%d erreurs envoyées, 20 au plus attendues", n)
	}
	if n := run(`logged`).ToInteger(); n != 1 {
		t.Errorf("console.error d'origine appelée %d fois", n)
	}

	// Le script de l'app part une seule fois, quand le corps est lu.
	if n := run(`appended.length`).ToInteger(); n != 0 {
		t.Errorf("script de l'app lancé avant la fin de la lecture")
	}
	run(`handlers["document.readystatechange"](); handlers["document.readystatechange"]()`)
	if got := run(`appended.join("|")`).String(); got != "script:init('</script>')" {
		t.Errorf("script lancé : %q", got)
	}
	if got := run(`Object.isFrozen(ajean) && Object.isFrozen(ajean.storage)`).ToBoolean(); !got {
		t.Error("window.ajean peut être modifié par l'app")
	}
}
