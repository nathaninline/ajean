// marked: GFM on, no auto-breaks (single newlines stay inline).
marked.setOptions({ gfm: true, breaks: false });
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
function md(src){ return src ? marked.parse(fixMd(src)) : ''; }
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
function toggleSide(){ document.getElementById('side').classList.toggle('open'); document.getElementById('backdrop').classList.toggle('open'); document.body.classList.toggle('drawer-open'); }
// Le prompt système est désormais réglé PAR PRESET (modal de preset), plus dans le
// menu. Ces fonctions restent en no-op pour ne rien casser si un ancien handler les
// appelle encore.
function saveSys(){}
function loadSys(){}
