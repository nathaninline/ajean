// Moteur MoE : installation « en un clic » du moteur spécialisé Qwen3.8 Flash Next.
// L'utilisateur choisit la version (Swift ou Classique) et la qualité (quant) ;
// tout le reste (cartes, RAM, mmap, réglages) est déduit côté serveur
// (backend_moe.go). L'installation est une tâche comme celles de llama.cpp :
// même suivi (#lc-job), même pastille, même annulation.
let moeState = null, moeFam = '', moeQuant = '', moeTarget = '';

async function loadMoe(){
  let s;
  try{ s = await jget('/api/moe'); }catch(_){ return; }
  moeState = s;
  const row = document.getElementById('lc-mode-moe');
  if(!row) return;
  // Hors Linux + NVIDIA, la ligne n'apparaît pas : rien à y proposer.
  row.hidden = !(s.env && s.env.supported);
  const st = document.getElementById('lc-moe-state');
  if(st) st.textContent = (s.installed||[]).length
    ? t('moe.state_installed').replace('{n}', s.installed.length)
    : '';
}

// presetId (optionnel) : ouverte depuis l'engrenage d'un modèle installé, la
// fenêtre se place directement sur lui.
async function openMoe(presetId){
  // état relu à chaque ouverture : juste après une installation, l'ancien
  // proposait encore « Installer »
  await loadMoe();
  if(!moeState) return;
  moeTarget = typeof presetId === 'string' ? presetId : '';
  const s = moeState, env = s.env || {};
  const cards = (env.gpus||[]).filter(g=>g.index===env.main || g.index===env.helper)
    .sort((a,b)=>(a.index===env.main?-1:1))
    .map(g=>g.name.replace(/^NVIDIA GeForce /,'')+' '+Math.round(g.vram_gb)+' Go');
  document.getElementById('moe-machine').textContent =
    t('moe.machine').replace('{gpus}', cards.join(' + '))
      .replace('{ram}', Math.round(env.ram_gb)).replace('{disk}', Math.round(env.disk_free_gb));
  if(!moeFam) moeFam = (s.families[0]||{}).id || '';
  if(moeTarget){
    for(const f of s.families){
      const hit = f.fits.find(x=>x.preset===moeTarget);
      if(hit){ moeFam = f.id; moeQuant = hit.quant.id; }
    }
  }
  moeRenderFams();
  moeRenderQuants(!moeTarget);
  showModal('moe-modal');
}
function closeMoe(){ hideModal('moe-modal'); }

function moeRenderFams(){
  const box = document.getElementById('moe-fams');
  box.innerHTML = moeState.families.map(f=>
    '<label><input type="radio" name="moe-fam" value="'+f.id+'"'+(f.id===moeFam?' checked':'')
    +' onchange="moeFam=this.value;moeRenderQuants(true)">'
    +'<span>'+escHtml(f.label)+'</span><span class="be-note">'+escHtml(f.about)+'</span></label>').join('');
}

function moeRenderQuants(pickReco){
  const f = moeState.families.find(x=>x.id===moeFam);
  if(!f) return;
  // à l'ouverture : le modèle actif s'il est de cette version, sinon un installé, sinon le conseillé
  if(pickReco || !f.fits.some(x=>x.quant.id===moeQuant && (x.ok || x.preset))){
    const cur = f.fits.find(x=>x.active) || f.fits.find(x=>x.preset);
    moeQuant = cur ? cur.quant.id : (f.recommend || '');
  }
  const box = document.getElementById('moe-quants');
  box.innerHTML = f.fits.map(x=>{
    const q = x.quant, sel = q.id===moeQuant;
    const tag = x.active ? t('moe.active') : x.preset ? t('moe.installed') : q.id===f.recommend ? t('moe.recommended') : '';
    const reco = tag ? ' <span class="lc-reco">'+tag+'</span>' : '';
    const usable = x.ok || !!x.preset;   // installé : activable même si l'espace disque manque
    const desc = x.preset ? escHtml(q.about)
      : x.ok ? escHtml(q.about)+' · '+t('moe.download').replace('{gb}', Math.round(x.disk_gb))
      : escHtml(x.why);
    return '<div class="lc-mode'+(sel?' moe-sel':'')+(usable?'':' moe-off')+'"'
      +(usable?' onclick="moeQuant=\''+q.id+'\';moeRenderQuants(false)"':'')+'>'
      +'<div class="lc-mode-t">'+q.id+reco+'</div><div class="lc-mode-d">'+desc+'</div></div>';
  }).join('');
  const fit = f.fits.find(x=>x.quant.id===moeQuant);
  const go = document.getElementById('moe-go');
  go.disabled = !fit || !!fit.active;
  document.getElementById('moe-bench').hidden = !(fit && fit.active);
  go.textContent = fit && fit.active ? t('moe.is_active') : fit && fit.preset ? t('moe.activate') : t('moe.install');
  moeRenderDisk(fit);
  moeRenderSettings(fit);
  moeRenderDetails(fit);
  document.getElementById('moe-note').textContent = !fit ? t('moe.nothing_fits')
    : fit.active ? t('moe.note_active') : fit.preset ? t('moe.note_installed')
    : (fit.mmap ? t('moe.note_mmap') : t('moe.note'));
}

// Place disque : libre, ce que ce choix occupe, ce qui restera.
function moeRenderDisk(fit){
  const el = document.getElementById('moe-disk');
  const free = Math.round((moeState.env||{}).disk_free_gb||0);
  if(!fit){ el.textContent = ''; return; }
  if(fit.preset){ el.textContent = t('moe.disk_installed').replace('{free}', free); return; }
  const need = Math.round(fit.disk_gb), left = free - need;
  el.textContent = (left >= 5 ? t('moe.disk_ok') : t('moe.disk_short'))
    .replace('{free}', free).replace('{need}', need).replace('{left}', Math.max(left, 0));
}

// Réglages modifiables d'un modèle installé.
function moeRenderSettings(fit){
  const fld = document.getElementById('moe-settings-fld'), save = document.getElementById('moe-save');
  const d = fit && fit.details;
  fld.hidden = save.hidden = !d;
  if(!d) return;
  const sel = (id, opts, cur) => '<select class="ctl" id="'+id+'" style="width:auto">'
    + opts.map(([v,l])=>'<option value="'+v+'"'+(String(v)===String(cur)?' selected':'')+'>'+escHtml(l)+'</option>').join('')+'</select>';
  const sw = (id, on) => '<label class="switch"><input type="checkbox" id="'+id+'"'+(on?' checked':'')+'><span class="slider"></span></label>';
  const rows = [
    [t('moe.d_ctx'), '<span class="moe-ctx"><input type="range" id="moe-s-ctx" min="32768" max="262144" step="4096" value="'+(+d.ctx||131072)+'" oninput="moeCtxLbl()"><span id="moe-s-ctx-v"></span></span>'],
    [t('moe.d_kv'), sel('moe-s-kv', [['fp16', t('moe.kv_fp16')],['int8', t('moe.kv_int8')]], d.kv)],
    [t('moe.d_mtp'), sel('moe-s-spec', [[2,'2'],[3,'3'],[4,'4']], d.spec)],
    [t('moe.s_vision'), sw('moe-s-vision', d.vision)],
  ];
  if(d.has_helper) rows.push([t('moe.s_helper'), sw('moe-s-helper', d.helper_on)]);
  document.getElementById('moe-settings').innerHTML = rows.map(([a,b])=>
    '<div class="moe-kv"><span>'+escHtml(a)+'</span><span>'+b+'</span></div>').join('');
  moeCtxLbl();
}
// valeur exacte en jetons (131 072 et non « 128K », source de confusion)
function moeCtxLbl(){
  const r = document.getElementById('moe-s-ctx'), o = document.getElementById('moe-s-ctx-v');
  if(!r || !o) return;
  o.textContent = (+r.value).toLocaleString('fr-FR');
  // partie remplie à gauche de la pastille (la piste est dessinée en CSS)
  r.style.setProperty('--p', ((r.value - r.min) / (r.max - r.min) * 100) + '%');
}

async function moeSaveSettings(){
  const f = moeState && moeState.families.find(x=>x.id===moeFam);
  const fit = f && f.fits.find(x=>x.quant.id===moeQuant);
  if(!fit || !fit.preset) return;
  const v = id => document.getElementById(id);
  const body = {preset: fit.preset, ctx: +v('moe-s-ctx').value, kv: v('moe-s-kv').value, spec: +v('moe-s-spec').value,
    vision: v('moe-s-vision').checked, helper: v('moe-s-helper') ? v('moe-s-helper').checked : false};
  if(fit.active && !await askConfirm(t('moe.save_confirm'), {title:t('moe.modal_title'), okText:t('moe.save')})) return;
  const r = await jpost('/api/moe/settings', body);
  if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
  toast(r.restarted ? t('moe.saved_restart') : t('moe.saved'));
  moeTarget = fit.preset;
  await openMoe(fit.preset);
  if(typeof loadPresets === 'function') loadPresets();
}

// La config qui tourne (ou tournera) pour un modèle installé.
function moeRenderDetails(fit){
  const fld = document.getElementById('moe-details-fld');
  const d = fit && fit.details;
  fld.hidden = !d;
  if(!d) return;
  const k = n => (+n >= 1024 ? Math.round(+n/1024)+'K' : n);
  const rows = [
    [t('moe.d_main'), d.main_gpu],
    [t('moe.d_helper'), d.helper_gpu || t('moe.d_none')],
    [t('moe.d_vision'), d.vision_gpu || t('moe.d_none')],
    [t('moe.d_ctx'), (+d.ctx).toLocaleString('fr-FR')+' '+t('moe.d_tokens')],
    [t('moe.d_kv'), d.kv + (d.kv_resident ? ' · '+t('moe.d_kv_resident').replace('{n}', k(d.kv_resident)) : '')],
    [t('moe.d_mtp'), t('moe.d_mtp_val').replace('{n}', d.spec)],
    [t('moe.d_ram'), d.drop ? t('moe.d_drop') : d.mmap ? t('moe.d_mmap') : t('moe.d_inram')],
    [t('moe.d_read'), t('moe.d_read_val').replace('{chunk}', k(d.prefill)).replace('{short}', d.short_read)],
    [t('moe.d_root'), t('moe.d_root_val').replace('{n}', d.cache_root)],
    [t('moe.d_version'), d.version],
  ];
  document.getElementById('moe-details').innerHTML = rows.map(([a,b])=>
    '<div class="moe-kv"><span>'+escHtml(a)+'</span><span>'+escHtml(b)+'</span></div>').join('');
}

async function moeInstall(){
  const f = moeState && moeState.families.find(x=>x.id===moeFam);
  const fit = f && f.fits.find(x=>x.quant.id===moeQuant);
  if(!fit || fit.active) return;
  // déjà installé : simple bascule, la même que dans la liste des modèles
  if(fit.preset){
    closeMoe();
    await switchTo(0, f.label+' '+moeQuant, fit.preset);
    loadMoe();
    return;
  }
  const msg = t('moe.confirm').replace('{name}', f.label+' '+moeQuant).replace('{gb}', Math.round(fit.disk_gb));
  if(!await askConfirm(msg, {title:t('moe.modal_title'), okText:t('moe.install')})) return;
  const r = await jpost('/api/moe/install', {family:moeFam, quant:moeQuant});
  if(!r.ok){ toast(t('llamacpp.error_prefix')+(r.error||'')); return; }
  closeMoe();
  document.getElementById('lc-details').open = true;
  lcStartPolling();
}

