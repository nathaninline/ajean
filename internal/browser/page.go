package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	viewMax     = 12000            // taille maximale d'une vue rendue au modèle
	textMax     = 11000            // part du texte de page dans cette vue
	settleMax   = 15 * time.Second // attente maximale d'une page qui se stabilise
	settlePoll  = 200 * time.Millisecond
	actionPause = 250 * time.Millisecond // laisse à un clic le temps de lancer sa navigation
)

// viewJS rend la page en texte, à partir du haut de la fenêtre : texte visible,
// et une ligne numérotée par élément interactif. Les éléments sont gardés dans
// window.__ajeanRefs pour qu'un clic retrouve le sien ; window.__ajeanNext note
// où la vue s'est arrêtée, pour que le défilement reprenne là.
const viewJS = `function(max){
const SEL='a[href],button,input,textarea,select,summary,[onclick],[contenteditable=""],[contenteditable="true"],[role=button],[role=link],[role=tab],[role=menuitem],[role=checkbox],[role=radio],[role=option],[role=combobox]';
const SKIP=/^(script|style|noscript|template|head|iframe|object|embed|canvas|svg)$/i, ROLE=/^(button|link|tab|menuitem|checkbox|radio|option|combobox)$/;
const refs=window.__ajeanRefs=[], out=[];
let line='', size=0, truncated=false, cur=null;
window.__ajeanNext=0;
const clean=(s,n)=>{s=(s||'').replace(/\s+/g,' ').trim();return s.length>n?s.slice(0,n)+'…':s;};
const quote=s=>'"'+clean(s,60)+'"';
const flush=()=>{
  const t=clean(line,2000);line='';
  if(!t||truncated)return;
  if(size+t.length>max){truncated=true;if(cur)window.__ajeanNext=cur.getBoundingClientRect().top+scrollY;return;}
  out.push(t);size+=t.length+1;
};
const fieldName=el=>clean(el.getAttribute('aria-label')||(el.labels&&el.labels[0]&&el.labels[0].innerText)||el.getAttribute('title')||el.name||'',60);
const describe=(el,nested)=>{
  const tag=el.tagName, role=el.getAttribute('role')||'', type=(el.getAttribute('type')||'text').toLowerCase(), det=[];
  let kind, name;
  if(tag==='INPUT'&&/^(button|submit|reset|image)$/.test(type)){kind='button';name=clean(el.value||el.getAttribute('aria-label')||el.alt||type,100);}
  else if(tag==='INPUT'&&/^(checkbox|radio)$/.test(type)){kind=type;name=fieldName(el);det.push(el.checked?'checked':'unchecked');}
  else if(tag==='INPUT'||tag==='TEXTAREA'){
    kind=tag==='INPUT'?'input':'textarea';name=fieldName(el);
    if(tag==='INPUT')det.push(type);
    if(el.placeholder)det.push('placeholder '+quote(el.placeholder));
    if(el.value)det.push(type==='password'?'filled':'value '+quote(el.value));
  }else if(tag==='SELECT'){
    kind='select';name=fieldName(el);
    const o=el.selectedOptions[0];
    det.push('current: '+(o?clean(o.label,60):'none'),'options: '+clean([...el.options].map(o=>o.label.trim()).join(' | '),300));
  }else{
    kind=ROLE.test(role)?role:tag==='A'?'link':tag==='BUTTON'?'button':tag==='SUMMARY'?'summary':el.isContentEditable?'editable':'clickable';
    name=clean(el.getAttribute('aria-label')||(nested?'':el.innerText)||el.getAttribute('title')||(el.querySelector('img[alt]')||{}).alt||(tag==='A'?el.getAttribute('href'):''),100);
    if(el.getAttribute('aria-checked')==='true')det.push('checked');
    if(el.getAttribute('aria-selected')==='true')det.push('selected');
  }
  if(el.disabled||el.getAttribute('aria-disabled')==='true')det.push('disabled');
  return kind+(name?': '+name:'')+(det.length?' ('+det.join(', ')+')':'');
};
const walk=(node,hidden)=>{
  if(truncated)return;
  if(node.nodeType===3){if(!hidden)line+=node.nodeValue;return;}
  if(node.nodeType!==1||SKIP.test(node.tagName)||node.getAttribute('aria-hidden')==='true')return;
  const el=node, cs=getComputedStyle(el), r=el.getBoundingClientRect();
  if(cs.display==='none')return;
  cur=el;
  // Caché, ou entièrement au-dessus de la fenêtre (déjà lu) : son texte est sauté, pas ses enfants.
  hidden=cs.visibility!=='visible'||(r.height>0&&r.bottom<0);
  const cell=cs.display==='table-cell', block=el.tagName==='BR'||!(cell||cs.display.startsWith('inline')||cs.display==='contents');
  if(block)flush();else if(cell&&line.trim())line+=' | ';
  if(el.matches(SEL)&&el.type!=='hidden'){
    const nested=!!el.querySelector(SEL);
    if(!hidden&&el.getClientRects().length){
      flush();refs.push(el);line='['+refs.length+'] '+describe(el,nested);flush();
    }
    if(!nested)return;
  }
  if(el.tagName==='IMG'){if(!hidden&&el.alt)line+=' '+el.alt+' ';return;}
  if(/^H[1-6]$/.test(el.tagName)&&!hidden)line+='# ';
  for(const c of (el.shadowRoot||el).childNodes)walk(c,hidden);
  if(block)flush();
};
if(document.body)walk(document.body,false);
flush();
return {title:document.title,url:location.href,text:out.join('\n'),truncated,y:Math.round(scrollY),h:Math.round(document.documentElement.scrollHeight)};
}`

// refJS : début commun des scripts qui agissent sur un élément numéroté. Une
// page qui a changé n'a plus la liste, ou plus l'élément : il faut relire.
const refJS = `const el=(window.__ajeanRefs||[])[n-1];
if(!el||!el.isConnected)return {err:'élément ['+n+'] introuvable : la page a changé depuis la dernière vue, refais action=read pour avoir les numéros à jour'};
el.scrollIntoView({block:'center',inline:'center',behavior:'instant'});
`

// clickJS rend le centre de l'élément si c'est bien lui qu'on y touche ; sinon
// (sans boîte, ou recouvert) le script clique lui-même.
const clickJS = `function(n){` + refJS + `
const r=el.getBoundingClientRect(), x=r.left+r.width/2, y=r.top+r.height/2;
const hit=r.width>0&&r.height>0?document.elementFromPoint(x,y):null;
if(hit&&(hit===el||el.contains(hit)))return {x,y,mouse:true};
el.click();return {};
}`

// focusJS place le curseur dans un champ de texte et sélectionne son contenu,
// pour que la saisie le remplace.
const focusJS = `function(n){` + refJS + `
const tag=el.tagName;
if(!(el.isContentEditable||tag==='TEXTAREA'||(tag==='INPUT'&&!/^(checkbox|radio|file|button|submit|reset|image|range|color)$/.test(el.type))))return {err:"l'élément ["+n+"] n'est pas un champ de texte"};
el.focus();
if(el.select)el.select();else{const s=getSelection(), g=document.createRange();g.selectNodeContents(el);s.removeAllRanges();s.addRange(g);}
return {};
}`

// selectJS choisit une option d'une liste par son libellé, sa valeur, ou à
// défaut un morceau de son libellé.
const selectJS = `function(n,want){` + refJS + `
if(el.tagName!=='SELECT')return {err:"l'élément ["+n+"] n'est pas une liste <select>"};
const w=want.trim().toLowerCase(), all=[...el.options];
const o=all.find(o=>o.label.trim().toLowerCase()===w)||all.find(o=>o.value.toLowerCase()===w)||all.find(o=>o.label.toLowerCase().includes(w));
if(!o)return {err:'option introuvable. Options : '+all.map(o=>o.label.trim()).join(' | ').slice(0,400)};
o.selected=true;
el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));
return {};
}`

// scrollJS : vers le bas, on reprend là où la vue s'est arrêtée si c'est plus
// loin qu'un écran (sinon le modèle relirait surtout ce qu'il a déjà lu).
const scrollJS = `function(down){
const se=document.scrollingElement||document.documentElement, y0=se.scrollTop, vh=innerHeight, next=window.__ajeanNext||0;
scrollTo({top:down?(next>y0+vh?next-40:y0+vh*0.9):y0-vh*0.9,behavior:'instant'});
return {moved:Math.abs(se.scrollTop-y0)>1,x:innerWidth/2,y:vh/2,dy:(down?1:-1)*vh*0.9};
}`

// stateJS : où en est le chargement, et à quelle adresse.
const stateJS = `function(){return {ready:document.readyState,url:location.href}}`

// command envoie une commande CDP à l'onglet piloté.
func (b *instance) command(method string, params map[string]any, out any) error {
	return b.conn.call(b.session, method, params, out, callTimeout)
}

// eval exécute une de NOS fonctions dans la page (jamais du code du modèle) ; ses
// arguments passent en JSON, donc en données.
func (b *instance) eval(fn string, out any, args ...any) error {
	list := make([]string, len(args))
	for i, a := range args {
		raw, err := json.Marshal(a)
		if err != nil {
			return err
		}
		list[i] = string(raw)
	}
	var r struct {
		Result           struct{ Value json.RawMessage }
		ExceptionDetails *struct{ Text string }
	}
	expr := "(" + fn + ")(" + strings.Join(list, ",") + ")"
	if err := b.command("Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}, &r); err != nil {
		return err
	}
	if r.ExceptionDetails != nil {
		return errors.New("script de page : " + r.ExceptionDetails.Text)
	}
	if out == nil || len(r.Result.Value) == 0 {
		return nil
	}
	return json.Unmarshal(r.Result.Value, out)
}

// actResult : ce que rendent nos scripts d'action. Err : le script a refusé
// (numéro périmé, élément qui ne convient pas) et dit pourquoi.
type actResult struct {
	Err          string
	Mouse, Moved bool
	X, Y, DY     float64
}

// act exécute un script d'action ; son refus devient une erreur.
func (b *instance) act(fn string, args ...any) (actResult, error) {
	var r actResult
	err := b.eval(fn, &r, args...)
	if err == nil && r.Err != "" {
		err = errors.New(r.Err)
	}
	return r, err
}

// settle attend que la page soit chargée et que son adresse ne bouge plus, sans
// jamais dépasser settleMax (certaines pages ne finissent jamais).
func (b *instance) settle() {
	var last string
	stable := 0
	for deadline := time.Now().Add(settleMax); time.Now().Before(deadline) && stable < 2; time.Sleep(settlePoll) {
		var st struct{ Ready, URL string }
		// Une erreur ici est normale en pleine navigation (contexte détruit).
		if b.eval(stateJS, &st) == nil && st.Ready == "complete" && st.URL == last {
			stable++
		} else {
			stable = 0
		}
		last = st.URL
	}
}

// snapshot : la page telle que viewJS la rend.
type snapshot struct {
	Title, URL, Text string
	Truncated        bool
	Y, H             int // défilement et hauteur totale, en pixels
}

// view rend la page courante au modèle, après avoir suivi un éventuel nouvel
// onglet, attendu la fin du chargement et revérifié où l'on a atterri.
func (b *instance) view() (string, error) {
	time.Sleep(actionPause)
	if err := b.syncTarget(); err != nil {
		return "", err
	}
	b.settle()
	var s snapshot
	if err := b.eval(viewJS, &s, textMax); err != nil {
		return "", fmt.Errorf("lecture de la page impossible : %v", err)
	}
	if strings.HasPrefix(s.URL, "chrome-error:") {
		return "", errors.New("la page n'a pas pu se charger (adresse injoignable ou bloquée)")
	}
	// Un clic ou une redirection mène parfois ailleurs que là où open a été permis :
	// sur une page sans réseau (file:, chrome:, data:), ou sur le refus du proxy.
	if err := checkURL(s.URL); err != nil && s.URL != blank {
		_ = b.command("Page.navigate", map[string]any{"url": blank}, nil)
		return "", fmt.Errorf("page refusée (%s) : %v ; le navigateur a été ramené sur une page vide", clip(s.URL, 200), err)
	}
	return s.render(), nil
}

// render met la page en forme pour le modèle : titre, adresse, position dans
// la page, puis le texte.
func (s snapshot) render() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Page : %s\nURL : %s\n", clip(s.Title, 200), clip(s.URL, 500))
	if s.Y > 0 {
		fmt.Fprintf(&sb, "(vue à partir de %d px sur %d : action=scroll direction=up pour remonter)\n", s.Y, s.H)
	}
	text := s.Text
	if text == "" {
		text = "(page vide)"
	}
	sb.WriteString("\n" + text)
	if s.Truncated {
		sb.WriteString("\n\n[vue tronquée : action=scroll direction=down pour lire la suite]")
	}
	return clip(sb.String(), viewMax)
}
