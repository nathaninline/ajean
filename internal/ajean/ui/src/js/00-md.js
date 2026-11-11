// marked: GFM on, no auto-breaks (single newlines stay inline).
marked.setOptions({ gfm: true, breaks: false });
// HTML brut dans le texte : affiché tel quel, jamais interprété. Un modèle qui
// parle de HTML écrit des balises hors bloc de code (« un <h3> par groupe ») :
// interprétée, une balise jamais refermée avalait toute la suite du message
// (texte soudain en titre, en gras, en majuscules). Seules passent quelques
// balises de mise en forme qui se referment d'elles-mêmes en fin de paragraphe.
const MD_RAW_OK = /^<\/?(br|sub|sup|kbd)\s*\/?>$/i;
const mdEscRaw = (s)=>s.replace(/[&<>]/g, c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));
marked.use({ renderer: { html(html, block){
  const raw = typeof html === 'string' ? html : ((html && (html.raw || html.text)) || '');
  if(MD_RAW_OK.test(raw.trim())) return raw;
  const esc = mdEscRaw(raw);
  // Bloc HTML entier : un paragraphe, lignes conservées.
  return block || /\n/.test(raw.trim()) ? '<p>'+esc.trim().replace(/\n/g, '<br>')+'</p>\n' : esc;
} } });
// Pre-process to fix common LLM markdown mistakes before handing to marked:
//  - code fences glued to preceding text on the same line ("foo ```ruby ...")
//  - fences without a closing newline
//  - 3+ consecutive blank lines collapsed to one (LLMs love padding)
function fixMd(s){
  if(!s) return '';
  // Force a newline before a fence that's been glued to preceding text
  // (common LLM mistake: "...crée le fichier et```ruby"). We do NOT split
  // *after* the fence — the part after the fence is the language identifier.
  s = s.replace(/([^\n])(\s*)(```+|~~~+)(?=\w*\s*\n)/g, '$1\n$3');
  // Collapse runs of blank lines that LLMs love to emit.
  s = s.replace(/\n{3,}/g, '\n\n');
  return s;
}
function md(src){
  if(!src) return '';
  const m = mathExtract(fixMd(src));
  const html = mdSanitize(marked.parse(m.text));
  return m.list.length ? html.replace(/(\d+)/g, (_, i)=>mathHtml(m.list[+i])) : html;
}

// --- Nettoyage du HTML rendu ------------------------------------------------
// marked laisse passer le HTML brut du texte. Or ce texte vient du modèle, d'une
// page web lue ou d'un fichier : une balise <meta http-equiv="refresh"> recopiée
// d'une page de connexion faisait quitter l'interface dès l'affichage, et un
// <img onerror=…> y aurait exécuté du code. Seules les balises que produit le
// markdown sont gardées (les autres deviennent du texte), sans attribut
// d'événement ni de style, et les liens ne gardent que des adresses sûres. Les
// formules (KaTeX) sont insérées APRÈS ce nettoyage, sur un repère.
const MD_TAGS = new Set(['P','BR','HR','H1','H2','H3','H4','H5','H6','UL','OL','LI','BLOCKQUOTE','PRE','CODE','EM','STRONG','B','I','U','S','DEL','INS','MARK','SMALL','SUB','SUP','KBD','A','IMG','TABLE','THEAD','TBODY','TFOOT','TR','TH','TD','SPAN','DIV','DETAILS','SUMMARY','INPUT','DL','DT','DD','ABBR','Q','CITE']);
const MD_ATTRS = new Set(['href','src','alt','title','class','colspan','rowspan','align','start','type','checked','disabled','open','width','height','lang']);
function mdSafeURL(u, img){
  const s = String(u || '').replace(/[\u0000- ]+/g, '');
  const sch = /^([a-z][a-z0-9+.-]*):/i.exec(s);
  if(!sch) return true;                                   // relative, #ancre, chemin du workspace
  const p = sch[1].toLowerCase();
  if(p === 'http' || p === 'https' || p === 'mailto') return true;
  return img && p === 'data' && /^data:image\/(png|jpe?g|gif|webp)[;,]/i.test(s);
}
function mdSanitize(html){
  if(!/</.test(html)) return html;
  const t = document.createElement('template');
  t.innerHTML = html;
  const walk = (node)=>{
    for(const el of Array.from(node.children)){
      if(!MD_TAGS.has(el.tagName) || (el.tagName === 'INPUT' && el.getAttribute('type') !== 'checkbox')){
        // Balise inconnue : son texte reste lisible, la balise ne vit pas.
        el.replaceWith(document.createTextNode(el.outerHTML));
        continue;
      }
      for(const a of Array.from(el.attributes)){
        const n = a.name.toLowerCase();
        if(!MD_ATTRS.has(n) || ((n === 'href' || n === 'src') && !mdSafeURL(a.value, el.tagName === 'IMG'))) el.removeAttribute(a.name);
      }
      if(el.tagName === 'INPUT') el.setAttribute('disabled', '');
      walk(el);
    }
  };
  walk(t.content);
  return t.innerHTML;
}

// --- Formules LaTeX (KaTeX) ---------------------------------------------------
// Les formules sont mises de côté AVANT marked, qui abîmerait \\, _ et * ; un
// repère (caractères à usage privé) prend leur place puis reçoit le rendu. Le
// code (blocs et `en ligne`) n'est jamais touché. $…$ n'est une formule que s'il
// ne commence ni ne finit par une espace et n'est pas suivi d'un chiffre : « 5 $
// et 10 $ » reste du texte. KaTeX (embarqué, /katex/) n'est chargé qu'à la
// première formule ; d'ici là la source s'affiche, puis est rendue sur place.
// (Pas de lookbehind dans l'expression : les iOS d'avant 16.4 refusent de
// charger le script entier. Ces contrôles-là sont faits dans le remplacement.)
const MATH_RE = /(```[\s\S]*?(?:```|$)|~~~[\s\S]*?(?:~~~|$)|`[^`\n]*`)|\$\$([\s\S]+?)\$\$|\\\[([\s\S]+?)\\\]|\\\(([\s\S]+?)\\\)|\$(?![\s$])((?:\\.|[^$\\\n])+?)\$(?![\d$])/g;
function mathExtract(s){
  const list = [];
  const text = s.replace(MATH_RE, (all, code, dd, br, pa, d, at)=>{
    if(code !== undefined) return all;
    // $…$ : pas collé à un mot ni échappé (\$) avant, pas d'espace avant le $ final.
    if(d !== undefined && (/[\\$\w]/.test(s[at-1] || '') || /\s$/.test(d))) return all;
    const display = dd !== undefined || br !== undefined;
    list.push({tex: (dd ?? br ?? pa ?? d).trim(), display});
    return '' + (list.length-1) + '';
  });
  return {text, list};
}
const mathEsc = (s)=>s.replace(/[&<>"]/g, c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
function mathHtml(f){
  if(window.katex){
    try{ return katex.renderToString(f.tex, {displayMode: f.display, throwOnError: false, output: 'html'}); }
    catch(_){ return '<code>'+mathEsc(f.tex)+'</code>'; }
  }
  mathLoad();
  return '<span class="math-pending" data-d="'+(f.display?1:0)+'">'+mathEsc(f.tex)+'</span>';
}
let mathLoading = null;
function mathLoad(){
  if(mathLoading) return mathLoading;
  const css = document.createElement('link');
  css.rel = 'stylesheet'; css.href = 'katex/katex.min.css';
  document.head.appendChild(css);
  mathLoading = new Promise((ok, ko)=>{
    const s = document.createElement('script');
    s.src = 'katex/katex.min.js'; s.onload = ok; s.onerror = ko;
    document.head.appendChild(s);
  }).then(()=>{
    document.querySelectorAll('.math-pending').forEach(el=>{
      try{ katex.render(el.textContent, el, {displayMode: el.dataset.d === '1', throwOnError: false, output: 'html'}); }catch(_){}
      el.classList.remove('math-pending');
    });
  }).catch(()=>{ mathLoading = null; });
  return mathLoading;
}
// Les garde-fous du serveur ("[stop: trop d'appels d'outils]") sont concaténés
// au texte de la réponse : ils arrivent donc en clair, au milieu du markdown.
// markNotices les sort du fil pour qu'on ne les prenne pas pour une phrase du
// modèle. En cours de streaming, la parenthèse fermante manque encore → aucune
// correspondance, donc pas d'encart qui clignote à chaque token.
const NOTICE_RE=/\[stop\s*:\s*([^\]]+)\]/g;
function markNotices(root){
  const walk=document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const hits=[];
  while(walk.nextNode()){
    const n=walk.currentNode;
    // Un exemple dans un bloc de code reste du code.
    if(n.parentElement && n.parentElement.closest('pre,code')) continue;
    NOTICE_RE.lastIndex=0;
    if(NOTICE_RE.test(n.nodeValue)) hits.push(n);
  }
  hits.forEach(n=>{
    const frag=document.createDocumentFragment();
    let rest=n.nodeValue, m;
    NOTICE_RE.lastIndex=0;
    while((m=NOTICE_RE.exec(rest))!==null){
      if(m.index) frag.appendChild(document.createTextNode(rest.slice(0,m.index)));
      const tag=document.createElement('span');
      tag.className='stopnote'; tag.textContent=m[1].trim();
      frag.appendChild(tag);
      rest=rest.slice(m.index+m[0].length);
      NOTICE_RE.lastIndex=0;
    }
    if(rest) frag.appendChild(document.createTextNode(rest));
    n.parentNode.replaceChild(frag, n);
  });
}
let msgs = [];
let busy = false;
function toggleSide(){
  // Le menu glisse : la barre de défilement flottante (fixe à l'écran) ne doit pas
  // rester en plan au milieu pendant ce temps.
  const th=document.getElementById('side-thumb'); if(th){ th.style.transition='none'; th.classList.remove('on'); void th.offsetWidth; th.style.transition=''; }
  document.getElementById('side').classList.toggle('open'); document.getElementById('backdrop').classList.toggle('open'); document.body.classList.toggle('drawer-open'); }
// Le prompt système est désormais réglé PAR PRESET (modal de preset), plus dans le
// menu. Ces fonctions restent en no-op pour ne rien casser si un ancien handler les
// appelle encore.
function saveSys(){}
function loadSys(){}
