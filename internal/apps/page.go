package apps

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
)

// policy : ce que le document d'une app a le droit de charger. Rien ne vient
// du réseau : scripts et styles en ligne, images et médias embarqués. C'est
// elle qui empêche une app de joindre Internet ou l'API d'AJEAN.
const policy = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
	"img-src data: blob:; media-src data: blob:; font-src data:; form-action 'none'; base-uri 'none'"

// baseCSS : un point de départ sobre, que la feuille de l'app redéfinit à sa guise.
const baseCSS = `:root { color-scheme: light dark; }
*, *::before, *::after { box-sizing: border-box; }
html, body { margin: 0; padding: 0; }
body { font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; line-height: 1.5; -webkit-text-size-adjust: 100%; }
button, input, select, textarea { font: inherit; }`

// bridgeJS installe window.ajean dans le document de l'app, puis lance son
// script. Chaque demande part vers la page d'AJEAN (parent.postMessage) avec
// un numéro, et la promesse rendue se dénoue quand la réponse portant ce
// numéro revient. Le texte se termine sur un appel ouvert : Page y ajoute le
// script de l'app, en chaîne JSON.
const bridgeJS = `(function (source) {
	"use strict";
	var pending = {}, last = 0, reported = 0;

	function call(op, args) {
		return new Promise(function (resolve, reject) {
			var id = ++last;
			pending[id] = { resolve: resolve, reject: reject };
			try {
				// Aller-retour par JSON : seules des données franchissent le pont.
				parent.postMessage({ ajean: 1, id: id, op: op, args: JSON.parse(JSON.stringify(args)) }, "*");
			} catch (e) {
				delete pending[id];
				reject(e);
			}
		});
	}

	window.addEventListener("message", function (event) {
		var m = event.data;
		if (event.source !== parent || !m || m.ajean !== 1 || !pending[m.id]) return;
		var p = pending[m.id];
		delete pending[m.id];
		if (m.ok) p.resolve(m.result);
		else p.reject(new Error(m.error || "ajean: request failed"));
	});

	function text(v) {
		if (v instanceof Error) return v.message;
		if (v && typeof v === "object") {
			try { return JSON.stringify(v); } catch (e) {}
		}
		return String(v);
	}

	// Borné par ouverture : une erreur dans une boucle d'animation ne doit pas
	// inonder la page d'AJEAN.
	function report(message) {
		if (++reported > 20) return;
		try {
			parent.postMessage({ ajean: 1, id: 0, op: "error", args: { message: String(message).slice(0, 2000) } }, "*");
		} catch (e) {}
	}

	window.addEventListener("error", function (e) {
		report((e.message || "Script error") + (e.lineno ? " (app.js line " + e.lineno + ")" : ""));
	});
	window.addEventListener("unhandledrejection", function (e) {
		report("Unhandled promise rejection: " + text(e.reason));
	});
	document.addEventListener("securitypolicyviolation", function (e) {
		report("Blocked by the sandbox (" + e.violatedDirective + "): " + (e.blockedURI || "inline code"));
	});
	var consoleError = console.error;
	console.error = function () {
		report("console.error: " + Array.prototype.map.call(arguments, text).join(" "));
		return consoleError.apply(console, arguments);
	};

	window.ajean = Object.freeze({
		storage: Object.freeze({
			get: function (key) { return call("storage.get", { key: key }); },
			set: function (key, value) { return call("storage.set", { key: key, value: value }); },
			remove: function (key) { return call("storage.remove", { key: key }); },
			keys: function () { return call("storage.keys", {}); }
		}),
		tools: Object.freeze({
			run: function (name, args) { return call("tools.run", { name: name, args: args || {} }); }
		}),
		ai: Object.freeze({
			ask: function (prompt) { return call("ai.ask", { prompt: prompt }); }
		})
	});

	// Le script de l'app est lancé dès que le corps est entièrement lu, et
	// avant DOMContentLoaded : une app qui attend cet événement le reçoit.
	// Posé par textContent, il ne peut ni fermer une balise ni déborder sur le
	// reste du document, quoi qu'il contienne.
	var started = false;
	document.addEventListener("readystatechange", function () {
		if (started) return;
		started = true;
		var script = document.createElement("script");
		script.textContent = source;
		document.body.appendChild(script);
	});
})(`

var (
	bodyOpenRe  = regexp.MustCompile(`(?i)<body\b[^>]*>`)
	bodyCloseRe = regexp.MustCompile(`(?i)</body\s*>`)
	headCloseRe = regexp.MustCompile(`(?i)</head\s*>`)
	styleRe     = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	styleEndRe  = regexp.MustCompile(`(?i)</(style)`)
)

// fragment réduit un document complet à ce qui se place dans le corps : le
// contenu de <body> et les feuilles de style de <head>. Un fragment est rendu
// tel quel.
func fragment(doc string) (styles, body string) {
	cut := bodyOpenRe.FindStringIndex(doc)
	if cut == nil {
		cut = headCloseRe.FindStringIndex(doc)
	}
	if cut == nil {
		return "", doc
	}
	head, body := doc[:cut[0]], doc[cut[1]:]
	if ends := bodyCloseRe.FindAllStringIndex(body, -1); ends != nil {
		body = body[:ends[len(ends)-1][0]]
	}
	return strings.Join(styleRe.FindAllString(head, -1), "\n"), strings.TrimSpace(body)
}

// document assemble la page d'une app. Le pont est dans l'en-tête, avant tout
// ce qu'a écrit l'assistant : une balise mal fermée dans le corps ne peut pas
// l'emporter avec elle.
func document(title string, s source) (string, error) {
	// json.Marshal échappe « < », « > » et les séparateurs de ligne Unicode :
	// la chaîne obtenue est un littéral JavaScript sûr au milieu d'une balise.
	js, err := json.Marshal(s.js)
	if err != nil {
		return "", err
	}
	styles, body := fragment(s.html)
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<meta http-equiv=\"Content-Security-Policy\" content=\"" + policy + "\">\n")
	b.WriteString("<title>" + html.EscapeString(title) + "</title>\n")
	b.WriteString("<style>\n" + baseCSS + "\n</style>\n")
	// « <\/style » veut dire la même chose pour une feuille de style, mais ne
	// ferme pas la balise.
	b.WriteString("<style>\n" + styleEndRe.ReplaceAllString(s.css, `<\/$1`) + "\n</style>\n")
	if styles != "" {
		b.WriteString(styles + "\n")
	}
	b.WriteString("<script>\n" + bridgeJS + string(js) + ");\n</script>\n")
	b.WriteString("</head>\n<body>\n" + body + "\n</body>\n</html>\n")
	return b.String(), nil
}

// Page : une app et le document complet à mettre dans son iframe (srcdoc).
func Page(id string) (app App, html string, err error) {
	mu.Lock()
	defer mu.Unlock()
	m, err := load(id)
	if err != nil {
		return app, "", err
	}
	s, err := readSource(id, m.Version)
	if err != nil {
		return app, "", err
	}
	html, err = document(m.Name, s)
	if err != nil {
		return app, "", err
	}
	return m.app(id), html, nil
}
