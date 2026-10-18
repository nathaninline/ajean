// Moteur MoE : installation « en un clic » du moteur spécialisé Qwen3.8 Flash Next.
// L'utilisateur choisit la version (Swift ou Classique) et la qualité (quant) ;
// tout le reste (cartes, RAM, mmap, réglages) est déduit côté serveur
// (backend_moe.go). L'installation est une tâche comme celles de llama.cpp :
// même suivi (#lc-job), même pastille, même annulation.
let moeState = null, moeFam = '', moeQuant = '', moeTarget = '', moeRequestedBackend = '';

async function loadMoe(backend=moeRequestedBackend){
  let s;
  try{ s = await jget('/api/moe'+(backend==='hip'?'?backend=hip':'')); }catch(_){ return; }
  moeState = s;
  if(backend==='hip') return;
  const hipRow=document.getElementById('lc-mode-hip');
  if(hipRow) hipRow.hidden=!(s.hip_recipes||[]).length && !(s.hip_installables||[]).length;
  const row = document.getElementById('lc-mode-moe');
  if(!row) return;
  // Hors Linux + NVIDIA compatible, la ligne n'apparaît pas : rien à y proposer.
  // Un prérequis manquant (Python, venv) la laisse visible, avec sa raison :
  // la cacher faisait disparaître le moteur sans explication (issue #125).
  const env = s.env || {};
  row.hidden = !(env.supported || env.visible);
  const st = document.getElementById('lc-moe-state');
  if(st) st.textContent = env.backend==='hip' ? t('moe.hip_available') : !env.supported ? (env.reason || '')
    : (s.installed||[]).length ? t('moe.state_installed').replace('{n}', s.installed.length)
    : '';
  const title=row.querySelector('[data-i18n]');
  if(title){const key=env.backend==='hip'?'moe.hip_engine_label':'moe.mode_title';title.setAttribute('data-i18n',key);title.textContent=t(key);}
  const description=row.querySelector('.lc-mode-d');
  if(description){const key=env.backend==='hip'?'moe.hip_engine_desc':'moe.mode_desc';description.setAttribute('data-i18n',key);description.textContent=t(key);}
}

// presetId (optionnel) : ouverte depuis l'engrenage d'un modèle installé, la
// fenêtre se place directement sur lui.
async function openMoe(presetId, backend){
  document.getElementById('moe-save').disabled=false;
  const modalTitle=document.getElementById('moe-modal-title');
  modalTitle.setAttribute('data-i18n','moe.modal_title');modalTitle.textContent=t('moe.modal_title');
  // état relu à chaque ouverture : juste après une installation, l'ancien
  // proposait encore « Installer »
  if(!backend && typeof presetId==='string' && moeState &&
     (moeState.hip_recipes||[]).some(r=>r.preset===presetId)) backend='hip';
  moeRequestedBackend=backend||'';
  await loadMoe(moeRequestedBackend);
  if(!moeState) return;
  if((moeState.env||{}).backend === "hip") { modalTitle.setAttribute("data-i18n", "moe.hip_title"); modalTitle.textContent=t("moe.hip_title"); openMoeHIP(presetId); return; }
  document.getElementById('moe-hip-result').hidden=true;
  document.getElementById('moe-go').title='';
  document.getElementById('moe-save').title='';
  document.getElementById('moe-save').setAttribute('data-i18n','moe.save');
  if(!(moeState.env||{}).supported){ toast(moeState.env.reason || t('moe.chip')); return; }
  moeTarget = typeof presetId === 'string' ? presetId : '';
  const s = moeState, env = s.env || {};
  const cards = (env.gpus||[]).filter(g=>g.index===env.main || g.index===env.helper)
    .sort((a,b)=>(a.index===env.main?-1:1))
    .map(g=>g.name.replace(/^NVIDIA GeForce /,'')+' '+Math.round(g.vram_gb)+' Go');
  document.getElementById('moe-machine').textContent =
    t('moe.machine').replace('{gpus}', cards.join(' + '))
      .replace('{ram}', Math.round(env.ram_gb)).replace('{disk}', Math.round(env.disk_free_gb));
  if(!s.families.some(f=>f.id===moeFam)) moeFam = (s.families[0]||{}).id || '';
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
function closeMoe(){ hideModal('moe-modal'); moeRequestedBackend=''; }

function moeRenderFams(){
  if(moeIsHIP()){ moeRenderHIPFams(); return; }
  const box = document.getElementById('moe-fams');
  box.innerHTML = moeState.families.map(f=>
    '<label><input type="radio" name="moe-fam" value="'+f.id+'"'+(f.id===moeFam?' checked':'')
    +' onchange="moeFam=this.value;moeRenderQuants(true)">'
    +'<span>'+escHtml(f.label)+'</span><span class="be-note">'+escHtml(f.about)+'</span></label>').join('');
}

function moeRenderQuants(pickReco){
  if(moeIsHIP()){ moeRenderHIPQuants(pickReco); return; }
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
  go.setAttribute('data-i18n',fit&&fit.active?'moe.is_active':fit&&fit.preset?'moe.activate':'moe.install');
  go.title='';
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
  if(moeIsHIP()){ moeRenderHIPSettings(fit); return; }
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
  // Seconde carte : aide aux experts (défaut mesuré) ou partage des couches,
  // souvent plus rapide avec deux cartes identiques (#121).
  if(d.has_helper) rows.push([t('moe.s_helper'), sel('moe-s-helper',
    [['off', t('moe.second_off')], ['helper', t('moe.second_helper')], ['layers', t('moe.second_layers')]],
    !d.helper_on ? 'off' : d.split_layers ? 'layers' : 'helper')]);
  // VRAM laissée à l'écran quand la carte principale gère aussi l'affichage (#147)
  const res = [0, 512, 1024, 1536, 2048, 3072, 4096];
  if(d.reserve_mib && !res.includes(d.reserve_mib)) res.push(d.reserve_mib);
  rows.push([t('moe.s_reserve'), sel('moe-s-reserve', res.sort((a,b)=>a-b).map(n=>[n, n ? (n/1024).toLocaleString('fr-FR')+' Go' : t('moe.reserve_none')]), d.reserve_mib||0)]);
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
  r.style.setProperty('--p', (r.max>r.min ? (r.value-r.min)/(r.max-r.min)*100 : 0) + '%');
}

async function moeSaveSettings(){
  if(moeIsHIP()){ await saveMoeHIP(); return; }
  const f = moeState && moeState.families.find(x=>x.id===moeFam);
  const fit = f && f.fits.find(x=>x.quant.id===moeQuant);
  if(!fit || !fit.preset) return;
  const v = id => document.getElementById(id);
  const body = {preset: fit.preset, ctx: +v('moe-s-ctx').value, kv: v('moe-s-kv').value, spec: +v('moe-s-spec').value,
    vision: v('moe-s-vision').checked, reserve_mib: +v('moe-s-reserve').value};
  const second = v('moe-s-helper') ? v('moe-s-helper').value : 'off';
  body.helper = second !== 'off';
  if(body.helper) body.split = second;
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
  if(moeIsHIP()){ moeRenderHIPDetails(fit); return; }
  const fld = document.getElementById('moe-details-fld');
  const d = fit && fit.details;
  fld.hidden = !d;
  if(!d) return;
  const k = n => (+n >= 1024 ? Math.round(+n/1024)+'K' : n);
  const rows = [
    [t('moe.d_main'), d.main_gpu],
    [t('moe.d_helper'), d.helper_gpu ? d.helper_gpu + (d.split_layers ? ' · '+t('moe.second_layers') : '') : t('moe.d_none')],
    [t('moe.d_vision'), d.vision_gpu || t('moe.d_none')],
    [t('moe.s_reserve'), d.reserve_mib ? (d.reserve_mib/1024).toLocaleString('fr-FR')+' Go' : t('moe.reserve_none')],
    [t('moe.d_ctx'), (+d.ctx).toLocaleString('fr-FR')+' '+t('moe.d_tokens')],
    [t('moe.d_kv'), d.kv + (d.kv_resident ? ' · '+t('moe.d_kv_resident').replace('{n}', k(d.kv_resident)) : '')],
    [t('moe.d_mtp'), t('moe.d_mtp_val').replace('{n}', d.spec)],
    [t('moe.d_ram'), d.drop ? t('moe.d_drop') : d.mmap ? t('moe.d_mmap') : t('moe.d_inram')],
    [t('moe.d_read'), t('moe.d_read_val').replace('{chunk}', d.prefill ? k(d.prefill) : 'auto').replace('{short}', d.short_read)],
    [t('moe.d_root'), t('moe.d_root_val').replace('{n}', d.cache_root)],
    [t('moe.d_version'), d.version],
  ];
  document.getElementById('moe-details').innerHTML = rows.map(([a,b])=>
    '<div class="moe-kv"><span>'+escHtml(a)+'</span><span>'+escHtml(b)+'</span></div>').join('');
}

async function moeInstall(){
  if(moeIsHIP()){ await installMoeHIP(); return; }
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


// HIP reuses the original MoE modal, family segments, quality cards and footer.
// Only verified operator metadata is shown; no CUDA choices or scores are inferred.
function moeIsHIP(){ return !!(moeState && (moeState.env||{}).backend==='hip'); }
function moeHIPStored(key){ try{ return localStorage.getItem(key)||''; }catch(_){ return ''; } }
function moeHIPRemember(recipe){
  if(!recipe) return;
  try{
    localStorage.setItem('ajean.moe.hip.recipe', recipe.id);
    localStorage.setItem('ajean.moe.hip.recipe.'+(recipe.derived_from||recipe.id), recipe.id);
  }catch(_){}
}
function moeHIPFits(){
  const groups=new Map();
  for(const recipe of (moeState.hip_recipes||[])){
    const key=recipe.derived_from||recipe.id;
    if(!groups.has(key)) groups.set(key, []);
    groups.get(key).push(recipe);
  }
  const out=[];
  for(const [key, recipes] of groups){
    const stored=moeHIPStored('ajean.moe.hip.recipe.'+key)||moeHIPStored('ajean.moe.hip.recipe');
    const recipe=recipes.find(r=>r.id===stored)||recipes.find(r=>r.id===key)||recipes[0];
    out.push({id:key, recipe, family:recipe.family, installed:true,
      quant:typeof recipe.quant==='string' ? recipe.quant : (recipe.quant||{}).id,
      details:recipe.details||{}, ready:recipe.ready_to_prepare===true});
  }
  for(const bundle of (moeState.hip_installables||[])){
    if(groups.has(bundle.id)) continue;
    out.push({id:bundle.id, bundle, family:bundle.family, installed:false,
      quant:typeof bundle.quant==='string' ? bundle.quant : (bundle.quant||{}).id,
      details:bundle.details||{}, ready:true});
  }
  return out;
}
function moeHIPSelected(){ return moeHIPFits().find(f=>f.family===moeFam && f.id===moeQuant); }
function openMoeHIP(presetId){
  const recipes=moeState.hip_recipes||[];
  const wanted=typeof presetId==='string' && presetId ? presetId : moeHIPStored('ajean.moe.hip.recipe');
  const selected=recipes.find(r=>r.id===wanted || r.preset===wanted || wanted.startsWith('AMD HIP '+r.id+' '));
  if(selected){ moeHIPRemember(selected); moeFam=selected.family; moeQuant=selected.derived_from||selected.id; }
  const fits=moeHIPFits();
  if(!fits.some(f=>f.family===moeFam)){ moeFam=(fits.find(f=>f.family==='swift')||fits[0]||{}).family||'swift'; }
  document.getElementById('moe-machine').textContent=t('moe.hip_machine');
  document.getElementById('moe-bench').hidden=true;
  const result=document.getElementById('moe-hip-result'); result.hidden=false; result.textContent='';
  moeRenderFams(); moeRenderQuants(false);
  showModal('moe-modal');
}
function moeRenderHIPFams(){
  const box=document.getElementById('moe-fams'); box.replaceChildren();
  const fits=moeHIPFits();
  for(const family of ['swift','qwen']){
    const label=document.createElement('label'), input=document.createElement('input');
    input.type='radio'; input.name='moe-fam'; input.value=family; input.checked=moeFam===family;
    input.disabled=!fits.some(f=>f.family===family);
    input.onchange=()=>{moeFam=family;moeRenderQuants(true);};
    const title=document.createElement('span'); title.textContent=t(family==='swift'?'moe.hip_swift':'moe.hip_classic');
    const about=document.createElement('span'); about.className='be-note';
    about.textContent=t(family==='swift'?'moe.hip_swift_about':'moe.hip_classic_about');
    label.append(input,title,about); box.appendChild(label);
  }
}
function moeRenderHIPQuants(pickFirst){
  const fits=moeHIPFits().filter(f=>f.family===moeFam);
  if(pickFirst || !fits.some(f=>f.id===moeQuant)) moeQuant=(fits[0]||{}).id||'';
  const box=document.getElementById('moe-quants'); box.replaceChildren();
  for(const fit of fits){
    const card=document.createElement('div');
    card.className='lc-mode'+(fit.id===moeQuant?' moe-sel':'')+(!fit.ready?' moe-off':'');
    card.dataset.hipId=fit.id; card.setAttribute('role','button'); card.tabIndex=0;
    card.setAttribute('aria-pressed',String(fit.id===moeQuant));
    const title=document.createElement('div'); title.className='lc-mode-t';
    title.textContent=fit.quant||t('moe.hip_quant_unknown');
    const badge=document.createElement('span'); badge.className='lc-reco';
    badge.textContent=fit.installed?t('moe.installed'):t('moe.hip_installable'); title.append(' ',badge);
    const desc=document.createElement('div'); desc.className='lc-mode-d';
    desc.textContent=(fit.recipe||fit.bundle).label+(fit.ready?'':' · '+t('moe.hip_not_verified'));
    const select=()=>{moeQuant=fit.id;if(fit.recipe)moeHIPRemember(fit.recipe);moeRenderQuants(false);};
    card.onclick=select;
    card.onkeydown=e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();select();}};
    card.append(title,desc); box.appendChild(card);
  }
  const fit=fits.find(f=>f.id===moeQuant);
  if(fit && fit.recipe) moeHIPRemember(fit.recipe);
  const go=document.getElementById('moe-go'), save=document.getElementById('moe-save');
  const installed=!!(fit&&fit.installed);
  go.disabled=!fit || installed || !fit.ready;
  go.textContent=t(installed?'moe.activate':'moe.hip_install');
  go.setAttribute('data-i18n',installed?'moe.activate':'moe.hip_install');
  go.title=installed?t('moe.hip_qualification'):'';
  save.hidden=!installed;
  save.disabled=!fit || !fit.ready;
  save.textContent=t('moe.hip_settings_save'); save.setAttribute('data-i18n','moe.hip_settings_save');
  document.getElementById('moe-bench').hidden=true;
  document.getElementById('moe-disk').textContent=installed?t('moe.hip_installed_note'):'';
  moeRenderSettings(fit); moeRenderDetails(fit);
  document.getElementById('moe-note').textContent=fit?t('moe.hip_qualification'):t('moe.hip_empty');
}
function moeHIPRules(fit){ return (fit && fit.recipe && fit.recipe.settings_supported)||{}; }
function moeHIPRows(target, rows){
  const box=document.getElementById(target); box.replaceChildren();
  for(const [name,value] of rows){
    const row=document.createElement('div'); row.className='moe-kv';
    const label=document.createElement('span'); label.textContent=name;
    const content=document.createElement('span');
    if(value instanceof Node)content.appendChild(value); else content.textContent=String(value);
    row.append(label,content); box.appendChild(row);
  }
}
function moeRenderHIPSettings(fit){
  const fld=document.getElementById('moe-settings-fld'); fld.hidden=!fit;
  if(!fit) return;
  const d=fit.details, rules=moeHIPRules(fit), rows=[];
  const select=(id,values,current,allowed)=>{
    const el=document.createElement('select'); el.id=id; el.className='ctl'; el.style.width='auto';
    const choices=Array.from(new Set([...(values||[]), ...(current!==undefined?[current]:[])]));
    for(const value of choices){const option=document.createElement('option');option.value=String(value);option.textContent=String(value);el.appendChild(option);}
    if(current!==undefined) el.value=String(current);
    el.disabled=!allowed; if(!allowed)el.title=t('moe.hip_readonly'); return el;
  };
  const range=document.createElement('input'); range.type='range'; range.id='moe-s-ctx';
  const ctxRule=rules.ctx && typeof rules.ctx==='object' && Number.isFinite(+rules.ctx.min) && Number.isFinite(+rules.ctx.max) && +rules.ctx.max>=+rules.ctx.min && +rules.ctx.step>0 ? rules.ctx : null;
  range.min=String(ctxRule?ctxRule.min:(d.ctx||0)); range.max=String(ctxRule?ctxRule.max:(d.ctx||0));
  range.step=String(ctxRule?ctxRule.step:1); range.value=String(d.ctx||0); range.disabled=!ctxRule;
  if(!ctxRule)range.title=t('moe.hip_readonly'); range.oninput=moeCtxLbl;
  const ctx=document.createElement('span'); ctx.className='moe-ctx';
  const value=document.createElement('span'); value.id='moe-s-ctx-v'; ctx.append(range,value);
  rows.push([t('moe.d_ctx'),d.ctx===undefined?t('moe.hip_setting_unset'):ctx]);
  rows.push([t('moe.d_kv'),select('moe-s-kv',rules.kv,d.kv,Array.isArray(rules.kv)&&rules.kv.length>0)]);
  rows.push([t('moe.d_mtp'),select('moe-s-spec',rules.spec,d.spec,Array.isArray(rules.spec)&&rules.spec.length>0)]);
  for(const [name,id] of [[t('moe.s_vision'),'moe-s-vision'],[t('moe.hip_helper_feature'),'moe-s-helper']]){
    const label=document.createElement('label'); label.className='switch'; label.title=t('moe.hip_unavailable');
    const input=document.createElement('input');input.type='checkbox';input.id=id;input.disabled=true;
    const slider=document.createElement('span');slider.className='slider';label.append(input,slider);
    const unavailable=document.createElement('span');unavailable.append(label,' '+t('moe.hip_unavailable')); rows.push([name,unavailable]);
  }
  moeHIPRows('moe-settings',rows); moeCtxLbl();
  const note=document.createElement('p'); note.className='muted';note.textContent=t('moe.hip_settings_note');
  document.getElementById('moe-settings').appendChild(note);
}
function moeRenderHIPDetails(fit){
  const fld=document.getElementById('moe-details-fld'); fld.hidden=!fit;
  if(!fit)return;
  const d=fit.details, missing=t('moe.hip_setting_unset');
  const number=v=>v===undefined||v===''?missing:Number(v).toLocaleString(currentLang()==='fr'?'fr-FR':'en-US');
  const rows=[
    [t('moe.hip_configured_gpus'),d.gpu||missing],
    [t('moe.s_reserve'),d.reserve_mib!==undefined&&d.reserve_mib!==''?Number(d.reserve_mib)/1024+' GiB':missing],
    [t('moe.d_ctx'),number(d.ctx)+' '+t('moe.d_tokens')],
    [t('moe.d_kv'),d.kv||missing],
    [t('moe.d_mtp'),d.spec===undefined?missing:t('moe.d_mtp_val').replace('{n}',d.spec)],
    [t('moe.hip_concurrency'),d.concurrency===undefined?missing:d.concurrency],
    [t('moe.d_version'),'Strata HIP'],
    [t('moe.s_vision'),t('moe.hip_unavailable')],
    [t('moe.hip_helper_feature'),t('moe.hip_unavailable')]
  ];
  if(d.kv_resident)rows.push([t('moe.d_kv_resident').replace('{n}',''),number(d.kv_resident)+' '+t('moe.d_tokens')]);
  if(typeof d.mmap==='boolean') rows.push([t('moe.d_ram'),d.mmap?t('moe.d_mmap'):t('moe.d_inram')]);
  moeHIPRows('moe-details',rows);
  const info=[t('moe.hip_machine')];
  if(d.gpu)info.push(t('moe.hip_configured_gpus')+' : '+d.gpu);
  if(d.reserve_mib!==undefined&&d.reserve_mib!=='')info.push(t('moe.s_reserve')+' : '+Number(d.reserve_mib)/1024+' GiB');
  document.getElementById('moe-machine').textContent=info.join(' · ');
}
async function saveMoeHIP(){
  const fit=moeHIPSelected(); if(!fit||!fit.recipe||!fit.ready)return;
  const rules=moeHIPRules(fit), body={id:fit.recipe.id};
  if(rules.ctx&&typeof rules.ctx==='object')body.ctx=+document.getElementById('moe-s-ctx').value;
  if(Array.isArray(rules.kv)&&rules.kv.length)body.kv=document.getElementById('moe-s-kv').value;
  if(Array.isArray(rules.spec)&&rules.spec.length)body.spec=+document.getElementById('moe-s-spec').value;
  const editable=Object.keys(body).length>1, button=document.getElementById('moe-save');button.disabled=true;
  const result=document.getElementById('moe-hip-result');
  try{
    const response=await jpost(editable?'/api/moe/hip/settings':'/api/moe/hip/prepare',body);
    if(!response.ok){result.textContent=response.error||t('moe.hip_error');return;}
    const selected=response.id||fit.recipe.id;
    try{localStorage.setItem('ajean.moe.hip.recipe.'+fit.id,selected);localStorage.setItem('ajean.moe.hip.recipe',selected);}catch(_){}
    await openMoe(selected);
    document.getElementById('moe-hip-result').textContent=t(editable?'moe.hip_settings_saved':'moe.hip_prepared');
    if(typeof loadPresets==='function')await loadPresets();
  }catch(_){result.textContent=t('moe.hip_error');}
  finally{button.disabled=false;}
}
async function installMoeHIP(){
  const fit=moeHIPSelected(); if(!fit||fit.installed||!fit.bundle)return;
  const button=document.getElementById('moe-go');button.disabled=true;
  try{
    const response=await jpost('/api/moe/hip/install',{id:fit.bundle.id});
    if(!response.ok){document.getElementById('moe-hip-result').textContent=response.error||t('moe.hip_error');return;}
    closeMoe();lcStartPolling();
  }catch(_){document.getElementById('moe-hip-result').textContent=t('moe.hip_error');}
  finally{button.disabled=false;}
}
