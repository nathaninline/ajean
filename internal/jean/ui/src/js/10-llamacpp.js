// Backend llama.cpp — la barre latérale sert UNIQUEMENT à installer les moteurs.
//   ⚡ Version rapide    = binaires officiels précompilés (aucune compilation)
//   🔧 Version optimisée = compilée pour la machine
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
    head.innerHTML = 'Installe le moteur d\'IA. La <b>version rapide</b> convient à presque tout le monde.';
  } else {
    head.innerHTML = 'Installe les moteurs ici. Tu choisis lequel utiliser quand tu édites un modèle (bouton <b>✎</b>).';
  }
  lcRenderMode('fast', fastInstalled);
  lcRenderMode('opt', optInstalled);

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
  if(installed){
    state.innerHTML = '<span class="lc-mode-active-tag">✓ installée</span>'
      + '<span class="lc-update-link" onclick="event.stopPropagation();lcUpdate(\''+mode+'\')">↻ mettre à jour</span>';
  } else {
    state.innerHTML = '<span class="lc-mode-go">→ cliquer pour installer</span>';
  }
}

// Clic sur une carte : installer le moteur (s'il ne l'est pas déjà).
async function lcPick(mode){
  const s = lcState || {}, pb = s.prebuilt || {};
  const installed = mode==='fast' ? !!pb.bin : !!s.bin;
  if(installed){
    toast('déjà installée — choisis-la dans l\'édition d\'un modèle (✎)');
    return;
  }
  if(mode === 'fast'){
    if(!await askConfirm('Installer la version rapide de llama.cpp : téléchargement prêt à l\'emploi (~2 min, aucune compilation).', {title:'Version rapide', okText:'Installer'})) return;
    const r = await jpost('/api/llamacpp/prebuilt', {});
    if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
  } else {
    if(!await askConfirm('Compiler la version optimisée pour ta machine. Ça peut prendre de longues minutes (surtout avec une carte NVIDIA).', {title:'Version optimisée', okText:'Compiler'})) return;
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
