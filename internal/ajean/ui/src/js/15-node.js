// ─── Postes distants ─────────────────────────────────────────────────────────
// Gestion des « postes » : d'autres PC qui se connectent au serveur pour que
// l'IA puisse agir dessus. Le propriétaire génère un code d'appairage, choisit
// les capacités autorisées et le dossier racine, puis peut lister/révoquer les
// postes. Sous-réglage du mode agent (grisé quand l'agent est off).
let nodeList = [];
let nodeTarget = '';
let nodeAllCaps = ['shell','read','write','list'];
const nodeCapLabels = {shell:'shell', read:'lire', write:'écrire', list:'lister'};

async function loadNode(){ renderNode(await jget('/api/node')); }

function renderNode(r){
  nodeList = (r && r.nodes) || [];
  nodeTarget = (r && r.target) || '';
  if(r && r.all_caps && r.all_caps.length) nodeAllCaps = r.all_caps;
  const connected = nodeList.filter(n=>n.connected).length;
  setBadge('node-badge', connected ? connected : null);
  const list = document.getElementById('node-list');
  list.textContent = '';
  if(!nodeList.length){
    list.innerHTML = '<div class="muted" style="font-size:12px">(aucun poste — appaire un PC pour que l\'IA puisse agir dessus)</div>';
    return;
  }
  // Sélecteur de CIBLE : sur quelle machine l'IA agit (ses outils bash/write/edit).
  renderNodeTarget(list, connected);
  nodeList.forEach(n=>{
    const row = document.createElement('div'); row.className = 'mcp-row'+(n.connected?'':' off');
    const dot = document.createElement('span'); dot.className = 'mcp-dot '+(n.connected?'mcp-dot-ok':'mcp-dot-off');
    dot.title = n.connected ? 'connecté' : 'hors ligne';

    const info = document.createElement('div'); info.className = 'mcp-info';
    const nm = document.createElement('div'); nm.className = 'mcp-name'; nm.textContent = n.name; nm.title = n.os||'';
    const meta = document.createElement('div'); meta.className = 'mcp-meta';
    const os = document.createElement('span'); os.className = 'mcp-tag'; os.textContent = n.os||'?'; meta.appendChild(os);
    const caps = document.createElement('span'); caps.className = 'mcp-tools';
    caps.textContent = (n.caps&&n.caps.length) ? n.caps.map(c=>nodeCapLabels[c]||c).join(', ') : 'aucune capacité';
    meta.appendChild(caps);
    if(n.root){ const rt = document.createElement('span'); rt.className='mcp-tools'; rt.textContent = '📁 '+n.root; rt.title=n.root; meta.appendChild(rt); }
    info.appendChild(nm); info.appendChild(meta);

    const edit = document.createElement('button'); edit.className='mcp-edit'; edit.title='régler'; edit.textContent='✎';
    edit.onclick = (e)=>{ e.stopPropagation(); openNodeEdit(n.id); };
    row.appendChild(dot); row.appendChild(info); row.appendChild(edit);
    row.onclick = ()=>openNodeEdit(n.id);
    list.appendChild(row);
  });
}

// Sélecteur « L'IA agit sur : Ce serveur / <poste connecté> ». Les mêmes outils
// (bash/write/edit) s'exécutent sur la machine choisie ; un poste hors ligne ne
// peut pas être cible (fail-closed côté serveur).
function renderNodeTarget(list, connected){
  const wrap = document.createElement('div'); wrap.className = 'node-target';
  const lab = document.createElement('div'); lab.className = 'node-target-l';
  lab.textContent = "L'IA agit sur :"; wrap.appendChild(lab);
  const opts = [{slug:'', name:'Ce serveur'}].concat(nodeList.filter(n=>n.connected).map(n=>({slug:n.slug, name:n.name})));
  // Cible sélectionnée mais hors ligne : on l'ajoute quand même, marquée, pour ne
  // pas la perdre silencieusement.
  if(nodeTarget && !opts.some(o=>o.slug===nodeTarget)){
    const n = nodeList.find(x=>x.slug===nodeTarget);
    opts.push({slug:nodeTarget, name:(n?n.name:nodeTarget)+' (hors ligne)', off:true});
  }
  opts.forEach(o=>{
    const l = document.createElement('label'); l.className = 'node-target-opt'+(o.slug===nodeTarget?' on':'')+(o.off?' off':'');
    const rb = document.createElement('input'); rb.type='radio'; rb.name='node-target'; rb.checked = o.slug===nodeTarget;
    rb.onchange = ()=>setNodeTarget(o.slug);
    const sp = document.createElement('span'); sp.textContent = o.name;
    l.appendChild(rb); l.appendChild(sp); wrap.appendChild(l);
  });
  list.appendChild(wrap);
}

async function setNodeTarget(slug){
  const r = await jpost('/api/node/target', {slug});
  nodeTarget = (r && r.target) || '';
  toast(slug ? ('l\'IA agit sur « '+slug+' »') : 'l\'IA agit sur ce serveur');
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
    const sp = document.createElement('span'); sp.textContent = nodeCapLabels[c]||c;
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
  if(!caps.length){ toast('choisis au moins une capacité'); return; }
  const root = document.getElementById('node-pair-root').value.trim();
  const r = await jpost('/api/node/pair', {caps, root});
  if(!r.ok){ toast(r.error||'échec'); return; }
  document.getElementById('node-pair-code').textContent = r.code;
  document.getElementById('node-pair-ttl').textContent = r.ttl_min || 10;
  const allow = caps.join(',');
  const rootArg = root ? (' --root "'+root+'"') : '';
  // Commande d'accès À DISTANCE (partout) via ajean.link : chiffrée de bout en
  // bout, le relais reste aveugle. --key = clé publique de l'agent (le poste
  // scelle vers elle), --machine = quelle machine joindre.
  const remote = 'ajean-remote install https://ajean.link --machine '+r.machine+
    ' --key '+r.agent_pub+' --code '+r.code+' --allow '+allow+rootArg;
  // Variante RÉSEAU LOCAL (connexion directe, sans passer par le relais).
  const lan = 'ajean-remote install '+location.origin+
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
  document.querySelector('#node-pair-modal .modal-head strong').textContent = 'Poste « '+n.name+' »'+(n.connected?' (connecté)':'');
  const footR = document.querySelector('#node-pair-modal .foot-r');
  footR.innerHTML = '';
  const cancel = document.createElement('button'); cancel.textContent='fermer'; cancel.onclick=()=>{ hideModal('node-pair-modal'); resetNodeFoot(); };
  const save = document.createElement('button'); save.className='btn-save'; save.textContent='enregistrer'; save.onclick=saveNodeCaps;
  footR.appendChild(cancel); footR.appendChild(save);
  const footL = document.querySelector('#node-pair-modal .foot-l');
  footL.innerHTML = '';
  const del = document.createElement('button'); del.className='btn-danger'; del.textContent='révoquer'; del.onclick=()=>revokeNode(id);
  footL.appendChild(del);
  showModal('node-pair-modal');
}
function resetNodeFoot(){
  document.querySelector('#node-pair-modal .modal-head strong').textContent = 'Appairer un poste distant';
  document.querySelector('#node-pair-modal .foot-l').innerHTML = '';
  const footR = document.querySelector('#node-pair-modal .foot-r');
  footR.innerHTML = '<button onclick="hideModal(\'node-pair-modal\')">fermer</button><button class="btn-save" onclick="genNodeCode()">générer le code</button>';
  nodeEditing = null;
}
async function saveNodeCaps(){
  if(!nodeEditing) return;
  const caps = nodeCapValues('node-pair-caps');
  const root = document.getElementById('node-pair-root').value.trim();
  const r = await jpost('/api/node/caps', {id:nodeEditing, caps, root});
  if(!r.ok){ toast(r.error||'échec'); return; }
  toast('poste mis à jour'); hideModal('node-pair-modal'); resetNodeFoot(); loadNode();
}
async function revokeNode(id){
  const n = nodeList.find(x=>x.id===id);
  if(!await askConfirm('Révoquer le poste « '+(n?n.name:'')+' » ? Sa clé sera oubliée et il sera déconnecté.', {title:'Révoquer le poste ?', okText:'Révoquer', danger:true})) return;
  await jpost('/api/node/revoke', {id});
  hideModal('node-pair-modal'); resetNodeFoot(); loadNode(); toast('poste révoqué');
}
