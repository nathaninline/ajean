package ajean

// apps_check.go — vérification d'une app par le HARNAIS, pour que l'IA n'ait pas
// à bricoler elle-même une page de test (faux pont, serveur http, navigateur) :
//
//   - contrôles statiques à chaque create/update : syntaxe JS (node --check si
//     présent), éléments visés par id absents du html ;
//   - rendu réel (action check, et automatiquement après create/update quand un
//     Chrome est disponible) : l'app tourne dans un Chrome headless, avec un pont
//     qui lance ses VRAIS scripts ; on rend les erreurs JS, les appels du pont,
//     le texte visible, un débordement sur téléphone et, sur demande, une capture.
//
// Pendant la vérification, storage est une COPIE en mémoire (rien n'est écrit)
// et ai.complete répond un texte d'exemple : le moteur est déjà occupé par le
// tour de l'IA qui a demandé la vérification.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// appKitCSS : styles de base injectés avant le CSS de l'app (même kit pour
// l'ouverture dans AJEAN et pour la vérification).
const appKitCSS = `:root{--bg:#fff;--fg:#111;--muted:#6b6b6b;--line:#e6e6e6;--card:#f6f6f6;--accent:#111;--accent-fg:#fff;color-scheme:light}
:root[data-theme=dark]{--bg:#0b0b0c;--fg:#f2f2f4;--muted:#a0a0a8;--line:#2a2a2e;--card:#18181a;--accent:#f5f5f5;--accent-fg:#111;color-scheme:dark}
*{box-sizing:border-box}html,body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
body{padding:16px;max-width:1100px;margin:0 auto}h1,h2,h3{line-height:1.25;margin:.2em 0 .5em}
.muted{color:var(--muted)}.row{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
.card{background:var(--card);border-radius:12px;padding:14px;margin:10px 0}
button,.btn{font:inherit;border:0;border-radius:10px;padding:9px 14px;min-height:40px;background:var(--card);color:var(--fg);cursor:pointer}
button.primary,.btn.primary{background:var(--accent);color:var(--accent-fg)}button:disabled{opacity:.5;cursor:default}
input,select,textarea{font:inherit;font-size:16px;color:var(--fg);background:var(--card);border:1px solid var(--line);border-radius:10px;padding:8px 10px;max-width:100%}
table{border-collapse:collapse;width:100%}th,td{text-align:left;padding:7px 8px;border-bottom:1px solid var(--line)}th{color:var(--muted);font-weight:600;font-size:13px}
@media (max-width:600px){body{padding:12px}table{display:block;overflow-x:auto}}`

// ---------- contrôles statiques ----------

var (
	appJSIDRe   = regexp.MustCompile(`getElementById\(\s*['"]([A-Za-z][\w-]*)['"]\s*\)|querySelector(?:All)?\(\s*['"]#([A-Za-z][\w-]*)['"]\s*\)`)
	appHTMLIDRe = regexp.MustCompile(`\bid\s*=\s*["']([^"']+)["']`)
	appJSNewID  = regexp.MustCompile(`\.id\s*=\s*['"]([A-Za-z][\w-]*)['"]|id\s*=\s*\\?["']([A-Za-z][\w-]*)\\?["']`)
)

// appStaticCheck rend des avertissements lisibles (vide si rien à signaler).
func appStaticCheck(c *AppCode) []string {
	var out []string
	if msg := appJSSyntax(c.JS); msg != "" {
		out = append(out, "syntaxe JS : "+msg)
	}
	have := map[string]bool{}
	for _, m := range appHTMLIDRe.FindAllStringSubmatch(c.HTML, -1) {
		have[m[1]] = true
	}
	// Ids créés par le JS lui-même (innerHTML, el.id = …) : pas des oublis.
	for _, m := range appJSNewID.FindAllStringSubmatch(c.JS, -1) {
		have[m[1]+m[2]] = true
	}
	missing := map[string]bool{}
	for _, m := range appJSIDRe.FindAllStringSubmatch(c.JS, -1) {
		id := m[1] + m[2]
		if !have[id] {
			missing[id] = true
		}
	}
	if len(missing) > 0 {
		ids := make([]string, 0, len(missing))
		for id := range missing {
			ids = append(ids, "#"+id)
		}
		sort.Strings(ids)
		out = append(out, "le JS vise des éléments absents du html : "+strings.Join(ids, ", "))
	}
	return out
}

// appJSSyntax : `node --check` sur le JS de l'app, si node est installé.
func appJSSyntax(js string) string {
	if strings.TrimSpace(js) == "" {
		return ""
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return ""
	}
	f, err := os.CreateTemp("", "ajean-appjs-*.js")
	if err != nil {
		return ""
	}
	defer os.Remove(f.Name())
	f.WriteString(js)
	f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := hideCmd(exec.CommandContext(ctx, node, "--check", f.Name())).CombinedOutput()
	if err == nil {
		return ""
	}
	// Garde « fichier:ligne », la ligne fautive et le message, sans le chemin temporaire.
	s := strings.ReplaceAll(string(out), f.Name(), "app.js")
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "at ") || strings.HasPrefix(l, "Node.js") {
			continue
		}
		if strings.TrimSpace(l) != "" {
			keep = append(keep, l)
		}
	}
	return clipRunes(strings.Join(keep, "\n"), 600)
}

// ---------- rendu headless ----------

// Pont de la vérification : même API que dans AJEAN, transport HTTP vers le
// serveur local temporaire (au lieu du MessageChannel de la page).
const appCheckBridgeJS = `(function(){
  window.__errs=[];
  function report(kind,msg,line){ if(window.__errs.length<10) window.__errs.push({kind:kind,message:String(msg||'').slice(0,300),line:line||0}); }
  window.addEventListener('error',function(e){ report(e.error&&e.error.name||'Error',e.message,e.lineno); });
  window.addEventListener('unhandledrejection',function(e){ var r=e.reason; report(r&&r.name||'Error',r&&r.message||r,0); });
  document.addEventListener('securitypolicyviolation',function(e){ if(String(e.blockedURI).indexOf('/call')<0) report('Sandbox','bloqué par le bac à sable (pas de réseau dans une app) : '+e.violatedDirective+' '+(e.blockedURI||''),e.lineNumber); });
  function call(method,args){ return fetch('/call',{method:'POST',body:JSON.stringify({method:method,args:args})}).then(function(r){return r.json();}).then(function(d){
    if(d.ok) return d.value; var er=new Error(d.error||'erreur'); er.code=d.code||'error'; throw er; }); }
  window.ajean={ app: __APP__,
    scripts:{ run:function(n,i,o){ return call('scripts.run',{name:n,input:i===undefined?null:i,timeout:o&&o.timeout}); }, list:function(){ return call('scripts.list',{}); } },
    files:{ write:function(n,d){ var sz=(d&&(d.size||d.byteLength||d.length))||0; return call('files.write',{name:n,size:sz}); },
      read:function(n,o){ return call('files.read',{name:n}).then(function(u){ var as=(o&&o.as)||'blob'; if(as==='dataurl') return u;
        var i=u.indexOf(','), bin=atob(u.slice(i+1)), a=new Uint8Array(bin.length); for(var k=0;k<bin.length;k++) a[k]=bin.charCodeAt(k);
        return as==='text' ? new TextDecoder().decode(a) : new Blob([a]); }); },
      list:function(){ return call('files.list',{}); }, delete:function(n){ return call('files.delete',{name:n}); },
      download:function(n){ return call('files.download',{name:n}); }, share:function(n){ return call('files.share',{name:n}); } },
    storage:{ get:function(k){ return call('storage.get',{key:k}); }, set:function(k,v){ return call('storage.set',{key:k,value:v}); },
      delete:function(k){ return call('storage.delete',{key:k}); }, list:function(p){ return call('storage.list',{prefix:p||''}); } },
    ai:{ complete:function(o){ return call('ai.complete',o||{}); } },
    ui:{ notify:function(t){ return call('ui.notify',{text:String(t)}); }, openUrl:function(u){ return call('ui.openUrl',{url:String(u)}); } } };
})();`

func appCheckDoc(m *AppMeta, n int, c *AppCode) string {
	meta, _ := json.Marshal(map[string]any{"id": m.ID, "name": m.Name, "version": n})
	// Même fermeture réseau que dans AJEAN (seul /call, le pont, est permis) :
	// une app qui marcherait ici grâce au réseau ne marcherait pas une fois ouverte.
	csp := "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'self'; form-action 'none'; frame-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'"
	return `<!doctype html><html data-theme="light"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="` + csp + `"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<style>` + appKitCSS + `</style><style>` + c.CSS + `</style><script>` +
		strings.Replace(appCheckBridgeJS, "__APP__", strings.ReplaceAll(string(meta), "<", `<`), 1) +
		`</script></head><body>` + c.HTML + "<script>\n" + c.JS + "\n</script></body></html>"
}

// appCheckCall : un appel du pont pendant la vérification.
type appCheckLog struct {
	mu      sync.Mutex
	calls   []string
	pending int
	storage map[string]json.RawMessage
}

func (l *appCheckLog) add(s string) {
	l.mu.Lock()
	if len(l.calls) < 20 {
		l.calls = append(l.calls, s)
	}
	l.mu.Unlock()
}

func (l *appCheckLog) busy() int { l.mu.Lock(); defer l.mu.Unlock(); return l.pending }

func appCheckHandle(ctx context.Context, id string, log *appCheckLog, method string, args map[string]any) (any, error) {
	switch method {
	case "scripts.run":
		name := str(args["name"])
		to, _ := args["timeout"].(float64)
		r := runAppScript(ctx, id, name, args["input"], time.Duration(to*float64(time.Second)))
		s := fmt.Sprintf("scripts.run(%s) → ok=%v exit=%d %dms", name, r.OK, r.Exit, r.Ms)
		if r.Error != "" {
			s += " erreur: " + r.Error
		}
		if r.OK && r.Data == nil {
			s += " ⚠ stdout non JSON"
		}
		if r.Stderr != "" {
			s += " stderr: " + clipRunes(strings.TrimSpace(r.Stderr), 200)
		}
		log.add(s)
		return r, nil
	case "scripts.list":
		return listAppScripts(id), nil
	case "storage.get", "storage.set", "storage.delete", "storage.list":
		op := strings.TrimPrefix(method, "storage.")
		key := str(args["key"])
		log.mu.Lock()
		defer log.mu.Unlock()
		switch op {
		case "get":
			if v, ok := log.storage[key]; ok {
				return v, nil
			}
			return nil, nil
		case "set":
			b, _ := json.Marshal(args["value"])
			log.storage[key] = b
			return true, nil
		case "delete":
			_, had := log.storage[key]
			delete(log.storage, key)
			return had, nil
		default:
			keys := []string{}
			for k := range log.storage {
				if strings.HasPrefix(k, str(args["prefix"])) {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			return keys, nil
		}
	case "ai.complete":
		log.add("ai.complete (réponse d'exemple pendant la vérification)")
		return map[string]any{"text": "(réponse d'exemple de l'IA pendant la vérification)", "truncated": false}, nil
	case "ui.notify":
		log.add("ui.notify: " + clipRunes(str(args["text"]), 120))
		return true, nil
	case "ui.openUrl":
		log.add("ui.openUrl: " + clipRunes(str(args["url"]), 120))
		return false, nil
	// Fichiers pendant la vérification : lecture des vrais fichiers, écritures
	// et suppressions seulement notées (rien ne change sur le disque).
	case "files.list":
		return listAppFiles(id), nil
	case "files.write", "files.delete", "files.download", "files.share":
		log.add(method + "(" + clipRunes(str(args["name"]), 80) + ") (simulé pendant la vérification)")
		if method == "files.write" {
			return map[string]any{"name": str(args["name"]), "size": args["size"]}, nil
		}
		return true, nil
	case "files.read":
		b, _, err := readAppFileChunk(id, str(args["name"]), 0, appFileChunk)
		if err != nil {
			return nil, err
		}
		return "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(b), nil
	}
	return nil, fmt.Errorf("méthode inconnue : %s", method)
}

// AppCheckResult : ce que la vérification a observé.
type AppCheckResult struct {
	Errors   []map[string]any
	Calls    []string
	Text     string
	Overflow int // largeur en trop sur téléphone (px), 0 = rien
	PNG      []byte
	Steps    []string // résultat de chaque étape d'interaction (check avec steps)
}

// appRender fait tourner l'app dans un Chrome headless dédié (fermé à la fin).
func appRender(ctx context.Context, id string, n int, shot bool, steps ...[]any) (*AppCheckResult, error) {
	m, err := readAppMeta(id)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		n = m.Version
	}
	c, err := readAppCode(id, n)
	if err != nil {
		return nil, err
	}
	if chromePath() == "" {
		return nil, fmt.Errorf("aucun Chrome/Chromium/Edge sur cette machine : vérification visuelle impossible")
	}
	st, _ := readAppStorage(id)
	log := &appCheckLog{storage: map[string]json.RawMessage{}}
	for k, v := range st {
		log.storage[k] = v
	}
	doc := appCheckDoc(m, n, c)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(doc))
	})
	mux.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string         `json:"method"`
			Args   map[string]any `json:"args"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req)
		log.mu.Lock()
		log.pending++
		log.mu.Unlock()
		v, err := appCheckHandle(ctx, id, log, req.Method, req.Args)
		log.mu.Lock()
		log.pending--
		log.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error(), "code": "invalid"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "value": v})
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln)
	defer srv.Close()

	s, err := cdpLaunch()
	if err != nil {
		return nil, err
	}
	defer s.close()
	if err := s.navigate("http://" + ln.Addr().String() + "/"); err != nil {
		return nil, err
	}
	// Attend que les appels du pont (scripts lancés au chargement) soient finis,
	// plus un court calme pour le rendu qui suit.
	settle := func(max time.Duration) {
		deadline := time.Now().Add(max)
		quiet := 0
		for time.Now().Before(deadline) && ctx.Err() == nil {
			if log.busy() == 0 {
				quiet++
				if quiet >= 4 {
					break
				}
			} else {
				quiet = 0
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	settle(appScriptTO + 5*time.Second)
	res := &AppCheckResult{}
	// Interactions demandées par l'IA (cliquer, taper…), comme un utilisateur :
	// le texte visible, les erreurs et la capture sont relevés APRÈS.
	if len(steps) > 0 {
		for i, st := range steps[0] {
			if i >= appMaxSteps {
				res.Steps = append(res.Steps, fmt.Sprintf("%d. ignorée (au plus %d étapes)", i+1, appMaxSteps))
				break
			}
			res.Steps = append(res.Steps, fmt.Sprintf("%d. %s", i+1, appRunStep(s, st)))
			settle(appScriptTO + 2*time.Second)
		}
	}
	if raw, err := s.eval(`JSON.stringify(window.__errs||[])`); err == nil {
		var sj string
		_ = json.Unmarshal(raw, &sj)
		_ = json.Unmarshal([]byte(sj), &res.Errors)
	}
	if raw, err := s.eval(`(document.body&&document.body.innerText||'').replace(/\n{3,}/g,'\n\n').slice(0,1500)`); err == nil {
		_ = json.Unmarshal(raw, &res.Text)
	}
	if shot {
		res.PNG, _ = s.screenshot()
	}
	// Téléphone : 390 px de large, la page ne doit pas déborder.
	if _, err := s.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": 390, "height": 844, "deviceScaleFactor": 1, "mobile": true}); err == nil {
		time.Sleep(300 * time.Millisecond)
		if raw, err := s.eval(`Math.max(0, document.documentElement.scrollWidth - window.innerWidth)`); err == nil {
			_ = json.Unmarshal(raw, &res.Overflow)
		}
	}
	log.mu.Lock()
	res.Calls = append(res.Calls, log.calls...)
	log.mu.Unlock()
	return res, nil
}

// appCheckReport met en forme le résultat pour l'IA (court, actionnable).
func appCheckReport(r *AppCheckResult) string {
	var b strings.Builder
	if len(r.Errors) == 0 {
		b.WriteString("Rendu : aucune erreur JS.\n")
	} else {
		b.WriteString("Rendu : ERREURS JS\n")
		for _, e := range r.Errors {
			line := ""
			if l, ok := e["line"].(float64); ok && l > 0 {
				line = fmt.Sprintf(" (ligne %d du document)", int(l))
			}
			fmt.Fprintf(&b, "- %v%s : %v\n", e["kind"], line, e["message"])
		}
	}
	if len(r.Steps) > 0 {
		b.WriteString("Étapes jouées :\n")
		for _, st := range r.Steps {
			b.WriteString("- " + st + "\n")
		}
	}
	if len(r.Calls) > 0 {
		b.WriteString("Appels du pont (chargement et étapes) :\n")
		for _, c := range r.Calls {
			b.WriteString("- " + c + "\n")
		}
	}
	if r.Overflow > 0 {
		fmt.Fprintf(&b, "⚠ Sur téléphone (390 px), la page déborde de %d px en largeur.\n", r.Overflow)
	}
	// Texte visible sur une ligne compacte : ligne à ligne, un tableau de bord
	// coûtait des centaines de tokens à chaque update pour rien.
	var parts []string
	for _, l := range strings.Split(r.Text, "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			parts = append(parts, l)
		}
	}
	t := strings.Join(parts, " · ")
	if t == "" {
		b.WriteString("⚠ Aucun texte visible après chargement.\n")
	} else {
		if n := len([]rune(t)); n > 600 {
			t = string([]rune(t)[:600]) + fmt.Sprintf(" … (%d car. au total)", n)
		}
		b.WriteString("Texte visible : " + t + "\n")
	}
	return b.String()
}

// toolAppCheck : action check (capture renvoyée au modèle si la vision est active).
func toolAppCheck(ctx context.Context, args map[string]any) (string, map[string]any) {
	id := strings.TrimSpace(str(args["id"]))
	n, _ := intArg(args, "version")
	shot := visionEnabled()
	steps, _ := args["steps"].([]any)
	r, err := appRender(ctx, id, n, shot, steps)
	if err != nil {
		return "[erreur] " + err.Error(), nil
	}
	msg := appCheckReport(r)
	if len(r.PNG) == 0 {
		return msg, nil
	}
	if ws := spaceWorkspace(ctx); ws != "" {
		name := "app-" + id + "-" + time.Now().Format("20060102-150405") + ".png"
		_ = os.MkdirAll(ws, 0o755)
		if os.WriteFile(filepath.Join(ws, name), r.PNG, 0o644) == nil {
			msg += "\nCapture (tu la vois ci-dessous) enregistrée sous « " + name + " » : ![app](" + name + ") pour la montrer."
		}
	}
	b, mime := prepareImageForModel(r.PNG, "image/png")
	return msg, imageURLPart(b, mime)
}

// appMaxSteps borne les interactions d'un check.
const appMaxSteps = 25

// appRunStep joue une étape d'interaction dans l'app et rend un compte rendu
// court. Formes acceptées : {click: "css"}, {type: "css", text: "…"},
// {select: "css", value: "…"}, {key: "Enter"}, {wait: ms}.
func appRunStep(s *cdpSession, raw any) string {
	st, ok := raw.(map[string]any)
	if !ok {
		return "étape illisible (objet attendu)"
	}
	q := func(v any) string { b, _ := json.Marshal(str(v)); return string(b) }
	run := func(desc, js string) string {
		out, err := s.eval(js)
		if err != nil {
			return desc + " : erreur " + err.Error()
		}
		var r string
		_ = json.Unmarshal(out, &r)
		if r != "" && r != "ok" {
			return desc + " : " + r
		}
		return desc + " : ok"
	}
	find := func(sel string) string {
		return `var el=document.querySelector(` + sel + `);if(!el)return 'introuvable';el.scrollIntoView({block:'center'});`
	}
	switch {
	case st["click"] != nil:
		return run("clic "+str(st["click"]), `(function(){`+find(q(st["click"]))+`if(el.disabled)return 'désactivé';el.click();return 'ok'})()`)
	case st["type"] != nil:
		return run("saisie dans "+str(st["type"]), `(function(){`+find(q(st["type"]))+`el.focus();var t=`+q(st["text"])+`;`+
			`var d=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value');if(d&&d.set)d.set.call(el,t);else el.value=t;`+
			`el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`)
	case st["select"] != nil:
		return run("choix dans "+str(st["select"]), `(function(){`+find(q(st["select"]))+`el.value=`+q(st["value"])+`;`+
			`if(el.value!==`+q(st["value"])+`)return 'valeur absente de la liste';`+
			`el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`)
	case st["key"] != nil:
		return run("touche "+str(st["key"]), `(function(){var el=document.activeElement||document.body,k=`+q(st["key"])+`,o={key:k,code:k,bubbles:true,cancelable:true};`+
			`el.dispatchEvent(new KeyboardEvent('keydown',o));el.dispatchEvent(new KeyboardEvent('keypress',o));el.dispatchEvent(new KeyboardEvent('keyup',o));`+
			`if(k==='Enter'&&el.form&&el.tagName==='INPUT'){if(el.form.requestSubmit)el.form.requestSubmit();else el.form.submit();}return 'ok'})()`)
	case st["wait"] != nil:
		ms, _ := st["wait"].(float64)
		if ms <= 0 || ms > 5000 {
			ms = 500
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return fmt.Sprintf("attente %d ms : ok", int(ms))
	}
	return "étape inconnue (click, type+text, select+value, key, wait)"
}
