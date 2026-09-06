// ─── Postes distants ─────────────────────────────────────────────────────────
// Gestion des « postes » : d'autres PC qui se connectent au serveur pour que
// l'IA puisse agir dessus. Le propriétaire génère un code d'appairage, choisit
// les capacités autorisées et le dossier racine, puis peut lister/révoquer les
// postes. Sous-réglage du mode agent (grisé quand l'agent est off).
let nodeList = [];
let nodeTarget = '';
let nodeAllCaps = ['shell','read','write','list'];
function nodeCapLabel(c){
  return {shell:'shell', read:t('node.cap_read'), write:t('node.cap_write'), list:t('node.cap_list')}[c] || c;
}

async function loadNode(){ renderNode(await jget('/api/node')); }

// Ouvre la modale de gestion (rafraîchit d'abord pour un état à jour). Appelée
// par le bouton PC/wifi du composeur.
function openNodeHub(){ loadNode(); showModal('node-hub-modal'); }

// Le bouton du composeur reflète SUR QUOI l'IA agit : écran = ce serveur, ondes
// wifi (teinté accent) = un poste distant est la cible active.
function updateNodeBtn(){
  const remote = !!nodeTarget;
  const n = remote ? nodeList.find(x=>x.slug===nodeTarget) : null;
  // Ancien bouton du composeur (retiré) : gardé au cas où un thème le réintroduit.
  const btn = document.getElementById('node-btn');
  if(btn){
    btn.classList.toggle('remote', remote);
    btn.title = remote ? (t('node.remote_nodes_ai_acts_on')+' « '+(n?n.name:nodeTarget)+' »') : t('node.remote_nodes_ai_acts_on')+' '+t('node.this_server');
  }
  // Indicateur wifi à gauche du + : visible seulement quand une cible distante est active.
  const indic = document.getElementById('node-indic');
  if(indic){
    indic.style.display = remote ? 'inline-flex' : 'none';
    indic.title = remote ? (t('node.ai_acts_on')+' « '+(n?n.name:nodeTarget)+' »') : '';
  }
}

function renderNode(r){
  nodeList = (r && r.nodes) || [];
  nodeTarget = (r && r.target) || '';
  if(r && r.all_caps && r.all_caps.length) nodeAllCaps = r.all_caps;
  updateNodeBtn();
  const list = document.getElementById('node-hub-list');
  if(!list) return;
  list.textContent = '';
  if(!nodeList.length){
    list.innerHTML = '<div class="muted" style="font-size:12px">'+t('node.no_paired_nodes')+'</div>';
    return;
  }
  const connected = nodeList.filter(n=>n.connected).length;
  // Sélecteur de CIBLE : sur quelle machine l'IA agit (ses outils bash/write/edit).
  renderNodeTarget(list, connected);
  // En-tête discret de section « Postes ».
  const head = document.createElement('div'); head.className='sess-head'; head.textContent=t('node.section_title'); list.appendChild(head);
  nodeList.forEach(n=>{
    const row = document.createElement('div'); row.className = 'node-row'+(n.connected?'':' off');
    const dot = document.createElement('span'); dot.className = 'node-dot';
    dot.title = n.connected ? t('node.connected') : t('node.offline');
    const info = document.createElement('div'); info.className = 'node-info';
    const nm = document.createElement('div'); nm.className = 'node-nm'; nm.textContent = n.name;
    const sub = document.createElement('div'); sub.className = 'node-sub';
    const caps = (n.caps&&n.caps.length) ? n.caps.map(c=>nodeCapLabel(c)).join(', ') : t('node.no_capability');
    sub.textContent = (n.connected?t('node.connected'):t('node.offline')) + ' · ' + (n.os||'?') + ' · ' + caps;
    sub.title = n.root ? (t('node.folder_label')+' : '+n.root) : '';
    info.appendChild(nm); info.appendChild(sub);
    // Chevron discret (réglages), révélé au survol.
    const go = document.createElement('span'); go.className='node-go';
    go.innerHTML = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6"/></svg>';
    row.appendChild(dot); row.appendChild(info); row.appendChild(go);
    row.onclick = ()=>openNodeEdit(n.id);
    list.appendChild(row);
  });
}

// Sélecteur « L'IA agit sur : Ce serveur / <poste connecté> ». Les mêmes outils
// (bash/write/edit) s'exécutent sur la machine choisie ; un poste hors ligne ne
// peut pas être cible (fail-closed côté serveur).
function renderNodeTarget(list, connected){
  const lab = document.createElement('div'); lab.className = 'node-seg-l';
  lab.textContent = t('node.ai_acts_on'); list.appendChild(lab);
  const seg = document.createElement('div'); seg.className = 'node-seg';
  const opts = [{slug:'', name:t('node.this_server_capitalized')}].concat(nodeList.filter(n=>n.connected).map(n=>({slug:n.slug, name:n.name})));
  // Cible sélectionnée mais hors ligne : on la garde, marquée, pour ne pas la perdre.
  if(nodeTarget && !opts.some(o=>o.slug===nodeTarget)){
    const n = nodeList.find(x=>x.slug===nodeTarget);
    opts.push({slug:nodeTarget, name:(n?n.name:nodeTarget)+' ('+t('node.offline')+')', off:true});
  }
  opts.forEach(o=>{
    const l = document.createElement('label'); l.className = (o.slug===nodeTarget?'on':'')+(o.off?' off':'');
    l.title = o.name;
    const rb = document.createElement('input'); rb.type='radio'; rb.name='node-target'; rb.checked = o.slug===nodeTarget;
    rb.onchange = ()=>setNodeTarget(o.slug);
    const sp = document.createElement('span'); sp.textContent = o.name;
    l.appendChild(rb); l.appendChild(sp); seg.appendChild(l);
  });
  list.appendChild(seg);
}

async function setNodeTarget(slug){
  const r = await jpost('/api/node/target', {slug});
  nodeTarget = (r && r.target) || '';
  toast(slug ? (t('node.ai_acts_on_lower')+' « '+slug+' »') : t('node.ai_acts_on_lower')+' '+t('node.this_server'));
  loadNode();
}

// ── Appairage : génère un code que le poste échange contre sa clé ────────────
function nodeCapCheckboxes(containerId, selected){
  const box = document.getElementById(containerId);
  box.textContent = '';
  const sel = new Set(selected||[]);
  nodeAllCaps.forEach(c=>{
    const lab = document.createElement('label'); lab.className='node-cap';
    const cb = document.createElement('input'); cb.type='checkbox'; cb.value=c; cb.checked = sel.has(c);
    const sp = document.createElement('span'); sp.textContent = nodeCapLabel(c);
    lab.appendChild(cb); lab.appendChild(sp);
    box.appendChild(lab);
  });
}
function nodeCapValues(containerId){
  return [...document.querySelectorAll('#'+containerId+' input:checked')].map(cb=>cb.value);
}

function openNodePair(){
  nodeCapCheckboxes('node-pair-caps', ['read','list']); // défaut prudent
  document.getElementById('node-pair-root').value = '';
  document.getElementById('node-pair-out').style.display = 'none';
  showModal('node-pair-modal');
}

async function genNodeCode(){
  const caps = nodeCapValues('node-pair-caps');
  if(!caps.length){ toast(t('node.choose_capability')); return; }
  const root = document.getElementById('node-pair-root').value.trim();
  const r = await jpost('/api/node/pair', {caps, root});
  if(!r.ok){ toast(r.error||t('node.failed')); return; }
  document.getElementById('node-pair-code').textContent = r.code;
  document.getElementById('node-pair-ttl').textContent = r.ttl_min || 10;
  const allow = caps.join(',');
  const rootArg = root ? (' --root "'+root+'"') : '';
  // Commande d'accès À DISTANCE (partout) via ajean.link : chiffrée de bout en
  // bout, le relais reste aveugle. --key = clé publique de l'agent (le poste
  // scelle vers elle), --machine = quelle machine joindre.
  const remote = 'ajean remote install https://ajean.link --machine '+r.machine+
    ' --key '+r.agent_pub+' --code '+r.code+' --allow '+allow+rootArg;
  // Variante RÉSEAU LOCAL (connexion directe, sans passer par le relais).
  const lan = 'ajean remote install '+location.origin+
    ' --key '+r.agent_pub+' --code '+r.code+' --allow '+allow+rootArg;
  document.getElementById('node-pair-cmd').textContent = remote;
  const lanEl = document.getElementById('node-pair-cmd-lan');
  if(lanEl) lanEl.textContent = lan;
  document.getElementById('node-pair-out').style.display = '';
  loadNode();
}

// ── Édition d'un poste appairé : capacités, dossier, révocation ──────────────
let nodeEditing = null;
function openNodeEdit(id){
  const n = nodeList.find(x=>x.id===id); if(!n) return;
  nodeEditing = id;
  nodeCapCheckboxes('node-pair-caps', n.caps);
  document.getElementById('node-pair-root').value = n.root||'';
  document.getElementById('node-pair-out').style.display = 'none';
  // Réutilise la modale d'appairage en mode édition : on remplace le pied.
  document.querySelector('#node-pair-modal .modal-head strong').textContent = t('node.node_prefix')+' « '+n.name+' »'+(n.connected?' ('+t('node.connected')+')':'');
  const footR = document.querySelector('#node-pair-modal .foot-r');
  footR.innerHTML = '';
  const cancel = document.createElement('button'); cancel.className='mbtn'; cancel.textContent=t('node.close_btn'); cancel.onclick=()=>{ hideModal('node-pair-modal'); resetNodeFoot(); };
  const save = document.createElement('button'); save.className='mbtn primary'; save.textContent=t('node.save_btn'); save.onclick=saveNodeCaps;
  footR.appendChild(cancel); footR.appendChild(save);
  const footL = document.querySelector('#node-pair-modal .foot-l');
  footL.innerHTML = '';
  const del = document.createElement('button'); del.className='mbtn danger'; del.textContent=t('node.revoke_btn'); del.onclick=()=>revokeNode(id);
  footL.appendChild(del);
  showModal('node-pair-modal');
}
function resetNodeFoot(){
  document.querySelector('#node-pair-modal .modal-head strong').textContent = t('node.pair_remote_node_title');
  document.querySelector('#node-pair-modal .foot-l').innerHTML = '';
  const footR = document.querySelector('#node-pair-modal .foot-r');
  footR.innerHTML = '<button class="mbtn" onclick="hideModal(\'node-pair-modal\')">'+t('node.close_btn')+'</button><button class="mbtn primary" onclick="genNodeCode()">'+t('node.generate_code_btn')+'</button>';
  nodeEditing = null;
}
async function saveNodeCaps(){
  if(!nodeEditing) return;
  const caps = nodeCapValues('node-pair-caps');
  const root = document.getElementById('node-pair-root').value.trim();
  const r = await jpost('/api/node/caps', {id:nodeEditing, caps, root});
  if(!r.ok){ toast(r.error||t('node.failed')); return; }
  toast(t('node.node_updated')); hideModal('node-pair-modal'); resetNodeFoot(); loadNode();
}
async function revokeNode(id){
  const n = nodeList.find(x=>x.id===id);
  if(!await askConfirm(t('node.confirm_revoke_prefix')+' « '+(n?n.name:'')+' » ? '+t('node.confirm_revoke_suffix'), {title:t('node.revoke_title'), okText:t('node.revoke_btn'), danger:true})) return;
  await jpost('/api/node/revoke', {id});
  hideModal('node-pair-modal'); resetNodeFoot(); loadNode(); toast(t('node.node_revoked'));
}
