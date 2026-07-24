// ─── Serveurs MCP ────────────────────────────────────────────────────────────
// Gestion des serveurs MCP (Model Context Protocol) : liste, ajout/édition via
// une modale, activation/désactivation, test de connexion. Sous-réglage du mode
// agent (grisé quand l'agent est off, comme mémoire/internet).
let mcpServers = [];
let mcpEditing = null; // nom du serveur en cours d'édition ('' ou null = nouveau)

async function loadMCP(){ renderMCP(await jget('/api/mcp')); }

function renderMCP(r){
  mcpServers = (r && r.servers) || [];
  const enabled = mcpServers.filter(s=>s.enabled).length;
  document.getElementById('mcp-badge').innerHTML = enabled
    ? '<span class="tag ok">'+enabled+'</span>'
    : (mcpServers.length ? '<span class="tag" style="opacity:.6">0</span>' : '');
  const list = document.getElementById('mcp-list');
  list.textContent = '';
  if(!mcpServers.length){
    list.innerHTML = '<div class="muted" style="font-size:12px">(aucun serveur — ajoute un serveur fichiers, une base de données, une API…)</div>';
    return;
  }
  mcpServers.forEach(s=>{
    const row = document.createElement('div');
    row.className = 'mcp-row'+(s.enabled?'':' off');

    // Pastille d'état : bleu accent = connecté, rouge = erreur, gris = éteint.
    const dot = document.createElement('span');
    dot.className = 'mcp-dot';
    if(!s.enabled) dot.classList.add('mcp-dot-off');
    else if(s.connected) dot.classList.add('mcp-dot-ok');
    else dot.classList.add('mcp-dot-err');
    dot.title = !s.enabled ? 'désactivé' : (s.connected ? 'connecté' : 'erreur de connexion');

    const info = document.createElement('div');
    info.className = 'mcp-info';
    const nm = document.createElement('div'); nm.className = 'mcp-name';
    nm.textContent = s.name; nm.title = s.name;
    const meta = document.createElement('div'); meta.className = 'mcp-meta';
    const tr = document.createElement('span'); tr.className = 'mcp-tag';
    tr.textContent = s.transport || '?'; meta.appendChild(tr);
    if(s.enabled && s.connected){
      const total = s.tools ? s.tools.length : 0;
      const off = s.disabled ? s.disabled.length : 0;
      const active = total - off;
      const tc = document.createElement('span'); tc.className = 'mcp-tools';
      tc.textContent = off ? (active+'/'+total+' outils') : (total+' outil'+(total>1?'s':''));
      if(s.tools && s.tools.length) tc.title = s.tools.join(', ');
      meta.appendChild(tc);
    } else if(s.enabled && s.error){
      const er = document.createElement('span'); er.className = 'mcp-err';
      er.textContent = 'erreur'; er.title = s.error;
      meta.appendChild(er);
    }
    info.appendChild(nm); info.appendChild(meta);

    // Interrupteur activer/désactiver. stopPropagation sur le label ENTIER (pas
    // seulement la checkbox) : sinon un clic sur le slider remonte à la ligne et
    // ouvre la modale en même temps que le toggle.
    const sw = document.createElement('label'); sw.className = 'switch mcp-switch';
    sw.onclick = (e)=>e.stopPropagation();
    const cb = document.createElement('input'); cb.type = 'checkbox'; cb.checked = !!s.enabled;
    cb.onchange = ()=>toggleMcp(s.name, cb.checked);
    const sl = document.createElement('span'); sl.className = 'slider';
    sw.appendChild(cb); sw.appendChild(sl);

    const edit = document.createElement('button');
    edit.className = 'mcp-edit'; edit.title = 'éditer'; edit.textContent = '✎';
    edit.onclick = (e)=>{ e.stopPropagation(); openMcp(s.name); };

    row.appendChild(dot); row.appendChild(info); row.appendChild(sw); row.appendChild(edit);
    row.onclick = ()=>openMcp(s.name);
    list.appendChild(row);
  });
}

async function toggleMcp(name, on){
  renderMCP(await jpost('/api/mcp/toggle', {name, on}));
  toast(on ? 'serveur activé' : 'serveur désactivé');
}

// Ouvre la modale d'ajout (name='') ou d'édition (nom existant).
function openMcp(name){
  mcpEditing = name || '';
  const s = name ? mcpServers.find(x=>x.name===name) : null;
  document.getElementById('mcp-modal-title').textContent = s ? 'Éditer « '+s.name+' »' : 'Nouveau serveur MCP';
  document.getElementById('mcp-name').value = s ? s.name : '';
  document.getElementById('mcp-name').disabled = !!s; // le nom identifie le serveur, pas de renommage
  const transport = s ? (s.transport||'stdio') : 'stdio';
  document.querySelector('input[name="mcp-transport"][value="'+transport+'"]').checked = true;
  mcpTransportUI(transport);
  document.getElementById('mcp-command').value = s ? (s.command||'') : '';
  document.getElementById('mcp-args').value = s && s.args ? s.args.join('\n') : '';
  document.getElementById('mcp-env').value = s && s.env ? Object.entries(s.env).map(([k,v])=>k+'='+v).join('\n') : '';
  document.getElementById('mcp-url').value = s ? (s.url||'') : '';
  document.getElementById('mcp-headers').value = s && s.headers ? Object.entries(s.headers).map(([k,v])=>k+': '+v).join('\n') : '';
  const st = document.getElementById('mcp-modal-status');
  st.textContent = ''; st.style.color = '';
  if(s && s.enabled && s.error){ st.textContent = '⚠️ '+s.error; st.style.color = 'var(--err)'; }
  else if(s && s.connected){ st.innerHTML = '<span style="color:var(--accent)">✓</span> connecté — '+(s.tools?s.tools.length:0)+' outil(s)'; }
  document.getElementById('mcp-del').style.display = s ? '' : 'none';
  renderMcpTools(s);
  document.getElementById('mcp-modal').style.display = 'flex';
}

// Liste des outils découverts du serveur, avec une case par outil pour le
// masquer/exposer à l'IA. Visible seulement pour un serveur connecté.
function renderMcpTools(s){
  const sec = document.getElementById('mcp-tools-section');
  const list = document.getElementById('mcp-tools-list');
  list.textContent = '';
  if(!s || !s.connected || !s.tools || !s.tools.length){ sec.style.display = 'none'; return; }
  sec.style.display = '';
  const off = new Set(s.disabled || []);
  document.getElementById('mcp-tools-count').textContent = '('+(s.tools.length-off.size)+'/'+s.tools.length+' actifs)';
  s.tools.slice().sort().forEach(tool=>{
    const row = document.createElement('label'); row.className = 'mcp-tool-row';
    const sw = document.createElement('span'); sw.className = 'switch mcp-tool-switch';
    const cb = document.createElement('input'); cb.type = 'checkbox'; cb.checked = !off.has(tool);
    cb.onchange = ()=>toggleMcpTool(s.name, tool, cb.checked);
    const sl = document.createElement('span'); sl.className = 'slider';
    sw.appendChild(cb); sw.appendChild(sl);
    const nm = document.createElement('code'); nm.className = 'mcp-tool-name'; nm.textContent = tool;
    row.appendChild(sw); row.appendChild(nm);
    list.appendChild(row);
  });
}

async function toggleMcpTool(server, tool, on){
  const r = await jpost('/api/mcp/tool', {name: server, tool, on});
  renderMCP(r);
  // Rafraîchit le compteur de la modale à partir de l'état à jour.
  const s = (r.servers||[]).find(x=>x.name===server);
  const off = s && s.disabled ? new Set(s.disabled) : new Set();
  if(s && s.tools) document.getElementById('mcp-tools-count').textContent = '('+(s.tools.length-off.size)+'/'+s.tools.length+' actifs)';
}

function closeMcp(){ document.getElementById('mcp-modal').style.display = 'none'; mcpEditing = null; }

function mcpTransportUI(t){
  document.getElementById('mcp-stdio-fields').style.display = t==='stdio' ? '' : 'none';
  document.getElementById('mcp-http-fields').style.display = t==='http' ? '' : 'none';
}

// Parse un textarea "KEY<sep>valeur" par ligne → objet. sep = '=' (env) ou ':' (headers).
function mcpParseKV(text, sep){
  const out = {};
  (text||'').split('\n').forEach(line=>{
    const s = line.trim();
    if(!s) return;
    const i = s.indexOf(sep);
    if(i <= 0) return;
    out[s.slice(0,i).trim()] = s.slice(i+1).trim();
  });
  return Object.keys(out).length ? out : undefined;
}

async function saveMcp(){
  const name = document.getElementById('mcp-name').value.trim();
  if(!name){ toast('donne un nom au serveur'); return; }
  const transport = document.querySelector('input[name="mcp-transport"]:checked').value;
  const body = {name};
  if(transport==='stdio'){
    body.command = document.getElementById('mcp-command').value.trim();
    if(!body.command){ toast('renseigne la commande'); return; }
    body.args = document.getElementById('mcp-args').value.split('\n').map(x=>x.trim()).filter(Boolean);
    body.env = mcpParseKV(document.getElementById('mcp-env').value, '=');
    body.url = ''; body.headers = undefined;
  } else {
    body.url = document.getElementById('mcp-url').value.trim();
    if(!body.url){ toast("renseigne l'URL"); return; }
    body.headers = mcpParseKV(document.getElementById('mcp-headers').value, ':');
    body.command = ''; body.args = undefined; body.env = undefined;
  }
  const st = document.getElementById('mcp-modal-status');
  st.textContent = 'connexion…'; st.style.color = '';
  const r = await jpost('/api/mcp/save', body);
  if(!r.ok){ st.textContent = '⚠️ '+(r.error||'échec'); st.style.color = 'var(--err)'; return; }
  renderMCP(r);
  const saved = (r.servers||[]).find(x=>x.name===name);
  if(saved && saved.connected){ toast('serveur connecté — '+(saved.tools?saved.tools.length:0)+' outil(s)'); closeMcp(); }
  else if(saved && saved.error){ st.textContent = '⚠️ enregistré mais connexion échouée : '+saved.error; st.style.color = 'var(--err)'; }
  else { toast('enregistré'); closeMcp(); }
}

async function deleteMcp(){
  if(!mcpEditing) return;
  if(!await askConfirm('Retirer le serveur « '+mcpEditing+' » ? Ses outils ne seront plus proposés à l\'IA.', {title:'Supprimer le serveur MCP ?', okText:'Supprimer', danger:true})) return;
  await jpost('/api/mcp/delete', {name: mcpEditing});
  closeMcp();
  loadMCP();
  toast('serveur supprimé');
}
