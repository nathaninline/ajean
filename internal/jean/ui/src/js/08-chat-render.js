let stickyBottom = true;
const chatEl = () => document.getElementById('chat');
function isNearBottom(){
  const c = chatEl();
  return c.scrollHeight - c.scrollTop - c.clientHeight < 60;
}
function scrollMaybe(){
  // Pendant le replay initial on NE force AUCUN reflow : lire scrollHeight à chaque
  // événement rejoué = un layout synchrone forcé sur un DOM qui grossit → coût
  // quadratique (20-30 s de rendu au refresh sur un long fil). Le scroll est fait
  // une seule fois à la fin du replay, via jumpBottom() au signal {caught_up}.
  if(REPLAYING) return;
  if(stickyBottom){
    const c = chatEl();
    c.scrollTop = c.scrollHeight;
  }
  document.getElementById('scrollbtn').classList.toggle('show', !stickyBottom);
}
function jumpBottom(){ stickyBottom = true; scrollMaybe(); }
document.addEventListener('DOMContentLoaded', ()=>{
  const c = chatEl();
  c.addEventListener('scroll', ()=>{
    stickyBottom = isNearBottom();
    document.getElementById('scrollbtn').classList.toggle('show', !stickyBottom);
  });
});

function addMsg(role, text){
  const el=document.createElement('div');
  el.className='msg '+role;
  const collapsible = (role==='reasoning' || role==='tool');
  // .body must be a real block so <p>/<pre>/<ul> margins behave properly.
  if(collapsible){
    el.classList.add('collapsible');
    el.innerHTML='<span class="label">'+role+'</span><div class="bodywrap"><div class="body"></div></div>';
    el.querySelector('.label').onclick=()=>toggleCollapse(el);
  } else {
    el.innerHTML='<span class="label">'+role+'</span><div class="body"></div>';
  }
  el.querySelector('.body').textContent=text;
  chatEl().appendChild(el);
  scrollMaybe();
  return el;
}
// Bulle « … » animée affichée dès l'envoi, retirée au 1er token/outil/erreur.
function addTyping(){
  const el=document.createElement('div');
  el.className='msg assistant typing';
  el.innerHTML='<span></span><span></span><span></span>';
  chatEl().appendChild(el); scrollMaybe();
  return el;
}
// Replie/déplie en douceur les bulles reasoning/tool. Hauteur animée en JS :
// on fige scrollHeight puis on va à 0 (fermeture) ou de 0 vers scrollHeight
// (ouverture), sans jamais dépasser. overflow:hidden clippe pendant l'animation.
function collapseBody(el){
  const bw=el.querySelector('.bodywrap'); if(!bw || el.classList.contains('collapsed')) return;
  bw.style.height = bw.scrollHeight+'px';   // fige les dimensions courantes
  bw.style.width  = bw.scrollWidth+'px';
  void bw.offsetHeight;                      // reflow pour que la transition parte de là
  el.classList.add('collapsed');
  bw.style.height = '0px';                    // → anime height ET width vers 0
  bw.style.width  = '0px';
}
function expandBody(el){
  const bw=el.querySelector('.bodywrap'); if(!bw) return;
  el.classList.remove('collapsed');
  bw.style.height=''; bw.style.width='';      // mesure les dimensions naturelles…
  const h=bw.scrollHeight, w=bw.scrollWidth;
  bw.style.height='0px'; bw.style.width='0px';// …repart de 0 (pas de flash, même frame)
  void bw.offsetHeight;
  bw.style.height=h+'px'; bw.style.width=w+'px';
  const done=e=>{ if(e.propertyName!=='height') return; bw.style.height=''; bw.style.width=''; bw.removeEventListener('transitionend',done); };
  bw.addEventListener('transitionend',done);
}
function toggleCollapse(el){ el.classList.contains('collapsed') ? expandBody(el) : collapseBody(el); }
// Replie toutes les bulles d'un tour une fois la réponse finale entamée.
function collapseAll(list){ for(const el of list){ if(el) collapseBody(el); } list.length=0; }
// Replie une bulle INSTANTANÉMENT (sans animation) — utilisé pendant le replay au
// chargement pour que les vieilles bulles apparaissent déjà fermées. La classe
// 'collapsed' seule ne gère que l'opacité ; la hauteur est en style inline, donc
// on la met à 0 transition désactivée.
function collapseInstant(el){
  const bw=el.querySelector('.bodywrap'); if(!bw) return;
  el.classList.add('collapsed');
  // Pas de `void bw.offsetHeight` ici : la bulle vient d'être créée et n'a jamais
  // été peinte dépliée, donc poser height:0 n'anime pas — inutile de forcer un
  // reflow par bulle (ce qui, multiplié par le replay, coûtait très cher).
  bw.style.transition='none';
  bw.style.height='0px'; bw.style.width='0px';
  requestAnimationFrame(()=>{ bw.style.transition=''; });
}
function setLabel(el, text){ el.querySelector('.label').textContent = text; }
function bodyOf(el){ return el.querySelector('.body'); }
// Render markdown into a message body in place; safe because md() escapes HTML.
function renderBody(el, text){ const b=bodyOf(el); b.innerHTML = md(text); addCopyButtons(b); scrollMaybe(); }
// Render a tool call as its own conversation message: the command the model
// wrote, then the response it got back. textContent keeps it injection-safe.
function renderToolMsg(el, tu){
  // Métadonnées d'affichage par outil : nom court + en-tête avec icône. Les outils
  // web (web_search/open/read/grep) doivent afficher 🌐, pas le fallback mémoire.
  const META = {
    bash:       {lbl:'terminal',  head:'⚙️ commande'},
    edit:       {lbl:'édition',   head:'✏️ édition'},
    web_search: {lbl:'recherche', head:'🌐 recherche web'},
    web_open:   {lbl:'page web',  head:'🌐 ouverture'},
    web_read:   {lbl:'page web',  head:'🌐 lecture'},
    web_grep:   {lbl:'page web',  head:'🌐 recherche'},
  };
  const meta = META[tu.name] || {lbl:'mémoire', head:'🧠 mémoire'};
  let lbl = meta.lbl;
  // Indication du volume de la réponse de l'outil (~tokens, estimation 1 tok ≈ 4 car).
  if(tu.result){ lbl += '  ·  ~' + Math.max(1, Math.round(tu.result.length/4)) + ' tok'; }
  setLabel(el, lbl);
  const body=bodyOf(el); body.innerHTML='';
  const head=document.createElement('div'); head.className='tool-head';
  head.textContent = meta.head;
  body.appendChild(head);
  if(tu.label){
    const pre=document.createElement('pre'); pre.className='tool-cmd';
    const code=document.createElement('code'); code.textContent=tu.label;
    if(tu.typing){ const car=document.createElement('span'); car.className='tool-caret'; car.textContent='▋'; code.appendChild(car); }
    pre.appendChild(code); body.appendChild(pre);
  }
  const hasResult = tu.result!==undefined && tu.result!=='';
  if(hasResult){
    const sub=document.createElement('div'); sub.className='tool-sub'; sub.textContent='réponse';
    body.appendChild(sub);
    const pre=document.createElement('pre');
    const code=document.createElement('code'); code.textContent=tu.result;
    pre.appendChild(code); body.appendChild(pre);
  } else if(!tu.done && !tu.typing){
    const wait=document.createElement('div'); wait.className='tool-wait'; wait.textContent='⏳ exécution en cours…';
    body.appendChild(wait);
  }
  addCopyButtons(body); scrollMaybe();
}
// Inject a "copier" button into every <pre> code block (idempotent).
function addCopyButtons(root){
  root.querySelectorAll('pre').forEach(pre=>{
    if(pre.querySelector('.copybtn')) return;
    const btn=document.createElement('button');
    btn.className='copybtn'; btn.type='button'; btn.textContent='copier';
    btn.onclick=async(e)=>{
      e.stopPropagation();
      const code=pre.querySelector('code'), txt=(code||pre).innerText;
      try{ await navigator.clipboard.writeText(txt); }
      catch(_){ const ta=document.createElement('textarea'); ta.value=txt; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); ta.remove(); }
      btn.textContent='copié ✓'; btn.classList.add('done');
      setTimeout(()=>{ btn.textContent='copier'; btn.classList.remove('done'); },1500);
    };
    pre.appendChild(btn);
  });
}
// Nouvelle conversation POUR TOUS LES APPAREILS : le serveur vide le fil et
// diffuse un {reset} ; le flux d'abonnement nettoie alors l'affichage.
function resetChat(){ jfetch('/api/chat/reset',{method:'POST'}).catch(()=>{}); toast('nouvelle conversation'); }
// Compaction : on demande à l'IA un résumé de la conversation destiné à la
// reprendre dans une session neuve, puis on repart d'un contexte propre seedé
// avec ce résumé. Réduit drastiquement les tokens tout en gardant le fil.
// Compaction MANUELLE : le compactage est automatique (façon Hermes) quand le
// contexte se remplit, mais ce bouton permet de le déclencher à la demande. Le
// serveur possède la conversation : on lance la compaction côté serveur et la
// progression (bannière « compactage en cours », résultat) arrive par le flux
// d'abonnement, comme pour la génération — donc visible sur tous les appareils.
async function compactContext(){
  if(!await askConfirm('Résumer les anciens tours pour libérer du contexte ? La conversation continue normalement.', {title:'Compacter le contexte', okText:'Compacter'})) return;
  const btn=document.getElementById('ctx-compact'); btn.disabled=true;
  try{
    const r=await jfetch('/api/chat/compact',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
    const j=await r.json().catch(()=>({}));
    if(!j.ok) toast(j.error||'compaction indisponible');
  }catch(e){ toast('erreur : '+(e.message||e)); }
  btn.disabled=false;
}
// Persistance de la conversation : on garde user+assistant en localStorage pour
// survivre à un refresh (les bulles tool/reasoning sont éphémères, non stockées).
function saveChat(){ try{ localStorage.setItem('jean.chat', JSON.stringify(msgs)); }catch(e){} }
// Source de vérité = SERVEUR. Au chargement on ouvre le flux d'abonnement
// permanent (connectStream), qui rejoue tout le fil depuis le serveur — texte,
// appels d'outils, vitesses, raisonnement — puis suit le direct. Plus de
// localStorage : le même contexte est partagé par tous les appareils.
// Source de vérité = SERVEUR : on ouvre le flux d'abonnement permanent qui rejoue
// tout le fil (texte, outils, vitesses via les horodatages serveur, raisonnement)
// puis suit le direct. Partagé par tous les appareils.
function restoreChat(){
  // On masque le chat le temps du replay pour ne pas voir défiler le haut puis
  // sauter en bas (effet de clignotement). Il est révélé, positionné en bas, au
  // signal {caught_up}. Filet de sécurité : révélé quoi qu'il arrive après 2s.
  const c=chatEl(); c.style.opacity='0';
  // Si {caught_up} tarde au-delà de 2s (replay anormalement long), on révèle quand
  // même — et on saute en bas DIRECTEMENT (scrollMaybe est neutralisé tant que
  // REPLAYING, donc on force ici le positionnement).
  setTimeout(()=>{ c.style.transition='opacity .15s'; c.style.opacity='1'; c.scrollTop=c.scrollHeight; }, 2000);
  connectStream();
}
function onKey(e){ if(e.key==='Enter' && !e.shiftKey){ e.preventDefault(); send(); } }
