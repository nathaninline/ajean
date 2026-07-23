function openBenchModal(){ document.getElementById('bench-modal').style.display = 'flex'; }
function closeBenchModal(){ document.getElementById('bench-modal').style.display = 'none'; }
async function runBenchUI(){
  const btn = document.getElementById('btn-bench');
  const rerun = document.getElementById('bench-rerun');
  const body = document.getElementById('bench-body');
  openBenchModal();
  btn.disabled = true; btn.textContent = '⏳ bench…';
  rerun.disabled = true;
  body.innerHTML =
    '<div style="text-align:center;padding:20px 0">' +
    '<div style="font-size:24px;animation:spin 1s linear infinite;display:inline-block">⏳</div>' +
    '<div class="muted" style="margin-top:8px">prompt 2000 tok + 300 decode<br>~10 secondes…</div>' +
    '</div>';
  try{
    const r = await jget('/api/bench');
    if(!r.ok){
      body.innerHTML = '<div style="color:var(--err);text-align:center">erreur: '+r.error+'</div>';
      return;
    }
    const x = r.result;
    body.innerHTML =
      '<div style="display:grid;grid-template-columns:1fr 1fr;gap:16px;text-align:center">' +
        '<div style="padding:14px;background:var(--panel);border:1px solid var(--border);border-radius:8px">' +
          '<div class="muted" style="font-size:11px;text-transform:uppercase;letter-spacing:.1em">Prefill</div>' +
          '<div style="font-size:26px;color:var(--accent);font-weight:600;margin:6px 0">'+x.prompt_per_second.toFixed(0)+'</div>' +
          '<div class="muted">tok/s</div>' +
          '<div class="muted" style="font-size:11px;margin-top:8px">'+x.prompt_n+' tok · '+(x.prompt_ms/1000).toFixed(2)+'s</div>' +
        '</div>' +
        '<div style="padding:14px;background:var(--panel);border:1px solid var(--border);border-radius:8px">' +
          '<div class="muted" style="font-size:11px;text-transform:uppercase;letter-spacing:.1em">Decode</div>' +
          '<div style="font-size:26px;color:var(--ok);font-weight:600;margin:6px 0">'+x.predicted_per_second.toFixed(1)+'</div>' +
          '<div class="muted">tok/s</div>' +
          '<div class="muted" style="font-size:11px;margin-top:8px">'+x.predicted_n+' tok · '+(x.predicted_ms/1000).toFixed(2)+'s</div>' +
        '</div>' +
      '</div>' +
      '<div class="muted" style="text-align:center;font-size:11px">total '+x.elapsed_sec.toFixed(2)+'s</div>';
  } finally {
    btn.disabled = false; btn.textContent = '⚡ bench';
    rerun.disabled = false;
    loadPresets();
  }
}
async function switchTo(n,name){
  if(!await askConfirm('Basculer vers « '+name+' » et redémarrer le service ?', {title:'Changer de preset', okText:'Basculer'})) return;
  toast('switching…');
  const r=await jpost('/api/switch',{n:n});
  toast(r.ok?'switched':'erreur'); setTimeout(loadAll,2000);
}
// editingKey = the identifier of the item being edited: a preset id (filename)
// or a skill name. Empty string = creating a new item.
let editingKey = '', editingKind = 'preset';
const KINDS = {
  // presets are keyed by `id` (filename) so several can share a display name;
  // skills keep name-as-identity (param 'name').
  preset: {label:'Preset', param:'id',   getUrl:'/api/preset', saveUrl:'/api/preset/save', delUrl:'/api/preset/delete', reload:()=>loadPresets()},
  mem:    {label:'Page',   param:'name', getUrl:'/api/mem',    saveUrl:'/api/mem/save',    delUrl:'/api/mem/delete',    reload:()=>loadMem()},
};
async function openItem(kind, key){
  const K = KINDS[kind];
  const r = await jfetch(K.getUrl + '?' + K.param + '=' + encodeURIComponent(key||''));
  const d = await r.json();
  editingKind = kind; editingKey = key || '';
  const display = d.name || key || '';
  document.getElementById('modal-title').textContent = key ? (K.label + ' · ' + display) : ('Nouveau ' + K.label.toLowerCase());
  document.getElementById('m-name').value = display;
  document.getElementById('m-content').value = d.content || '';
  document.getElementById('m-del').style.display = key ? 'inline-block' : 'none';
  // Model picker is preset-only: it edits the MODEL= line of config.env.
  const modelRow = document.getElementById('m-model-row');
  const delModelWrap = document.getElementById('m-del-model-wrap');
  if(kind === 'preset'){
    modelRow.style.display = 'flex';
    document.getElementById('m-hf-url').value = '';
    document.getElementById('m-hf-progress').style.display = 'none';
    document.getElementById('m-del-model').checked = false;
    document.getElementById('m-quant').value = currentQuantInTextarea();
    delModelWrap.style.display = key ? 'inline-flex' : 'none';
    await Promise.all([populateBackend(), populateModelPicker()]);
  } else {
    modelRow.style.display = 'none';
    delModelWrap.style.display = 'none';
  }
  document.getElementById('modal').style.display = 'flex';
}

// Pretty-print bytes — handy for the dropdown options.
function fmtSize(b){
  if(b > 1e9) return (b/1e9).toFixed(1)+' GB';
  if(b > 1e6) return (b/1e6).toFixed(0)+' MB';
  if(b > 1e3) return (b/1e3).toFixed(0)+' KB';
  return b+' B';
}
// Read the current MODEL= value out of the textarea, handling quotes / spaces.
function currentModelInTextarea(){
  const txt = document.getElementById('m-content').value;
  const m = txt.match(/^\s*MODEL\s*=\s*"?([^"\n]*)"?\s*$/m);
  return m ? m[1].trim() : '';
}
async function populateModelPicker(){
  const sel = document.getElementById('m-model');
  const list = await jget('/api/models');
  const cur = currentModelInTextarea();
  // Strip directory; config.env conventionally only stores the file basename.
  const curBase = (cur.split('/').pop() || '').trim();
  let html = '<option value="">— choisir un modèle —</option>';
  let matched = false;
  for(const m of list){
    const sel = (m.name === curBase) ? ' selected' : '';
    if(sel) matched = true;
    html += '<option value="'+m.name+'"'+sel+'>'+m.name+'  ('+fmtSize(m.size)+')</option>';
  }
  // If MODEL points to something not in JEAN_HOME, show it as a disabled hint.
  if(cur && !matched){
    html += '<option value="" disabled selected>('+(curBase||cur)+' — introuvable dans JEAN_HOME)</option>';
  }
  sel.innerHTML = html;
}
function onPickModel(){
  const val = document.getElementById('m-model').value;
  if(!val) return;
  const ta = document.getElementById('m-content');
  if(/^\s*MODEL\s*=.*$/m.test(ta.value)){
    ta.value = ta.value.replace(/^\s*MODEL\s*=.*$/m, 'MODEL="'+val+'"');
  } else {
    // No MODEL= line yet — prepend so it's visible at the top.
    ta.value = 'MODEL="'+val+'"\n' + ta.value;
  }
  toast('MODEL='+val);
}

// Choix du moteur PAR MODÈLE : 3 options (rapide / optimisé / personnalisé)
// qui réécrivent la ligne BIN= du preset. C'est LE point où on décide quel
// backend fait tourner ce modèle — la barre latérale ne fait qu'installer.
function currentBinInTextarea(){
  const txt = document.getElementById('m-content').value;
  const m = txt.match(/^\s*BIN\s*=\s*"?([^"\n]*)"?\s*$/m);
  return m ? m[1].trim() : '';
}
// Compare deux chemins de binaire en neutralisant séparateurs et casse (Windows).
function sameBinPath(a, b){
  const n = x => String(x||'').replace(/\\/g,'/').replace(/\/+$/,'').toLowerCase();
  return !!a && !!b && n(a) === n(b);
}
// Écrit (ou remplace) la ligne BIN= dans le contenu du preset.
function setBinInTextarea(val){
  const ta = document.getElementById('m-content');
  if(/^\s*BIN\s*=.*$/m.test(ta.value)) ta.value = ta.value.replace(/^\s*BIN\s*=.*$/m, 'BIN="'+val+'"');
  else ta.value = 'BIN="'+val+'"\n' + ta.value;
}
let beFastPath = '', beOptPath = '';
async function populateBackend(){
  // Chemins des deux moteurs gérés + liste des backends détectés (dossier jean).
  let lc = {}; try{ lc = await jget('/api/llamacpp'); }catch(_){}
  beFastPath = (lc.prebuilt && lc.prebuilt.bin) || '';
  beOptPath  = lc.bin || '';
  document.getElementById('be-fast-note').textContent = beFastPath ? '' : 'non installée';
  document.getElementById('be-opt-note').textContent  = beOptPath  ? '' : 'non installée';
  // Menu « backend détecté » du mode personnalisé : tout ce qu'on trouve dans
  // le dossier backends de jean (l'utilisateur peut y déposer son propre build).
  const detected = await jget('/api/backends');
  const sel = document.getElementById('m-backend-detected');
  let html = '<option value="">— ou choisir un backend détecté —</option>';
  for(const b of (detected||[])) html += '<option value="'+b.path+'">'+b.name+'</option>';
  sel.innerHTML = html;
  document.getElementById('be-drop-hint').textContent = lc.backends_dir
    ? ('Astuce : dépose ton binaire dans un sous-dossier de '+lc.backends_dir+' pour le voir apparaître ci-dessus.') : '';
  // Sélectionne l'option correspondant au BIN actuel du preset.
  const cur = currentBinInTextarea();
  let mode = 'custom';
  if(sameBinPath(cur, beFastPath)) mode = 'fast';
  else if(sameBinPath(cur, beOptPath)) mode = 'opt';
  const radio = document.querySelector('input[name=m-be][value='+mode+']');
  if(radio) radio.checked = true;
  toggleBackendCustom(mode);
  if(mode === 'custom') document.getElementById('m-backend-path').value = cur;
}
function toggleBackendCustom(mode){
  document.getElementById('m-backend-custom').style.display = (mode==='custom') ? 'block' : 'none';
}
function onBackendMode(mode){
  toggleBackendCustom(mode);
  if(mode === 'fast'){
    if(!beFastPath){ toast('installe d\'abord la version rapide (section Backend llama.cpp)'); return; }
    setBinInTextarea(beFastPath); toast('moteur : rapide');
  } else if(mode === 'opt'){
    if(!beOptPath){ toast('installe d\'abord la version optimisée (section Backend llama.cpp)'); return; }
    setBinInTextarea(beOptPath); toast('moteur : optimisé');
  }
  // custom : on attend que l'utilisateur saisisse un chemin / choisisse un backend
}
function onCustomPath(){
  const v = document.getElementById('m-backend-path').value.trim();
  if(v) setBinInTextarea(v);
}
function onPickDetected(){
  const v = document.getElementById('m-backend-detected').value;
  if(!v) return;
  document.getElementById('m-backend-path').value = v;
  setBinInTextarea(v); toast('moteur personnalisé');
}
// Read/write the QUANT= override line in the preset textarea.
function currentQuantInTextarea(){
  const m = document.getElementById('m-content').value.match(/^\s*#?\s*QUANT\s*=\s*"?([^"\n]*)"?\s*$/mi);
  return m ? m[1].trim() : '';
}
function applyQuant(){
  const val = document.getElementById('m-quant').value.trim();
  const ta = document.getElementById('m-content');
  const re = /^\s*#?\s*QUANT\s*=.*$/mi;
  if(val){
    if(re.test(ta.value)) ta.value = ta.value.replace(re, 'QUANT="'+val+'"');
    else ta.value = ta.value.replace(/\s*$/,'') + '\nQUANT="'+val+'"\n';
  } else {
    ta.value = ta.value.replace(re, '').replace(/\n{3,}/g,'\n\n');
  }
}
const openPreset = (id)=>openItem('preset', id);
const openMem    = (n)=>openItem('mem', n);
function closeModal(){ document.getElementById('modal').style.display = 'none'; }
async function saveItem(){
  const K = KINDS[editingKind];
  const name = document.getElementById('m-name').value.trim();
  const content = document.getElementById('m-content').value;
  if(!name){ toast('nom requis'); return; }
  // Presets: keyed by id (filename); duplicate display names are allowed.
  // Skills: keyed by name, rename via `old`.
  const payload = editingKind==='preset'
    ? {id: editingKey, name, content}
    : {name, old: editingKey, content};
  const r = await jpost(K.saveUrl, payload);
  if(!r.ok){ toast('erreur : ' + (r.error||'')); return; }
  toast('enregistré'); closeModal(); K.reload();
}
async function delItem(){
  if(!editingKey) return;
  const K = KINDS[editingKind];
  const name = document.getElementById('m-name').value.trim() || editingKey;
  const delModel = editingKind==='preset' && document.getElementById('m-del-model').checked;
  let msg = 'Supprimer le ' + K.label.toLowerCase() + ' « ' + name + ' » ?';
  if(delModel) msg += '\n\n⚠️ Le fichier .gguf du modèle sera AUSSI supprimé du disque (irréversible).';
  if(!await askConfirm(msg, {title:'Suppression', okText:'Supprimer', danger:true})) return;
  const payload = editingKind==='preset'
    ? {id: editingKey, deleteModel: delModel}
    : {name: editingKey};
  const r = await jpost(K.delUrl, payload);
  if(!r.ok){ toast('erreur : ' + (r.error||'')); return; }
  if(delModel){
    if(r.modelError) toast('preset supprimé, modèle : ' + r.modelError);
    else if(r.modelDeleted) toast('preset + modèle supprimés');
    else toast('supprimé (aucun modèle référencé)');
  } else { toast('supprimé'); }
  closeModal(); K.reload();
}
// Download a .gguf from Hugging Face, polling progress until done.
let dlPoll = null;
async function startDownload(){
  const url = document.getElementById('m-hf-url').value.trim();
  if(!url){ toast('colle un lien .gguf'); return; }
  const btn = document.getElementById('m-hf-btn');
  const prog = document.getElementById('m-hf-progress');
  btn.disabled = true;
  prog.style.display = 'block';
  prog.textContent = 'démarrage…';
  const r = await jpost('/api/models/download', {url});
  if(!r.ok){ prog.innerHTML = '<span style="color:var(--err)">erreur : '+(r.error||'')+'</span>'; btn.disabled=false; return; }
  const fname = r.filename;
  if(dlPoll) clearInterval(dlPoll);
  dlPoll = setInterval(async ()=>{
    const list = await jget('/api/models/download/status');
    const st = (list||[]).find(d=>d.filename===fname);
    if(!st){ return; }
    if(st.error){
      prog.innerHTML = '<span style="color:var(--err)">erreur : '+st.error+'</span>';
      clearInterval(dlPoll); dlPoll=null; btn.disabled=false; return;
    }
    if(st.finished){
      prog.innerHTML = '<span style="color:var(--ok)">✓ '+fname+' téléchargé ('+fmtSize(st.done)+')</span>';
      clearInterval(dlPoll); dlPoll=null; btn.disabled=false;
      await populateModelPicker();
      document.getElementById('m-model').value = fname; onPickModel();
      return;
    }
    const pct = st.total>0 ? Math.round(st.done*100/st.total) : 0;
    const tot = st.total>0 ? ' / '+fmtSize(st.total)+' ('+pct+'%)' : '';
    prog.textContent = '⬇ '+fmtSize(st.done)+tot+' — '+fname;
  }, 800);
}
// Smart autoscroll: follow the bottom while the user hasn't manually scrolled
// up. Re-stick when they scroll back near bottom themselves.
