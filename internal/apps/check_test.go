package apps

import (
	"strings"
	"testing"

	"ajean/internal/tool"
)

func TestCheckCleanApp(t *testing.T) {
	s := source{
		html: `<main><h1 id="title">Tâches</h1><input id="new" placeholder="Ajouter"><button id="add">+</button><ul id="list"></ul><a href="#top">haut</a></main>`,
		css:  `@media (prefers-color-scheme: dark) { body { background: #111 } } main { background: url("data:image/png;base64,AAAA") }`,
		js: `const list = document.getElementById("list");
			async function load() {
				const todos = (await ajean.storage.get("todos")) ?? [];
				list.innerHTML = todos.map((t, i) => '<li id="row-' + i + '">' + t + '</li>').join("");
				document.querySelector("#title").textContent = todos.length + " tâches";
				document.querySelectorAll('#late').forEach((el) => el.remove());
			}
			document.getElementById("add").addEventListener("click", async () => {
				const late = document.createElement("p"); late.id = "late";
				await ajean.storage.set("todos", [document.querySelector('#new').value]);
				load().catch(showAlert); myprompt(); obj.confirm();
			});
			load();`,
	}
	warnings, err := check(s)
	if err != nil || len(warnings) != 0 {
		t.Errorf("app saine : %v, avertissements %q", err, warnings)
	}
}

func TestCheckWarnings(t *testing.T) {
	base := source{html: `<p id="out"></p>`, js: `var out = document.getElementById("out")`}
	for label, c := range map[string]struct {
		s    source
		want string
	}{
		"localStorage":     {source{js: `localStorage.setItem("a", 1)`}, "ajean.storage"},
		"sessionStorage":   {source{js: `var v = window.sessionStorage.getItem("a")`}, "ajean.storage"},
		"cookie":           {source{js: `document.cookie = "a=1"`}, "ajean.storage"},
		"fetch":            {source{js: `fetch ("https://api.example/x").then(r => r.json())`}, "ajean.tools.run"},
		"XMLHttpRequest":   {source{js: `var x = new XMLHttpRequest()`}, "ajean.tools.run"},
		"WebSocket":        {source{js: `new WebSocket("wss://x")`}, "ajean.tools.run"},
		"alert":            {source{js: `if (bad) alert("non")`}, "dans la page"},
		"confirm en ligne": {source{html: `<button onclick="confirm('sûr ?')">x</button>`}, "dans la page"},
		"prompt":           {source{js: `var n = window.prompt("nom ?")`}, "dans la page"},
		"eval":             {source{js: `var f = new Function("return 1")`}, "eval"},
		"script src":       {source{html: `<script defer SRC="lib.js"></script>`}, "<script src"},
		"link href":        {source{html: `<link rel="stylesheet" href="fonts/inter.css">`}, "<link href"},
		"link relatif":     {source{html: `<LINK rel=stylesheet href=style.css>`}, "<link href"},
		"image distante":   {source{html: `<img src="https://cdn.example/a.png">`}, "Adresse externe"},
		"lien externe":     {source{html: `<a href='//example.com'>site</a>`}, "Adresse externe"},
		"css distant":      {source{css: `body { background: url( "http://cdn.example/a.png" ) }`}, "Adresse externe"},
		"css import":       {source{css: `@import "theme.css";`}, "Adresse externe"},
		"form":             {source{html: `<form id="f"><input></form>`}, "submit"},
		"submit":           {source{js: `el.addEventListener('submit', save)`}, "submit"},
		"document complet": {source{html: `<html><body><p>x</p></body></html>`}, "fragment de corps"},
		"id absent":        {source{js: `document.getElementById("total").textContent = 1; document.querySelector('#chart'); document.getElementById("out")`}, "absents du HTML : chart, total ("},
		"syntaxe récente":  {source{js: `async function* pages() { yield 1 }`}, "n'a pas pu être vérifié"},
		"drapeau de regex": {source{js: `var re = /[\p{L}--[a-z]]/v`}, "n'a pas pu être vérifié"},
	} {
		s := base
		if c.s.html != "" {
			s.html = c.s.html + base.html
		}
		if c.s.css != "" {
			s.css = c.s.css
		}
		if c.s.js != "" {
			s.js = c.s.js
		}
		warnings, err := check(s)
		if err != nil {
			t.Errorf("%s : refusé (%v), avertissement attendu", label, err)
			continue
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], c.want) {
			t.Errorf("%s : avertissements %q, un seul attendu contenant %q", label, warnings, c.want)
		}
	}
}

// Ce que le vérificateur accepte de la syntaxe d'aujourd'hui : un script
// valide dans un navigateur ne doit pas être refusé.
func TestCheckModernSyntaxAccepted(t *testing.T) {
	for label, js := range map[string]string{
		"async/await":         `async function f() { await g() } const h = async () => { await 1 }; (async () => { await f() })()`,
		"chaînage optionnel":  `var a = b?.c?.[0]?.(1); delete a?.b`,
		"coalescence":         `var a = b ?? 1; a ??= 2; a ||= 3; a &&= 4`,
		"classes":             `class A { x = 1; static y = 2; #p = 3; get p() { return this.#p } static #q() {} static { A.z = 1 } static has(o) { return #p in o } }`,
		"décomposition":       `const {a = 1, b: {c} = {}, ...rest} = o; const [x, ...ys] = l; f(...ys); const p = {...o, x}`,
		"gabarits":            "var s = `a ${b} c`; tag`x`",
		"générateurs":         `function* g() { yield* h() } async function f() { for await (const x of y) {} }`,
		"nombres":             `var n = 1_000_000 + 2 ** 3 + 0o17 + 0b11 + 10n > 0`,
		"catch sans variable": `try { f() } catch { g() }`,
		"regex":               `var r = /(?<year>\d{4})-(?<m>\d\d)/u, l = /(?<=a)b/, p = /\p{L}+/u, s = /a.b/s`,
		"bibliothèque":        `[1].at(-1); "a".replaceAll("a", "b"); Object.hasOwn(a, "b"); structuredClone(a)`,
		"commentaire html":    "var a = 1 <!-- x\n",
		"script vide":         ``,
	} {
		if warnings, err := check(source{html: "<p>x</p>", js: js}); err != nil || len(warnings) != 0 {
			t.Errorf("%s : %v, avertissements %q", label, err, warnings)
		}
	}
}

func TestCheckRefusals(t *testing.T) {
	big := strings.Repeat("x", maxFile+1)
	for label, s := range map[string]source{
		"script cassé":       {html: "<p>x</p>", js: `function run( { return 1`},
		"accolade manquante": {html: "<p>x</p>", js: `document.body.onclick = () => { go()`},
		"import":             {html: "<p>x</p>", js: `import x from "./lib.js"`},
		"export":             {html: "<p>x</p>", js: `export const a = 1`},
		"await hors async":   {html: "<p>x</p>", js: `const data = await ajean.storage.get("k")`},
		"html trop gros":     {html: big},
		"css trop gros":      {html: "<p>x</p>", css: big},
		"js trop gros":       {html: "<p>x</p>", js: "//" + big},
	} {
		if _, err := check(s); err == nil {
			t.Errorf("%s : refus attendu", label)
		}
	}
	if _, err := check(source{html: strings.Repeat("x", maxFile), js: "//" + strings.Repeat("x", maxFile-2)}); err != nil {
		t.Errorf("fichiers à la borne : %v", err)
	}
}

// Les vérifications arrivent bien jusqu'au modèle, par le résultat de l'outil.
func TestCheckThroughTool(t *testing.T) {
	setup(t)
	got := call(map[string]any{"action": "save", "name": "Météo", "html": "<p id=\"temp\"></p>", "js": "fetch('https://api.example').then(r => r.json()).then(d => { document.getElementById('tmp').textContent = localStorage.x })"})
	if !tool.IsOK(got) || !strings.Contains(got, "(app:meteo)") {
		t.Fatalf("save : %s", got)
	}
	for _, want := range []string{"Avertissements", "ajean.tools.run", "ajean.storage", "absents du HTML : tmp"} {
		if !strings.Contains(got, want) {
			t.Errorf("résultat sans %q :\n%s", want, got)
		}
	}
	got = call(map[string]any{"action": "save", "id": "meteo", "js": "function ( {"})
	if !tool.IsErr(got) || !strings.Contains(got, "ne se compile pas") {
		t.Errorf("script cassé : %s", got)
	}
	if a := List()[0]; a.Version != 1 {
		t.Errorf("un enregistrement refusé a créé la version %d", a.Version)
	}
	got = call(map[string]any{"action": "edit", "id": "meteo", "file": "js", "old": "getElementById('tmp')", "new": "getElementById('temp')"})
	if !tool.IsOK(got) || strings.Contains(got, "absents du HTML") || !strings.Contains(got, "ajean.storage") {
		t.Errorf("edit : %s", got)
	}
}
