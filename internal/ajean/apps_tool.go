package ajean

// apps_tool.go — l'outil `app` du modèle : créer, modifier, relire les apps et
// leurs scripts (voir apps.go). Aucune action ne supprime une app : c'est un
// geste de l'utilisateur, dans l'écran Applications.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

func appTool() Tool {
	sp := func() map[string]any { return map[string]any{"type": "string"} }
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name: "app",
			Description: "Small html/css/js apps the user opens in Applications; no network, data from your scripts. " +
				"Call action=guide before your first create.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "enum": []string{"guide", "list", "read", "create", "update", "check", "export", "script_write", "script_edit", "script_read", "script_delete", "script_test", "data_get", "data_set"}},
					"id":     sp(), "name": sp(), "description": sp(),
					"icon": sp(),
					"html": sp(), "css": sp(), "js": sp(), "note": sp(), "part": sp(),
					"edits": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
						"part": sp(), "old": sp(), "new": sp()}}},
					"script": sp(), "key": sp(),
					"content": sp(), "from_dir": map[string]any{"type": "boolean"},
					"scripts": map[string]any{"type": "array", "description": "[{name, content, input}]"},
					"input":   map[string]any{"description": "JSON: script_test input, data_set value"},
					"steps":   map[string]any{"type": "array"},
				},
				"required": []string{"action"},
			},
		},
	}
}

// appToolLabel : « action cible » (cible = nom, sinon id, puis le script),
// le même texte pendant la frappe (appStreamLabel) et à la fin.
func appToolLabel(args map[string]any) string {
	return appLabelOf(str(args["action"]), str(args["name"]), str(args["id"]), str(args["script"]))
}

func appLabelOf(act, name, id, script string) string {
	tgt := name
	if tgt == "" {
		tgt = id
	}
	if script != "" {
		if tgt != "" {
			tgt += " / "
		}
		tgt += script
	}
	return strings.TrimSpace(act + " " + tgt)
}

// appStreamLabel : libellé pendant que le modèle écrit encore les arguments.
func appStreamLabel(raw string) string {
	return appLabelOf(previewArg(raw, "action"), previewArg(raw, "name"), previewArg(raw, "id"), previewArg(raw, "script"))
}

// appBodyKey : la partie en cours de frappe (html, css, js, script, retouche),
// c'est-à-dire la dernière clé de contenu apparue dans les arguments.
func appBodyKey(raw string) string {
	best, at := "", -1
	for _, k := range []string{"html", "css", "js", "content", "new"} {
		if i := strings.LastIndex(raw, `"`+k+`"`); i > at {
			best, at = k, i
		}
	}
	return best
}

// appDiff : ce qu'une écriture de l'outil app a changé, pour la bulle.
func appDiff(args map[string]any) (lines []DiffLine, added, removed int) {
	switch strings.ToLower(str(args["action"])) {
	case "script_write":
		lines, added = addedDiff(str(args["content"]))
	case "create", "update", "script_edit":
		for _, k := range []string{"html", "css", "js"} {
			if v := str(args[k]); v != "" {
				l, a := addedDiff(v)
				lines, added = append(lines, l...), added+a
			}
		}
		if eds, ok := args["edits"].([]any); ok {
			for _, e := range eds {
				m, _ := e.(map[string]any)
				l, a, r := lineDiff(str(m["old"]), str(m["new"]))
				lines, added, removed = append(lines, l...), added+a, removed+r
			}
		}
	}
	return capLines(lines), added, removed
}

func appGuide() string {
	py, ps := "python3", "bash (.sh) or python3"
	if runtime.GOOS == "windows" {
		py, ps = "python", "PowerShell (.ps1) or python"
	}
	return `# AJEAN apps: how to build one

An app = 3 parts (html, css, js) + optional scripts. It opens full screen inside AJEAN, in a sandboxed frame with NO network, NO cookies, NO localStorage, NO popups. Everything goes through the bridge window.ajean (promises).

## Parts
- html: the BODY content only. Forbidden: <script>, <meta>, <base>, <iframe>, on…= attributes (onclick…). Put styles in css; style="" and el.style are allowed for dynamic values.
- css: your styles. A small kit is already loaded (system font, light/dark follows AJEAN's theme, CSS vars --bg --fg --muted --line --accent --card, classes .btn .btn.primary .card .row .muted, inputs/tables styled). Keep it monochrome and sober, no borders everywhere.
- js: plain modern JS (no import, no external library, no CDN). Wire events with addEventListener. Handle <form> submit in JS (e.preventDefault()).
- UI text in the user's language, short and serious. Must work on a phone (fluid layout, inputs 16px, touch targets 44px).

## Bridge (all return promises; failures reject with an Error that has .code)
- ajean.scripts.run(name, input, {timeout}) → {ok, exit, data, stdout, stderr, error, ms}. Runs scripts/<name> of THIS app on the user's machine. input (JSON, 4 MB max) arrives on stdin. data = stdout parsed as JSON when stdout is JSON. Check ok AND data.ok before using data. timeout in seconds: 60 by default, 300 max.
- ajean.scripts.list() → [{name, size}]
- ajean.files: the app's own files (photos, PDFs, exports), 50 MB per file, 500 MB per app, kept between openings. Its scripts see the same files in the folder given by env AJEAN_APP_FILES (read and write them there).
  - write(name, data) → {name, size}; data = Blob/File (e.g. from <input type=file> or canvas.toBlob), ArrayBuffer, typed array, data URL or text. name: no folder, e.g. "page-1.jpg".
  - read(name, {as: 'blob'|'dataurl'|'text'}) → Blob by default (URL.createObjectURL works for <img>). list() → [{name, size, modified}]. delete(name).
  - download(name): the user's browser downloads the file. share(name, {title, text}): native share sheet on phones (mail, messaging…), download when not available. Call them from a click.
  - Big data (images, documents) goes through files, NEVER as base64 inside scripts.run input or stdout: write the files, pass their names in the input, let the script write its result file, then read/download/share it.
- ajean.storage.get(key) / set(key, value) / delete(key) / list(prefix): small JSON memory kept between openings (1 MB max). Not encrypted: no secrets.
- ajean.ai.complete({system, prompt} or {system, messages:[{role, content}]}, max_tokens) → {text}. Asks the active model, without tools. Slow on local models: show a loading state.
- ajean.ui.notify(text): small message shown by AJEAN.
- ajean.ui.openUrl(url): opens an http(s) link in a new tab after the user confirms. Plain <a href> links and <a download> do NOT work (sandbox): use openUrl and files.download.
- ajean.app → {id, name, version}

## Browser capabilities inside an app
- Photos: <input type="file" accept="image/*" capture="environment"> opens the camera on a phone and works everywhere: use it as the main way. Live camera (navigator.mediaDevices.getUserMedia) is allowed but only when AJEAN is opened in https (app.ajean.link), not on a local http address: offer it as an extra, with the file input as fallback.
- Canvas, image processing, Blob, FileReader, TextEncoder work. No network at all (fetch, images or fonts from the web, WebSocket): everything external goes through a script.
- Limits: html+css+js 256 KB per version; storage 1 MB; files as above.

## Scripts = how an app gets data
Pass them in create/update as scripts:[{name, content, input}] (or script_write later): the harness writes AND runs each one with its input and shows you the result. Rules:
- Read the input from stdin (JSON), print ONE JSON value on stdout, nothing else (logs go to stderr). Exit code 0 on success.
- Working dir = the app's data/ folder (env AJEAN_APP_DATA): keep files the script needs there. The apps folder is closed to bash/write: only the app's own scripts touch data/.
- Never put a password, token or API key in a script: the app is not the place for secrets. If a source needs one, tell the user instead of copying it in.
- Host: ` + runtime.GOOS + `. Use ` + ps + ` (interpreter: ` + py + ` for .py, node for .js). A library is fine if it is installed: make the script report what it needs (e.g. try import, put the version in its JSON) and the harness run shows you right away.
- 60 s per run by default (scripts.run timeout up to 300 s), 1 MB of stdout. Results that are files (PDF, images) go in AJEAN_APP_FILES, not on stdout.
- Credentials (mail, API keys): never copy them into a script or the app. A script may read a config file that already exists on the machine (say which one in the app), or one in data/ that the user fills.
- Scripts run with the user's rights: read-only by default; a script that sends (mail, message), writes outside the app or deletes must only do what its name says, and the app must show what will happen and get a click to confirm first.
- To test a script that needs files, write test files with bash in a temporary folder and pass their paths in the input; the script may also accept file names relative to AJEAN_APP_FILES.

## Workflow (the harness does the testing: keep your calls few)
1. ONE create call with name, description, icon (ONE emoji), html, css, js AND scripts. The harness writes the scripts, runs them, checks the JS syntax and the ids used by the js, then opens the app in a headless browser with the real scripts. It reports script results, JS errors, bridge calls, visible text and phone overflow.
2. If the report shows a problem, fix it with update (edits = exact excerpt → replacement, or whole parts, plus a note): the same checks run again. When the report is clean, stop and tell the user the app is ready (a card with an Open button appears in the chat).
3. Do NOT build test pages, mock bridges, servers or browser sessions yourself, and do NOT read back the app you just wrote. check (id) re-runs the render and adds a screenshot when you need to see it. To test it like a user, add steps (click, type+text, select+value, key, wait): they run in order, then you get the report and the screenshot of the result.
4. Later changes: update with edits. Scripts: script_edit (script, edits [{old, new}]) instead of rewriting them. If the user reports an error, read the part concerned (part=js…) and fix it.
5. Deliver first, refine after. Aim for a first version the user can try within a few calls, then improve from what THEY report. Prefer a simple feature that always works over a clever one that must be tuned: e.g. for a document scanner, let the user drag the 4 corners on the photo (with a rough automatic guess at most) instead of perfecting automatic edge detection. Never build test datasets, benchmarks or long tuning loops on your own: if something is hard, ship the simple version and say what could come next.
6. Big apps (js over ~20 KB) or JS you want to test with node: export (id) copies the app to a work folder; edit the files there with file, test, then update with id, from_dir=true and a note. The harness takes the files, re-runs changed scripts and re-checks the app. Do not paste a whole big part again in update.
7. The app's saved data (what ajean.storage holds: agenda entries, lists…) is yours to read and fill: data_get (id, optional key) shows it, data_set (id, key, input = the JSON value, null deletes the key) writes it. To add or fix entries, read the key, change the value and write it back. Never change the code or add a seed just to put data in, and never look for the data on disk.`
}

// toolApp exécute l'outil `app`.
func toolApp(ctx context.Context, args map[string]any) string {
	act := strings.ToLower(strings.TrimSpace(str(args["action"])))
	id := strings.TrimSpace(str(args["id"]))
	errf := func(e error) string { return "[erreur] " + e.Error() }
	// Les scripts du create s'écrivent {name, content} : le modèle réutilise
	// volontiers name pour script_* au lieu de script. On l'accepte.
	if strings.HasPrefix(act, "script_") && str(args["script"]) == "" && appScriptRe.MatchString(str(args["name"])) {
		args["script"] = args["name"]
	}
	switch act {
	case "guide":
		return appGuide()
	case "list":
		apps := listApps()
		if len(apps) == 0 {
			return "Aucune app pour l'instant."
		}
		var b strings.Builder
		for _, m := range apps {
			sc := []string{}
			for _, s := range listAppScripts(m.ID) {
				sc = append(sc, s.Name)
			}
			fmt.Fprintf(&b, "- %s %s (id %s, v%d) : %s", m.Icon, m.Name, m.ID, m.Version, m.Description)
			if len(sc) > 0 {
				fmt.Fprintf(&b, " · scripts : %s", strings.Join(sc, ", "))
			}
			b.WriteString("\n")
		}
		return b.String()
	case "read":
		m, err := readAppMeta(id)
		if err != nil {
			return errf(err)
		}
		c, err := readAppCode(id, m.Version)
		if err != nil {
			return errf(err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "App %s « %s » version %d\n", m.ID, m.Name, m.Version)
		part := strings.ToLower(str(args["part"]))
		for _, p := range []struct{ k, v string }{{"html", c.HTML}, {"css", c.CSS}, {"js", c.JS}} {
			if part != "" && part != p.k {
				continue
			}
			fmt.Fprintf(&b, "\n=== %s ===\n%s\n", p.k, p.v)
		}
		if part == "" {
			sc := listAppScripts(id)
			if len(sc) > 0 {
				b.WriteString("\n=== scripts ===\n")
				for _, s := range sc {
					fmt.Fprintf(&b, "- %s (%d o) — relire avec script_read\n", s.Name, s.Size)
				}
			}
		}
		return b.String()
	case "create":
		c := &AppCode{HTML: str(args["html"]), CSS: str(args["css"]), JS: str(args["js"])}
		m, err := createApp(str(args["name"]), str(args["description"]), str(args["icon"]), c, "création")
		if err != nil {
			return errf(err)
		}
		head := fmt.Sprintf("App « %s » créée : version %d (id %s). [app:%s:%d]", m.Name, m.Version, m.ID, m.ID, m.Version)
		return head + appAfterWrite(ctx, m.ID, args, c) + appDeliverNudge(m.ID)
	case "update":
		if _, ok := args["name"]; ok {
			n, d, ic := str(args["name"]), str(args["description"]), str(args["icon"])
			var dp, ip *string
			if _, ok := args["description"]; ok {
				dp = &d
			}
			if _, ok := args["icon"]; ok {
				ip = &ic
			}
			if _, err := setAppMeta(id, &n, dp, ip); err != nil {
				return errf(err)
			}
		} else if _, ok := args["icon"]; ok {
			ic := str(args["icon"])
			if _, err := setAppMeta(id, nil, nil, &ic); err != nil {
				return errf(err)
			}
		}
		var p AppPatch
		// from_dir : les parties viennent du dossier de travail (action export),
		// où l'IA a édité et testé le code avec file et bash. Pas de 60 Ko de JS
		// dans les arguments (lent, et parfois tronqué).
		if b, _ := args["from_dir"].(bool); b {
			dir := appDevDir(ctx, id)
			for _, f := range []string{"index.html", "style.css", "app.js"} {
				raw, err := os.ReadFile(filepath.Join(dir, f))
				if err != nil {
					continue
				}
				v := string(raw)
				switch f {
				case "index.html":
					p.HTML = &v
				case "style.css":
					p.CSS = &v
				case "app.js":
					p.JS = &v
				}
			}
			if p.HTML == nil && p.CSS == nil && p.JS == nil {
				return errf(fmt.Errorf("dossier %s vide ou absent : fais d'abord action=export", dir))
			}
			// Scripts du dossier qui diffèrent de ceux de l'app : réécrits et lancés.
			ents, _ := os.ReadDir(filepath.Join(dir, "scripts"))
			var sc []any
			for _, e := range ents {
				src, err := os.ReadFile(filepath.Join(dir, "scripts", e.Name()))
				if err != nil || e.IsDir() {
					continue
				}
				if cur, err := appScriptPath(id, e.Name()); err == nil {
					if old, err := os.ReadFile(cur); err == nil && string(old) == string(src) {
						continue
					}
				}
				sc = append(sc, map[string]any{"name": e.Name(), "content": string(src)})
			}
			if len(sc) > 0 {
				args["scripts"] = sc
			}
		}
		for _, k := range []string{"html", "css", "js"} {
			if v, ok := args[k].(string); ok {
				v := v
				switch k {
				case "html":
					p.HTML = &v
				case "css":
					p.CSS = &v
				case "js":
					p.JS = &v
				}
			}
		}
		if raw, ok := args["edits"]; ok {
			b, _ := json.Marshal(raw)
			if err := json.Unmarshal(b, &p.Edits); err != nil {
				return errf(fmt.Errorf("edits invalide : %v", err))
			}
		}
		before, err := readAppMeta(id)
		if err != nil {
			return errf(err)
		}
		m, err := updateApp(id, p, str(args["note"]))
		if err != nil {
			return errf(err)
		}
		_, hasScripts := args["scripts"]
		if m.Version == before.Version && !hasScripts {
			return fmt.Sprintf("App « %s » : nom/description/icône mis à jour, code inchangé (version %d).", m.Name, m.Version)
		}
		c, _ := readAppCode(m.ID, m.Version)
		head := fmt.Sprintf("App « %s » modifiée : version %d (id %s). [app:%s:%d]", m.Name, m.Version, m.ID, m.ID, m.Version)
		if m.Version == before.Version {
			head = fmt.Sprintf("App « %s » : scripts mis à jour, code inchangé (version %d).", m.Name, m.Version)
		}
		return head + appAfterWrite(ctx, m.ID, args, c) + appDeliverNudge(m.ID)
	case "export":
		m, err := readAppMeta(id)
		if err != nil {
			return errf(err)
		}
		c, err := readAppCode(id, m.Version)
		if err != nil {
			return errf(err)
		}
		dir := appDevDir(ctx, id)
		if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
			return errf(err)
		}
		for f, v := range map[string]string{"index.html": c.HTML, "style.css": c.CSS, "app.js": c.JS} {
			if err := os.WriteFile(filepath.Join(dir, f), []byte(v), 0o644); err != nil {
				return errf(err)
			}
		}
		for _, s := range listAppScripts(id) {
			if p, err := appScriptPath(id, s.Name); err == nil {
				if b, err := os.ReadFile(p); err == nil {
					_ = os.WriteFile(filepath.Join(dir, "scripts", s.Name), b, 0o755)
				}
			}
		}
		return fmt.Sprintf("[ok] version %d de l'app copiée dans %s (index.html, style.css, app.js, scripts/). Édite ces fichiers avec file, teste des fonctions du JS avec node si utile, puis action=update, id=%s, from_dir=true, note=… : le harnais reprend les fichiers, relance les scripts modifiés et revérifie l'app.", m.Version, dir, id)
	case "script_edit":
		name := strings.TrimSpace(str(args["script"]))
		p, err := appScriptPath(id, name)
		if err != nil {
			return errf(err)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return errf(fmt.Errorf("script introuvable : %s", name))
		}
		src := string(raw)
		eds := []AppEdit{}
		if raw, ok := args["edits"]; ok {
			b, _ := json.Marshal(raw)
			_ = json.Unmarshal(b, &eds)
		}
		if o := str(args["old"]); o != "" {
			eds = append(eds, AppEdit{Old: o, New: str(args["new"])})
		}
		if len(eds) == 0 {
			return errf(fmt.Errorf("donne old et new (ou edits [{old, new}])"))
		}
		for i, e := range eds {
			switch strings.Count(src, e.Old) {
			case 0:
				return errf(fmt.Errorf("retouche %d : extrait introuvable dans %s.%s", i+1, name, appNearHint(src, e.Old)))
			case 1:
				src = strings.Replace(src, e.Old, e.New, 1)
			default:
				return errf(fmt.Errorf("retouche %d : extrait présent plusieurs fois dans %s, donne un extrait plus long", i+1, name))
			}
		}
		if err := writeAppScript(id, name, src); err != nil {
			return errf(err)
		}
		r := runAppScript(ctx, id, name, args["input"], appScriptTO)
		return fmt.Sprintf("[ok] script %s retouché et lancé :\n", name) + appScriptReport(r)
	case "script_write":
		name := strings.TrimSpace(str(args["script"]))
		if err := writeAppScript(id, name, str(args["content"])); err != nil {
			return errf(err)
		}
		r := runAppScript(ctx, id, name, args["input"], appScriptTO)
		return fmt.Sprintf("[ok] script %s écrit et lancé :\n", name) + appScriptReport(r)
	case "script_read":
		p, err := appScriptPath(id, strings.TrimSpace(str(args["script"])))
		if err != nil {
			return errf(err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return errf(fmt.Errorf("script introuvable"))
		}
		return string(b)
	case "script_delete":
		if err := deleteAppScript(id, strings.TrimSpace(str(args["script"]))); err != nil {
			return errf(err)
		}
		return "[ok] script supprimé"
	case "script_test":
		r := runAppScript(ctx, id, strings.TrimSpace(str(args["script"])), args["input"], appScriptTO)
		return appScriptReport(r)
	case "data_get":
		return appDataGet(id, strings.TrimSpace(str(args["key"])))
	case "data_set":
		return appDataSet(id, strings.TrimSpace(str(args["key"])), args)
	}
	return "[erreur] action inconnue : " + act + " (guide, list, read, create, update, check, export, script_write, script_edit, script_read, script_delete, script_test, data_get, data_set)"
}

// appScriptReport : résultat d'un script, court et lisible pour l'IA.
func appScriptReport(r AppRunResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ok: %v · exit: %d · %d ms\n", r.OK, r.Exit, r.Ms)
	if r.Error != "" {
		fmt.Fprintf(&b, "erreur: %s\n", r.Error)
	}
	if r.Data != nil {
		b.WriteString("stdout est du JSON valide (data).\n")
		// Le script a tourné mais dit lui-même avoir échoué : à signaler, l'IA
		// lisait « ok: true » et passait à la suite.
		if m, ok := r.Data.(map[string]any); ok {
			if v, has := m["ok"]; has && v == false {
				b.WriteString("⚠ le script répond ok:false : sa source de données a échoué.\n")
			} else if m["error"] != nil || m["erreur"] != nil {
				b.WriteString("⚠ le script renvoie une erreur dans ses données.\n")
			}
		}
	} else if strings.TrimSpace(r.Stdout) != "" {
		b.WriteString("⚠ stdout n'est PAS un JSON valide : l'app recevra data vide.\n")
	}
	if out := strings.TrimSpace(r.Stdout); out != "" {
		fmt.Fprintf(&b, "stdout:\n%s\n", clipRunes(out, 2000))
	}
	if r.Stderr != "" {
		fmt.Fprintf(&b, "stderr:\n%s\n", clipRunes(strings.TrimSpace(r.Stderr), 1500))
	}
	return b.String()
}

// appAfterWrite : après create/update, le harnais fait le travail de test que
// l'IA faisait à la main. Il écrit et lance les scripts passés dans le même
// appel, contrôle le JS, puis rend l'app dans un Chrome headless avec ses vrais
// scripts. Le rapport tient en quelques lignes.
func appAfterWrite(ctx context.Context, id string, args map[string]any, c *AppCode) string {
	var b strings.Builder
	if list, ok := args["scripts"].([]any); ok {
		for _, it := range list {
			sc, _ := it.(map[string]any)
			name := strings.TrimSpace(str(sc["name"]))
			if err := writeAppScript(id, name, str(sc["content"])); err != nil {
				fmt.Fprintf(&b, "\n\nScript %s : [erreur] %v", name, err)
				continue
			}
			fmt.Fprintf(&b, "\n\nScript %s écrit et lancé : %s", name, appScriptReport(runAppScript(ctx, id, name, sc["input"], appScriptTO)))
		}
	}
	if c != nil {
		if w := appStaticCheck(c); len(w) > 0 {
			b.WriteString("\n\nContrôle du code :\n- " + strings.Join(w, "\n- "))
		}
	}
	if chromePath() == "" {
		b.WriteString("\n\n(Pas de navigateur sur cette machine : rendu non vérifié.)")
		return b.String()
	}
	r, err := appRender(ctx, id, 0, false)
	if err != nil {
		fmt.Fprintf(&b, "\n\nRendu non vérifié : %v", err)
		return b.String()
	}
	b.WriteString("\n\n" + appCheckReport(r))
	return b.String()
}

// appDevDir : dossier de travail d'une app (action export), dans le workspace
// de l'espace courant. Jetable : la vraie app reste dans apps/.
func appDevDir(ctx context.Context, id string) string {
	return filepath.Join(spaceWorkspace(ctx), "apps-dev", id)
}

// appWrites retient l'heure des dernières versions de chaque app, pour
// appDeliverNudge.
var appWrites = struct {
	mu sync.Mutex
	at map[string][]time.Time
}{at: map[string][]time.Time{}}

// appDeliverNudge : vécu sur un scanner de documents, l'IA a produit version
// sur version pendant des heures (en peaufinant un algorithme) sans jamais
// montrer l'app. Au-delà de 4 versions en une heure, le résultat de l'outil le
// lui rappelle.
func appDeliverNudge(id string) string {
	appWrites.mu.Lock()
	defer appWrites.mu.Unlock()
	now := time.Now()
	keep := []time.Time{now}
	for _, t := range appWrites.at[id] {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	appWrites.at[id] = keep
	if len(keep) < 5 {
		return ""
	}
	return fmt.Sprintf("\n\n⚠ %d versions de cette app en moins d'une heure. Arrête ici : si l'app fonctionne, présente-la à l'utilisateur et demande-lui de l'essayer. Améliore ensuite d'après ce qu'il constate, pas avant.", len(keep))
}

// appDataMax borne ce que data_get renvoie d'un coup au modèle.
const appDataMax = 12000

// appDataGet lit les données enregistrées de l'app (ajean.storage) : une clé,
// ou toutes si elles tiennent, sinon la liste des clés et leur taille.
func appDataGet(id, key string) string {
	if _, err := readAppMeta(id); err != nil {
		return "[erreur] " + err.Error()
	}
	m, _ := readAppStorage(id)
	if key != "" {
		v, ok := m[key]
		if !ok {
			return "[ok] clé « " + key + " » absente"
		}
		return "[ok] " + key + " = " + clipRunes(string(v), appDataMax)
	}
	if len(m) == 0 {
		return "[ok] aucune donnée enregistrée pour cette app"
	}
	all, _ := json.Marshal(m)
	if len(all) <= appDataMax {
		return "[ok] " + string(all)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("[ok] données trop grandes pour tout lire ; clés (taille) :\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s (%d o)\n", k, len(m[k]))
	}
	b.WriteString("Lis une clé avec data_get + key.")
	return b.String()
}

// appDataSet écrit une clé (input = la valeur JSON) ; input null = suppression.
func appDataSet(id, key string, args map[string]any) string {
	v, has := args["input"]
	if !has {
		return "[erreur] data_set : la valeur va dans input (null pour supprimer la clé)"
	}
	if v == nil {
		had, err := appStorageOp(id, "delete", key, nil, "")
		if err != nil {
			return "[erreur] " + err.Error()
		}
		if had == true {
			return "[ok] clé « " + key + " » supprimée"
		}
		return "[ok] clé « " + key + " » déjà absente"
	}
	// Valeur passée en texte JSON (« {"events":[…]} ») : on la prend telle quelle.
	var raw json.RawMessage
	if sv, ok := v.(string); ok && (strings.HasPrefix(strings.TrimSpace(sv), "{") || strings.HasPrefix(strings.TrimSpace(sv), "[")) && json.Valid([]byte(sv)) {
		raw = json.RawMessage(sv)
	} else {
		raw, _ = json.Marshal(v)
	}
	if _, err := appStorageOp(id, "set", key, raw, ""); err != nil {
		return "[erreur] " + err.Error()
	}
	return fmt.Sprintf("[ok] clé « %s » enregistrée (%d o). L'app la lit à sa prochaine ouverture ; déjà ouverte, il faut la rouvrir.", key, len(raw))
}
