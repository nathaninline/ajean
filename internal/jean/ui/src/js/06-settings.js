async function loadPresets(){
  const p=await jget('/api/presets');
  const act = p.find(x=>x.active);
  // Build via DOM (not string concat) so preset names can contain anything —
  // spaces, accents, quotes, < > & — without breaking markup or handlers.
  const sp = document.getElementById('status-preset');
  sp.textContent = act ? act.name : '';
  sp.title = act ? 'preset actif' : '';
  const cont=document.getElementById('presets');
  cont.innerHTML='';
  if(!p.length){ cont.innerHTML='<span class="muted">(aucun)</span>'; return; }
  p.forEach((x,i)=>{
    const row=document.createElement('div');
    row.className='preset'+(x.active?' active':'');
    row.onclick=()=>switchTo(i+1, x.name);
    const info=document.createElement('div'); info.className='preset-info';
    const nm=document.createElement('div'); nm.className='preset-name';
    nm.textContent=(x.active?'● ':'')+x.name; nm.title=x.name;
    // Second row: quant tag + bench perf, so the title row stays full-width.
    const meta=document.createElement('div'); meta.className='preset-meta';
    if(x.quant){
      const q=document.createElement('span'); q.className='qtag';
      q.textContent=x.quant; q.title='quantization';
      meta.appendChild(q);
    }
    if(x.reasoning){
      const rt=document.createElement('span'); rt.className='rtag';
      rt.title='raisonnement actif ('+x.reasoning+')';
      rt.innerHTML='<svg viewBox="0 0 24 24" width="10" height="10" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18h6"/><path d="M10 22h4"/><path d="M12 2a7 7 0 0 0-4 12.7c.6.5 1 1.3 1 2.1V18h6v-1.2c0-.8.4-1.6 1-2.1A7 7 0 0 0 12 2z"/></svg>';
      rt.appendChild(document.createTextNode(x.reasoning));
      meta.appendChild(rt);
    }
    if(x.bench){
      const bt=document.createElement('span'); bt.className='btag';
      bt.title='prefill / decode — dernier bench de ce preset';
      bt.textContent=x.bench.prefill.toFixed(0)+'-'+x.bench.decode.toFixed(0)+' t/s';
      meta.appendChild(bt);
    }
    info.appendChild(nm);
    if(meta.children.length) info.appendChild(meta);
    const edit=document.createElement('button');
    edit.className='preset-edit'; edit.title='éditer'; edit.textContent='✎';
    edit.onclick=(e)=>{ e.stopPropagation(); openPreset(x.id); };
    row.appendChild(info); row.appendChild(edit);
    cont.appendChild(row);
  });
}
// « Mode agent » = accès machine + skills réunis en un seul interrupteur.
// Quand il est actif, un « a » blanc apparaît en fondu devant « jean » → « ajean ».
async function loadAgent(){
  const s=await jget('/api/agent');
  const on = s.enabled;
  document.getElementById('agent-toggle').checked = on;
  document.getElementById('toollimit-toggle').checked = (s.tool_limit !== false);
  document.getElementById('compact-toggle').checked = (s.compact !== false);
  document.getElementById('agent-badge').innerHTML = on
    ? '<span class="tag ok">on</span>' : '<span class="tag" style="opacity:.6">off</span>';
  document.getElementById('brand').classList.toggle('agent', on);
  setAgentGate(on);
  if(s.mem_mode){ document.getElementById('mem-mode').value = s.mem_mode; renderMemModeDesc(s.mem_mode); }
  memPages = (s.pages || s.skills || []).slice().sort((a,b)=>a.name.localeCompare(b.name));
  memShown = MEM_PAGE;
  document.getElementById('mem-count').textContent = memPages.length ? '('+memPages.length+')' : '';
  document.getElementById('mem-search').style.display = memPages.length > MEM_PAGE ? '' : 'none';
  renderMemList();
}
// Mémoire + accès internet sont des sous-réglages du mode agent : sans agent, ni
// les outils mem_* ni les outils web ne sont fournis (voir globalCaps côté Go). On
// grise donc ces blocs quand l'agent est off pour que l'UI ne mente pas.
function setAgentGate(on){
  ['mem-block','net-block','param-block'].forEach(id=>{
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
async function toggleToolLimit(){
  const on=document.getElementById('toollimit-toggle').checked;
  await jpost('/api/agent/tool-limit',{on});
  loadAgent();
}
async function toggleCompact(){
  const on=document.getElementById('compact-toggle').checked;
  await jpost('/api/agent/compact',{on});
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
let internetOn=false;
function renderInternet(s){
  internetOn = !!s.enabled;
  document.getElementById('internet-toggle').checked = internetOn;
  if(document.activeElement !== document.getElementById('crawl-url'))
    document.getElementById('crawl-url').value = s.url || '';
  document.getElementById('internet-badge').innerHTML = internetOn
    ? '<span class="tag ok">on</span>' : '<span class="tag" style="opacity:.6">off</span>';
  const st=document.getElementById('internet-status');
  if(!s.url){ st.textContent='serveur non configuré'; st.style.color=''; }
  else if(s.reachable){ st.innerHTML='<span style="color:var(--accent)">✓</span> serveur joignable — outils web actifs'; }
  else { st.innerHTML='⚠️ serveur injoignable — les outils web ne seront pas proposés'; }
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
function toggleKeyReveal(){ OAI_REVEAL=!OAI_REVEAL; const inp=document.getElementById('oai-key'); if(OAI_KEY) inp.value = OAI_REVEAL ? OAI_KEY : (OAI_KEY.slice(0,8)+'…'+OAI_KEY.slice(-4)); }
async function apiKeyAction(action){
  if(action==='clear' && !await askConfirm('Retirer la clé rend l\'endpoint OpenAI accessible SANS authentification. Le service va redémarrer.', {title:'Retirer la clé API ?', okText:'Retirer'})) return;
  if(action==='generate' && OAI_KEY && !await askConfirm('Générer une nouvelle clé invalide l\'ancienne (les clients devront être mis à jour). Le service va redémarrer.', {title:'Régénérer la clé ?', okText:'Générer'})) return;
  OAI_REVEAL=(action==='generate'); // révèle la clé fraîche pour qu'on puisse la copier
  toast('application…');
  renderApiKey(await jpost('/api/apikey', {action}));
}
async function toggleOAIPublic(){
  const on = document.getElementById('oai-public-toggle').checked;
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
  if(on && !url){ toast('renseigne d\'abord l\'URL du serveur Crawl4AI'); document.getElementById('internet-toggle').checked=false; return; }
  renderInternet(await jpost('/api/internet',{enabled:on, url}));
}
async function saveCrawlUrl(){
  const url=document.getElementById('crawl-url').value.trim();
  renderInternet(await jpost('/api/internet',{url}));
}
async function loadAll(){ await Promise.all([loadStatus(),loadVram(),loadRam(),loadCfg(),loadPresets(),loadAgent(),loadInternet(),loadApiKey(),loadPrefs(),loadLlamacpp()]); }
async function act(a){ toast(a+'…'); await jpost('/api/'+a); setTimeout(loadAll,1500); }
