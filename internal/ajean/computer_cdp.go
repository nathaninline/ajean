package ajean

// computer_cdp.go — pilote un Chromium/Chrome LOCAL via le protocole DevTools
// (CDP), en Go pur (websocket coder/websocket déjà présent), sans dépendance
// lourde type chromedp/playwright. C'est le moteur « web » du computer use :
// ouvrir une URL, lire l'arbre d'accessibilité (éléments interactifs numérotés,
// façon set-of-marks), cliquer/taper/défiler, et — à la demande — capturer un
// screenshot pour la vision.
//
// Choix d'archi (cf. la discussion) : machine HÔTE d'ajean, moteur Chromium via
// CDP. Une seule session navigateur est maintenue (lazy) et réutilisée entre les
// appels d'outils, comme le cache web de chat_internet.go.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

var (
	cdpMu  sync.Mutex
	cdpCur *cdpSession
)

// Taille FIXE du viewport piloté (et donc du screenshot). Communiquée au modèle
// pour que ses coordonnées browser_click_xy lues sur l'image tombent juste.
const (
	cuViewW = 1280
	cuViewH = 800
)

// cdpSession détient le process Chrome, la connexion websocket CDP vers la cible
// « page », et le multiplexage requête/réponse (un lecteur en tâche de fond
// distribue les réponses aux appels en attente par id).
type cdpSession struct {
	cmd     *exec.Cmd
	userDir string
	ws      *websocket.Conn
	wctx    context.Context
	wcancel context.CancelFunc

	mu      sync.Mutex
	nextID  int
	pending map[int]chan cdpMessage

	netMu    sync.Mutex
	inflight int       // requêtes réseau en cours (pour l'attente d'inactivité)
	lastNet  time.Time // dernier événement réseau

	lastSnap string // dernier snapshot renvoyé, pour dé-dupliquer les états identiques
}

type cdpMessage struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ─── découverte du binaire navigateur ────────────────────────────────────────

// chromePath localise un binaire Chromium/Chrome/Edge exploitable. AJEAN_CHROME
// force un chemin ; sinon on tente les emplacements standards par OS puis le PATH.
func chromePath() string {
	if p := strings.TrimSpace(os.Getenv("AJEAN_CHROME")); p != "" {
		return p
	}
	var cands []string
	switch runtime.GOOS {
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			if base == "" {
				continue
			}
			cands = append(cands,
				filepath.Join(base, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`),
				filepath.Join(base, `Chromium\Application\chrome.exe`),
			)
		}
	case "darwin":
		cands = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	default:
		for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge"} {
			if p, err := exec.LookPath(n); err == nil {
				return p
			}
		}
	}
	for _, c := range cands {
		if c != "" {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	for _, n := range []string{"chrome", "google-chrome", "chromium"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// ─── lancement + connexion ───────────────────────────────────────────────────

func cdpLaunch() (*cdpSession, error) {
	bin := chromePath()
	if bin == "" {
		return nil, fmt.Errorf("navigateur Chromium/Chrome/Edge introuvable — installe-le ou définis AJEAN_CHROME=<chemin du binaire>")
	}
	// Balaye les profils temporaires abandonnés (Chrome tué sans close() lors d'un
	// redémarrage du service) : sinon /tmp se remplit de dossiers ajean-cu-* au fil
	// des restarts.
	sweepStaleCUDirs()
	userDir, err := os.MkdirTemp("", "ajean-cu-")
	if err != nil {
		return nil, err
	}
	// Headless par défaut (marche sur le serveur sans écran) ; AJEAN_CU_HEADFUL=1
	// ouvre une vraie fenêtre visible sur une machine de bureau.
	args := []string{
		"--remote-debugging-port=0",
		"--user-data-dir=" + userDir,
		"--no-first-run", "--no-default-browser-check",
		"--disable-gpu", "--disable-extensions",
		"--disable-background-networking",
		"--disable-features=Translate,BackForwardCache",
		// Indispensables sur un serveur headless sous un compte non-root : sans
		// --no-sandbox Chrome plante au lancement (namespaces refusés), et /dev/shm
		// y est souvent minuscule → --disable-dev-shm-usage évite les crash onglet.
		"--no-sandbox", "--disable-dev-shm-usage",
		"--window-size=1280,900",
		"about:blank",
	}
	if os.Getenv("AJEAN_CU_HEADFUL") == "" {
		args = append([]string{"--headless=new"}, args...)
	}
	cmd := exec.Command(bin, args...)
	if err := cmd.Start(); err != nil {
		os.RemoveAll(userDir)
		return nil, err
	}
	// Chrome écrit le port de debug réel dans DevToolsActivePort (ligne 1).
	port := ""
	portFile := filepath.Join(userDir, "DevToolsActivePort")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(portFile); err == nil {
			if line := strings.SplitN(strings.TrimSpace(string(b)), "\n", 2)[0]; line != "" {
				port = line
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if port == "" {
		_ = cmd.Process.Kill()
		os.RemoveAll(userDir)
		return nil, fmt.Errorf("Chrome n'a pas exposé son port de debug (DevToolsActivePort)")
	}
	wsURL, err := cdpPageWS(port)
	if err != nil {
		_ = cmd.Process.Kill()
		os.RemoveAll(userDir)
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		cancel()
		_ = cmd.Process.Kill()
		os.RemoveAll(userDir)
		return nil, err
	}
	c.SetReadLimit(-1) // les screenshots dépassent la limite par défaut (32 Kio)
	s := &cdpSession{cmd: cmd, userDir: userDir, ws: c, wctx: ctx, wcancel: cancel, pending: map[int]chan cdpMessage{}}
	go s.readLoop()
	_, _ = s.call("Page.enable", nil)
	_, _ = s.call("Runtime.enable", nil)
	_, _ = s.call("DOM.enable", nil)
	_, _ = s.call("Network.enable", nil) // pour l'attente d'inactivité réseau (settle)
	// Viewport DÉTERMINISTE : sans ça, headless donne une taille bizarre (mesuré :
	// 1264x749) que le modèle interprétait mal pour browser_click_xy (il visait trop
	// bas). On force exactement cuViewW x cuViewH, DPR 1 → le screenshot fait cette
	// taille, et les coordonnées lues dessus = coordonnées de clic, 1:1.
	_, _ = s.call("Emulation.setDeviceMetricsOverride", map[string]any{
		"width": cuViewW, "height": cuViewH, "deviceScaleFactor": 1, "mobile": false,
	})
	// Garder toute navigation dans le MÊME onglet : le pilote n'est attaché qu'à une
	// cible « page ». Un lien target=_blank ou un window.open ouvrait un onglet que
	// le pilote ne voyait pas → l'IA croyait que « rien ne se passe » et bouclait
	// (vécu sur le test d'inscription sur un site). Ce script, réévalué au début de
	// CHAQUE document, neutralise les nouveaux onglets.
	_, _ = s.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": cdpSameTabJS})
	return s, nil
}

// cdpSameTabJS force les navigations « nouvel onglet » à rester dans l'onglet
// courant (window.open redirigé, target=_blank retiré au clic).
const cdpSameTabJS = `(function(){try{
  window.open=function(u){if(u)location.href=u;return window;};
  document.addEventListener('click',function(e){
    var a=e.target&&e.target.closest&&e.target.closest('a[target]');
    if(a)a.removeAttribute('target');
  },true);
}catch(e){}})();`

// cdpPageWS interroge l'API HTTP de debug pour trouver l'URL websocket d'une
// cible de type « page ».
func cdpPageWS(port string) (string, error) {
	cl := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := cl.Get("http://127.0.0.1:" + port + "/json")
		if err == nil {
			var targets []struct {
				Type string `json:"type"`
				WS   string `json:"webSocketDebuggerUrl"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&targets)
			resp.Body.Close()
			for _, t := range targets {
				if t.Type == "page" && t.WS != "" {
					return t.WS, nil
				}
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return "", fmt.Errorf("aucune cible 'page' exposée par Chrome")
}

func (s *cdpSession) readLoop() {
	for {
		_, data, err := s.ws.Read(s.wctx)
		if err != nil {
			s.mu.Lock()
			for _, ch := range s.pending {
				close(ch)
			}
			s.pending = map[int]chan cdpMessage{}
			s.mu.Unlock()
			return
		}
		var m cdpMessage
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.ID != 0 {
			s.mu.Lock()
			ch := s.pending[m.ID]
			delete(s.pending, m.ID)
			s.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		// Événements réseau : on suit le nombre de requêtes en vol pour que settle()
		// puisse attendre l'inactivité réseau (fin du chargement AJAX).
		switch m.Method {
		case "Network.requestWillBeSent":
			s.netMu.Lock()
			s.inflight++
			s.lastNet = time.Now()
			s.netMu.Unlock()
		case "Network.loadingFinished", "Network.loadingFailed":
			s.netMu.Lock()
			if s.inflight > 0 {
				s.inflight--
			}
			s.lastNet = time.Now()
			s.netMu.Unlock()
		}
	}
}

// call envoie une commande CDP et attend sa réponse (borné à 30 s).
func (s *cdpSession) call(method string, params map[string]any) (json.RawMessage, error) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	ch := make(chan cdpMessage, 1)
	s.pending[id] = ch
	s.mu.Unlock()

	payload := map[string]any{"id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	data, _ := json.Marshal(payload)
	wctx, cancel := context.WithTimeout(s.wctx, 30*time.Second)
	defer cancel()
	if err := s.ws.Write(wctx, websocket.MessageText, data); err != nil {
		return nil, err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("connexion CDP fermée")
		}
		if m.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		return m.Result, nil
	case <-wctx.Done():
		return nil, fmt.Errorf("%s: délai dépassé", method)
	}
}

func (s *cdpSession) close() {
	if s == nil {
		return
	}
	if s.ws != nil {
		_ = s.ws.Close(websocket.StatusNormalClosure, "")
	}
	if s.wcancel != nil {
		s.wcancel()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.userDir != "" {
		os.RemoveAll(s.userDir)
	}
}

// cdpGet renvoie la session courante, en (re)lançant Chrome au besoin. Un ping
// léger valide qu'une session existante répond encore.
func cdpGet() (*cdpSession, error) {
	cdpMu.Lock()
	defer cdpMu.Unlock()
	if cdpCur != nil {
		if _, err := cdpCur.call("Runtime.evaluate", map[string]any{"expression": "1", "returnByValue": true}); err == nil {
			return cdpCur, nil
		}
		cdpCur.close()
		cdpCur = nil
	}
	s, err := cdpLaunch()
	if err != nil {
		return nil, err
	}
	cdpCur = s
	return s, nil
}

// cdpShutdown ferme la session navigateur si elle existe (appelé quand on coupe
// le computer use, pour ne pas laisser un Chrome headless traîner).
// sweepStaleCUDirs supprime les profils Chrome temporaires (ajean-cu-*) de plus
// d'une heure laissés dans le dossier temp — best-effort, silencieux.
func sweepStaleCUDirs() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Hour)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "ajean-cu-") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.RemoveAll(filepath.Join(os.TempDir(), e.Name()))
		}
	}
}

func cdpShutdown() {
	cdpMu.Lock()
	defer cdpMu.Unlock()
	if cdpCur != nil {
		cdpCur.close()
		cdpCur = nil
	}
}

// ─── évaluation JS ───────────────────────────────────────────────────────────

// cdpEval exécute une expression JS dans la page et renvoie sa valeur (JSON brut).
func (s *cdpSession) eval(expr string) (json.RawMessage, error) {
	res, err := s.call("Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	})
	if err != nil {
		return nil, err
	}
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	_ = json.Unmarshal(res, &r)
	if r.ExceptionDetails != nil {
		return nil, fmt.Errorf("JS: %s", r.ExceptionDetails.Text)
	}
	return r.Result.Value, nil
}

// ─── snapshot d'accessibilité (set-of-marks) ─────────────────────────────────

// cdpHelpersJS : fonctions partagées par le snapshot et browser_find, préfixées au
// script évalué. Points clés :
//   - ajActionable ne garde que les éléments RÉELLEMENT cliquables/saisissables :
//     on écarte les conteneurs (list, listitem, banner, navigation, menu, region,
//     div/span muets…) qui polluaient la liste et embrouillaient le modèle.
//   - ajName enrichit un champ de formulaire avec son <label> (« Prénom » plutôt
//     que « firstName »), et NE RÉVÈLE PAS la valeur d'un champ mot de passe
//     (le secret ne doit pas repartir dans le contexte/l'export).
const cdpHelpersJS = `
function ajRole(el){var r=el.getAttribute('role');if(r)return r;var t=el.tagName.toLowerCase();if(t==='a')return 'link';if(t==='button')return 'button';if(t==='select')return 'combobox';if(t==='textarea')return 'textbox';if(t==='input'){var ty=(el.type||'text').toLowerCase();var m={checkbox:'checkbox',radio:'radio',button:'button',submit:'button',reset:'button',file:'button',range:'slider',password:'password',search:'searchbox'};return m[ty]||'textbox';}return t;}
function ajName(el){var t=el.tagName.toLowerCase();var lbl=el.getAttribute('aria-label')||'';if(!lbl){var lb=el.getAttribute('aria-labelledby');if(lb){var e2=document.getElementById(lb.split(' ')[0]);if(e2)lbl=e2.innerText||'';}}if(!lbl&&el.labels&&el.labels.length)lbl=el.labels[0].innerText||'';if(!lbl)lbl=el.getAttribute('placeholder')||el.getAttribute('title')||el.getAttribute('alt')||'';var val='';if((t==='input'||t==='textarea')&&el.type!=='password'&&el.type!=='hidden')val=el.value||'';var n=val?(lbl?(lbl+' = '+val):val):lbl;if(!n&&t!=='input'&&t!=='select'&&t!=='textarea')n=el.innerText||'';if(!n){var c=el.querySelector('img[alt],[aria-label],[title],svg title');if(c)n=c.getAttribute&&(c.getAttribute('alt')||c.getAttribute('aria-label')||c.getAttribute('title'))||c.textContent||'';}if(!n)n=el.getAttribute('name')||el.getAttribute('value')||'';return (n||'').replace(/\s+/g,' ').trim().slice(0,100);}
function ajActionable(el){var t=el.tagName.toLowerCase();if(t==='a')return !!el.getAttribute('href');if(t==='button'||t==='input'||t==='select'||t==='textarea'||t==='summary')return el.type!=='hidden';if(el.isContentEditable)return true;if(el.getAttribute('onclick'))return true;var r=(el.getAttribute('role')||'').toLowerCase();return {button:1,link:1,checkbox:1,radio:1,tab:1,menuitem:1,menuitemcheckbox:1,menuitemradio:1,option:1,'switch':1,combobox:1,textbox:1,searchbox:1,slider:1,spinbutton:1}[r]===1;}
function ajVis(el){var r=el.getBoundingClientRect();if(r.width<3||r.height<3)return false;var s=getComputedStyle(el);if(s.visibility==='hidden'||s.display==='none'||s.opacity==='0')return false;if(r.bottom<0||r.top>innerHeight||r.right<0||r.left>innerWidth)return false;return true;}
var ajSel='a[href],button,input,select,textarea,[role],[onclick],[tabindex],summary,[contenteditable="true"]';
// Traversée du SHADOW DOM : les bandeaux cookies et composants web (usercentrics,
// cookiebot…) vivent dans des shadow roots que querySelectorAll ne voit pas → le
// modèle ne pouvait ni les lister ni les cliquer (test Lady Sushi). ajQueryAll /
// ajByRef / ajClear descendent récursivement dans chaque shadowRoot ouvert.
function ajQueryAll(){var out=[];function w(r){var e=r.querySelectorAll(ajSel);for(var i=0;i<e.length;i++)out.push(e[i]);var a=r.querySelectorAll('*');for(var j=0;j<a.length;j++)if(a[j].shadowRoot)w(a[j].shadowRoot);}w(document);return out;}
function ajByRef(ref){function f(r){var e=r.querySelector('[data-ajean-ref="'+ref+'"]');if(e)return e;var a=r.querySelectorAll('*');for(var i=0;i<a.length;i++)if(a[i].shadowRoot){var x=f(a[i].shadowRoot);if(x)return x;}return null;}return f(document);}
function ajClear(){function w(r){var o=r.querySelectorAll('[data-ajean-ref]');for(var i=0;i<o.length;i++)o[i].removeAttribute('data-ajean-ref');var a=r.querySelectorAll('*');for(var j=0;j<a.length;j++)if(a[j].shadowRoot)w(a[j].shadowRoot);}w(document);}
`

// cdpSnapshotJS marque chaque élément ACTIONNABLE et VISIBLE d'un attribut
// data-ajean-ref numéroté, mémorise le centre de chacun dans window.__ajeanRefs,
// et renvoie {title,url,refs,above,below}. Compact volontairement.
const cdpSnapshotJS = cdpHelpersJS + `(function(){
  ajClear();
  var els=ajQueryAll().filter(function(el){return ajActionable(el)&&ajVis(el);});
  var map={},refs=[],i=0;
  for(var k=0;k<els.length&&i<200;k++){var el=els[k];var nm=ajName(el),rl=ajRole(el);
    i++;var r=el.getBoundingClientRect();map[i]={x:r.left+r.width/2,y:r.top+r.height/2};
    el.setAttribute('data-ajean-ref',String(i));
    refs.push('['+i+'] '+rl+(nm?' "'+nm+'"':''));
  }
  window.__ajeanRefs=map;
  var de=document.documentElement;
  var sh=Math.max(de.scrollHeight,document.body?document.body.scrollHeight:0);
  return {title:document.title||'',url:location.href,refs:refs,
    above:Math.round(scrollY),below:Math.round(Math.max(0,sh-scrollY-innerHeight))};
})()`

type cuSnap struct {
	Title string   `json:"title"`
	URL   string   `json:"url"`
	Refs  []string `json:"refs"`
	Above int      `json:"above"` // pixels défilables au-dessus de la vue
	Below int      `json:"below"` // pixels défilables en dessous de la vue
}

// snapshot lit l'arbre interactif courant et le formate pour le modèle.
func (s *cdpSession) snapshot() (string, error) {
	val, err := s.eval(cdpSnapshotJS)
	if err != nil {
		return "", err
	}
	var snap cuSnap
	if err := json.Unmarshal(val, &snap); err != nil {
		return "", fmt.Errorf("snapshot illisible: %v", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n%s\n\n", snap.Title, snap.URL)
	if len(snap.Refs) == 0 {
		b.WriteString("(aucun élément interactif détecté — page vide, en cours de chargement, ou rendue sur canvas ; réessaie browser_snapshot, ou utilise browser_screenshot pour voir)")
	} else {
		fmt.Fprintf(&b, "%d élément(s) interactif(s) VISIBLE(s) — clique par numéro avec browser_click(ref) :\n", len(snap.Refs))
		b.WriteString(strings.Join(snap.Refs, "\n"))
	}
	// Indice de défilement : la liste ne couvre QUE la partie visible. Sans ce
	// repère, le modèle croit la page épuisée après un scroll (3 éléments restants)
	// et se met à deviner des URLs au lieu de défiler (vécu sur le test de navigation).
	if snap.Below > 20 || snap.Above > 20 {
		b.WriteString("\n\n")
		if snap.Below > 20 {
			fmt.Fprintf(&b, "↓ encore ~%d px sous la vue — browser_scroll(down) pour révéler d'autres éléments. ", snap.Below)
		}
		if snap.Above > 20 {
			fmt.Fprintf(&b, "↑ ~%d px au-dessus — browser_scroll(up) pour y revenir.", snap.Above)
		}
	}
	return strings.TrimRight(b.String(), " "), nil
}

// snapshotDedup renvoie le snapshot courant, ou une ligne courte s'il est
// RIGOUREUSEMENT identique au dernier renvoyé (clic sans effet, ré-ouverture de
// la même page…). Évite de re-lister 20+ éléments pour rien — les numéros restent
// valides puisque le DOM n'a pas changé. Économie de contexte pour petits modèles.
func (s *cdpSession) snapshotDedup() (string, error) {
	full, err := s.snapshot()
	if err != nil {
		return "", err
	}
	if full == s.lastSnap {
		return "(page inchangée depuis le dernier affichage — les mêmes numéros restent valides)", nil
	}
	s.lastSnap = full
	return full, nil
}

// cdpFindJS cherche, sur TOUTE la page (pas seulement le viewport), les éléments
// interactifs dont le rôle ou le nom contient la requête, leur pose un
// data-ajean-ref numéroté, fait défiler le premier au centre, et renvoie la liste.
// Sert à localiser un lien/bouton hors écran sans défiler à l'aveugle.
// browser_find est NON DESTRUCTIF (contrairement au snapshot) : il n'efface PAS les
// data-ajean-ref existants — sinon les numéros du dernier snapshot deviendraient
// invalides et un browser_click ensuite échouerait (« introuvable », vécu au test #11).
// Il RÉUTILISE le ref d'un élément déjà numéroté, et n'attribue un NOUVEAU numéro
// (au-delà du max courant) qu'aux éléments hors écran encore sans ref.
const cdpFindJS = `function(q){
  q=(q||'').toLowerCase();
  var pool=ajQueryAll();
  var maxRef=0;for(var m=0;m<pool.length;m++){var v=parseInt(pool[m].getAttribute('data-ajean-ref'),10)||0;if(v>maxRef)maxRef=v;}
  var els=pool.filter(ajActionable);
  var refs=[],count=0,first=null;
  for(var k=0;k<els.length&&count<60;k++){var el=els[k];var nm=ajName(el),rl=ajRole(el);
    if((rl+' '+nm).toLowerCase().indexOf(q)<0)continue;
    var st=getComputedStyle(el);if(st.display==='none'||st.visibility==='hidden')continue;
    var r=el.getBoundingClientRect();if(r.width<1&&r.height<1)continue;
    var ref=el.getAttribute('data-ajean-ref');
    if(!ref){maxRef++;ref=String(maxRef);el.setAttribute('data-ajean-ref',ref);}
    if(!first)first=el;count++;
    refs.push('['+ref+'] '+rl+(nm?' "'+nm+'"':''));
  }
  if(first)first.scrollIntoView({block:'center'});
  return {refs:refs};
}`

func (s *cdpSession) find(query string) (string, error) {
	qb, _ := json.Marshal(query)
	val, err := s.eval(cdpHelpersJS + "(" + cdpFindJS + ")(" + string(qb) + ")")
	if err != nil {
		return "", err
	}
	var r struct {
		Refs []string `json:"refs"`
	}
	if err := json.Unmarshal(val, &r); err != nil {
		return "", fmt.Errorf("recherche illisible: %v", err)
	}
	if len(r.Refs) == 0 {
		return fmt.Sprintf("Aucun élément contenant « %s » sur cette page. Essaie un autre mot, ou c'est peut-être ailleurs (autre page/lien).", query), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d élément(s) contenant « %s » (le 1er est amené dans la vue) — clique par numéro :\n", len(r.Refs), query)
	b.WriteString(strings.Join(r.Refs, "\n"))
	return b.String(), nil
}

// ─── actions ─────────────────────────────────────────────────────────────────

// waitLoad attend (au mieux) que la page ait fini de charger après une navigation,
// puis que le DOM se STABILISE (contenu rendu en AJAX après readyState=complete).
func (s *cdpSession) waitLoad() {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		val, err := s.eval(`document.readyState`)
		if err == nil && strings.Trim(string(val), `"`) == "complete" {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	s.settle()
}

// settle attend l'INACTIVITÉ RÉSEAU : sur une SPA, readyState passe à « complete »
// avant que le contenu chargé en AJAX (résultats de recherche, étape de tunnel…)
// ne soit arrivé. On rend la main dès qu'il n'y a plus de requête en vol depuis
// ~600 ms, borné à 6 s. Ça supprime le « bash sleep » que le modèle bricolait
// avant un browser_snapshot (vécu au test de navigation #10).
func (s *cdpSession) settle() {
	const quiet = 600 * time.Millisecond
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		s.netMu.Lock()
		inflight := s.inflight
		since := time.Since(s.lastNet)
		s.netMu.Unlock()
		if inflight <= 0 && since >= quiet {
			return
		}
		time.Sleep(120 * time.Millisecond)
	}
}

func (s *cdpSession) navigate(url string) error {
	// Toute navigation = nouvelle page : on oublie le dernier snapshot pour que le
	// browser_open qui suit renvoie la page COMPLÈTE, jamais « (page inchangée) »
	// (piège quand la session persiste d'un test à l'autre, vécu sur Lady Sushi).
	s.lastSnap = ""
	// « back » / « forward » : navigation dans l'historique (récupération d'erreur),
	// pas une URL — sinon on en ferait « https://back ».
	switch strings.ToLower(strings.TrimSpace(url)) {
	case "back":
		_, err := s.eval("history.back();'ok'")
		s.waitLoad()
		return err
	case "forward":
		_, err := s.eval("history.forward();'ok'")
		s.waitLoad()
		return err
	}
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}
	res, err := s.call("Page.navigate", map[string]any{"url": url})
	if err != nil {
		return err
	}
	// Page.navigate signale l'échec réseau dans errorText (DNS, connexion refusée…) ;
	// et une page d'erreur Chrome a une location « chrome-error:// ». Dans les deux
	// cas on renvoie une erreur EXPLICITE plutôt qu'un faux snapshot « Actualiser »
	// que le modèle re-tentait en boucle (vécu : la page de compte ré-ouvert 2×).
	var nr struct {
		ErrorText string `json:"errorText"`
	}
	_ = json.Unmarshal(res, &nr)
	s.waitLoad()
	loc, _ := s.eval("location.href")
	// ERR_ABORTED est bénin (redirection/téléchargement) : on ne le traite pas comme
	// un échec. Le signal fiable est une location « chrome-error:// ».
	failed := strings.Contains(string(loc), "chrome-error")
	if nr.ErrorText != "" && nr.ErrorText != "net::ERR_ABORTED" {
		failed = true
	}
	if failed {
		reason := nr.ErrorText
		if reason == "" {
			reason = "page injoignable"
		}
		return fmt.Errorf("chargement de %s échoué (%s) — URL/hôte invalide, DNS, ou site bloqué ; corrige l'URL, essaie 'back', ou passe par un lien de la page", url, reason)
	}
	return nil
}

// refCenter renvoie les coordonnées viewport (fraîches) d'un élément marqué, en
// le faisant d'abord défiler au centre.
func (s *cdpSession) refCenter(ref int) (float64, float64, error) {
	expr := cdpHelpersJS + fmt.Sprintf(`(function(){var el=ajByRef(%d);if(!el){return null;}el.scrollIntoView({block:'center',inline:'center'});var r=el.getBoundingClientRect();return {x:r.left+r.width/2,y:r.top+r.height/2};})()`, ref)
	val, err := s.eval(expr)
	if err != nil {
		return 0, 0, err
	}
	if string(val) == "null" || len(val) == 0 {
		return 0, 0, fmt.Errorf("élément [%d] introuvable — refais un browser_snapshot (la page a peut-être changé)", ref)
	}
	var p struct{ X, Y float64 }
	if err := json.Unmarshal(val, &p); err != nil {
		return 0, 0, err
	}
	return p.X, p.Y, nil
}

func (s *cdpSession) clickRef(ref int) error {
	x, y, err := s.refCenter(ref)
	if err != nil {
		return err
	}
	for _, typ := range []string{"mousePressed", "mouseReleased"} {
		if _, err := s.call("Input.dispatchMouseEvent", map[string]any{
			"type": typ, "x": x, "y": y, "button": "left", "clickCount": 1,
		}); err != nil {
			return err
		}
	}
	s.waitLoad()
	return nil
}

// clickXY clique à des coordonnées pixel du VIEWPORT (lues sur le screenshot).
// Seul moyen d'atteindre ce que le DOM ne voit pas : un bandeau de consentement
// dans un iframe cross-origin, un canvas, une carte (vécu sur Lady Sushi).
func (s *cdpSession) clickXY(x, y float64) error {
	for _, typ := range []string{"mousePressed", "mouseReleased"} {
		if _, err := s.call("Input.dispatchMouseEvent", map[string]any{
			"type": typ, "x": x, "y": y, "button": "left", "clickCount": 1,
		}); err != nil {
			return err
		}
	}
	s.waitLoad()
	return nil
}

func (s *cdpSession) typeText(text string) error {
	_, err := s.call("Input.insertText", map[string]any{"text": text})
	return err
}

// typeInto écrit dans le champ [ref]. Cas spécial des listes déroulantes NATIVES
// (<select>, rôle « combobox ») : leur menu s'ouvre en natif hors du DOM, donc
// ses options ne sont pas cliquables dans le snapshot (galère du test de navigation #14).
// Ici on SÉLECTIONNE directement l'option dont le texte contient la valeur, et on
// émet input/change. Sinon on focus+sélectionne le contenu et on laisse insertText
// remplacer (comportement texte normal).
func (s *cdpSession) typeInto(ref int, text string) error {
	vb, _ := json.Marshal(text)
	expr := cdpHelpersJS + fmt.Sprintf(`(function(ref,val){
  var el=ajByRef(ref);
  if(!el)return 'NOTFOUND';
  el.scrollIntoView({block:'center'});
  if(el.tagName.toLowerCase()==='select'){
    var o=el.options,lv=val.toLowerCase(),k=-1;
    for(var i=0;i<o.length;i++){var tx=(o[i].text||'').toLowerCase();if(tx===lv||tx.indexOf(lv)>=0||(o[i].value||'').toLowerCase()===lv){k=i;break;}}
    if(k<0)return 'NOOPT';
    el.selectedIndex=k;el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));
    return 'SELECT';
  }
  el.focus();
  try{if(typeof el.select==='function')el.select();else if(window.getSelection){var r=document.createRange();r.selectNodeContents(el);var s=getSelection();s.removeAllRanges();s.addRange(r);}}catch(e){}
  return 'TEXT';
})(%d,%s)`, ref, string(vb))
	val, err := s.eval(expr)
	if err != nil {
		return err
	}
	switch strings.Trim(string(val), `"`) {
	case "NOTFOUND":
		return fmt.Errorf("champ [%d] introuvable — refais un browser_snapshot", ref)
	case "NOOPT":
		return fmt.Errorf("aucune option de la liste [%d] ne correspond à « %s » — fais un browser_screenshot pour voir les choix", ref, text)
	case "SELECT":
		return nil // liste déroulante réglée, rien à taper
	default: // TEXT
		return s.typeText(text)
	}
}

// cdpKeys mappe les touches nommées vers leur description CDP.
var cdpKeys = map[string]struct {
	key, code string
	vk        int
}{
	"enter":     {"Enter", "Enter", 13},
	"tab":       {"Tab", "Tab", 9},
	"backspace": {"Backspace", "Backspace", 8},
	"escape":    {"Escape", "Escape", 27},
	"esc":       {"Escape", "Escape", 27},
	"delete":    {"Delete", "Delete", 46},
	"up":        {"ArrowUp", "ArrowUp", 38},
	"down":      {"ArrowDown", "ArrowDown", 40},
	"left":      {"ArrowLeft", "ArrowLeft", 37},
	"right":     {"ArrowRight", "ArrowRight", 39},
	"home":      {"Home", "Home", 36},
	"end":       {"End", "End", 35},
	"pageup":    {"PageUp", "PageUp", 33},
	"pagedown":  {"PageDown", "PageDown", 34},
}

func (s *cdpSession) pressKey(name string) error {
	k, ok := cdpKeys[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return fmt.Errorf("touche inconnue « %s » — connues : enter, tab, backspace, escape, delete, up/down/left/right, home, end, pageup, pagedown", name)
	}
	for _, typ := range []string{"rawKeyDown", "keyUp"} {
		if _, err := s.call("Input.dispatchKeyEvent", map[string]any{
			"type": typ, "key": k.key, "code": k.code,
			"windowsVirtualKeyCode": k.vk, "nativeVirtualKeyCode": k.vk,
		}); err != nil {
			return err
		}
	}
	s.waitLoad()
	return nil
}

func (s *cdpSession) scroll(dir string) error {
	dy, dx := 0, 0
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "down", "":
		dy = 600
	case "up":
		dy = -600
	case "right":
		dx = 600
	case "left":
		dx = -600
	default:
		return fmt.Errorf("direction inconnue « %s » (up, down, left, right)", dir)
	}
	_, err := s.eval(fmt.Sprintf("window.scrollBy(%d,%d);'ok'", dx, dy))
	if err == nil {
		time.Sleep(200 * time.Millisecond)
	}
	return err
}

// screenshot capture le viewport en PNG (octets décodés).
func (s *cdpSession) screenshot() ([]byte, error) {
	res, err := s.call("Page.captureScreenshot", map[string]any{"format": "png"})
	if err != nil {
		return nil, err
	}
	var r struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(r.Data)
}
