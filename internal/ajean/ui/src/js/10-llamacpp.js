// Backend llama.cpp — la barre latérale sert UNIQUEMENT à installer les moteurs.
//   « llama.cpp précompilé »   = binaires officiels, aucune compilation
//   « llama.cpp compilé »      = compilé ici, pour cette machine
//   « llama.cpp personnalisé » = un fork (modèles à quant spéciale)
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

  lcRenderReco(s.reco);
  lcRenderMode('fast', fastInstalled);
  lcRenderMode('opt', optInstalled);
  lcRenderCustomCard();

  // Job en cours (page rechargée pendant une install) → on raccroche l'affichage.
  if(s.job && s.job.exists && s.job.running && !lcPoll){
    document.getElementById('lc-details').open = true;
    lcStartPolling();
  }
  // Job terminé/interrompu qu'on n'a pas encore montré (rechargement APRÈS coup,
  // typiquement quand le service a redémarré) : on l'affiche sans polling.
  else if(s.job && s.job.exists && !s.job.running && !lcPoll && s.job.error && !lcEndShown){
    document.getElementById('lc-details').open = true;
    document.getElementById('lc-job').style.display = '';
    lcLogNext = 0;
    document.getElementById('lc-log').textContent = '';
    lcEndShown = true;
    lcPollJob(true); // quiet : simple rattrapage, pas de toast ni de rechargement
  }
  lcChipSync(s.job);
  // Préchauffe la liste des cartes du moteur actif : interroger le moteur prend
  // 1 à 3 s (init CUDA/Vulkan), et sans ça l'encart « cartes graphiques » de
  // l'éditeur apparaissait après coup, une fois le reste déjà affiché.
  if(typeof prefetchGpuDevices === 'function') prefetchGpuDevices(s.config_bin);
}

// --- Pastille de rappel hors panneau ---------------------------------------
// Le détail de l'installation vit dans le panneau latéral, qui est un tiroir
// FERMÉ sur téléphone : après un rechargement, rien ne disait qu'une
// compilation tournait encore. La pastille le dit, et y ramène en un clic.
let lcSeenEnd = false, lcEndShown = false;
function lcChipLabel(action){
  return {install:t('llamacpp.chip_install'), update:t('llamacpp.chip_update'),
          prebuilt:t('llamacpp.chip_prebuilt'), custom:t('llamacpp.chip_custom')}[action] || t('llamacpp.chip_default');
}
function lcChipSync(j){
  const chip = document.getElementById('lc-chip');
  if(!chip) return;
  if(!j || !j.exists || (!j.running && (!j.error || lcSeenEnd))){ chip.hidden = true; return; }
  chip.hidden = false;
  chip.classList.toggle('failed', !j.running && !!j.error);
  chip.textContent = j.running
    ? '⏳ ' + lcChipLabel(j.action) + ' — ' + (j.phase || '…')
    : '✗ ' + lcChipLabel(j.action) + ' ' + t('llamacpp.interrupted');
}
// Clic sur la pastille : ouvrir le tiroir sur la section Moteur.
function lcChipOpen(){
  const side = document.getElementById('side');
  if(!side.classList.contains('open')) toggleSide();
  const det = document.getElementById('lc-details');
  det.open = true;
  det.scrollIntoView({block:'center'});
  if(!document.getElementById('lc-chip').classList.contains('failed')) return;
  lcSeenEnd = true;
  lcChipSync(null);
}

// Place la pastille « conseillé » sur la carte que le SERVEUR recommande pour
// cette machine (voir recommendedMode) : le précompilé partout, sauf sur Linux
// avec une carte NVIDIA où il ne donnerait que du Vulkan. La raison est ajoutée
// à la description de la carte, pour expliquer plutôt que d'imposer.
function lcRenderReco(reco){
  const mode = (reco && reco.mode) || 'fast';
  for(const m of ['fast','opt']){
    const badge = document.getElementById('lc-reco-'+m);
    if(badge) badge.hidden = (m !== mode);
  }
  // La raison REMPLACE la description générique de la carte conseillée : elle
  // dit déjà ce que fait l'option et pourquoi c'est le bon choix ici. (loadAll
  // repasse par là, donc pas d'accumulation possible.)
  const desc = document.getElementById('lc-desc-'+mode);
  if(desc && reco && reco.why) desc.textContent = reco.why;
}

function lcRenderMode(mode, installed){
  const card  = document.getElementById(mode==='fast' ? 'lc-mode-fast' : 'lc-mode-opt');
  const state = document.getElementById(mode==='fast' ? 'lc-fast-state' : 'lc-opt-state');
  card.classList.toggle('installed', installed);
  // Lien de vérification : interroge la dernière version SANS rien installer.
  // event.stopPropagation empêche le clic de la carte (qui lance l'install).
  const check = '<span class="lc-update-link" onclick="event.stopPropagation();lcCheck(\''+mode+'\')">'+t('llamacpp.check_version')+'</span>';
  if(installed){
    state.innerHTML = '<span class="lc-mode-active-tag">'+t('llamacpp.installed_tag')+'</span>'
      + '<span class="lc-update-link" onclick="event.stopPropagation();lcUpdate(\''+mode+'\')">'+t('llamacpp.update_link')+'</span>'
      + check;
  } else {
    state.innerHTML = '<span class="lc-mode-go">'+t('llamacpp.click_to_install')+'</span>' + check;
  }
}

// lcCheck vérifie s'il existe une version plus récente AVANT toute installation
// (endpoints de check dédiés, sans effet de bord). Résultat affiché en toast.
async function lcCheck(mode){
  toast(t('llamacpp.checking'));
  try{
    if(mode === 'fast'){
      const r = await jpost('/api/llamacpp/prebuilt/check', {});
      if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
      if(r.update) toast(t('llamacpp.new_version_available')+r.latest+(r.current ? ' ('+t('llamacpp.currently_installed')+' : '+r.current+')' : ''));
      else toast(t('llamacpp.prebuilt_up_to_date')+(r.latest ? ' ('+r.latest+')' : ''));
    } else {
      const r = await jpost('/api/llamacpp/check', {});
      if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
      if(r.behind > 0) toast(r.behind+' '+t('llamacpp.commits_available'));
      else toast(t('llamacpp.compiled_up_to_date'));
    }
  }catch(_){ toast(t('llamacpp.network_error')); }
}

// Clic sur une carte : installer le moteur (s'il ne l'est pas déjà).
async function lcPick(mode){
  const s = lcState || {}, pb = s.prebuilt || {};
  const installed = mode==='fast' ? !!pb.bin : !!s.bin;
  if(installed){
    toast(t('llamacpp.already_installed'));
    return;
  }
  if(mode === 'fast'){
    if(!await askConfirm(t('llamacpp.confirm_prebuilt_body'), {title:t('llamacpp.prebuilt_title'), okText:t('llamacpp.install_btn')})) return;
    const r = await jpost('/api/llamacpp/prebuilt', {});
    if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
  } else {
    if(!await askConfirm(t('llamacpp.confirm_compile_body'), {title:t('llamacpp.compiled_title'), okText:t('llamacpp.compile_btn')})) return;
    const r = await jpost('/api/llamacpp/install', {});
    if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
  }
  lcStartPolling();
}

// « Mettre à jour » sur une carte installée.
async function lcUpdate(mode){
  if(mode === 'fast'){
    if(!await askConfirm(t('llamacpp.confirm_update_prebuilt_body'), {title:t('llamacpp.update_title'), okText:t('llamacpp.update_title')})) return;
    const r = await jpost('/api/llamacpp/prebuilt', {});
    if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
  } else {
    if(!await askConfirm(t('llamacpp.confirm_update_compiled_body'), {title:t('llamacpp.update_title'), okText:t('llamacpp.update_title')})) return;
    const r = await jpost('/api/llamacpp/update', {clean:false});
    if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
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
    ? '<span class="lc-mode-active-tag">✓ '+n+' '+(n>1?t('llamacpp.installed_plural'):t('llamacpp.installed_singular'))+'</span><span class="lc-mode-go">'+t('llamacpp.manage_link')+'</span>'
    : '<span class="lc-mode-go">'+t('llamacpp.view_install_link')+'</span>';
}

function openCustomBackends(){
  showModal('lc-custom-modal');
  document.getElementById('lc-custom-url').value = '';
  document.getElementById('lc-custom-name').value = '';
  loadCustomBackends();
}
function closeCustomBackends(){ hideModal('lc-custom-modal'); }

// Liste les backends custom dans le modal, avec un bouton supprimer par ligne.
async function loadCustomBackends(){
  const box = document.getElementById('lc-custom-list');
  box.innerHTML = '<span class="muted" style="font-size:12px">'+t('llamacpp.loading')+'</span>';
  let list = [];
  try{ list = await jget('/api/backends/custom') || []; }catch(_){}
  lcCustomBackends = list;
  if(!list.length){ box.innerHTML = '<span class="muted" style="font-size:12px">'+t('llamacpp.no_custom_backends')+'</span>'; return; }
  box.innerHTML = list.map(b=>{
    const nm = String(b.name).replace(/[<>&]/g,'');
    const used = b.in_use ? '<span class="mcp-tag" style="border-color:var(--accent);color:var(--accent)">'+t('llamacpp.in_use_tag')+'</span>' : '';
    return '<div class="mcp-row" style="cursor:default">'
      + '<span class="mcp-dot '+(b.in_use?'mcp-dot-ok':'mcp-dot-off')+'"></span>'
      + '<div class="mcp-info"><div class="mcp-name">'+nm+'</div>'
      + '<div class="mcp-meta">'+used+'<span class="mcp-tag" title="'+String(b.path).replace(/"/g,'&quot;')+'">'+String(b.path).split('/').slice(-3).join('/').replace(/[<>&]/g,'')+'</span></div></div>'
      + '<button class="btn-danger" style="padding:3px 8px;font-size:11px" onclick="lcUninstallCustom(\''+nm.replace(/'/g,"\\'")+'\')">'+t('llamacpp.delete_btn')+'</button>'
      + '</div>';
  }).join('');
}

// Installer un backend CUSTOM depuis une URL de dépôt Git (fork llama.cpp).
// Cloné + compilé dans backends/<nom>, SANS toucher au moteur global : il
// apparaît ensuite dans le menu « backend détecté » de l'éditeur de modèle.
async function lcInstallCustom(){
  const url = (document.getElementById('lc-custom-url').value||'').trim();
  if(!url){ toast(t('llamacpp.paste_git_url')); return; }
  const name = (document.getElementById('lc-custom-name').value||'').trim();
  if(!await askConfirm(t('llamacpp.confirm_custom_prefix')+'\n'+url+'\n\n'+t('llamacpp.confirm_custom_suffix'), {title:t('llamacpp.custom_backend_title'), okText:t('llamacpp.install_btn')})) return;
  const r = await jpost('/api/llamacpp/install-custom', {repo:url, name});
  if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
  closeCustomBackends();
  document.getElementById('lc-details').open = true;
  lcStartPolling();
}

// Désinstaller (supprime le dossier backends/<name>). Le serveur refuse si le
// backend sert de moteur au modèle actif.
async function lcUninstallCustom(name){
  if(!await askConfirm(t('llamacpp.confirm_uninstall_prefix')+' '+name+' '+t('llamacpp.confirm_uninstall_suffix'), {title:t('llamacpp.uninstall_title'), okText:t('llamacpp.uninstall_btn'), danger:true})) return;
  const r = await jpost('/api/llamacpp/uninstall-custom', {name});
  if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
  toast(t('llamacpp.backend_deleted'));
  loadCustomBackends();
  lcRenderCustomCard();
}

// --- Progression de l'installation (téléchargement / compilation) ----------
function lcBusy(on){ document.querySelector('.lc-modes').classList.toggle('busy', on); }

function lcStartPolling(){
  document.getElementById('lc-job').style.display = '';
  document.getElementById('lc-log').textContent = '';
  const dis = document.getElementById('lc-job-dismiss'); if(dis) dis.hidden = true;
  lcLogNext = 0;
  lcSeenEnd = false; lcEndShown = false;
  lcBusy(true);
  if(lcPoll) clearInterval(lcPoll);
  lcPoll = setInterval(lcPollJob, 1000);
  lcPollJob();
}

async function lcPollJob(quiet){
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
  lcChipSync(j);
  if(j.running){
    phaseEl.innerHTML = '<span class="lc-spin">⏳</span> <span>'+String(j.phase||'…').replace(/[<>&]/g,'')+'</span>';
    return;
  }
  if(lcPoll){ clearInterval(lcPoll); lcPoll = null; }
  lcBusy(false);
  // Job terminé : le bouton « masquer » devient utile — il efface définitivement
  // ce résultat pour qu'une erreur ne réapparaisse pas à chaque démarrage (#37).
  const dis = document.getElementById('lc-job-dismiss');
  if(dis) dis.hidden = false;
  if(j.error){
    phaseEl.innerHTML = '<span style="color:var(--err)">✗ '+String(j.error).replace(/[<>&]/g,'')+'</span>';
    const pre = document.getElementById('lc-log');
    if(pre.hasAttribute('hidden')) lcToggleLog();
    pre.scrollTop = pre.scrollHeight;
    if(!quiet) toast(t('llamacpp.failed_toast'));
  } else {
    phaseEl.innerHTML = '<span style="color:var(--ok)">✓ '+String(j.phase||t('llamacpp.done')).replace(/[<>&]/g,'')+'</span>';
    if(!quiet) toast(t('llamacpp.ready_toast'));
  }
  if(!quiet) loadAll();
}

// Masque (efface) un job terminé : côté serveur l'entête persistée et le journal
// sont supprimés, donc l'erreur en rouge ne revient plus au prochain démarrage.
async function lcDismissJob(){
  try{
    const r = await jpost('/api/llamacpp/job/dismiss', {});
    if(!r.ok){ toast(r.error||t('llamacpp.cannot_dismiss')); return; }
  }catch(_){ toast(t('llamacpp.network_error')); return; }
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
