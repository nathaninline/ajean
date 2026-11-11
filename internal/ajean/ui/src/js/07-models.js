// 07-models.js : ce qui reste de l'ancien éditeur de presets (0.19). Le benchmark
// du modèle du chat, deux utilitaires partagés et l'éditeur de pages mémoire.
// Les modèles eux-mêmes sont dans 07b-services.js.
// Tailles du test au choix : mémorisées dans le navigateur.
const BENCH_PROMPTS = [2000, 10000, 25000, 50000, 100000], BENCH_OUTPUTS = [100, 300, 1000, 3000];
let benchCfg = { prompt: 2000, output: 300 };
try{ benchCfg = { ...benchCfg, ...JSON.parse(localStorage.getItem('ajean-bench') || '{}') }; }catch(e){}
const benchK = n => n >= 1000 ? (n / 1000).toFixed(n % 1000 ? 1 : 0) + 'k' : String(n);
function benchSeg(id, key, list){
  const el = document.getElementById(id);
  el.innerHTML = list.map(v => '<label><input type="radio" name="'+id+'" value="'+v+'"><span>'+benchK(v)+'</span></label>').join('');
  el.querySelectorAll('input').forEach(i => {
    i.checked = +i.value === benchCfg[key];
    i.onchange = () => { benchCfg[key] = +i.value; try{ localStorage.setItem('ajean-bench', JSON.stringify(benchCfg)); }catch(e){} };
  });
}
// Ouvre la fenêtre sur les réglages ; le test part avec « lancer ».
function openBenchModal(){
  benchSeg('bench-seg-prompt', 'prompt', BENCH_PROMPTS);
  benchSeg('bench-seg-output', 'output', BENCH_OUTPUTS);
  const body = document.getElementById('bench-body');
  if(body) body.innerHTML = '<div class="muted" style="text-align:center">'+t('bench.pick')+'</div>';
  showModal('bench-modal');
}
function closeBenchModal(){ hideModal('bench-modal'); }
async function runBenchUI(){
  const rerun = document.getElementById('bench-rerun');
  const body = document.getElementById('bench-body');
  if(rerun) rerun.disabled = true;
  body.innerHTML =
    '<div style="text-align:center;padding:20px 0">' +
    '<div class="spinner" style="margin:0 auto"></div>' +
    '<div class="muted" style="margin-top:8px">'+t('models.bench.desc').replace('{p}', benchK(benchCfg.prompt)).replace('{n}', benchK(benchCfg.output))+'</div>' +
    '</div>';
  // Avancement : « Prefill 42 % » puis « Génération 120 / 300 ».
  const info = body.querySelector('.muted');
  const t0 = Date.now();
  let polling = true;
  (async () => {
    while(polling){
      await new Promise(res => setTimeout(res, 1000));
      if(!polling) break;
      let p; try{ p = await jget('/api/bench/progress'); }catch(e){ continue; }
      if(!polling || !p || !p.running) continue;
      const s = Math.round((Date.now() - t0) / 1000) + ' s';
      if(p.generated > 0) info.textContent = tf('bench.gen', 'Génération') + ' ' + p.generated + ' / ' + p.output + ' · ' + s;
      else if(p.prefill_done > 0 && p.prompt > 0) info.textContent = 'Prefill ' + Math.min(100, Math.round(100 * p.prefill_done / p.prompt)) + ' % · ' + s;
      else info.textContent = 'Prefill… · ' + s;
    }
  })();
  try{
    const r = await jget('/api/bench?prompt=' + benchCfg.prompt + '&n=' + benchCfg.output);
    polling = false;
    if(!r.ok){
      body.innerHTML = '<div style="color:var(--err);text-align:center">'+t('models.bench.error')+r.error+'</div>';
      return;
    }
    const x = r.result;
    body.innerHTML =
      '<div style="display:grid;grid-template-columns:1fr 1fr;gap:16px;text-align:center">' +
        '<div style="padding:14px;background:var(--panel);border:1px solid var(--border);border-radius:8px">' +
          '<div class="muted" style="font-size:11px;text-transform:uppercase;letter-spacing:.1em">'+t('chat.prefill_cap')+'</div>' +
          '<div style="font-size:26px;color:var(--accent);font-weight:600;margin:6px 0">'+x.prompt_per_second.toFixed(0)+'</div>' +
          '<div class="muted">tok/s</div>' +
          '<div class="muted" style="font-size:11px;margin-top:8px">'+x.prompt_n+' tok · '+(x.prompt_ms/1000).toFixed(2)+'s</div>' +
        '</div>' +
        '<div style="padding:14px;background:var(--panel);border:1px solid var(--border);border-radius:8px">' +
          '<div class="muted" style="font-size:11px;text-transform:uppercase;letter-spacing:.1em">'+t('chat.decode_cap')+'</div>' +
          '<div style="font-size:26px;color:var(--ok);font-weight:600;margin:6px 0">'+x.predicted_per_second.toFixed(1)+'</div>' +
          '<div class="muted">tok/s</div>' +
          '<div class="muted" style="font-size:11px;margin-top:8px">'+x.predicted_n+' tok · '+(x.predicted_ms/1000).toFixed(2)+'s</div>' +
        '</div>' +
      '</div>' +
      '<div class="muted" style="text-align:center;font-size:11px">'+t('models.bench.total')+' '+x.elapsed_sec.toFixed(2)+'s</div>';
  } finally {
    polling = false;
    if(rerun) rerun.disabled = false;
    loadPresets();
    if(typeof loadServices === 'function') loadServices();
  }
}

// Taille lisible (octets → KB/MB/GB).
function fmtSize(b){
  if(b > 1e9) return (b/1e9).toFixed(1)+' GB';
  if(b > 1e6) return (b/1e6).toFixed(0)+' MB';
  if(b > 1e3) return (b/1e3).toFixed(0)+' KB';
  return b+' B';
}
// Échappe une valeur avant de l'injecter dans du HTML.
function escHtml(s){
  return String(s==null?'':s).replace(/[&<>"']/g, c =>
    ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

// ---- Éditeur de pages mémoire (#mem-modal) ---------------------------------
// memEditingKey = nom de la page ouverte ; vide = création. La fenêtre s'ouvre
// tout de suite (voile .loading) et se remplit ensuite : en accès distant, le
// clic répondait sinon avec une seconde de retard. memOpenSeq protège d'une
// réponse en retard qui écraserait une page ouverte après elle.
let memEditingKey = '';
let memOpenSeq = 0;
function memProjectParam(){
  // Page d'un AUTRE projet en consultation (MEM_VIEW_PROJECT, 18-projects.js).
  return (typeof MEM_VIEW_PROJECT!=='undefined' && MEM_VIEW_PROJECT) ? MEM_VIEW_PROJECT : '';
}
async function openMem(key){
  const seq = ++memOpenSeq;
  memEditingKey = key || '';
  const modalEl = document.getElementById('mem-edit-modal');
  const title = document.getElementById('mem-edit-title');
  const nameEl = document.getElementById('mem-name');
  const content = document.getElementById('mem-content');
  title.textContent = key ? t('models.mem.label') + ' · ' + key : t('models.mem.new_label');
  nameEl.value = key || '';
  content.value = '';
  document.getElementById('mem-del').style.display = key ? 'inline-flex' : 'none';
  modalEl.classList.add('loading');
  showModal('mem-edit-modal');
  // Hauteur réelle de l'en-tête pour le voile : à mesurer APRÈS l'affichage.
  const mHead = modalEl.querySelector('.modal-head');
  if(mHead) modalEl.querySelector('.modal-card').style.setProperty('--mh', mHead.offsetHeight+'px');
  const body = modalEl.querySelector('.pe-body'); if(body) body.scrollTop = 0;
  if(key){
    const proj = memProjectParam();
    let d = {};
    try{
      const r = await jfetch('/api/mem?name=' + encodeURIComponent(key) + (proj ? '&project=' + encodeURIComponent(proj) : ''));
      d = await r.json();
    }catch(_){ d = {}; }
    if(seq !== memOpenSeq) return;
    const display = d.name || key;
    title.textContent = t('models.mem.label') + ' · ' + display;
    nameEl.value = display;
    content.value = d.content || '';
  }
  modalEl.classList.add('no-anim');
  modalEl.classList.remove('loading');
  if(body) body.scrollTop = 0;
  requestAnimationFrame(()=>requestAnimationFrame(()=>modalEl.classList.remove('no-anim')));
  if(!key) nameEl.focus();
}
function closeMem(){ hideModal('mem-edit-modal'); document.getElementById('mem-edit-modal').classList.remove('loading'); }
async function saveMem(){
  const name = document.getElementById('mem-name').value.trim();
  if(!name){ toast(t('models.name_required')); return; }
  const payload = {name, old: memEditingKey, content: document.getElementById('mem-content').value};
  const proj = memProjectParam(); if(proj) payload.project = proj;
  const r = await jpost('/api/mem/save', payload);
  if(!r.ok){ toast(t('common.error_prefix') + (r.error||'')); return; }
  toast(t('models.saved')); closeMem(); loadMem();
}
async function delMem(){
  if(!memEditingKey) return;
  const name = document.getElementById('mem-name').value.trim() || memEditingKey;
  const msg = t('models.delete_confirm_prefix') + t('models.mem.label').toLowerCase() + t('models.delete_confirm_mid') + name + t('models.delete_confirm_suffix');
  if(!await askConfirm(msg, {title:t('models.delete_title'), okText:t('models.delete_ok'), danger:true})) return;
  const payload = {name: memEditingKey};
  const proj = memProjectParam(); if(proj) payload.project = proj;
  const r = await jpost('/api/mem/delete', payload);
  if(!r.ok){ toast(t('common.error_prefix') + (r.error||'')); return; }
  toast(t('models.deleted')); closeMem(); loadMem();
}
