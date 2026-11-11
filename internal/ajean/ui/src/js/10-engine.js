// Moteur llama.cpp — il est LIVRÉ avec AJEAN, comme dans Ollama : une version
// précise de llama.cpp, validée pour cette version d'AJEAN, installée toute
// seule (au premier lancement, puis à chaque mise à jour d'AJEAN qui change de
// version). L'utilisateur ne choisit pas de moteur et n'en met pas à jour.
// Cette section montre donc seulement :
//   - la version livrée et la variante installée (CUDA, Vulkan, Metal, CPU…),
//   - la progression de l'installation automatique,
//   - un geste de dépannage : « réparer » (retélécharger), et le choix d'une
//     variante quand la détection automatique sert mal la machine.
let engState = null, lcPoll = null, lcLogNext = 0;

async function loadEngine(){
  let s;
  try{ s = await jget('/api/engine'); }catch(_){ return; }
  engState = s;
  engRender(s);

  // Job en cours (page rechargée pendant une installation) → on raccroche l'affichage.
  if(s.job && s.job.exists && s.job.running && !lcPoll){
    lcStartPolling();
  }
  // Job terminé en erreur qu'on n'a pas encore montré (rechargement APRÈS coup,
  // typiquement quand le service a redémarré) : on l'affiche sans polling.
  else if(s.job && s.job.exists && !s.job.running && !lcPoll && s.job.error && !lcEndShown){
    document.getElementById('lc-job').style.display = '';
    lcLogNext = 0;
    document.getElementById('lc-log').textContent = '';
    lcEndShown = true;
    lcPollJob(true); // quiet : simple rattrapage, pas de toast ni de rechargement
  }
  lcChipSync(s.job);
  // Moteur absent et aucune installation en cours ni en échec affiché : on le dit
  // par la pastille (elle ouvre la fenêtre du moteur, d'où on peut l'installer).
  if(!s.installed && !s.dev_bin && !(s.job && s.job.exists && (s.job.running || (s.job.error && !lcSeenEnd)))){
    const chip = document.getElementById('lc-chip');
    if(chip){ chip.hidden = false; chip.classList.add('failed'); chip.innerHTML = '<span class="lc-chip-x">!</span><span class="lc-chip-lbl"></span>'; chip.lastChild.textContent = t('engine.chip_missing'); }
  }
  // Préchauffe la liste des cartes vues par le moteur : l'interroger prend 1 à
  // 3 s (init CUDA/Vulkan), et sans ça l'encart « cartes graphiques » de
  // l'éditeur apparaissait après coup, une fois le reste déjà affiché.
  if(s.installed && typeof prefetchGpuDevices === 'function') prefetchGpuDevices();
}

function engRender(s){
  const esc = x => String(x==null?'':x).replace(/[<>&"]/g, c=>({'<':'&lt;','>':'&gt;','&':'&amp;','"':'&quot;'}[c]));
  document.getElementById('eng-version').textContent = s.version || '';
  const st = document.getElementById('eng-state');
  const running = s.job && s.job.exists && s.job.running;
  if(s.installed){
    st.className = 'eng-state ok';
    st.textContent = s.label || t('engine.state_ready');
  } else if(running){
    st.className = 'eng-state';
    st.textContent = t('engine.state_installing');
  } else {
    st.className = 'eng-state warn';
    st.textContent = t('engine.state_missing');
  }
  // Choix de la variante : seulement quand la machine en a plusieurs.
  const wrap = document.getElementById('eng-variant-wrap'), sel = document.getElementById('eng-variant');
  const vs = s.variants || [];
  wrap.hidden = vs.length < 2;
  if(vs.length >= 2){
    const names = {cuda:'CUDA', rocm:'ROCm', vulkan:'Vulkan', metal:'Metal', cpu:'CPU'};
    sel.innerHTML = '<option value="auto">'+esc(t('engine.variant_auto'))+'</option>'
      + vs.map(v=>'<option value="'+esc(v)+'">'+esc(names[v]||v)+'</option>').join('');
    sel.value = s.forced || 'auto';
  }
  document.getElementById('eng-repair').textContent = s.installed ? t('engine.repair') : t('engine.install_now');
  document.querySelector('.eng-card').classList.toggle('busy', !!running);
  // Dossiers des moteurs d'avant la 0.20 : AJEAN ne s'en sert plus et ne les
  // supprime pas de lui-même, il les signale.
  const leg = document.getElementById('eng-legacy');
  const list = s.legacy || [];
  leg.hidden = !list.length;
  if(list.length){
    leg.innerHTML = '<div>'+esc(t('engine.legacy_note'))+'</div>'
      + list.map(l=>'<div class="eng-legacy-p" title="'+esc(l.what)+'">'+esc(l.path)+'</div>').join('');
  }
}

// « réparer » : retélécharge le moteur de la version livrée.
async function engRepair(){
  const installed = engState && engState.installed;
  if(installed && !await askConfirm(t('engine.repair_confirm'), {title:t('engine.repair_title'), okText:t('engine.repair')})) return;
  const r = await jpost('/api/engine/repair', {});
  if(!r.ok){ toast(t('engine.error_prefix')+(r.error||'')); return; }
  lcStartPolling();
}

// Impose une variante sur cette machine (ou revient à l'automatique), puis
// réinstalle le moteur dans cette variante.
async function engSetVariant(v){
  const cur = (engState && engState.forced) || 'auto';
  if(v === cur) return;
  if(!await askConfirm(t('engine.variant_confirm'), {title:t('engine.variant_title'), okText:t('engine.variant_apply')})){
    document.getElementById('eng-variant').value = cur;
    return;
  }
  const r = await jpost('/api/engine/repair', {variant:v});
  if(!r.ok){ toast(t('engine.error_prefix')+(r.error||'')); document.getElementById('eng-variant').value = cur; return; }
  lcStartPolling();
}

// --- Pastille de rappel hors panneau ---------------------------------------
// Le détail de l'installation vit dans le panneau latéral, qui est un tiroir
// FERMÉ sur téléphone : après un rechargement, rien ne disait qu'une
// installation tournait encore. La pastille le dit, et y ramène en un clic.
let lcSeenEnd = false, lcEndShown = false;
function lcChipLabel(action){
  return action === 'repair' ? t('engine.chip_repair') : t('engine.chip_install');
}
function lcChipSync(j){
  const chip = document.getElementById('lc-chip');
  if(!chip) return;
  if(!j || !j.exists || (!j.running && (!j.error || lcSeenEnd))){ chip.hidden = true; return; }
  chip.hidden = false;
  chip.classList.toggle('failed', !j.running && !!j.error);
  if(j.running){
    // Spinner posé une seule fois : réécrire innerHTML à chaque tick
    // redémarrerait son animation (« saute »). On ne met à jour que le label.
    let lbl = chip.querySelector('.lc-chip-lbl');
    if(!lbl || !chip.querySelector('.lc-spin')){
      chip.innerHTML = '<span class="lc-spin"></span><span class="lc-chip-lbl"></span>';
      lbl = chip.querySelector('.lc-chip-lbl');
    }
    lbl.textContent = lcChipLabel(j.action) + ' — ' + (j.phase || '…');
  } else {
    chip.innerHTML = '<span class="lc-chip-x">✗</span><span class="lc-chip-lbl">'
      + String(lcChipLabel(j.action) + ' ' + t('engine.interrupted')).replace(/[<>&]/g,'') + '</span>';
  }
}
// La fenêtre du moteur : version en place, journal de l'installation, réparer.
// Elle ne s'ouvre que depuis la pastille : le moteur n'a plus de section au menu.
function openEngine(){ showModal('eng-modal'); loadEngine(); }
function closeEngine(){ hideModal('eng-modal'); }
// Clic sur la pastille : ouvrir la fenêtre du moteur.
function lcChipOpen(){
  openEngine();
  if(!document.getElementById('lc-chip').classList.contains('failed')) return;
  lcSeenEnd = true;
  lcChipSync(null);
}

// --- Progression de l'installation -----------------------------------------
function lcBusy(on){ document.querySelector('.eng-card').classList.toggle('busy', on); }

function lcStartPolling(){
  document.getElementById('lc-job').style.display = '';
  document.getElementById('lc-log').textContent = '';
  const dis = document.getElementById('lc-job-dismiss'); if(dis) dis.hidden = true;
  const can = document.getElementById('lc-job-cancel'); if(can) can.hidden = true; // s'affiche au 1er poll « running »
  lcLogNext = 0;
  lcSeenEnd = false; lcEndShown = false;
  lcBusy(true);
  if(lcPoll) clearInterval(lcPoll);
  lcPoll = setInterval(lcPollJob, 1000);
  lcPollJob();
}

async function lcPollJob(quiet){
  let j;
  try{ j = await jget('/api/engine/job?from='+lcLogNext); }catch(_){ return; }
  if(!j.exists) return;
  const phaseEl = document.getElementById('lc-job-phase');
  if(j.lines && j.lines.length){
    const pre = document.getElementById('lc-log');
    const stick = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 20;
    pre.textContent += j.lines.join('\n') + '\n';
    if(stick) pre.scrollTop = pre.scrollHeight;
  }
  if(typeof j.next === 'number') lcLogNext = j.next;
  lcChipSync(j);
  if(j.running){
    // Ne PAS recréer le spinner à chaque tick (1 s) : réécrire innerHTML
    // redémarrerait l'animation CSS → le loading « saute ». On pose la structure
    // une seule fois, puis on ne met à jour que le TEXTE de la phase.
    let txt = phaseEl.querySelector('.lc-phase-txt');
    if(!txt){
      phaseEl.innerHTML = '<span class="lc-spin"></span><span class="lc-phase-txt"></span>';
      txt = phaseEl.querySelector('.lc-phase-txt');
    }
    txt.textContent = j.phase || '…';
    const cancel = document.getElementById('lc-job-cancel');
    if(cancel) cancel.hidden = false; // arrêt possible tant que ça tourne
    return;
  }
  if(lcPoll){ clearInterval(lcPoll); lcPoll = null; }
  lcBusy(false);
  const cancelBtn = document.getElementById('lc-job-cancel');
  if(cancelBtn) cancelBtn.hidden = true; // job terminé → plus rien à arrêter
  // Job terminé : le bouton « masquer » devient utile — il efface définitivement
  // ce résultat pour qu'une erreur ne réapparaisse pas à chaque démarrage (#37).
  const dis = document.getElementById('lc-job-dismiss');
  if(dis) dis.hidden = false;
  if(j.error){
    phaseEl.innerHTML = '<span style="color:var(--err)">✗ '+String(j.error).replace(/[<>&]/g,'')+'</span>';
    const pre = document.getElementById('lc-log');
    if(pre.hasAttribute('hidden')) lcToggleLog();
    pre.scrollTop = pre.scrollHeight;
    if(!quiet) toast(t('engine.failed_toast'));
  } else {
    phaseEl.innerHTML = '<span style="color:var(--ok)">✓ '+String(j.phase||t('engine.done')).replace(/[<>&]/g,'')+'</span>';
    if(!quiet) toast(t('engine.ready_toast'));
  }
  if(!quiet) loadAll();
}

// Arrête l'installation en cours (téléchargement). Le polling continue et
// affichera « annulée ».
async function lcCancelJob(){
  if(!await askConfirm(t('engine.cancel_confirm'), {title:t('engine.cancel_title'), okText:t('engine.job_cancel'), danger:true})) return;
  const btn = document.getElementById('lc-job-cancel');
  if(btn){ btn.disabled = true; }
  try{
    const r = await jpost('/api/engine/job/cancel', {});
    if(!r.ok){ toast(r.error || t('engine.network_error')); }
    else { toast(t('engine.cancel_requested')); }
  }catch(_){ toast(t('engine.network_error')); }
  if(btn){ btn.disabled = false; }
}

// Masque (efface) un job terminé : côté serveur l'entête persistée et le journal
// sont supprimés, donc l'erreur en rouge ne revient plus au prochain démarrage.
async function lcDismissJob(){
  try{
    const r = await jpost('/api/engine/job/dismiss', {});
    if(!r.ok){ toast(r.error||t('engine.cannot_dismiss')); return; }
  }catch(_){ toast(t('engine.network_error')); return; }
  if(lcPoll){ clearInterval(lcPoll); lcPoll = null; }
  document.getElementById('lc-job').style.display = 'none';
  const dis = document.getElementById('lc-job-dismiss'); if(dis) dis.hidden = true;
  lcSeenEnd = true; lcEndShown = false;
  lcChipSync(null);
}

function lcToggleLog(){
  const pre = document.getElementById('lc-log');
  const bar = document.querySelector('.lc-logbar');
  if(pre.hasAttribute('hidden')){ pre.removeAttribute('hidden'); bar.classList.add('open'); pre.scrollTop = pre.scrollHeight; }
  else { pre.setAttribute('hidden',''); bar.classList.remove('open'); }
}
