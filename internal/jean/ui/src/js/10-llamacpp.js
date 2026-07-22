// Backend llama.cpp — vue simple : deux modes au choix.
//   ⚡ Version rapide    = binaires officiels précompilés (aucune compilation)
//   🔧 Version optimisée = compilée pour la machine
// Un clic sur une carte l'installe (ou l'active si déjà installée). La carte
// active propose « mettre à jour ». Aucun détail technique affiché.
let lcState = null, lcPoll = null, lcLogNext = 0;

async function loadLlamacpp(){
  let s;
  try{ s = await jget('/api/llamacpp'); }catch(_){ return; }
  lcState = s;
  const pb = s.prebuilt || {};
  const fastActive = !!pb.in_use;
  const optActive  = s.installed && s.in_use && !pb.in_use;

  const head = document.getElementById('lc-status');
  if(!fastActive && !optActive){
    head.innerHTML = 'Le moteur d\'IA n\'est pas encore installé. Choisis une version ci-dessous — la <b>rapide</b> convient à presque tout le monde.';
  } else if(fastActive){
    head.innerHTML = 'Moteur actif : <b style="color:var(--accent)">version rapide</b>.';
  } else {
    head.innerHTML = 'Moteur actif : <b style="color:var(--accent)">version optimisée</b>.';
  }
  lcRenderMode('fast', fastActive, !!pb.bin);
  lcRenderMode('opt', optActive, !!s.bin);

  // Job en cours (page rechargée pendant une install) → on raccroche l'affichage.
  if(s.job && s.job.exists && s.job.running && !lcPoll){
    document.getElementById('lc-details').open = true;
    lcStartPolling();
  }
}

function lcRenderMode(mode, active, installed){
  const card  = document.getElementById(mode==='fast' ? 'lc-mode-fast' : 'lc-mode-opt');
  const state = document.getElementById(mode==='fast' ? 'lc-fast-state' : 'lc-opt-state');
  card.classList.toggle('active', active);
  if(active){
    state.innerHTML = '<span class="lc-mode-active-tag">✓ Actif</span>'
      + '<span class="lc-update-link" onclick="event.stopPropagation();lcUpdateActive()">↻ mettre à jour</span>';
  } else if(installed){
    state.innerHTML = '<span class="lc-mode-go">→ cliquer pour l\'activer</span>';
  } else {
    state.innerHTML = '<span class="lc-mode-go">→ cliquer pour installer</span>';
  }
}

// Clic sur une carte : activer / installer le mode correspondant.
async function lcPick(mode){
  const s = lcState || {}, pb = s.prebuilt || {};
  if(mode === 'fast'){
    if(pb.in_use) return;                 // déjà actif
    if(pb.bin){                            // déjà téléchargée → simple bascule
      if(!await askConfirm('Basculer sur la version rapide (déjà installée) ? Le moteur va redémarrer.', {title:'Version rapide', okText:'Basculer'})) return;
      return lcUse('fast');
    }
    if(!await askConfirm('Installer la version rapide de llama.cpp : téléchargement prêt à l\'emploi (~2 min, aucune compilation). Le moteur va redémarrer à la fin.', {title:'Version rapide', okText:'Installer'})) return;
    const r = await jpost('/api/llamacpp/prebuilt', {});
    if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
    lcStartPolling();
  } else {
    if(s.installed && s.in_use && !pb.in_use) return; // déjà actif
    if(s.bin){                             // déjà compilée → simple bascule
      if(!await askConfirm('Basculer sur la version optimisée (déjà compilée) ? Le moteur va redémarrer.', {title:'Version optimisée', okText:'Basculer'})) return;
      return lcUse('opt');
    }
    if(!await askConfirm('Compiler la version optimisée pour ta machine. Ça peut prendre de longues minutes (surtout avec une carte NVIDIA). Le moteur va redémarrer à la fin.', {title:'Version optimisée', okText:'Compiler'})) return;
    const r = await jpost('/api/llamacpp/install', {});
    if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
    lcStartPolling();
  }
}

// Bascule instantanée entre deux versions déjà installées (pas de recompilation).
async function lcUse(mode){
  toast('bascule…');
  const r = await jpost('/api/llamacpp/use', {mode});
  if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
  toast(mode==='fast' ? 'version rapide activée' : 'version optimisée activée');
  setTimeout(loadAll, 1500);
}

// « Mettre à jour » sur la carte active.
async function lcUpdateActive(){
  const s = lcState || {}, pb = s.prebuilt || {};
  if(pb.in_use){
    if(!await askConfirm('Vérifier et installer la dernière version rapide. Le moteur va redémarrer.', {title:'Mettre à jour', okText:'Mettre à jour'})) return;
    const r = await jpost('/api/llamacpp/prebuilt', {});
    if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
  } else {
    if(!await askConfirm('Vérifier et installer la dernière version optimisée (recompilation si besoin). Le moteur va redémarrer.', {title:'Mettre à jour', okText:'Mettre à jour'})) return;
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
