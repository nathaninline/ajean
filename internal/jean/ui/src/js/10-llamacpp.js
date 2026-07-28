// Backend llama.cpp — la barre latérale sert UNIQUEMENT à installer les moteurs.
//   « Version rapide »    = binaires officiels précompilés (aucune compilation)
//   « Version optimisée » = compilée pour la machine
// Le CHOIX du moteur utilisé se fait par modèle (preset), pas ici — voir la
// section « Moteur » dans l'éditeur de modèle. Ça évite que la barre latérale
// et les presets se battent pour la ligne BIN.
let lcState = null, lcPoll = null, lcLogNext = 0;

async function loadLlamacpp(){
  let s;
  try{ s = await jget('/api/llamacpp'); }catch(_){ return; }
  lcState = s;
  const pb = s.prebuilt || {};
  const fastInstalled = !!pb.bin;
  const optInstalled  = !!s.bin;

  const head = document.getElementById('lc-status');
  if(!fastInstalled && !optInstalled){
    head.innerHTML = 'Installez le moteur d\'IA. La <b>version rapide</b> convient à presque tout le monde.';
  } else {
    head.innerHTML = 'Installez les moteurs ici. Vous choisissez lequel utiliser en éditant un modèle (bouton <b>✎</b>).';
  }
  lcRenderMode('fast', fastInstalled);
  lcRenderMode('opt', optInstalled);
  lcRenderCustomCard();

  // Job en cours (page rechargée pendant une install) → on raccroche l'affichage.
  if(s.job && s.job.exists && s.job.running && !lcPoll){
    document.getElementById('lc-details').open = true;
    lcStartPolling();
  }
}

function lcRenderMode(mode, installed){
  const card  = document.getElementById(mode==='fast' ? 'lc-mode-fast' : 'lc-mode-opt');
  const state = document.getElementById(mode==='fast' ? 'lc-fast-state' : 'lc-opt-state');
  card.classList.toggle('installed', installed);
  // Lien de vérification : interroge la dernière version SANS rien installer.
  // event.stopPropagation empêche le clic de la carte (qui lance l'install).
  const check = '<span class="lc-update-link" onclick="event.stopPropagation();lcCheck(\''+mode+'\')">vérifier la version</span>';
  if(installed){
    state.innerHTML = '<span class="lc-mode-active-tag">✓ installée</span>'
      + '<span class="lc-update-link" onclick="event.stopPropagation();lcUpdate(\''+mode+'\')">↻ mettre à jour</span>'
      + check;
  } else {
    state.innerHTML = '<span class="lc-mode-go">→ cliquer pour installer</span>' + check;
  }
}

// lcCheck vérifie s'il existe une version plus récente AVANT toute installation
// (endpoints de check dédiés, sans effet de bord). Résultat affiché en toast.
async function lcCheck(mode){
  toast('vérification…');
  try{
    if(mode === 'fast'){
      const r = await jpost('/api/llamacpp/prebuilt/check', {});
      if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
      if(r.update) toast('nouvelle version disponible : '+r.latest+(r.current ? ' (installée : '+r.current+')' : ''));
      else toast('moteur rapide à jour ✓'+(r.latest ? ' ('+r.latest+')' : ''));
    } else {
      const r = await jpost('/api/llamacpp/check', {});
      if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
      if(r.behind > 0) toast(r.behind+' nouveau(x) commit(s) disponible(s) — utilisez « mettre à jour »');
      else toast('moteur optimisé à jour ✓');
    }
  }catch(_){ toast('erreur réseau'); }
}

// Clic sur une carte : installer le moteur (s'il ne l'est pas déjà).
async function lcPick(mode){
  const s = lcState || {}, pb = s.prebuilt || {};
  const installed = mode==='fast' ? !!pb.bin : !!s.bin;
  if(installed){
    toast('déjà installée — choisissez-la dans l\'édition d\'un modèle (✎)');
    return;
  }
  if(mode === 'fast'){
    if(!await askConfirm('Installer la version rapide de llama.cpp : téléchargement prêt à l\'emploi (~2 min, aucune compilation).', {title:'Version rapide', okText:'Installer'})) return;
    const r = await jpost('/api/llamacpp/prebuilt', {});
    if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
  } else {
    if(!await askConfirm('Compiler la version optimisée pour votre machine. Ça peut prendre de longues minutes (surtout avec une carte NVIDIA).', {title:'Version optimisée', okText:'Compiler'})) return;
    const r = await jpost('/api/llamacpp/install', {});
    if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
  }
  lcStartPolling();
}

// « Mettre à jour » sur une carte installée.
async function lcUpdate(mode){
  if(mode === 'fast'){
    if(!await askConfirm('Vérifier et installer la dernière version rapide.', {title:'Mettre à jour', okText:'Mettre à jour'})) return;
    const r = await jpost('/api/llamacpp/prebuilt', {});
    if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
  } else {
    if(!await askConfirm('Vérifier et installer la dernière version optimisée (recompilation si besoin).', {title:'Mettre à jour', okText:'Mettre à jour'})) return;
    const r = await jpost('/api/llamacpp/update', {clean:false});
    if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
  }
  lcStartPolling();
}

// --- Backends personnalisés (3e carte + modal de gestion) ------------------
let lcCustomBackends = [];

// État affiché sur la carte « Backend personnalisé » : nombre d'installés.
async function lcRenderCustomCard(){
  const state = document.getElementById('lc-custom-state');
  if(!state) return;
  try{ lcCustomBackends = await jget('/api/backends/custom') || []; }catch(_){ lcCustomBackends = []; }
  const n = lcCustomBackends.length;
  document.getElementById('lc-mode-custom').classList.toggle('installed', n>0);
  state.innerHTML = n>0
    ? '<span class="lc-mode-active-tag">✓ '+n+' installé'+(n>1?'s':'')+'</span><span class="lc-mode-go">→ gérer</span>'
    : '<span class="lc-mode-go">→ voir / installer</span>';
}

function openCustomBackends(){
  document.getElementById('lc-custom-modal').style.display = 'flex';
  document.getElementById('lc-custom-url').value = '';
  document.getElementById('lc-custom-name').value = '';
  loadCustomBackends();
}
function closeCustomBackends(){ document.getElementById('lc-custom-modal').style.display = 'none'; }

// Liste les backends custom dans le modal, avec un bouton supprimer par ligne.
async function loadCustomBackends(){
  const box = document.getElementById('lc-custom-list');
  box.innerHTML = '<span class="muted" style="font-size:12px">chargement…</span>';
  let list = [];
  try{ list = await jget('/api/backends/custom') || []; }catch(_){}
  lcCustomBackends = list;
  if(!list.length){ box.innerHTML = '<span class="muted" style="font-size:12px">aucun backend personnalisé pour l\'instant.</span>'; return; }
  box.innerHTML = list.map(b=>{
    const nm = String(b.name).replace(/[<>&]/g,'');
    const used = b.in_use ? '<span class="mcp-tag" style="border-color:var(--accent);color:var(--accent)">utilisé</span>' : '';
    return '<div class="mcp-row" style="cursor:default">'
      + '<span class="mcp-dot '+(b.in_use?'mcp-dot-ok':'mcp-dot-off')+'"></span>'
      + '<div class="mcp-info"><div class="mcp-name">'+nm+'</div>'
      + '<div class="mcp-meta">'+used+'<span class="mcp-tag" title="'+String(b.path).replace(/"/g,'&quot;')+'">'+String(b.path).split('/').slice(-3).join('/').replace(/[<>&]/g,'')+'</span></div></div>'
      + '<button class="btn-danger" style="padding:3px 8px;font-size:11px" onclick="lcUninstallCustom(\''+nm.replace(/'/g,"\\'")+'\')">supprimer</button>'
      + '</div>';
  }).join('');
}

// Installer un backend CUSTOM depuis une URL de dépôt Git (fork llama.cpp).
// Cloné + compilé dans backends/<nom>, SANS toucher au moteur global : il
// apparaît ensuite dans le menu « backend détecté » de l'éditeur de modèle.
async function lcInstallCustom(){
  const url = (document.getElementById('lc-custom-url').value||'').trim();
  if(!url){ toast('collez l\'URL d\'un dépôt Git'); return; }
  const name = (document.getElementById('lc-custom-name').value||'').trim();
  if(!await askConfirm('Cloner et compiler ce backend depuis :\n'+url+'\n\nCela peut prendre de longues minutes (surtout avec une carte NVIDIA). Il ne remplace pas le moteur global — vous le choisirez par modèle.', {title:'Backend personnalisé', okText:'Installer'})) return;
  const r = await jpost('/api/llamacpp/install-custom', {repo:url, name});
  if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
  closeCustomBackends();
  document.getElementById('lc-details').open = true;
  lcStartPolling();
}

// Désinstaller (supprime le dossier backends/<name>). Le serveur refuse si le
// backend sert de moteur au modèle actif.
async function lcUninstallCustom(name){
  if(!await askConfirm('Supprimer le backend « '+name+' » ? Son dossier compilé sera effacé. Les modèles qui l\'utilisent devront être repointés sur un autre moteur.', {title:'Supprimer le backend', okText:'Supprimer', danger:true})) return;
  const r = await jpost('/api/llamacpp/uninstall-custom', {name});
  if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
  toast('backend supprimé');
  loadCustomBackends();
  lcRenderCustomCard();
}

// --- Progression de l'installation (téléchargement / compilation) ----------
function lcBusy(on){ document.querySelector('.lc-modes').classList.toggle('busy', on); }

function lcStartPolling(){
  document.getElementById('lc-job').style.display = '';
  document.getElementById('lc-log').textContent = '';
  lcLogNext = 0;
  lcBusy(true);
  if(lcPoll) clearInterval(lcPoll);
  lcPoll = setInterval(lcPollJob, 1000);
  lcPollJob();
}

async function lcPollJob(){
  let j;
  try{ j = await jget('/api/llamacpp/job?from='+lcLogNext); }catch(_){ return; }
  if(!j.exists) return;
  const phaseEl = document.getElementById('lc-job-phase');
  if(j.lines && j.lines.length){
    const pre = document.getElementById('lc-log');
    const stick = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 20;
    pre.textContent += j.lines.join('\n') + '\n';
    if(stick) pre.scrollTop = pre.scrollHeight;
  }
  if(typeof j.next === 'number') lcLogNext = j.next;
  if(j.running){
    phaseEl.innerHTML = '<span class="lc-spin">⏳</span> <span>'+String(j.phase||'…').replace(/[<>&]/g,'')+'</span>';
    return;
  }
  clearInterval(lcPoll); lcPoll = null;
  lcBusy(false);
  if(j.error){
    phaseEl.innerHTML = '<span style="color:var(--err)">✗ '+String(j.error).replace(/[<>&]/g,'')+'</span>';
    const pre = document.getElementById('lc-log');
    if(pre.hasAttribute('hidden')) lcToggleLog();
    pre.scrollTop = pre.scrollHeight;
    toast('échec — voir les détails');
  } else {
    phaseEl.innerHTML = '<span style="color:var(--ok)">✓ '+String(j.phase||'terminé').replace(/[<>&]/g,'')+'</span>';
    toast('c\'est prêt ✓');
  }
  loadAll();
}

function lcToggleLog(){
  const pre = document.getElementById('lc-log');
  const bar = document.querySelector('.lc-logbar');
  if(pre.hasAttribute('hidden')){ pre.removeAttribute('hidden'); bar.classList.add('open'); pre.scrollTop = pre.scrollHeight; }
  else { pre.setAttribute('hidden',''); bar.classList.remove('open'); }
}
