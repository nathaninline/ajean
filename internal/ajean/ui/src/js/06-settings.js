// --- Réserve de hauteur des blocs peuplés par le réseau ---------------------
// Ces blocs sont vides (ou réduits à « … ») tant que le serveur n'a pas répondu,
// puis prennent leur vraie taille : tout le menu sautait alors d'un cran, ce qui
// se voyait surtout sur les sections du bas (« Moteur »). On mémorise leur
// hauteur d'une session à l'autre et on la réserve au démarrage ; la réserve
// tombe dès que le vrai contenu est là (elle ne fait que TENIR la place, elle
// n'affiche jamais rien de faux).
const HRESERVE=['cfg','vram','ram-details','presets','internet-status','mcp-list'];
function reserveHeights(){
  HRESERVE.forEach(id=>{
    try{
      const h=parseInt(localStorage.getItem('ajean-h-'+id)||'0',10);
      const el=document.getElementById(id);
      if(el && h>0) el.style.minHeight=h+'px';
    }catch(e){}
  });
}
function releaseHeights(){
  HRESERVE.forEach(id=>{
    const el=document.getElementById(id); if(!el) return;
    el.style.minHeight='';
    try{ localStorage.setItem('ajean-h-'+id, String(el.offsetHeight||0)); }catch(e){}
  });
}
document.addEventListener('DOMContentLoaded', reserveHeights);
// Numéro (1-based) du preset vers lequel on bascule, tant que le serveur n'a pas
// fini de redémarrer le service. Basculer prend plusieurs secondes : sans ça, la
// liste restait FIGÉE sur l'ancien actif et on ne savait pas si le clic avait pris.
// Voir switchTo() dans 07-models.js.
let pendingPreset = 0;
// Dernière sélection PEINTE. La liste est redessinée à chaque rafraîchissement
// (et il y en a beaucoup : sondage de bascule, loadAll…) ; sans ce repère, les
// animations d'entrée (barre qui glisse, puce qui apparaît) repartaient à chaque
// redessin et la puce semblait clignoter en boucle. On ne les rejoue que quand la
// sélection CHANGE vraiment.
let paintedSel = '';
async function loadPresets(){
  const p=await jget('/api/presets');
  // Bascule terminée : le preset visé est devenu l'actif, on éteint l'attente.
  if(pendingPreset && p[pendingPreset-1] && p[pendingPreset-1].active) pendingPreset = 0;
  const act = p.find(x=>x.active);
  // Build via DOM (not string concat) so preset names can contain anything —
  // spaces, accents, quotes, < > & — without breaking markup or handlers.
  const sp = document.getElementById('status-preset');
  sp.textContent = act ? act.name : '';
  sp.title = act ? 'preset actif' : '';
  const cont=document.getElementById('presets');
  cont.innerHTML='';
  if(!p.length){ cont.innerHTML='<span class="muted">(aucun)</span>'; return; }
  // Signature de la sélection : quel preset est actif, et vers lequel on bascule.
  const sel = (act?act.id:'')+'|'+pendingPreset;
  const moved = sel !== paintedSel; paintedSel = sel;
  p.forEach((x,i)=>{
    const row=document.createElement('div');
    const pend = !x.active && pendingPreset===i+1;
    row.className='preset'+(x.active?' active':'')+(pend?' pending':'')+(moved?' sel-anim':'');
    // Réordonnable à la souris (SortableJS, init plus bas). data-id sert à relire
    // l'ordre après un déplacement.
    row.dataset.id = x.id;
    // Guard : un glissement ne doit pas être pris pour un clic qui bascule le preset.
    row.onclick=()=>{ if(presetJustDragged) return; switchTo(i+1, x.name); };
    const info=document.createElement('div'); info.className='preset-info';
    const nm=document.createElement('div'); nm.className='preset-name';
    // Puce de l'actif : un ÉLÉMENT rond en CSS, pas le caractère « ● ». Le glyphe
    // est dessiné par la police du système — sur Windows il sortait plus petit et
    // plus bas que sur macOS. Un disque CSS a la même taille et la même assiette
    // partout.
    // Puce réservée à l'actif POUR DE BON : pendant la bascule, seule la barre
    // orange parle ; la puce apparaît quand la barre passe au blanc.
    if(x.active){ const d=document.createElement('i'); d.className='preset-dot'; nm.appendChild(d); }
    nm.appendChild(document.createTextNode(x.name)); nm.title=x.name;
    // Second row: quant tag + bench perf, so the title row stays full-width.
    const meta=document.createElement('div'); meta.className='preset-meta';
    if(x.quant){
      const q=document.createElement('span'); q.className='qtag';
      q.textContent=x.quant; q.title='quantization';
      meta.appendChild(q);
    }
    if(x.ctx){
      const c=document.createElement('span'); c.className='ctag';
      c.title='taille du contexte';
      c.textContent=x.ctx;
      meta.appendChild(c);
    }
    if(x.bench){
      const bt=document.createElement('span'); bt.className='btag';
      bt.title='prefill / decode — dernier bench de ce preset';
      bt.textContent=x.bench.prefill.toFixed(0)+'-'+x.bench.decode.toFixed(0)+' t/s';
      meta.appendChild(bt);
    }
    // Bulle « capacités » à droite des autres : icônes vision (œil) et raisonnement
    // (ampoule), regroupées dans une seule pastille comme les autres tags.
    const caps=[], capTitle=[];
    if(x.vision){
      caps.push('<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>');
      capTitle.push('vision (analyse d\'images)');
    }
    if(x.reasoning){
      caps.push('<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18h6"/><path d="M10 21h4"/><path d="M12 3a6 6 0 0 0-4 10.5c.6.6 1 1.4 1 2.5h6c0-1.1.4-1.9 1-2.5A6 6 0 0 0 12 3z"/></svg>');
      capTitle.push('raisonnement'+(typeof x.reasoning==='string'?' ('+x.reasoning+')':''));
    }
    if(caps.length){
      const cap=document.createElement('span'); cap.className='captag';
      cap.innerHTML=caps.join('');
      cap.title=capTitle.join(' · ');
      meta.appendChild(cap);
    }
    info.appendChild(nm);
    if(meta.children.length) info.appendChild(meta);
    const edit=document.createElement('button');
    // Engrenage (et non plus un crayon) : la silhouette est ronde, donc elle se
    // lit comme centrée dans son coin, là où le crayon penché tirait de travers.
    edit.className='preset-edit'; edit.title='réglages du preset';
    edit.innerHTML='<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 11-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 11-2.83-2.83l.06-.06a1.65 1.65 0 00.33-1.82 1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 112.83-2.83l.06.06a1.65 1.65 0 001.82.33H9a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 112.83 2.83l-.06.06a1.65 1.65 0 00-.33 1.82V9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"/></svg>';
    edit.onclick=(e)=>{ e.stopPropagation(); openPreset(x.id); };
    // Pas de poignée : la ligne entière est déplaçable, on attrape où on veut.
    row.appendChild(info); row.appendChild(edit);
    cont.appendChild(row);
  });
  initPresetSortable(cont);
}
// ─── Réordonnancement des presets (SortableJS) ───────────────────────────────
// Glissement fluide (animation de poussée des voisins), ordre persisté côté
// serveur (/api/presets/order). SortableJS est vendorisé dans l'UI (00-sortable).
let presetJustDragged = false;
let presetSortable = null;
function initPresetSortable(cont){
  if(typeof Sortable==='undefined') return; // lib absente : on garde la liste simple
  // Une seule instance, attachée au conteneur (qui persiste entre les rendus).
  if(presetSortable) return;
  presetSortable = Sortable.create(cont, {
    animation: 160,
    easing: 'cubic-bezier(.2,.7,.3,1)',
    // forceFallback : Sortable gère lui-même le clone qui suit le curseur, au lieu
    // de l'image native du navigateur (rendue semi-transparente, non stylable).
    // Le clone (.preset-drag) reste donc PLEIN, comme la ligne d'origine.
    forceFallback: true,
    fallbackTolerance: 3,
    // Il faut MAINTENIR l'appui avant que le glissement démarre : sinon, sur mobile,
    // un simple défilement attrapait un preset. Le délai ne s'applique qu'au toucher
    // (souris immédiate), et un petit mouvement pendant le délai est toléré.
    delay: 220,
    delayOnTouchOnly: true,
    touchStartThreshold: 6,
    ghostClass: 'preset-ghost',   // l'emplacement cible laissé dans la liste
    chosenClass: 'preset-chosen', // l'élément saisi
    dragClass: 'preset-drag',     // le clone plein qui suit le curseur
    onStart(){ presetJustDragged = true; },
    onEnd(){
      // Un léger délai pour que le clic de fin de drag ne bascule pas le preset.
      setTimeout(()=>{ presetJustDragged = false; }, 60);
      const ids=[...cont.children].map(r=>r.dataset.id).filter(Boolean);
      jpost('/api/presets/order', {ids}).catch(()=>{});
    },
  });
}
// « Mode agent » = accès machine + skills réunis en un seul interrupteur.
// Quand il est actif, un « a » blanc apparaît en fondu devant « ajean » → « ajean ».
async function loadAgent(){
  const s=await jget('/api/agent');
  const on = s.enabled;
  document.getElementById('agent-toggle').checked = on;
  document.getElementById('compact-toggle').checked = (s.compact !== false);
  document.getElementById('machines-toggle').checked = !!s.machines;
  setBadge('agent-badge', on, on?'actif':'inactif');
  document.getElementById('brand').classList.toggle('agent', on);
  setAgentGate(on);
  if(s.mem_mode){ document.getElementById('mem-mode').value = s.mem_mode; renderMemModeDesc(s.mem_mode); }
  memPages = (s.pages || s.skills || []).slice().sort((a,b)=>a.name.localeCompare(b.name));
  memShown = MEM_PAGE;
  document.getElementById('mem-count').textContent = memPages.length ? '('+memPages.length+')' : '';
  document.getElementById('mem-search-row').style.display = memPages.length > MEM_PAGE ? '' : 'none';
  renderMemList();
}
// Mémoire + accès internet sont des sous-réglages du mode agent : sans agent, ni
// les outils mem_* ni les outils web ne sont fournis (voir globalCaps côté Go). On
// grise donc ces blocs quand l'agent est off pour que l'UI ne mente pas.
function setAgentGate(on){
  ['mem-block','net-block','mcp-block','param-block'].forEach(id=>{
    const el=document.getElementById(id); if(el) el.classList.toggle('gated', !on);
  });
}
// Repli/dépli de la liste des pages mémoire (fermée par défaut → gagne de la place).
function toggleMemPages(){
  const body=document.getElementById('mem-pages-body');
  const bar=document.getElementById('mem-pages-bar');
  const open=body.hasAttribute('hidden');
  if(open){ body.removeAttribute('hidden'); bar.classList.add('open'); }
  else { body.setAttribute('hidden',''); bar.classList.remove('open'); }
}
// Liste mémoire scalable : recherche + rendu plafonné (les milliers de pages ne
// déroulent plus une barre géante). memShown grimpe par paliers via « voir plus ».
let memPages=[], memShown=0; const MEM_PAGE=50;
function renderMemList(){
  const q=(document.getElementById('mem-search').value||'').trim().toLowerCase();
  const list=document.getElementById('mem-list');
  list.textContent='';
  if(!memPages.length){ list.innerHTML='<div class="muted">(aucune page mémoire)</div>'; return; }
  const matches = q ? memPages.filter(x=>(x.name+' '+(x.desc||'')).toLowerCase().includes(q)) : memPages;
  if(!matches.length){ list.innerHTML='<div class="muted">(aucun résultat pour « '+q.replace(/[<>&]/g,'')+' »)</div>'; return; }
  const shown = matches.slice(0, memShown);
  shown.forEach(x=>{
    const row=document.createElement('div'); row.className='preset'; row.style.fontSize='12px';
    row.onclick=()=>openMem(x.name);
    const span=document.createElement('span');
    const b=document.createElement('b'); b.style.color='var(--text)'; b.textContent=x.name; span.appendChild(b);
    if(x.desc){ const d=document.createElement('span'); d.className='muted'; d.textContent=' — '+x.desc; span.appendChild(d); }
    const btn=document.createElement('button'); btn.textContent='edit'; btn.style.cssText='margin:0;padding:2px 8px;font-size:11px';
    btn.onclick=e=>{ e.stopPropagation(); openMem(x.name); };
    row.appendChild(span); row.appendChild(btn); list.appendChild(row);
  });
  if(matches.length > shown.length){
    const more=document.createElement('div'); more.className='mem-more';
    more.textContent='+ voir plus ('+(matches.length-shown.length)+' de plus)';
    more.style.cursor='pointer';
    more.onclick=()=>{ memShown+=MEM_PAGE; renderMemList(); };
    list.appendChild(more);
  }
}
// alias : plusieurs appelants rafraîchissent juste la liste des pages mémoire
const loadMem = loadAgent;
async function toggleAgent(){
  const on=document.getElementById('agent-toggle').checked;
  if(on && !await askConfirm("L'IA aura un accès shell complet au serveur (bash) et pourra lire/écrire sa mémoire.", {title:'Activer le mode agent ?', okText:'Activer', danger:true})){ document.getElementById('agent-toggle').checked=false; return; }
  await jpost('/api/agent/toggle',{on});
  loadAgent();
}
async function toggleCompact(){
  const on=document.getElementById('compact-toggle').checked;
  await jpost('/api/agent/compact',{on});
}
// Gestion autonome des machines (postes) : donne à l'IA les outils machines_*
// et le briefing. Off par défaut, aucun effet quand décoché.
async function toggleMachines(){
  const on=document.getElementById('machines-toggle').checked;
  await jpost('/api/agent/machines',{on});
}
// Mode mémoire (3 états) — indépendant du mode agent.
const MEM_DESC={
  always:'L\'IA cherche dans sa mémoire avant de répondre et sauve d\'elle-même ce qui mérite d\'être retenu.',
  ondemand:'Les outils mémoire existent mais l\'IA ne les utilise QUE si tu le demandes (« souviens-toi de… », « qu\'avais-tu retenu sur… »).',
  off:'Mémoire coupée : aucun accès en lecture ni écriture, l\'IA répond sans mémoire.'
};
function renderMemModeDesc(m){ const d=document.getElementById('mem-mode-desc'); if(d) d.textContent=MEM_DESC[m]||''; }
async function setMemMode(){
  const mode=document.getElementById('mem-mode').value;
  const r=await jpost('/api/memory',{mode});
  renderMemModeDesc(r.mode||mode);
}
// Accès internet : serveur Crawl4AI + drapeau. Actif ET fonctionnel = pastille verte.
let internetOn=false, webEngine='go';
function renderInternet(s){
  internetOn = !!s.enabled;
  webEngine = s.engine || 'go';
  document.getElementById('internet-toggle').checked = internetOn;
  const sel=document.getElementById('web-engine');
  if(sel && document.activeElement!==sel) sel.value = webEngine;
  // Les réglages Crawl4AI n'ont aucun sens avec le moteur intégré.
  const cf=document.getElementById('crawl-fields');
  if(cf) cf.style.display = (webEngine==='crawl4ai') ? '' : 'none';
  if(document.activeElement !== document.getElementById('crawl-url'))
    document.getElementById('crawl-url').value = s.url || '';
  // La pastille ne redit PAS ce que l'interrupteur montre déjà (actif/inactif) :
  // elle ne sert qu'à signaler l'anomalie — actif mais serveur injoignable, cas
  // où les outils web ne sont pas proposés au modèle.
  if(internetOn && !s.reachable) setBadge('internet-badge','warn','injoignable');
  else setBadge('internet-badge', null);
  // Clé du serveur Crawl4AI : le champ reste vide (la clé n'est jamais renvoyée),
  // on indique seulement si une clé est enregistrée.
  const ks=document.getElementById('crawl-key-state'), kc=document.getElementById('crawl-key-clear');
  if(ks){
    ks.textContent = s.key_set ? ('clé enregistrée '+(s.key_hint||'')) : 'aucune clé — serveur ouvert';
    if(kc) kc.style.display = s.key_set ? '' : 'none';
    const ki=document.getElementById('crawl-key');
    if(ki && document.activeElement!==ki) ki.value='';
    if(ki) ki.placeholder = s.key_set ? 'clé enregistrée — saisir pour remplacer' : 'clé API (si le serveur en exige une)';
  }
  const st=document.getElementById('internet-status');
  if(webEngine!=='crawl4ai'){ st.innerHTML='<span style="color:var(--accent)">✓</span> moteur intégré — aucune installation requise (pas de rendu JavaScript)'; }
  else if(!s.url){ st.textContent='serveur non configuré'; st.style.color=''; }
  else if(s.reachable){ st.innerHTML='<span style="color:var(--accent)">✓</span> serveur joignable — outils web actifs'; }
  else { st.innerHTML='⚠ serveur injoignable — les outils web ne seront pas proposés'; }
}
async function loadInternet(){ renderInternet(await jget('/api/internet')); }
// --- Accès OpenAI (endpoint /v1 + clé API des complétions) -----------------
let OAI_KEY='', OAI_REVEAL=false;
async function copyText(txt, msg){
  if(!txt){ toast('rien à copier'); return; }
  try{ await navigator.clipboard.writeText(txt); }
  catch(_){ const ta=document.createElement('textarea'); ta.value=txt; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); ta.remove(); }
  toast(msg||'copié');
}
function copyApiKey(){ copyText(OAI_KEY, OAI_KEY?'clé copiée':'aucune clé'); }
function renderApiKey(d){
  OAI_KEY = d.key || '';
  // URL de l'endpoint : llama-server tourne sur d.port (≠ port de l'UI web).
  // On prend l'hôte annoncé par le serveur (IP LAN détectée côté Go) : dans le
  // tunnel ajean.link, location.hostname serait le domaine du relais (faux) —
  // l'accès OpenAI reste TOUJOURS l'adresse locale de la machine.
  const host = d.host || location.hostname;
  document.getElementById('oai-url').value = 'http://'+host+':'+d.port+'/v1';
  // Endpoint PUBLIC (ajean.link) : affiché seulement si l'accès public est activé.
  const tg = document.getElementById('oai-public-toggle');
  if(tg) tg.checked = !!d.oai_public;
  const pubWrap = document.getElementById('oai-public-wrap');
  if(d.oai_public && d.machine){
    document.getElementById('oai-public-url').value = 'https://'+d.machine+'.oai.ajean.link/v1';
    pubWrap.style.display = '';
  } else {
    pubWrap.style.display = 'none';
  }
  const inp=document.getElementById('oai-key');
  if(!d.set){ inp.value='(aucune clé — serveur ouvert)'; inp.style.opacity=.6; }
  else { inp.style.opacity=1; inp.value = OAI_REVEAL ? OAI_KEY : d.masked; }
  document.getElementById('oai-key-eye').style.display = d.set ? '' : 'none';
}
async function loadApiKey(){ renderApiKey(await jget('/api/apikey')); }
// --- Export de la conversation ---------------------------------------------
// xTurnsTotal : nombre d'échanges du fil, borne haute du curseur. Vient de
// /api/chat/state ; 0 = conversation vide, et la fenêtre n'a alors rien à régler.
let xTurnsTotal = 0;
async function openExportModal(){
  showModal('export-modal');
  try{
    const st = await jget('/api/chat/state');
    xTurnsTotal = (st && st.turns) || 0;
  }catch(e){ xTurnsTotal = 0; }
  // Rien à exporter : on remplace les réglages par un message, plutôt que de
  // proposer de tailler un fichier qui serait vide de toute façon.
  const vide = xTurnsTotal === 0;
  document.getElementById('x-empty').style.display = vide ? '' : 'none';
  document.getElementById('x-body').style.display = vide ? 'none' : '';
  document.getElementById('x-go').style.display = vide ? 'none' : '';
  if(vide) return;
  const el = document.getElementById('x-turns');
  el.max = String(xTurnsTotal);
  el.value = el.max;               // par défaut : tout le fil
  el.disabled = xTurnsTotal < 2;   // un seul échange : rien à trancher
  onExportFormat();
}
function closeExportModal(){ hideModal('export-modal'); }
// Le format ne change QUE le contenant : les options de contenu sont les mêmes
// des deux côtés. Il n'y a donc plus rien à montrer ou cacher ici, seulement le
// résumé à rafraîchir.
function onExportFormat(){ onExportPreview(); }
function exportFormat(){
  const r = document.querySelector('input[name=x-fmt]:checked');
  return r ? r.value : 'md';
}
// Construit la requête d'export à partir des cases. On n'envoie QUE ce qui
// s'écarte du défaut : l'URL reste lisible, et un export complet redevient le
// simple /api/chat/export.
function exportQuery(){
  const p = new URLSearchParams();
  if(exportFormat() === 'json') p.set('format','json');
  const on = id => document.getElementById(id).checked;
  if(!on('x-reasoning')) p.set('reasoning','0');
  if(!on('x-tools')) p.set('tools','0');
  if(!on('x-results')) p.set('results','0');
  const t = exportTurns();
  if(t > 0) p.set('turns', String(t));
  const q = p.toString();
  return '/api/chat/export' + (q ? '?'+q : '');
}
// Valeur de portée à envoyer : 0 = tout le fil. La butée DROITE du curseur vaut
// « toute la conversation », donc on n'envoie rien plutôt qu'un nombre qui se
// périmerait à l'échange suivant.
function exportTurns(){
  const v = parseInt(document.getElementById('x-turns').value, 10) || 0;
  if(!xTurnsTotal || v >= xTurnsTotal) return 0;
  return v;
}
function onExportPreview(){
  // Sortie d'outil sans bulle d'outil n'a aucun sens : la case suit.
  const tools = document.getElementById('x-tools');
  const results = document.getElementById('x-results');
  results.disabled = !tools.checked;
  if(!tools.checked) results.checked = false;
  paintExportRange();
  const off = [];
  if(!document.getElementById('x-reasoning').checked) off.push('raisonnements');
  if(!tools.checked) off.push('outils');
  else if(!results.checked) off.push('sorties d\'outils');
  document.getElementById('x-note').textContent = off.length
    ? 'Export sans ' + off.join(' ni ') + '.'
    : '';
}
// Remplit la piste du curseur et son libellé. Purement local : appelé à chaque
// pixel de glissement, il ne doit RIEN demander au serveur.
function paintExportRange(){
  const el = document.getElementById('x-turns');
  const n = parseInt(el.value, 10) || 0;
  const max = Math.max(1, xTurnsTotal);
  // La pastille ne parcourt pas toute la largeur : elle va de THUMB/2 à
  // largeur - THUMB/2. On coupe le remplissage dans SON repère, sinon la piste
  // colorée déborde à côté d'elle aux extrémités (même calcul que la barre GPU).
  const frac = max > 1 ? (n - 1) / (max - 1) : 1;
  const fill = document.getElementById('x-fill');
  if(fill) fill.style.width = 'calc(' + (frac*100).toFixed(2) + '% - ' + ((frac - .5) * GPU_THUMB).toFixed(2) + 'px)';
  const scope = document.getElementById('x-scope');
  if(!scope) return;
  scope.textContent = n >= xTurnsTotal
    ? 'toute la conversation (' + xTurnsTotal + ' échange' + (xTurnsTotal>1?'s':'') + ')'
    : n + ' dernier' + (n>1?'s':'') + ' échange' + (n>1?'s':'') + ' sur ' + xTurnsTotal;
}
async function runExport(){
  const btn = document.getElementById('x-go');
  btn.disabled = true;
  try{ await downloadExport(exportQuery()); closeExportModal(); }
  finally{ btn.disabled = false; }
}
// On passe par jfetch (et pas par un simple <a href>) pour deux raisons : la clé
// de pilotage voyage dans un en-tête Authorization, qu'un lien ne porterait pas,
// et le chemin de base change derrière le tunnel (/u/<id>).
async function downloadExport(url){
  toast('préparation de l\'export…');
  try{
    const r = await jfetch(url);
    if(!r.ok){ toast('erreur : HTTP ' + r.status); return; }
    // Derrière app.ajean.link, le proxy chiffré réemballe TOUTE réponse en JSON :
    // un export Markdown revenait donc comme une chaîne JSON entre guillemets,
    // échappements compris. On la déballe. En local, rien à faire — le type de
    // contenu n'est pas du JSON, et un export JSON est déjà à sa place.
    let text = await r.text();
    if((r.headers.get('Content-Type')||'').includes('json')){
      try{ const v = JSON.parse(text); if(typeof v === 'string') text = v; }catch(_){}
    }
    const blob = new Blob([text]);
    // Nom du fichier : celui proposé par le serveur (horodaté), à défaut un nom
    // déduit du format DEMANDÉ — le type renvoyé ne dit plus rien derrière le
    // tunnel, où tout arrive en application/json.
    const cd = r.headers.get('Content-Disposition') || '';
    const m = cd.match(/filename="([^"]+)"/);
    const name = m ? m[1] : 'ajean-conversation.' + (url.includes('format=json') ? 'json' : 'md');
    // ⚠️ blobURL, surtout pas `url` : ce nom est déjà celui du paramètre, et le
    // redéclarer ici mettrait la ligne `jfetch(url)` ci-dessus dans la zone morte
    // du const — l'export échouerait avant même de partir.
    const blobURL = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = blobURL; a.download = name;
    document.body.appendChild(a); a.click(); a.remove();
    // Révocation différée : Safari annule le téléchargement si l'URL disparaît
    // dans la foulée du clic.
    setTimeout(()=>URL.revokeObjectURL(blobURL), 10000);
    toast('exporté : ' + name);
  }catch(e){ toast('erreur : ' + e.message); }
}
// --- Écoute réseau du moteur (HOST + pare-feu) ------------------------------
// Le retour d'utilisateur qui a motivé ce réglage : « à part le chat dans le
// navigateur, impossible d'utiliser ton URL dans les logiciels en local ». Le
// moteur écoutait sur 127.0.0.1 et rien ne le disait nulle part.
function renderNetwork(st){
  if(!st) return;
  const cb = document.getElementById('lan-toggle');
  if(cb) cb.checked = !!st.exposed;
  const warn = document.getElementById('lan-warn');
  if(!warn) return;
  if(!st.exposed){
    warn.style.display = '';
    warn.innerHTML = '<div class="muted" style="margin:0">Le moteur n\'écoute que sur cette machine : l\'adresse ci-dessus ne répond pas depuis un autre ordinateur.</div>';
    return;
  }
  // Exposé mais bloqué par le pare-feu : le cas le plus déroutant (« ça écoute
  // partout » et pourtant rien ne passe). On donne la commande à coller.
  if(st.hint){
    warn.style.display = '';
    // --err et non --warn : la palette est volontairement monochrome et --warn y
    // est un gris, illisible comme alerte. Ici il y a une vraie action à faire.
    warn.innerHTML = '<div style="margin:0;color:var(--err)">⚠ '
      + escHtml(st.hint).replace(/\n/g,'<br>') + '</div>';
    return;
  }
  warn.style.display = 'none';
}
async function loadNetwork(){
  try{ const r = await jget('/api/network'); renderNetwork(r && r.status); }catch(e){}
}
async function toggleLAN(){
  const cb = document.getElementById('lan-toggle');
  const on = cb.checked;
  const r = await jpost('/api/network', {exposed:on});
  if(!r || !r.ok){ cb.checked = !on; toast('erreur : ' + ((r&&r.error)||'')); return; }
  renderNetwork(r.status);
  // llama-server ne lit --host qu'au lancement : sans redémarrage, l'interrupteur
  // affiche un état que le moteur en cours ne respecte pas encore.
  if(await askConfirm('Le moteur doit redémarrer pour appliquer ce changement (le modèle sera rechargé).',
                      {title: on ? 'Ouvrir sur le réseau' : 'Fermer sur le réseau', okText:'Redémarrer'})){
    await act('restart'); // même chemin que les boutons du panneau Moteur
  }
  loadApiKey();
}
function toggleKeyReveal(){ OAI_REVEAL=!OAI_REVEAL; const inp=document.getElementById('oai-key'); if(OAI_KEY) inp.value = OAI_REVEAL ? OAI_KEY : (OAI_KEY.slice(0,8)+'…'+OAI_KEY.slice(-4)); const eye=document.getElementById('oai-key-eye'); if(eye) eye.textContent = OAI_REVEAL ? 'masquer' : 'afficher'; }
async function apiKeyAction(action){
  if(action==='clear' && !await askConfirm('Retirer la clé rend l\'endpoint OpenAI accessible SANS authentification. Le service va redémarrer.', {title:'Retirer la clé API ?', okText:'Retirer'})) return;
  if(action==='generate' && OAI_KEY && !await askConfirm('Générer une nouvelle clé invalide l\'ancienne (les clients devront être mis à jour). Le service va redémarrer.', {title:'Régénérer la clé ?', okText:'Générer'})) return;
  OAI_REVEAL=(action==='generate'); // révèle la clé fraîche pour qu'on puisse la copier
  toast('application…');
  renderApiKey(await jpost('/api/apikey', {action}));
}
async function toggleOAIPublic(){
  const cb = document.getElementById('oai-public-toggle');
  const on = cb.checked;
  // L'accès OpenAI public passe par ajean.link (<machine>.oai.ajean.link) : il exige
  // que l'accès distant soit activé sur ce serveur. Sinon, on annule et on explique.
  if(on){
    let linked = false;
    try{ const s = await jget('/api/link/status'); linked = !!(s && s.linked); }catch(e){}
    if(!linked){
      cb.checked = false;
      await askAlert('Vous devez activer l\'accès distant (ajean.link) pour bénéficier de cette fonctionnalité. Ouvrez le panneau « Accès distant » pour connecter ce serveur.', {title:'Accès distant requis'});
      return;
    }
  }
  await jpost('/api/oai/public', {enabled:on});
  toast(on ? 'accès public activé' : 'accès public coupé');
  loadApiKey();
}
async function apiKeySet(){
  const k = await askPrompt('Colle ta clé API (ou laisse vide pour annuler) :', {title:'Définir la clé API', placeholder:'sk-…'});
  if(!k || !k.trim()) return;
  OAI_REVEAL=true; toast('application…');
  renderApiKey(await jpost('/api/apikey', {action:'set', key:k.trim()}));
}
async function toggleInternet(){
  const on=document.getElementById('internet-toggle').checked;
  const url=document.getElementById('crawl-url').value.trim();
  // Crawl4AI sans URL de serveur = rien à joindre. Le moteur intégré, lui, n'a
  // besoin d'aucun réglage : on active directement.
  if(on && webEngine==='crawl4ai' && !url){ toast('renseigne d\'abord l\'URL du serveur Crawl4AI'); document.getElementById('internet-toggle').checked=false; return; }
  renderInternet(await jpost('/api/internet',{enabled:on, url}));
}
async function saveWebEngine(){
  const engine=document.getElementById('web-engine').value;
  renderInternet(await jpost('/api/internet',{engine}));
  toast(engine==='crawl4ai' ? 'moteur Crawl4AI sélectionné' : 'moteur intégré sélectionné');
}
async function saveCrawlUrl(){
  const url=document.getElementById('crawl-url').value.trim();
  renderInternet(await jpost('/api/internet',{url}));
  toast(url ? 'serveur Crawl4AI enregistré' : 'serveur Crawl4AI retiré');
}
async function saveCrawlKey(){
  const el=document.getElementById('crawl-key'), key=el.value.trim();
  if(!key){ toast('saisis une clé (ou « retirer » pour l\'enlever)'); return; }
  renderInternet(await jpost('/api/internet',{key}));
  toast('clé enregistrée');
}
async function clearCrawlKey(){
  if(!await askConfirm('Retirer la clé du serveur Crawl4AI ?')) return;
  document.getElementById('crawl-key').value='';
  renderInternet(await jpost('/api/internet',{key:''}));
  toast('clé retirée');
}
async function loadAll(){
  // allSettled et pas all : un seul chargement en échec (accès distant coupé,
  // clé API absente…) ne doit pas empêcher la suite — et surtout pas laisser les
  // hauteurs réservées en place pour toujours.
  await Promise.allSettled([loadStatus(),loadVram(),loadRam(),loadCfg(),loadPresets(),loadAgent(),loadInternet(),loadMCP(),loadNode(),loadApiKey(),loadNetwork(),loadPrefs(),loadLlamacpp(),loadRemote(),loadTasks()]);
  releaseHeights(); // tout est en place : on rend la main et on mesure pour la prochaine fois
}
async function act(a){ toast(a+'…'); await jpost('/api/'+a); setTimeout(loadAll,1500); }
