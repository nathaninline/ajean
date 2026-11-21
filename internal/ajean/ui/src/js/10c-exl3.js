// Moteur ExLlama (quants EXL3, servis par TabbyAPI) : installation, mise à jour
// et modèles dans la section Moteur (backend_exl3.go), réglages par preset dans
// l'éditeur de modèle (ENGINE=exl3). L'installation et les téléchargements sont
// des tâches comme celles de llama.cpp : même suivi (#lc-job), même annulation.
let exl3State = null;

async function loadExl3(check){
  let s;
  try{ s = await jget('/api/exl3' + (check ? '?check=1' : '')); }catch(_){ return null; }
  exl3State = s;
  const env = s.env || {};
  const row = document.getElementById('lc-mode-exl3');
  // visible dès qu'une carte NVIDIA est là : un pilote trop ancien est expliqué
  // plutôt que de faire disparaître le moteur
  const visible = !!(env.supported || (env.gpus||[]).length);
  if(row){
    row.hidden = !visible;
    row.classList.toggle('installed', !!s.installed);
  }
  const be = document.getElementById('m-be-exl3');
  if(be) be.hidden = !visible && !(typeof cfgReadKey === 'function' && cfgReadKey('ENGINE') === 'exl3');
  if(typeof beSelSync === 'function') beSelSync();
  const st = document.getElementById('lc-exl3-state');
  if(st){
    if(!env.supported) st.textContent = env.reason || '';
    else if(s.installed){
      st.innerHTML = '<span class="lc-mode-active-tag">'+t('llamacpp.installed_tag')+'</span>'
        + '<span class="lc-update-link" onclick="event.stopPropagation();exl3Install(true)">'+t('llamacpp.update_link')+'</span>'
        + '<span class="lc-update-link" onclick="event.stopPropagation();exl3Check()">'+t('llamacpp.check_version')+'</span>';
    } else {
      st.innerHTML = '<span class="lc-mode-go">'+t('llamacpp.click_to_install')+'</span>';
    }
  }
  return s;
}

function exl3GB(n){ return n+' '+t('exl3.unit_gb'); }
function exl3GpuName(g){ return String(g.name||'').replace(/^NVIDIA GeForce /,'').replace(/^NVIDIA /,''); }
function exl3Date(iso){
  if(!iso) return '';
  const d = new Date(iso);
  return isNaN(d) ? '' : d.toLocaleDateString(currentLang() === 'fr' ? 'fr-FR' : 'en-GB');
}
function exl3Kv(a, b){ return '<div class="moe-kv"><span>'+escHtml(a)+'</span><span>'+b+'</span></div>'; }

// « vérifier » : dernière version de TabbyAPI, sans rien installer.
async function exl3Check(){
  toast(t('llamacpp.checking'));
  const s = await loadExl3(true);
  if(!s) return;
  if(s.latest_error){ toast(t('llamacpp.error_prefix')+s.latest_error); return; }
  if(s.latest && s.latest.available) toast(t('exl3.update_available').replace('{date}', exl3Date(s.latest.date)));
  else toast(t('exl3.up_to_date'));
}

async function openExl3(){
  showModal('exl3-modal');
  await loadExl3(true);
  exl3RenderModal();
}
function closeExl3(){ hideModal('exl3-modal'); }

function exl3RenderModal(){
  const s = exl3State || {}, env = s.env || {};
  const cards = (env.gpus||[]).map(g=>exl3GpuName(g)+' '+exl3GB(Math.round(g.vram_gb))).join(' + ');
  document.getElementById('exl3-machine').textContent = env.supported
    ? t('exl3.machine').replace('{gpus}', cards || '?').replace('{driver}', env.driver||'?')
        .replace('{cuda}', env.cuda === 'cu13' ? 'CUDA 13' : 'CUDA 12').replace('{disk}', Math.round(env.disk_free_gb||0))
    : (env.reason || '');
  const ins = s.installed;
  let eng = '';
  if(ins){
    eng += exl3Kv(t('exl3.d_exllama'), escHtml(String(ins.exllamav3 || '?').split('+')[0]));
    eng += exl3Kv(t('exl3.d_tabby'), escHtml(String(ins.commit||'').slice(0,7)) + (ins.date ? ' · '+escHtml(exl3Date(ins.date)) : ''));
    eng += exl3Kv(t('exl3.d_torch'), escHtml(String(ins.torch || '?').split('+')[0]) + (ins.cuda ? ' · ' + (ins.cuda === 'cu13' ? 'CUDA 13' : 'CUDA 12') : ''));
    let upd = s.latest_error ? escHtml(s.latest_error)
      : !s.latest ? '…'
      : s.latest.available ? '<span class="lc-reco">'+escHtml(t('exl3.update_available').replace('{date}', exl3Date(s.latest.date)))+'</span>'
      : escHtml(t('exl3.up_to_date'));
    eng += exl3Kv(t('exl3.d_update'), upd);
  } else {
    eng = exl3Kv(t('exl3.d_state'), escHtml(env.supported ? t('exl3.not_installed') : (env.reason||'')));
  }
  document.getElementById('exl3-engine').innerHTML = eng;
  const models = s.models || [];
  document.getElementById('exl3-models').innerHTML = models.length
    ? models.map(m=>exl3Kv(m.name, escHtml(exl3ModelDesc(m)))).join('')
    : exl3Kv(t('exl3.no_models'), '');
  const go = document.getElementById('exl3-go');
  go.hidden = !!ins;
  go.disabled = !env.supported;
  document.getElementById('exl3-update').hidden = !ins;
  document.getElementById('exl3-remove').hidden = !ins;
  document.getElementById('exl3-note').textContent = ins ? t('exl3.note_installed') : t('exl3.note_install');
}

function exl3ModelDesc(m){
  const parts = [];
  if(m.bpw) parts.push(m.bpw+' bpw');
  if(m.size_gb) parts.push(exl3GB(Math.round(m.size_gb*10)/10));
  if(m.vision) parts.push(t('exl3.vision_tag'));
  return parts.join(' · ');
}

// Installation (commit validé) ou mise à jour (dernier commit de TabbyAPI).
async function exl3Install(latest){
  const s = exl3State || await loadExl3();
  if(!s) return;
  if(!(s.env||{}).supported){ toast((s.env||{}).reason || t('exl3.mode_title')); return; }
  const body = latest ? t('exl3.confirm_update') : t('exl3.confirm_install');
  if(!await askConfirm(body, {title:t('exl3.modal_title'), okText: latest ? t('exl3.update') : t('exl3.install')})) return;
  const r = await jpost('/api/exl3/install', {latest: !!latest});
  if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
  closeExl3();
  exl3FollowJob();
}

async function exl3Uninstall(){
  if(!await askConfirm(t('exl3.confirm_uninstall'), {title:t('exl3.modal_title'), okText:t('exl3.uninstall'), danger:true})) return;
  const r = await jpost('/api/exl3/uninstall', {});
  if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
  toast(t('exl3.uninstalled'));
  await loadExl3();
  exl3RenderModal();
}

// Suivi d'une tâche dans la section Moteur du panneau.
function exl3FollowJob(){
  const side = document.getElementById('side');
  if(side && !side.classList.contains('open') && typeof toggleSide === 'function') toggleSide();
  const det = document.getElementById('lc-details');
  if(det){ det.open = true; det.scrollIntoView({block:'center'}); }
  lcStartPolling();
}

async function exl3Download(){
  const url = document.getElementById('exl3-dl-url').value.trim();
  if(!url) return;
  const r = await jpost('/api/exl3/download', {url});
  if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
  document.getElementById('exl3-dl-url').value = '';
  toast(t('exl3.download_started').replace('{name}', r.name||''));
  closeExl3();
  exl3FollowJob();
}

// ---------------------------------------------------------------------------
// Éditeur de preset (ENGINE=exl3)
// ---------------------------------------------------------------------------

function exl3Installed(){ return !!(exl3State && exl3State.installed); }

// Le modal montre les blocs ExLlama à la place de ceux de llama.cpp.
function syncEngineView(){
  const on = cfgReadKey('ENGINE') === 'exl3';
  document.getElementById('modal').classList.toggle('eng-exl3', on);
  if(on) populateExl3Fields();
}

// Bascule vers ExLlama depuis le sélecteur de moteur : valeurs de départ mesurées
// sur le serveur de test (cache 8 bits, lecture par blocs de 2048).
function exl3Select(){
  cfgWriteKey('ENGINE', 'exl3');
  if(!cfgReadKey('EXL3_CACHE_MODE')) cfgWriteKey('EXL3_CACHE_MODE', '8,8');
  if(!cfgReadKey('EXL3_CHUNK')) cfgWriteKey('EXL3_CHUNK', '2048');
  const models = (exl3State && exl3State.models) || [];
  if(!cfgReadKey('EXL3_MODEL') && models.length) exl3PickModel(models[0]);
  syncEngineView();
}

function exl3PickModel(m){
  cfgWriteKey('EXL3_MODEL', m.path);
  cfgWriteKey('EXL3_VISION', m.vision ? '1' : '');
}

function exl3Model(){
  const cur = cfgReadKey('EXL3_MODEL');
  return ((exl3State && exl3State.models) || []).find(m => m.path === cur || m.name === cur) || null;
}

// Cartes d'un preset ExLlama : clé EXL3_GPUS (ordre PCI, celui de nvidia-smi ;
// vide = toutes), jamais CUDA_VISIBLE_DEVICES, réglage machine de llama.cpp
// reporté d'un preset à l'autre. Un preset écrit par la 0.18.13 y avait mis ses
// cartes : on les déplace.
function exl3FixGpus(){
  const old = cfgReadKey('CUDA_VISIBLE_DEVICES');
  if(old){
    if(!cfgReadKey('EXL3_GPUS')) cfgWriteKey('EXL3_GPUS', old);
    cfgWriteKey('CUDA_VISIBLE_DEVICES', '');
  }
}

// Les cartes que voit le moteur (EXL3_GPUS, vide = toutes), dans
// l'ordre CUDA : c'est cet ordre que suivent la répartition et cuda:N.
function exl3VisibleGpus(){
  const all = ((exl3State && exl3State.env) || {}).gpus || [];
  const sel = cfgReadKey('EXL3_GPUS').split(',').map(x=>x.trim()).filter(Boolean);
  if(!sel.length) return all;
  return sel.map(i => all.find(g => String(g.index) === i)).filter(Boolean);
}

function populateExl3Fields(){
  const s = exl3State || {};
  exl3FixGpus();
  const models = s.models || [];
  const cur = cfgReadKey('EXL3_MODEL');
  const opt = (v, l, sel) => '<option value="'+escHtml(v)+'"'+(sel?' selected':'')+'>'+escHtml(l)+'</option>';
  let html = models.length ? '' : opt('', t('preset.exl3_no_model'), true);
  let known = false;
  for(const m of models){
    const sel = m.path === cur || m.name === cur;
    known = known || sel;
    html += opt(m.path, m.name+' · '+exl3ModelDesc(m), sel);
  }
  if(cur && !known) html = opt(cur, cur, true) + html;
  document.getElementById('s-exl3-model').innerHTML = html;
  // modèle brouillon : un autre dossier EXL3 (même tokenizer)
  const dm = cfgReadKey('EXL3_DRAFT_MODEL');
  document.getElementById('s-exl3-dmodel').innerHTML = opt('', '—', !dm)
    + models.filter(m => m.path !== cur).map(m => opt(m.path, m.name, m.path === dm || m.name === dm)).join('');
  const val = (id, key) => { document.getElementById(id).value = cfgReadKey(key); };
  val('s-exl3-ctx', 'CTX'); val('s-exl3-cache', 'EXL3_CACHE'); val('s-exl3-batch', 'EXL3_BATCH');
  val('s-exl3-dtok', 'EXL3_DRAFT_TOKENS'); val('s-exl3-conf', 'EXL3_DRAFT_CONFIDENCE'); val('s-exl3-hybrid', 'EXL3_HYBRID');
  document.getElementById('s-exl3-dyn').checked = cfgReadKey('EXL3_DRAFT_DYNAMIC') === '1';
  document.getElementById('s-exl3-tpb').value = cfgReadKey('EXL3_TP_BACKEND'); val('s-exl3-rbudget', 'REASONING_BUDGET'); val('s-exl3-reserve', 'EXL3_RESERVE');
  document.getElementById('s-exl3-cache-mode').value = cfgReadKey('EXL3_CACHE_MODE') || 'FP16';
  document.getElementById('s-exl3-chunk').value = cfgReadKey('EXL3_CHUNK') || '2048';
  document.getElementById('s-exl3-draft').value = cfgReadKey('EXL3_DRAFT') || 'disabled';
  document.getElementById('s-exl3-tp').checked = cfgReadKey('EXL3_TP') === '1';
  document.getElementById('s-exl3-vision').checked = cfgReadKey('EXL3_VISION') === '1';
  const rsw = document.getElementById('s-reasoning');
  document.getElementById('s-exl3-reasoning').checked = rsw ? rsw.checked : !/^off$/i.test(cfgReadKey('REASONING'));
  renderExl3Gpus();
  syncExl3Rows();
  requestAnimationFrame(fitSelects);
}

// Lignes qui dépendent d'un autre réglage.
function syncExl3Rows(){
  const draft = document.getElementById('s-exl3-draft').value;
  document.getElementById('s-exl3-dtok-row').hidden = draft === 'disabled';
  document.getElementById('s-exl3-dmodel-row').hidden = draft !== 'model';
  const dynOk = draft === 'mtp' || draft === 'model';
  document.getElementById('s-exl3-dyn-row').hidden = !dynOk;
  document.getElementById('s-exl3-conf-row').hidden = !dynOk || !document.getElementById('s-exl3-dyn').checked;
  document.getElementById('s-exl3-rbudget-row').hidden = !document.getElementById('s-exl3-reasoning').checked;
  const m = exl3Model(), vsw = document.getElementById('s-exl3-vision');
  const noVision = !!m && !m.vision;
  vsw.disabled = noVision;
  if(noVision && vsw.checked){ vsw.checked = false; cfgWriteKey('EXL3_VISION', ''); }
  document.getElementById('s-exl3-vision-sub').textContent = noVision ? t('preset.exl3_no_vision') : t('preset.exl3_vision_sub');
  const vrow = document.getElementById('s-exl3-vdev-row');
  vrow.hidden = !vsw.checked;
  if(vsw.checked){
    const cur = cfgReadKey('EXL3_VISION_DEVICE');
    const gpus = exl3VisibleGpus();
    let html = '<option value="">'+escHtml(t('preset.exl3_vdev_auto'))+'</option>'
      + '<option value="cpu">'+escHtml(t('preset.exl3_vdev_cpu'))+'</option>';
    gpus.forEach((g, i) => { html += '<option value="cuda:'+i+'">'+escHtml(exl3GpuName(g))+'</option>'; });
    const sel = document.getElementById('s-exl3-vdev');
    sel.innerHTML = html;
    sel.value = cur;
    if(sel.value !== cur) sel.value = '';
  }
  const tpRow = document.getElementById('s-exl3-tp-row');
  if(tpRow) tpRow.hidden = exl3VisibleGpus().length < 2;
  const tpbRow = document.getElementById('s-exl3-tpb-row');
  if(tpbRow) tpbRow.hidden = !!(tpRow && tpRow.hidden) || !document.getElementById('s-exl3-tp').checked;
  const resRow = document.getElementById('s-exl3-reserve-row');
  if(resRow) resRow.hidden = !!cfgReadKey('EXL3_GPU_SPLIT');
}

// Cartes graphiques : utilisée ou non, et Go de poids sur chacune (vide = auto).
function renderExl3Gpus(){
  const all = ((exl3State && exl3State.env) || {}).gpus || [];
  const grp = document.getElementById('m-exl3-gpu');
  grp.hidden = all.length < 2;
  if(all.length < 2) return;
  const vis = exl3VisibleGpus();
  const split = cfgReadKey('EXL3_GPU_SPLIT').split(/[,; ]+/).filter(Boolean);
  document.getElementById('s-exl3-gpus').innerHTML = all.map(g => {
    const pos = vis.indexOf(g);
    const on = pos >= 0;
    const gb = on && split[pos] != null ? split[pos] : '';
    return '<div class="pe-row">'
      + '<span class="pe-row-l"><span>'+escHtml(exl3GpuName(g))+'</span><span class="pe-sub">'+exl3GB(Math.round(g.vram_gb))+' · GPU '+g.index+'</span></span>'
      + '<span class="pe-row-c"><input class="pe-val exl3-split" data-gpu="'+g.index+'" type="number" min="0" step="0.5" placeholder="'+escHtml(t('preset.exl3_vdev_auto'))+'"'
      + (on ? '' : ' disabled')+' value="'+escHtml(gb)+'" oninput="onExl3Split()"><span class="pe-unit">'+t('exl3.unit_gb')+'</span>'
      + '<label class="pe-switch"><input type="checkbox" class="exl3-use" data-gpu="'+g.index+'"'+(on?' checked':'')+' onchange="onExl3Use()"><span class="pe-sl"></span></label></span>'
      + '</div>';
  }).join('');
}

function onExl3Use(){
  const all = ((exl3State && exl3State.env) || {}).gpus || [];
  const used = [...document.querySelectorAll('.exl3-use')].filter(c => c.checked).map(c => c.dataset.gpu);
  if(!used.length){ toast(t('preset.exl3_need_gpu')); renderExl3Gpus(); return; }
  cfgWriteKey('EXL3_GPUS', used.length === all.length ? '' : used.join(','));
  // l'ordre a changé : la répartition et la carte de la vision ne correspondent plus
  cfgWriteKey('EXL3_GPU_SPLIT', '');
  cfgWriteKey('EXL3_VISION_DEVICE', '');
  if(used.length < 2) cfgWriteKey('EXL3_TP', '');
  renderExl3Gpus();
  document.getElementById('s-exl3-tp').checked = cfgReadKey('EXL3_TP') === '1';
  syncExl3Rows();
}

function onExl3Split(){
  const vals = exl3VisibleGpus().map(g => {
    const el = document.querySelector('.exl3-split[data-gpu="'+g.index+'"]');
    return el ? el.value.trim() : '';
  });
  // une répartition n'a de sens que complète
  cfgWriteKey('EXL3_GPU_SPLIT', vals.every(v => v !== '' && +v >= 0) ? vals.join(',') : '');
  syncExl3Rows();
}

function onExl3Model(){
  const v = document.getElementById('s-exl3-model').value;
  const m = ((exl3State && exl3State.models) || []).find(x => x.path === v);
  if(m) exl3PickModel(m); else cfgWriteKey('EXL3_MODEL', v);
  populateExl3Fields();
}

function onExl3Draft(){
  const v = document.getElementById('s-exl3-draft').value;
  cfgWriteKey('EXL3_DRAFT', v === 'disabled' ? '' : v);
  if(v === 'mtp' && !cfgReadKey('EXL3_DRAFT_TOKENS')){ cfgWriteKey('EXL3_DRAFT_TOKENS', '3'); document.getElementById('s-exl3-dtok').value = '3'; }
  syncExl3Rows();
}

// Le même interrupteur que celui des réglages llama.cpp (masqués) : saveItem
// lit s-reasoning pour écrire REASONING.
function onExl3Reasoning(on){
  const rsw = document.getElementById('s-reasoning');
  if(rsw) rsw.checked = on;
  cfgWriteKey('REASONING', on ? 'on' : 'off');
  syncExl3Rows();
}

// Téléchargement depuis l'éditeur : suivi sous le champ, le modèle est choisi
// une fois arrivé.
let exl3DlTimer = null;
async function exl3PresetDownload(){
  const inp = document.getElementById('s-exl3-dl'), note = document.getElementById('s-exl3-dl-note');
  const url = inp.value.trim();
  if(!url) return;
  const r = await jpost('/api/exl3/download', {url});
  if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
  inp.value = '';
  note.style.display = '';
  note.textContent = t('exl3.download_started').replace('{name}', r.name||'');
  if(exl3DlTimer) clearInterval(exl3DlTimer);
  exl3DlTimer = setInterval(async () => {
    let j;
    try{ j = await jget('/api/llamacpp/job?from=999999'); }catch(_){ return; }
    if(!document.getElementById('modal').classList.contains('eng-exl3')){ clearInterval(exl3DlTimer); exl3DlTimer = null; return; }
    if(j.running){ note.textContent = j.phase || '…'; return; }
    clearInterval(exl3DlTimer); exl3DlTimer = null;
    if(j.error){ note.textContent = '✗ ' + j.error; return; }
    note.textContent = '✓ ' + (j.phase || '');
    await loadExl3();
    const m = ((exl3State && exl3State.models) || []).find(x => x.name === r.name);
    if(m) exl3PickModel(m);
    populateExl3Fields();
  }, 1500);
}
