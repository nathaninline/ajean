let stickyBottom = true;
const chatEl = () => document.getElementById('chat');
function isNearBottom(){
  const c = chatEl();
  return c.scrollHeight - c.scrollTop - c.clientHeight < 60;
}
// La zone de chat réserve une gouttière de barre de défilement de chaque côté
// (scrollbar-gutter: stable both-edges) ; le composer, lui, n'est pas défilant.
// On mesure la gouttière réelle et on la reporte sur le composer, sinon les
// messages sont en retrait par rapport à la zone de saisie — décalage visible
// surtout quand la barre latérale est escamotée.
function syncGutter(){
  const chat=document.getElementById('chat'); if(!chat) return;
  const g=Math.max(0,(chat.offsetWidth-chat.clientWidth)/2);
  document.documentElement.style.setProperty('--sbw', g+'px');
}
addEventListener('resize', syncGutter);
addEventListener('DOMContentLoaded', syncGutter);
// Le composeur flotte au-dessus du fil et sa hauteur VARIE (saisie multi-lignes,
// pièces jointes en attente). On la mesure et on la reporte en variable CSS pour
// que le padding bas du fil dégage toujours exactement la carte — sinon, sur une
// conversation courte pas encore défilable, le texte se glissait sous le composeur
// sans qu'on puisse le faire remonter. On re-scrolle après coup si on était collé
// en bas (le padding qui change déplace le bas).
// Mesure la hauteur RÉELLE du composeur → --composer-h. Le composeur est en
// superposition (absolu) : #chat réserve cette hauteur (+ marge) en padding-bas
// pour que le fil défile derrière la carte sans que la fin se cache dessous.
function syncComposerPad(){
  const comp=document.getElementById('composer'); if(!comp) return;
  document.documentElement.style.setProperty('--composer-h', comp.offsetHeight+'px');
  scrollMaybe();
}
// Initialise la mesure de la hauteur du composeur de façon ROBUSTE sur iOS Safari.
// Piège : au retour via le cache page (bfcache) ou selon le timing, `DOMContentLoaded`
// peut ne PAS se redéclencher → sans ça `--composer-h` restait à sa valeur par
// défaut (150px), souvent plus petite que le composeur réel (safe-area, nom du
// preset sur 2 lignes…), donc la fin de la réponse se cachait sous la carte de
// saisie. On (re)mesure sur tous les points d'entrée + quelques filets différés
// (polices/statut chargés tard), et l'observateur est posé une seule fois.
function initComposerPad(){
  syncComposerPad();
  const comp=document.getElementById('composer');
  if(comp && window.ResizeObserver && !comp._roPad){ comp._roPad=new ResizeObserver(syncComposerPad); comp._roPad.observe(comp); }
  setTimeout(syncComposerPad, 300);
  setTimeout(syncComposerPad, 1200);
}
addEventListener('resize', syncComposerPad);
addEventListener('orientationchange', syncComposerPad);
addEventListener('DOMContentLoaded', initComposerPad);
addEventListener('load', initComposerPad);
addEventListener('pageshow', initComposerPad); // iOS : rechargement depuis le bfcache

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
  el.innerHTML='<span class="dot"></span>';
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
// Ajoute « +N -N » colorés à l'étiquette d'une bulle. L'étiquette reste visible
// une fois la bulle repliée : c'est le seul endroit où le volume d'une écriture
// survit au repli, donc on le met là plutôt que dans le corps seul.
function setLabelCounts(el, add, del){
  const lab=el.querySelector('.label');
  const cnt=document.createElement('span'); cnt.className='diff-count';
  if(add) cnt.appendChild(Object.assign(document.createElement('span'),{className:'a',textContent:'+'+add}));
  if(add && del) cnt.appendChild(document.createTextNode(' '));
  if(del) cnt.appendChild(Object.assign(document.createElement('span'),{className:'d',textContent:'-'+del}));
  lab.appendChild(cnt);
}
// Ligne de mesures sous une réponse (prefill / decode). Les étiquettes VOUS/AJEAN
// sont masquées dans cette mise en page, donc les chiffres qu'on y écrivait
// avaient disparu : ils ont leur propre ligne, discrète, sous le texte. Toujours
// affichée (plus de réglage pour la cacher).
function setStats(el, text){
  if(!el) return;
  let s = el.querySelector(':scope > .statline');
  if(!s){
    s=document.createElement('div'); s.className='statline';
    // Apparition en fondu, à la PREMIÈRE pose seulement : la ligne arrive une fois
    // la réponse finie, un surgissement sec accrochait l'œil. Les mises à jour
    // suivantes ne rejouent pas l'animation (elle clignoterait), et le rejeu du
    // journal au chargement n'anime rien du tout.
    if(!(typeof REPLAYING!=='undefined' && REPLAYING)) s.classList.add('statline-in');
    el.appendChild(s);
  }
  s.textContent = text;
}
function bodyOf(el){ return el.querySelector('.body'); }
// Render markdown into a message body in place; safe because md() escapes HTML.
function renderBody(el, text){ const b=bodyOf(el); b.innerHTML = md(encodeMdLinkSpaces(text)); markNotices(b); addCopyButtons(b); markFileLinks(b); scrollMaybe(); }
// Render a tool call as its own conversation message: the command the model
// wrote, then the response it got back. textContent keeps it injection-safe.
function renderToolMsg(el, tu){
  // Métadonnées d'affichage par outil : nom court + en-tête. Les outils web
  // (web_search/open/read/grep) ont leur propre libellé, pas le fallback mémoire.
  const META = {
    bash:       {lbl:'terminal',  head:'commande'},
    write:      {lbl:'fichier',   head:'écriture'},
    edit:       {lbl:'édition',   head:'édition'},
    web_search: {lbl:'recherche', head:'recherche web'},
    web_open:   {lbl:'page web',  head:'ouverture'},
    web_read:   {lbl:'page web',  head:'lecture'},
    web_grep:   {lbl:'page web',  head:'recherche'},
    mem_search: {lbl:'mémoire',   head:'recherche mémoire'},
    mem_read:   {lbl:'mémoire',   head:'lecture mémoire'},
    mem_add:    {lbl:'mémoire',   head:'nouvelle page'},
    mem_edit:   {lbl:'mémoire',   head:'édition mémoire'},
    mem_delete: {lbl:'mémoire',   head:'suppression mémoire'},
    task_list:  {lbl:'tâches',    head:'tâches planifiées'},
    task_create:{lbl:'tâche',     head:'nouvelle tâche'},
    task_update:{lbl:'tâche',     head:'modification tâche'},
    task_delete:{lbl:'tâche',     head:'suppression tâche'},
    see_image:  {lbl:'image',     head:'vision'},
    machines_list:{lbl:'machines', head:'postes disponibles'},
    machines_use: {lbl:'machines', head:'bascule de machine'},
  };
  // Outils MCP (nom mcp__<serveur>__<outil>) : en-tête = nom du serveur, libellé lisible,
  // pas le fallback générique. On extrait serveur et outil du nom namespacé.
  let meta = META[tu.name];
  if(!meta && tu.name && tu.name.indexOf('mcp__')===0){
    const parts = tu.name.slice(5).split('__');
    const server = parts.shift() || 'mcp';
    const tool = parts.join('__') || tu.name;
    meta = {lbl: tool, head: server};
  }
  meta = meta || {lbl:'outil', head:'outil'};
  let lbl = meta.lbl;
  // Indication du volume de la réponse de l'outil (~tokens, estimation 1 tok ≈ 4 car).
  if(tu.result){ lbl += '  ·  ~' + Math.max(1, Math.round(tu.result.length/4)) + ' tok'; }
  setLabel(el, lbl);
  // Volume de l'écriture (final si le diff est là, provisoire pendant la frappe)
  // reporté sur l'étiquette, pour rester lisible bulle repliée.
  let add=0, del=0;
  if(tu.diff && tu.diff.length){ tu.diff.forEach(l=>{ if(l.op==='+') add++; else if(l.op==='-') del++; }); }
  else if(tu.body){ add=tu.body.split('\n').length; }
  if(add||del) setLabelCounts(el, add, del);
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
  // Écriture EN COURS : le modèle tape encore le contenu. On l'affiche ligne à
  // ligne, dans la même forme que le diff final, pour que la bulle se remplisse
  // sous les yeux au lieu de rester vide puis de s'ouvrir d'un coup. Seule la
  // dernière ligne est « fraîche » (fondu) : réanimer tout à chaque événement
  // ferait clignoter le bloc entier.
  if(tu.body && !(tu.diff && tu.diff.length)){
    const lines=tu.body.split('\n');
    const sub=document.createElement('div'); sub.className='tool-sub';
    sub.textContent='écriture en cours'; // le +N vit sur l'étiquette (visible repliée)
    body.appendChild(sub);
    const pre=document.createElement('pre'); pre.className='diff live';
    lines.forEach((t,i)=>{
      const ln=document.createElement('span');
      ln.className='dl add'+(i===lines.length-1?' fresh':'');
      ln.textContent='+ '+t;
      if(i===lines.length-1 && tu.typing){
        const car=document.createElement('span'); car.className='tool-caret'; car.textContent='▋';
        ln.appendChild(car);
      }
      pre.appendChild(ln);
    });
    body.appendChild(pre);
    // Le bloc est re-créé à chaque événement : on le recale en bas pour suivre
    // la ligne en cours (max-height côté CSS l'empêche de pousser le fil).
    pre.scrollTop = pre.scrollHeight;
  }
  // Diff d'une écriture (fichier ou page de mémoire) : lignes ajoutées en vert,
  // retirées en rouge, contexte en gris — comme un diff de terminal.
  if(tu.diff && tu.diff.length){
    const sub=document.createElement('div'); sub.className='tool-sub';
    sub.textContent='modifications'; // le +N -N vit sur l'étiquette (visible repliée)
    body.appendChild(sub);
    const pre=document.createElement('pre'); pre.className='diff';
    tu.diff.forEach(l=>{
      const ln=document.createElement('span');
      ln.className='dl'+(l.op==='+'?' add':l.op==='-'?' del':'');
      ln.textContent=(l.op==='+'?'+':l.op==='-'?'-':' ')+' '+l.text;
      pre.appendChild(ln);
    });
    body.appendChild(pre);
  }
  const hasResult = tu.result!==undefined && tu.result!=='';
  if(hasResult){
    const sub=document.createElement('div'); sub.className='tool-sub'; sub.textContent='réponse';
    body.appendChild(sub);
    const pre=document.createElement('pre');
    const code=document.createElement('code'); code.textContent=tu.result;
    pre.appendChild(code); body.appendChild(pre);
  } else if(!tu.done && !tu.typing){
    const wait=document.createElement('div'); wait.className='tool-wait'; wait.textContent='exécution en cours…';
    body.appendChild(wait);
  }
  addCopyButtons(body); scrollMaybe();
}
// Inject a "copier" button into every <pre> code block (idempotent).
function addCopyButtons(root){
  root.querySelectorAll('pre').forEach(pre=>{
    if(pre.querySelector('.copybtn')) return;
    // Pas de bouton copier sur un diff : on copierait les préfixes + / - .
    if(pre.classList.contains('diff')) return;
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
// ===== Historique des conversations (issue #33) ============================
// « clear chat » archive la conversation côté serveur ; ce modal les liste, avec
// pour chacune « recharger » (elle redevient active, la courante étant archivée à
// son tour) et « supprimer » (définitif).
function openHistoryModal(){ showModal('history-modal'); loadHistory(); }
function closeHistoryModal(){ hideModal('history-modal'); }
function fmtHistDate(ms){
  const d = new Date(ms||0);
  try{ return d.toLocaleString([], {dateStyle:'medium', timeStyle:'short'}); }
  catch(_){ return d.toLocaleString(); }
}
async function loadHistory(){
  const box = document.getElementById('history-list'); if(!box) return;
  box.innerHTML = '<span class="muted" style="font-size:12px">chargement…</span>';
  let list = [];
  try{ const r = await jget('/api/chat/history'); list = (r && r.conversations) || []; }
  catch(_){ box.innerHTML = '<span class="muted" style="font-size:12px">erreur de chargement</span>'; return; }
  if(!list.length){ box.innerHTML = '<span class="muted" style="font-size:12px">aucune conversation archivée. « clear chat » en rangera une ici.</span>'; return; }
  box.innerHTML = '';
  for(const c of list){
    const row = document.createElement('div'); row.className = 'mcp-row'; row.style.cursor = 'default';
    // Étoile favori : épingle la conversation en tête de liste. Teintée accent
    // quand active (pas de vert — préférence).
    // Boutons compacts en icônes (info-bulle au survol) pour ne pas déborder.
    const iconBtn = (glyph, title, onclick, extra)=>{
      const b = document.createElement('button');
      b.textContent = glyph; b.title = title; b.onclick = onclick;
      b.style.cssText = 'padding:2px 6px;font-size:14px;line-height:1;background:none;border:none;cursor:pointer;color:var(--dim);' + (extra||'');
      return b;
    };
    const info = document.createElement('div'); info.className = 'mcp-info'; info.style.minWidth = '0';
    const name = document.createElement('div'); name.className = 'mcp-name';
    // Étoile pleine (lecture seule) en tête d'un favori ; on l'épingle/dépingle
    // depuis le modal de renommage.
    name.textContent = (c.fav?'★ ':'') + (c.title || 'Conversation');
    name.style.cssText = 'overflow:hidden;text-overflow:ellipsis;white-space:nowrap' + (c.fav?';color:var(--accent)':'');
    const meta = document.createElement('div'); meta.className = 'mcp-meta';
    const sub = document.createElement('span'); sub.style.cssText = 'font-size:11px;color:var(--dim)';
    const n = c.turns || 0;
    sub.textContent = fmtHistDate(c.saved_at) + ' · ' + n + ' message' + (n>1?'s':'');
    meta.appendChild(sub);
    info.appendChild(name); info.appendChild(meta);
    const load = iconBtn('↺', 'Recharger cette conversation', ()=>restoreHistory(c.id));
    const ren = iconBtn('✎', 'Renommer / favori', ()=>renameHistory(c.id, c.title, c.fav));
    const del = iconBtn('✕', 'Supprimer définitivement', ()=>deleteHistory(c.id, c.title));
    row.appendChild(info); row.appendChild(load); row.appendChild(ren); row.appendChild(del);
    box.appendChild(row);
  }
}
// Renommer + (dé)favoriser dans le même modal : le champ nom + une case « favori »
// pré-cochée selon l'état courant.
async function renameHistory(id, current, wasFav){
  wasFav = !!wasFav;
  const name = await askPrompt('Nom de la conversation (laisser vide pour le nom automatique) :', {title:'Renommer la conversation', okText:'Enregistrer', default: current||'', placeholder:'ex. Config serveur GPU', check:'Favori (épinglé en tête)', checkOn:wasFav});
  if(name===null) return; // annulé
  const fav = askChecked();
  try{
    const r = await jpost('/api/chat/history/rename', {id, title:name});
    if(!r.ok){ toast(r.error || 'renommage impossible'); return; }
    if(fav!==wasFav){ await jpost('/api/chat/history/fav', {id, fav}); }
  }catch(_){ toast('erreur réseau'); return; }
  loadHistory();
}
// Supprime toutes les conversations SAUF les favoris.
async function clearAllHistory(){
  if(!await askConfirm('Supprimer toutes les conversations archivées, sauf les favoris ? Cette action est irréversible.', {title:'Tout supprimer', okText:'Tout supprimer', danger:true})) return;
  let r; try{ r = await jpost('/api/chat/history/clear', {}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'suppression impossible'); return; }
  toast((r.deleted||0) + ' conversation' + ((r.deleted>1)?'s':'') + ' supprimée' + ((r.deleted>1)?'s':''));
  loadHistory();
}
async function restoreHistory(id){
  let r; try{ r = await jpost('/api/chat/history/restore', {id}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'impossible de recharger'); return; }
  closeHistoryModal();
  toast('conversation rechargée');
}
async function deleteHistory(id, title){
  if(!await askConfirm('Supprimer définitivement « ' + (title || 'cette conversation') + ' » ? Cette action est irréversible.', {title:'Supprimer la conversation', okText:'Supprimer', danger:true})) return;
  let r; try{ r = await jpost('/api/chat/history/delete', {id}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'suppression impossible'); return; }
  loadHistory();
}
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
function saveChat(){ try{ localStorage.setItem('ajean.chat', JSON.stringify(msgs)); }catch(e){} }
// Source de vérité = SERVEUR. Au chargement on ouvre le flux d'abonnement
// permanent (connectStream), qui rejoue tout le fil depuis le serveur — texte,
// appels d'outils, vitesses, raisonnement — puis suit le direct. Plus de
// localStorage : le même contexte est partagé par tous les appareils.
// Source de vérité = SERVEUR : on ouvre le flux d'abonnement permanent qui rejoue
// tout le fil (texte, outils, vitesses via les horodatages serveur, raisonnement)
// puis suit le direct. Partagé par tous les appareils.
// Voile de chargement du fil. setChatLoading(null) le masque, setChatLoading(txt)
// l'affiche avec ce libellé (« chargement… » au départ, « connexion au serveur… »
// si le flux tombe). Sans lui, une connexion lente affiche un chat vide qu'on ne
// distingue pas d'une conversation réellement vide.
function setChatLoading(msg){
  const el=document.getElementById('chat-loading');
  if(!el) return;
  if(!msg){ el.classList.remove('show'); return; }
  document.getElementById('chat-loading-text').textContent=msg;
  el.classList.add('show');
}
// --- Accueil du fil vide ---------------------------------------------------
// Le logo n'est pas dupliqué dans le HTML : on clone celui de la barre latérale
// (#brand) en retirant ses id (un id ne peut exister qu'une fois) et le numéro
// de version. Les couleurs sont reprises par les classes .ce-*.
function fillEmptyLogo(){
  const box=document.getElementById('ce-logo'), brand=document.getElementById('brand');
  if(!box || !brand || box.childElementCount) return;
  ['brand-a','brand-word'].forEach(id=>{
    const src=brand.querySelector('#'+id); if(!src) return;
    const el=src.cloneNode(true); el.removeAttribute('id');
    if(id==='brand-word') el.classList.add('ce-word');
    box.appendChild(el);
  });
}
// Affiché seulement quand le fil ne contient AUCUNE bulle et que le replay est
// terminé — sinon il apparaîtrait une fraction de seconde à chaque chargement,
// juste avant que les messages rejoués n'arrivent.
function syncChatEmpty(){
  const box=document.getElementById('chat-empty'); if(!box) return;
  fillEmptyLogo();
  const empty = !REPLAYING && !chatEl().querySelector('.msg');
  box.classList.toggle('show', empty);
}
document.addEventListener('DOMContentLoaded', ()=>{
  const c=chatEl(); if(!c) return;
  // Le fil est peuplé par des dizaines de chemins différents (replay, direct,
  // reset, effacement). On observe donc le DOM plutôt que d'appeler la synchro
  // depuis chacun d'eux — le coût est nul, le callback est groupé et sort tout
  // de suite pendant le replay.
  new MutationObserver(()=>syncChatEmpty()).observe(c, {childList:true});
  syncChatEmpty();
});
function restoreChat(){
  // On masque le chat le temps du replay pour ne pas voir défiler le haut puis
  // sauter en bas (effet de clignotement). Il est révélé, positionné en bas, au
  // signal {caught_up}. Filet de sécurité : révélé quoi qu'il arrive après 2s.
  const c=chatEl(); c.style.opacity='0';
  setChatLoading('chargement de la conversation…');
  // Si {caught_up} tarde au-delà de 2s (replay anormalement long), on révèle quand
  // même — et on saute en bas DIRECTEMENT (scrollMaybe est neutralisé tant que
  // REPLAYING, donc on force ici le positionnement). Le voile, lui, RESTE : tant
  // que le replay n'est pas fini, ce qui est affiché est incomplet et il faut le
  // dire. Il finit de toute façon par tomber au {caught_up} ou au filet de 15s.
  setTimeout(()=>{ c.style.transition='opacity .15s'; c.style.opacity='1'; c.scrollTop=c.scrollHeight; }, 2000);
  setTimeout(()=>{ setChatLoading(null); }, 15000);
  connectStream();
}
function onKey(e){ if(e.key==='Enter' && !e.shiftKey){ e.preventDefault(); send(); } }
// La zone de saisie s'ajuste à son contenu : une ligne au repos, puis elle
// grandit jusqu'à sa max-height (au-delà, elle défile). Appelée à la frappe, à
// l'envoi et au chargement.
function autoGrow(ta){
  ta = ta || document.getElementById('input');
  if(!ta) return;
  ta.style.height='auto';
  const max=parseInt(getComputedStyle(ta).maxHeight,10)||200;
  ta.style.height=Math.min(ta.scrollHeight, max)+'px';
  ta.style.overflowY = ta.scrollHeight>max ? 'auto' : 'hidden';
}
