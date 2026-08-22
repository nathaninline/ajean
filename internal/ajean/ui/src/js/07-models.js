function openBenchModal(){ showModal('bench-modal'); }
function closeBenchModal(){ hideModal('bench-modal'); }
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
    btn.disabled = false; btn.textContent = 'bench';
    rerun.disabled = false;
    loadPresets();
  }
}
async function switchTo(n,name){
  if(!await askConfirm('Basculer vers « '+name+' » et redémarrer le service ?', {title:'Changer de preset', okText:'Basculer'})) return;
  toast('switching…');
  // Retour visuel IMMÉDIAT : la ligne visée s'allume et clignote. Le serveur met
  // plusieurs secondes à redémarrer le service ; sans ça la liste ne bougeait pas
  // d'un pouce pendant tout ce temps et le clic semblait sans effet.
  pendingPreset = n; loadPresets();
  const r=await jpost('/api/switch',{n:n});
  if(!r.ok){ pendingPreset = 0; toast('erreur'); loadPresets(); return; }
  toast('switched');
  // Le preset ACTIF, c'est celui dont l'empreinte est celle de la configuration : le serveur
  // l'écrit AVANT de répondre (et relance le service en arrière-plan, voir
  // handleSwitch), donc un rafraîchissement immédiat suffit — la barre passe au
  // blanc tout de suite. Le chargement du modèle par llama-server continue
  // derrière ; il ne conditionne pas la sélection.
  try{ await loadPresets(); }catch(e){}
  // Filet : si l'empreinte n'a pas encore basculé (bascule lente côté disque), on
  // reste en attente le temps qu'il faut, sans dépasser ~60 s.
  for(let i=0; i<40 && pendingPreset===n; i++){
    await new Promise(r=>setTimeout(r,1500));
    try{ await loadPresets(); }catch(e){}
  }
  pendingPreset = 0;
  loadAll();
}
// editingKey = the identifier of the item being edited: a preset id (filename)
// or a skill name. Empty string = creating a new item.
let editingKey = '', editingKind = 'preset';
const KINDS = {
  // presets are keyed by `id` (filename) so several can share a display name;
  // skills keep name-as-identity (param 'name').
  // newLabel / nameHint : « Nouveau Page » et « Nom du preset » dans l'éditeur de
  // mémoire venaient d'un libellé unique décliné mécaniquement.
  preset: {label:'', newLabel:'Nouveau preset', nameHint:'Nom du preset',
           param:'id',   getUrl:'/api/preset', saveUrl:'/api/preset/save', delUrl:'/api/preset/delete', reload:()=>loadPresets()},
  mem:    {label:'Page',   newLabel:'Nouvelle page',  nameHint:'Nom de la page',
           param:'name', getUrl:'/api/mem',    saveUrl:'/api/mem/save',    delUrl:'/api/mem/delete',    reload:()=>loadMem()},
};
// ⚠️ La modale s'ouvre AVANT d'aller chercher quoi que ce soit. Elle attendait
// auparavant la fin de 3 requêtes (contenu + backends + modèles disponibles) :
// en accès distant, ça faisait un clic sans réaction pendant une seconde, comme
// si le bouton était mort. On affiche la coquille tout de suite, puis on la
// remplit. `openSeq` protège du cas « deux ouvertures coup sur coup » : une
// réponse en retard ne doit jamais écraser la modale ouverte après elle.
// Pendant ce remplissage la modale est en `.loading` : un rond tourne à la place
// du formulaire, qui n'est révélé qu'une fois TOUT en place (contenu, réglages,
// backends, GPU) — sinon on voyait les champs se peupler un par un.
let openSeq = 0;
async function openItem(kind, key){
  const K = KINDS[kind];
  const seq = ++openSeq;
  editingKind = kind; editingKey = key || '';
  document.getElementById('modal-title').textContent = key ? (K.label ? K.label + ' · ' + key : key) : K.newLabel;
  document.getElementById('m-name').value = key || '';
  document.getElementById('m-name').placeholder = K.nameHint;
  document.getElementById('m-content').value = '';
  document.getElementById('m-del').style.display = key ? 'inline-flex' : 'none';
  // Model picker is preset-only: it edits the MODEL= line of the preset.
  const modelRow = document.getElementById('m-model-row');
  const settingsRow = document.getElementById('m-settings-row');
  const samplingRow = document.getElementById('m-sampling-row');
  const rawHead = document.getElementById('m-raw-head');
  const rawToggle = document.getElementById('m-raw-toggle');
  const rawBody = document.getElementById('m-raw-body');
  const rawCaret = document.getElementById('m-raw-caret');
  // Marque le type sur la modale : le CSS s'en sert pour retirer la carte autour
  // du contenu d'une page mémoire (le textarea a déjà sa propre bordure).
  document.getElementById('modal').classList.toggle('kind-mem', kind === 'mem');
  if(kind === 'preset'){
    modelRow.style.display = 'flex';
    settingsRow.style.display = 'flex';
    if(samplingRow) samplingRow.style.display = 'flex';
    document.getElementById('m-hf-url').value = '';
    resetDlUI();
    // Preset : la config brute est une ligne repliable, fermée par défaut.
    rawHead.textContent = 'Configuration';
    rawToggle.style.display = '';
    rawBody.style.display = 'none';
    rawCaret.classList.remove('open');
    // Dossiers de modèles : replié par défaut (réglage rare), chargé à l'ouverture.
    document.getElementById('m-dir-path').value = '';
    setModelDirsOpen(false);
  } else {
    modelRow.style.display = 'none';
    settingsRow.style.display = 'none';
    if(samplingRow) samplingRow.style.display = 'none';
    // Page mémoire : le contenu EST le champ principal — affiché en clair.
    rawHead.textContent = 'Contenu';
    rawToggle.style.display = 'none';
    rawBody.style.display = '';
  }
  const modalEl=document.getElementById('modal');
  modalEl.classList.add('loading');
  showModal('modal');
  // Le voile s'arrête au bas de l'en-tête : on lui passe sa hauteur réelle. ⚠️ À
  // mesurer APRÈS l'affichage — un élément en display:none mesure 0.
  const mHead=modalEl.querySelector('.modal-head');
  if(mHead) modalEl.querySelector('.modal-card').style.setProperty('--mh', mHead.offsetHeight+'px');
  // ⚠️ APRÈS showModal, jamais avant : le corps de la modale est un conteneur
  // défilant RÉUTILISÉ d'une ouverture à l'autre, et écrire scrollTop sur un
  // élément en display:none ne fait RIEN (il n'a pas de boîte de rendu) — le
  // navigateur restaurait donc l'ancienne position à l'affichage, et on rouvrait
  // la fiche au milieu. On le refait après le remplissage, la hauteur ayant changé.
  const topPeBody=()=>{ const b=document.querySelector('#modal .pe-body'); if(b) b.scrollTop=0; };
  topPeBody();
  // --- Remplissage, une fois la modale à l'écran -----------------------------
  const r = await jfetch(K.getUrl + '?' + K.param + '=' + encodeURIComponent(key||''));
  const d = await r.json();
  if(seq !== openSeq) return;              // une autre ouverture a pris la main
  const display = d.name || key || '';
  document.getElementById('modal-title').textContent = key ? (K.label ? K.label + ' · ' + display : display) : K.newLabel;
  document.getElementById('m-name').value = display;
  document.getElementById('m-content').value = d.content || '';
  if(kind === 'preset'){
    // Ces deux-là LISENT le contenu : elles doivent passer après son arrivée.
    document.getElementById('m-quant').value = currentQuantInTextarea();
    populateSettings();
    attachDownload();                      // téléchargement encore en cours côté serveur ?
    await Promise.all([populateBackend(), populateModelPicker(), populateMmproj(), populateSpecDraft(), populateDlDirs()]);
    if(seq !== openSeq) return;
  }
  // On lève le voile SANS transition (voir la note dans styles.css), puis on rend
  // les transitions deux frames plus tard, une fois le nouvel état peint.
  modalEl.classList.add('no-anim');
  modalEl.classList.remove('loading');
  topPeBody();
  requestAnimationFrame(()=>requestAnimationFrame(()=>modalEl.classList.remove('no-anim')));
}

// Pretty-print bytes — handy for the dropdown options.
function fmtSize(b){
  if(b > 1e9) return (b/1e9).toFixed(1)+' GB';
  if(b > 1e6) return (b/1e6).toFixed(0)+' MB';
  if(b > 1e3) return (b/1e3).toFixed(0)+' KB';
  return b+' B';
}
// Retire les guillemets ENTOURANTS d'une valeur, et seulement eux. Les anciens
// motifs `"?([^"\n]*)"?` s'arrêtaient au premier guillemet INTERNE : une valeur
// comme `--chat-template-file "/etc/ajean/tpl.jinja"` était lue tronquée, puis
// réécrite avec des guillemets non appariés (issue #17).
function unquoteVal(v){
  v = String(v==null?'':v).trim();
  const q = v[0];
  if(v.length >= 2 && (q === '"' || q === "'") && v[v.length-1] === q) return v.slice(1,-1);
  return v;
}
// Lit la valeur d'une clé KEY= dans un texte au format .env.
function readEnvKey(txt, key){
  const m = String(txt).match(new RegExp('^[ \\t]*'+key+'[ \\t]*=(.*)$','m'));
  return m ? unquoteVal(m[1]) : '';
}
// Read the current MODEL= value out of the textarea, handling quotes / spaces.
function currentModelInTextarea(){
  return readEnvKey(document.getElementById('m-content').value, 'MODEL');
}
// Échappe une valeur avant de l'injecter dans du HTML (chemins de fichiers :
// on ne contrôle pas ce que l'utilisateur y met).
function escHtml(s){
  return String(s==null?'':s).replace(/[&<>"']/g, c =>
    ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}
// Compare deux chemins/noms de modèle en neutralisant séparateurs et casse.
function samePath(a, b){
  const n = x => String(x||'').trim().replace(/\\/g,'/').replace(/\/+$/,'').toLowerCase();
  return !!a && !!b && n(a) === n(b);
}
function baseName(p){ return String(p||'').replace(/\\/g,'/').split('/').pop().trim(); }
async function populateModelPicker(){
  const sel = document.getElementById('m-model');
  const list = await jget('/api/models');
  const cur = currentModelInTextarea();
  // Les modèles peuvent venir de plusieurs dossiers (disque externe…) : on les
  // regroupe par dossier, AJEAN_HOME d'abord (l'API les renvoie dans cet ordre).
  const groups = [];
  for(const m of (list||[])){
    let g = groups.find(x => x.dir === m.dir);
    if(!g){ g = {dir: m.dir, home: m.home, items: []}; groups.push(g); }
    g.items.push(m);
  }
  let html = '<option value="">— choisir un modèle —</option>';
  let matched = false;
  for(const g of groups){
    let opts = '';
    for(const m of g.items){
      // Le preset peut référencer le chemin complet OU le simple nom de fichier.
      const on = samePath(cur, m.value) || samePath(cur, m.path) ||
                 (g.home && samePath(baseName(cur), m.name)) ? ' selected' : '';
      if(on) matched = true;
      // Modèle en plusieurs fichiers : l'API ne renvoie que la première tranche
      // (les autres ne se lancent pas seules) et la taille de la famille entière.
      // On le dit, et surtout on signale les tranches manquantes — sinon le
      // modèle se sélectionne sans broncher puis le moteur meurt au démarrage.
      let tag = '';
      if(m.shards > 1) tag = ' · ' + m.shards + ' fichiers';
      if(m.missing && m.missing.length) tag += ' · ⚠ ' + m.missing.length + ' manquant' + (m.missing.length>1?'s':'');
      opts += '<option value="'+escHtml(m.value)+'"'+on+'>'+escHtml(m.name)+'  ('+fmtSize(m.size)+tag+')</option>';
    }
    html += groups.length > 1
      ? '<optgroup label="'+escHtml(g.home ? 'dossier ajean' : g.dir)+'">'+opts+'</optgroup>'
      : opts;
  }
  // MODEL pointe vers un fichier qu'on ne trouve dans aucun dossier déclaré :
  // on déplie alors la section des dossiers, c'est là que ça se répare.
  if(cur && !matched){
    html += '<option value="" disabled selected>('+escHtml(cur)+' — introuvable ; ajoute son dossier ci-dessous)</option>';
  }
  sel.innerHTML = html;
  if(cur && !matched) setModelDirsOpen(true);
}
// ---- Dossiers de modèles : AJEAN_HOME + dossiers ajoutés (disque externe…) ---
// Réglage rare : replié derrière une ligne, comme l'éditeur de .env brut.
function setModelDirsOpen(open){
  const b = document.getElementById('m-dirs-body');
  const c = document.getElementById('m-dirs-caret');
  if(!b) return;
  b.style.display = open ? '' : 'none';
  if(c) c.classList.toggle('open', open);
  if(open) populateModelDirs();
}
function toggleModelDirs(){
  setModelDirsOpen(document.getElementById('m-dirs-body').style.display === 'none');
}
async function populateModelDirs(){
  const box = document.getElementById('m-dirs-list');
  if(!box) return;
  let d = {};
  try{ d = await jget('/api/models/dirs'); }catch(_){ return; }
  // Construit en DOM (pas en HTML concaténé) : les chemins viennent de
  // l'utilisateur et se retrouveraient sinon dans un attribut onclick.
  box.innerHTML = '';
  for(const x of (d.dirs||[])){
    const row = document.createElement('div');
    const p = document.createElement('span');
    p.textContent = x.path + ' ';
    const info = document.createElement('span');
    info.className = 'muted';
    const n = x.count >= 0 ? (x.count + ' modèle' + (x.count>1?'s':'')) : 'illisible';
    const free = x.free >= 0 ? ', ' + fmtSize(x.free) + ' libres' : '';
    info.textContent = '(' + n + free + (x.home ? ', dossier ajean' : '') + ')';
    row.append(p, info);
    if(!x.home){
      const del = document.createElement('button');
      del.className = 'pe-link';
      del.textContent = 'retirer';
      del.style.marginLeft = '8px';
      del.onclick = () => removeModelDir(x.path);
      row.append(del);
    }
    box.append(row);
  }
}
async function addModelDir(){
  const inp = document.getElementById('m-dir-path');
  const p = inp.value.trim();
  if(!p){ toast('indique un chemin de dossier'); return; }
  const r = await jpost('/api/models/dirs', {path: p, action: 'add'});
  if(!r.ok){ toast('erreur : ' + (r.error||'')); return; }
  inp.value = '';
  toast('dossier ajouté');
  await Promise.all([populateModelDirs(), populateModelPicker(), populateDlDirs()]);
}
async function removeModelDir(p){
  const r = await jpost('/api/models/dirs', {path: p, action: 'remove'});
  if(!r.ok){ toast('erreur : ' + (r.error||'')); return; }
  toast('dossier retiré');
  await Promise.all([populateModelDirs(), populateModelPicker(), populateDlDirs()]);
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

// --- Vision (projecteur multimodal --mmproj) --------------------------------
// Le projecteur est un .gguf séparé du modèle : il apparaît donc dans la même
// liste que les modèles (/api/models). On le range dans sa PROPRE clé MMPROJ,
// que backend_serve.go traduit en --mmproj — plutôt que dans EXTRA_ARGS, pour
// que le chemin soit résolu comme celui du modèle (nom simple = cherché dans
// les dossiers déclarés).
function currentMmprojInTextarea(){
  // Clé dédiée d'abord ; à défaut, un --mmproj posé à la main dans EXTRA_ARGS
  // (anciens presets), pour que le champ montre la vision déjà configurée.
  return readEnvKey(document.getElementById('m-content').value, 'MMPROJ')
      || eaGetValued('--mmproj');
}
function onPickMmproj(){
  // On retire un éventuel --mmproj d'EXTRA_ARGS pour ne pas le charger deux fois,
  // et on centralise le choix dans la clé MMPROJ.
  eaSetValued('--mmproj', '');
  cfgWriteKey('MMPROJ', document.getElementById('m-mmproj').value);
  syncMmprojCpuRow();
}
// L'option « projecteur sur le CPU » n'a de sens qu'avec un mmproj chargé : on
// masque la ligne quand aucune vision n'est sélectionnée, plutôt que de laisser
// un interrupteur sans effet occuper la place.
function syncMmprojCpuRow(){
  const row = document.getElementById('s-mmproj-cpu-row');
  if(!row) return;
  // Source de vérité = la config (clé MMPROJ / --mmproj), pas le select : à
  // l'ouverture du preset cette ligne est synchronisée AVANT que le select des
  // projecteurs soit rempli, donc lire sa valeur masquerait la ligne à tort.
  row.style.display = currentMmprojInTextarea() ? '' : 'none';
}
// Un projecteur multimodal se reconnaît à « mmproj » dans son nom : c'est la
// convention de nommage universelle (llama.cpp, HF). On ne liste que ceux-là —
// mêler les modèles de plusieurs Go n'aiderait pas à choisir des yeux.
function isMmprojName(name){ return /mmproj/i.test(String(name||'')); }
async function populateMmproj(){
  const sel = document.getElementById('m-mmproj');
  if(!sel) return;
  const list = await jget('/api/models');
  const cur = currentMmprojInTextarea();
  const items = (list||[]).filter(m => isMmprojName(m.name));
  let html = '<option value="">— aucune —</option>';
  let matched = false;
  for(const m of items){
    const on = samePath(cur, m.value) || samePath(cur, m.path) ||
               samePath(baseName(cur), m.name) ? ' selected' : '';
    if(on) matched = true;
    html += '<option value="'+escHtml(m.value)+'"'+on+'>'+escHtml(m.name)+' ('+fmtSize(m.size)+')</option>';
  }
  // MMPROJ pointe sur un fichier absent des dossiers déclarés (ou nommé hors
  // convention) : on le garde affiché plutôt que de l'effacer silencieusement.
  if(cur && !matched){
    html += '<option value="'+escHtml(cur)+'" selected>'+escHtml(baseName(cur)||cur)+' (introuvable)</option>';
  }
  sel.innerHTML = html;
}

// Choix du moteur PAR MODÈLE : 3 options (précompilé / compilé / personnalisé)
// qui réécrivent la ligne BIN= du preset. C'est LE point où on décide quel
// backend fait tourner ce modèle — la barre latérale ne fait qu'installer.
function currentBinInTextarea(){
  return readEnvKey(document.getElementById('m-content').value, 'BIN');
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
// Le moteur précompilé s'installe dans un dossier versionné (…/llama-b10280/) :
// le BIN d'un preset écrit avant une mise à jour ne vaut plus le chemin courant,
// alors qu'il désigne bien ce moteur. On reconnaît donc l'appartenance au
// dossier, sinon tous les presets retombaient en « personnalisé » à chaque MAJ.
function underDir(p, dir){
  const n = x => String(x||'').replace(/\\/g,'/').replace(/\/+$/,'').toLowerCase();
  return !!p && !!dir && n(p).startsWith(n(dir)+'/');
}
let beFastPath = '', beOptPath = '', beFastDir = '';
async function populateBackend(){
  // Chemins des deux moteurs gérés + liste des backends détectés (dossier ajean).
  let lc = {}; try{ lc = await jget('/api/llamacpp'); }catch(_){}
  beFastPath = (lc.prebuilt && lc.prebuilt.bin) || '';
  beFastDir  = (lc.prebuilt && lc.prebuilt.dir) || '';
  beOptPath  = lc.bin || '';
  // Menu « backend détecté » du mode personnalisé : tout ce qu'on trouve dans
  // le dossier backends de ajean (l'utilisateur peut y déposer son propre build).
  const detected = await jget('/api/backends');
  const sel = document.getElementById('m-backend-detected');
  let html = '<option value="">— ou choisir un backend détecté —</option>';
  for(const b of (detected||[])) html += '<option value="'+b.path+'">'+b.name+'</option>';
  sel.innerHTML = html;
  document.getElementById('be-drop-hint').textContent = lc.backends_dir
    ? ('Astuce : déposez votre binaire dans un sous-dossier de '+lc.backends_dir+' pour le voir apparaître ci-dessus.') : '';
  // Sélectionne l'option correspondant au BIN actuel du preset.
  const cur = currentBinInTextarea();
  let mode = 'custom';
  if(sameBinPath(cur, beFastPath) || underDir(cur, beFastDir)) mode = 'fast';
  else if(sameBinPath(cur, beOptPath)) mode = 'opt';
  const radio = document.querySelector('input[name=m-be][value='+mode+']');
  if(radio) radio.checked = true;
  toggleBackendCustom(mode);
  if(mode === 'custom') document.getElementById('m-backend-path').value = cur;
  // Attendu (et non lancé dans le vide) : la liste des GPU change la hauteur du
  // bloc « moteur ». Sans ce await, elle arrivait APRÈS la levée du voile de
  // chargement et on voyait le bloc bouger tout seul.
  await loadGpuDevices();
}
function toggleBackendCustom(mode){
  document.getElementById('m-backend-custom').style.display = (mode==='custom') ? 'block' : 'none';
}
function onBackendMode(mode){
  toggleBackendCustom(mode);
  if(mode === 'fast'){
    if(!beFastPath){ toast('installez d\'abord llama.cpp précompilé (section MOTEUR)'); return; }
    setBinInTextarea(beFastPath); toast('moteur : llama.cpp précompilé');
    loadGpuDevices();
  } else if(mode === 'opt'){
    if(!beOptPath){ toast('installez d\'abord llama.cpp compilé (section MOTEUR)'); return; }
    setBinInTextarea(beOptPath); toast('moteur : llama.cpp compilé');
    loadGpuDevices();
  }
  // custom : on attend que l'utilisateur saisisse un chemin / choisisse un backend
}
function onCustomPath(){
  const v = document.getElementById('m-backend-path').value.trim();
  if(v){ setBinInTextarea(v); loadGpuDevices(); }
}
function onPickDetected(){
  const v = document.getElementById('m-backend-detected').value;
  if(!v) return;
  document.getElementById('m-backend-path').value = v;
  setBinInTextarea(v); toast('moteur personnalisé');
  loadGpuDevices();
}

// --- Cartes graphiques (--device / --tensor-split) --------------------------
// Ces réglages vivent dans le PRESET, pas dans une variable d'environnement :
// --device est compris par TOUS les backends (CUDA, Vulkan, ROCm), alors que
// CUDA_VISIBLE_DEVICES n'a aucun effet sur un moteur Vulkan — d'où des modèles
// qui s'étalaient sur les deux cartes malgré une sélection « GPU 1 ».
// La lecture/écriture des flags passe par eaGetValued/eaSetValued (le tokenizer
// d'EXTRA_ARGS déjà utilisé par les autres réglages) : un seul parseur.

// Diamètre de la pastille du curseur, en pixels — DOIT rester synchronisé avec
// .gpu-range::-webkit-slider-thumb / ::-moz-range-thumb dans styles.css.
const GPU_THUMB = 16;
let gpuDevices = [];
const gpuCache = {}; // bin -> devices : évite de relancer le moteur à chaque ouverture

// Interroge le moteur du preset (--list-devices) : les noms de device et leur
// ordre lui appartiennent, une liste issue de nvidia-smi désignerait la mauvaise
// carte sur un backend Vulkan. Le groupe reste MASQUÉ tant que la réponse n'est
// pas là, et le reste s'il y a moins de deux cartes : rien à arbitrer, et ça
// évite un encart qui surgit après coup.
// Remplit le cache sans rien peindre (appelé au chargement de la page pour le
// moteur actif) : à l'ouverture de l'éditeur, l'encart est déjà prêt.
async function prefetchGpuDevices(bin){
  if(!bin || gpuCache[bin]) return;
  try{
    const r = await jpost('/api/backends/devices', {bin});
    if(r && r.ok) gpuCache[bin] = r.devices || [];
  }catch(_){}
}

async function loadGpuDevices(){
  const group = document.getElementById('m-gpu-group');
  const bin = currentBinInTextarea();
  if(!bin){ group.hidden = true; gpuDevices = []; return; }
  if(gpuCache[bin]){ gpuDevices = gpuCache[bin]; renderGpu(); return; }
  let r = {};
  try{ r = await jpost('/api/backends/devices', {bin}); }catch(_){ r = {ok:false}; }
  // Le moteur a pu changer pendant l'appel (clic rapide) : on ne peint que si la
  // réponse concerne toujours le moteur affiché.
  if(currentBinInTextarea() !== bin) return;
  gpuDevices = (r.ok && r.devices) ? r.devices : [];
  if(r.ok) gpuCache[bin] = gpuDevices;
  renderGpu();
}

function selectedGpuIds(){
  const sel = eaGetValued('--device').split(',').filter(Boolean);
  const known = gpuDevices.map(d => d.id);
  const kept = sel.filter(id => known.includes(id));
  return kept.length ? kept : known; // aucune contrainte = toutes les cartes
}

function gpuEsc(t){
  return String(t).replace(/[<>&"]/g, c => ({'<':'&lt;','>':'&gt;','&':'&amp;','"':'&quot;'}[c]));
}

function renderGpu(){
  const group = document.getElementById('m-gpu-group');
  // Une seule carte (ou aucune) : rien à répartir, rien à choisir.
  if(gpuDevices.length < 2){ group.hidden = true; return; }
  const sel = selectedGpuIds();
  let html = gpuDevices.map(d => {
    // Mémoire à 0 = le moteur n'a pas pu la lire (carte déjà saturée par le
    // modèle en cours) : on n'affiche pas « 0,0 Go », qui serait un mensonge.
    const gb = d.total_mib > 0 ? ' · ' + (d.total_mib/1024).toFixed(1).replace('.', ',') + ' Go' : '';
    return '<div class="pe-row">'
      + '<span class="pe-row-l">' + gpuEsc(gpuLabel(d))
      + '<span class="pe-sub">' + gpuEsc(d.id) + gb + '</span></span>'
      + '<span class="pe-row-c"><label class="pe-switch"><input type="checkbox" value="' + gpuEsc(d.id) + '"'
      + (sel.includes(d.id) ? ' checked' : '') + ' onchange="onGpuPick()"><span class="pe-sl"></span></label></span>'
      + '</div>';
  }).join('');
  if(sel.length > 1) html += splitHtml(sel);
  document.getElementById('m-gpu-list').innerHTML = html;
  document.getElementById('m-gpu-note').textContent = sel.length === gpuDevices.length
    ? 'Toutes les cartes sont utilisées.'
    : 'Ce modèle n\'utilisera que : ' + sel.map(id => gpuLabel(gpuById(id))).join(', ') + '.';
  group.hidden = false;
  if(sel.length > 1) paintSplit();
}

function gpuById(id){ return gpuDevices.find(d => d.id === id) || {id}; }
// Nom lisible : « NVIDIA GeForce RTX 5060 Ti » → « RTX 5060 Ti ». CUDA0/Vulkan1
// ne disent rien à personne ; le numéro reste en sous-titre pour ceux qui
// veulent savoir ce qui part dans --device.
function gpuLabel(d){
  return String((d && d.name) || (d && d.id) || '')
    .replace(/^NVIDIA\s+GeForce\s+/i, '')
    .replace(/^NVIDIA\s+/i, '')
    .replace(/^AMD\s+(Radeon\s+)?/i, '')
    .replace(/^Intel\(R\)\s+/i, '')
    .trim() || (d && d.id) || '';
}

// Répartition : UNE seule barre, sur laquelle on glisse directement (le curseur
// est transparent, posé par-dessus). Trois cartes ou plus : un pourcentage par
// carte. On n'écrit --tensor-split QUE si l'utilisateur y touche : sans lui,
// llama.cpp répartit tout seul.
function splitHtml(sel){
  const shares = currentShares(sel);
  let inner;
  if(sel.length === 2){
    inner = '<div class="gpu-split">'
      + '<div class="gpu-bar" id="m-split-bar"></div>'
      + '<input type="range" class="gpu-range" id="m-split-range" min="0" max="100" step="0.5"'
      + ' value="' + (shares[0]*100).toFixed(1) + '" oninput="onSplitRange()"'
      + ' aria-label="part de ' + gpuEsc(gpuLabel(gpuById(sel[0]))) + '"></div>';
  } else {
    inner = '<div class="gpu-bar" id="m-split-bar"></div>'
      + '<div class="gpu-shares">' + sel.map((id, i) =>
        '<label class="gpu-share"><span>' + gpuEsc(gpuLabel(gpuById(id))) + '</span>'
        + '<input type="number" min="0" max="100" step="0.5" value="' + (shares[i]*100).toFixed(1) + '"'
        + ' oninput="onSplitNumbers()"><span class="pe-unit">%</span></label>').join('') + '</div>';
  }
  return '<div class="pe-row">'
    + '<span class="pe-row-l">Répartition<span class="pe-sub">part des couches par carte</span></span>'
    + '<span class="pe-row-c" id="m-split-state"></span></div>'
    + '<div class="pe-row stack">' + inner
    + '<div class="gpu-legend" id="m-split-legend"></div></div>';
}

// Parts actuelles, normalisées à 1. Sans --tensor-split : proportionnelles à la
// VRAM de chaque carte, ce que llama.cpp fait de son côté — la barre montre donc
// ce qui se passe réellement plutôt qu'un partage égal trompeur.
function currentShares(sel){
  const raw = eaGetValued('--tensor-split').split(',').map(parseFloat).filter(n => !isNaN(n));
  let vals = (raw.length === sel.length) ? raw
    : sel.map(id => gpuById(id).total_mib || 0);
  // Mémoires inconnues (lecture à 0) : à défaut de proportion crédible, on
  // montre un partage égal plutôt qu'une barre pleine sur une seule carte.
  if(vals.every(v => !v)) vals = sel.map(() => 1);
  const sum = vals.reduce((a, b) => a + b, 0) || 1;
  return vals.map(v => v/sum);
}

// Repeint UNIQUEMENT la barre, la légende et l'état. Surtout PAS le bloc entier :
// remplacer le HTML pendant un glissement détruit le curseur en cours d'usage —
// c'est ce qui faisait qu'il ne répondait qu'au clic.
function paintSplit(){
  const sel = selectedGpuIds();
  if(sel.length < 2) return;
  const shares = currentShares(sel);
  const bar = document.getElementById('m-split-bar');
  // La pastille du curseur ne parcourt PAS toute la largeur : elle va de
  // GPU_THUMB/2 à largeur - GPU_THUMB/2, sinon elle déborderait de la piste. Une
  // coupure à « s % » pile tombait donc à côté d'elle (jusqu'à ~5 px d'écart aux
  // extrêmes), ce qui laissait voir la piste claire juste à droite de la
  // pastille. On coupe dans le repère de la pastille : s*(largeur - THUMB) + THUMB/2.
  // Le dernier segment prend le reste (flex:1) : aucun filet résiduel à droite,
  // quels que soient les arrondis.
  const withThumb = sel.length === 2 && !!document.getElementById('m-split-range');
  if(bar) bar.innerHTML = shares.map((s, i) => {
    const cls = i%2 ? 'alt' : '';
    if(i === shares.length - 1) return '<span class="' + cls + '" style="flex:1"></span>';
    const w = withThumb
      ? 'calc(' + (s*100).toFixed(3) + '% - ' + ((s - 0.5) * GPU_THUMB).toFixed(2) + 'px)'
      : (s*100).toFixed(3) + '%';
    return '<span class="' + cls + '" style="width:' + w + '"></span>';
  }).join('');
  const leg = document.getElementById('m-split-legend');
  if(leg) leg.innerHTML = sel.map((id, i) =>
    '<span><i class="' + (i%2 ? 'alt' : '') + '"></i>' + gpuEsc(gpuLabel(gpuById(id)))
    + ' ' + (shares[i]*100).toFixed(1).replace('.', ',') + ' %</span>').join('');
  const st = document.getElementById('m-split-state');
  if(st) st.innerHTML = eaGetValued('--tensor-split')
    ? '<button class="pe-link" onclick="resetSplit()">réinitialiser</button>'
    : '<span class="pe-unit">automatique</span>';
}
function writeSplit(shares){
  const sum = shares.reduce((a, b) => a + b, 0) || 1;
  eaSetValued('--tensor-split', shares.map(s => (s/sum).toFixed(3)).join(','));
  paintSplit();
}
function onSplitRange(){
  const v = parseFloat(document.getElementById('m-split-range').value) / 100;
  writeSplit([v, 1-v]);
}
function onSplitNumbers(){
  const vals = [...document.querySelectorAll('.gpu-share input')].map(i => parseFloat(i.value) || 0);
  if(vals.reduce((a, b) => a + b, 0) <= 0) return; // saisie en cours
  writeSplit(vals);
}
function resetSplit(){
  eaSetValued('--tensor-split', '');
  const sel = selectedGpuIds();
  const r = document.getElementById('m-split-range');
  if(r && sel.length === 2) r.value = (currentShares(sel)[0]*100).toFixed(1);
  const nums = [...document.querySelectorAll('.gpu-share input')];
  if(nums.length){
    const sh = currentShares(sel);
    nums.forEach((inp, i) => { inp.value = (sh[i]*100).toFixed(1); });
  }
  paintSplit();
}

function onGpuPick(){
  const ids = [...document.querySelectorAll('#m-gpu-list input[type=checkbox]:checked')].map(c => c.value);
  if(!ids.length){ toast('gardez au moins une carte'); renderGpu(); return; }
  // Toutes cochées = pas de contrainte : on retire le flag plutôt que de figer
  // des noms de device qui changeraient avec le moteur.
  eaSetValued('--device', ids.length === gpuDevices.length ? '' : ids.join(','));
  // Une répartition écrite pour N cartes n'a plus de sens pour N-1.
  if(ids.length < 2) eaSetValued('--tensor-split', '');
  renderGpu();
}

// Read/write the QUANT= override line in the preset textarea.
function currentQuantInTextarea(){
  const m = document.getElementById('m-content').value.match(/^\s*#?\s*QUANT\s*=(.*)$/mi);
  return m ? unquoteVal(m[1]) : '';
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
// ---- Réglages simples : champs de formulaire <-> lignes du fichier .env -----
// Le fichier de config (textarea m-content) reste la source de vérité : chaque
// champ lit/écrit sa ligne KEY=… , comme le font déjà MODEL/BIN/QUANT.
function cfgReadKey(key){
  return readEnvKey(document.getElementById('m-content').value, key);
}
function cfgWriteKey(key, val){
  const ta = document.getElementById('m-content');
  val = String(val==null?'':val).trim();
  const reLine = new RegExp('^[ \\t]*'+key+'[ \\t]*=.*$','m');
  if(val === ''){ // clé vidée → on retire la ligne
    ta.value = ta.value.replace(new RegExp('^[ \\t]*'+key+'[ \\t]*=.*\\n?','m'),'').replace(/\n{3,}/g,'\n\n');
    return;
  }
  const line = key+'='+(/\s/.test(val) ? '"'+val+'"' : val);
  if(reLine.test(ta.value)) ta.value = ta.value.replace(reLine, line);
  else ta.value = ta.value.replace(/\s*$/,'') + '\n'+line+'\n';
}
// EXTRA_ARGS : liste de drapeaux passés tels quels à llama-server. On les édite
// jeton par jeton pour les interrupteurs (mlock, flash-attn, n-cpu-moe…), sans
// toucher aux autres drapeaux (--jinja, --device…) qui restent en config brute.
function eaTokens(){ return cfgReadKey('EXTRA_ARGS').split(/\s+/).filter(Boolean); }
function eaSetTokens(t){ cfgWriteKey('EXTRA_ARGS', t.join(' ')); }
function eaHasFlag(flag){ return eaTokens().includes(flag); }
function eaToggleFlag(flag, on){
  const t = eaTokens().filter(x=>x!==flag);
  if(on) t.push(flag);
  eaSetTokens(t);
}
function eaGetValued(flag){
  const t = eaTokens(), i = t.indexOf(flag);
  return (i>=0 && i+1<t.length && !t[i+1].startsWith('-')) ? t[i+1] : '';
}
function eaSetValued(flag, val){
  const t = eaTokens(), i = t.indexOf(flag);
  if(i>=0){ const hadVal = i+1<t.length && !t[i+1].startsWith('-'); t.splice(i, hadVal?2:1); }
  val = String(val||'').trim();
  if(val !== '') t.push(flag, val);
  eaSetTokens(t);
}
// Remplit les champs de réglage depuis le contenu courant du preset.
function populateSettings(){
  const set = (id,v)=>{ const e=document.getElementById(id); if(e) e.value=v; };
  set('s-ctx', cfgReadKey('CTX'));
  set('s-ngl', cfgReadKey('NGL'));
  set('s-threads', cfgReadKey('THREADS'));
  set('s-batch', cfgReadKey('BATCH'));
  set('s-ubatch', cfgReadKey('UBATCH'));
  set('s-tbatch', cfgReadKey('THREADS_BATCH'));
  set('s-np', eaGetValued('-np'));
  set('s-kv', cfgReadKey('KV_TYPE'));
  // Échantillonnage (envoyé par requête, voir applySampling côté serveur).
  set('s-temp', cfgReadKey('TEMP'));
  set('s-topp', cfgReadKey('TOP_P'));
  set('s-topk', cfgReadKey('TOP_K'));
  set('s-minp', cfgReadKey('MIN_P'));
  set('s-presp', cfgReadKey('PRESENCE_PENALTY'));
  set('s-reppen', cfgReadKey('REPEAT_PENALTY'));
  set('s-effort', cfgReadKey('REASONING_EFFORT'));
  set('s-moe', eaGetValued('--n-cpu-moe'));
  set('s-spec-n', eaGetValued('--spec-draft-n-max'));
  setSpecType(eaGetValued('--spec-type'));
  const chk = (id,v)=>{ const e=document.getElementById(id); if(e) e.checked=v; };
  // Raisonnement : trois états dans le fichier, deux positions sur l'interrupteur.
  // « off » est une interdiction explicite passée au moteur ; la clé ABSENTE, elle,
  // laisse le moteur suivre le gabarit du modèle — donc un modèle à raisonnement
  // réfléchit quand même. L'interrupteur ne peut pas montrer cette nuance, le
  // sous-titre la dit. (Les presets créés ou modifiés depuis cette version
  // écrivent toujours on ou off, la question ne se pose plus pour eux.)
  const rz = cfgReadKey('REASONING');
  chk('s-reasoning', /^(on|1|true|auto|deepseek)$/i.test(rz));
  const rzSub = document.getElementById('s-reasoning-sub');
  if(rzSub){
    rzSub.textContent = rz ? 'réflexion étape par étape'
                           : 'réflexion étape par étape — non précisé : le modèle décide';
  }
  chk('s-kvunified', eaHasFlag('--kv-unified'));
  chk('s-flash', eaHasFlag('--flash-attn') && !/^off$/i.test(eaGetValued('--flash-attn')));
  chk('s-mlock', eaHasFlag('--mlock'));
  chk('s-nommap', eaHasFlag('--no-mmap'));
  chk('s-mmproj-cpu', eaHasFlag('--no-mmproj-offload'));
  syncMmprojCpuRow();
}
// --- Décodage spéculatif (--spec-type / --spec-draft-n-max) ----------------
// Sélectionne le type courant. --spec-type accepte en réalité une LISTE séparée
// par des virgules ; une valeur composée (ou un type sorti après cette version
// de ajean) ne correspondrait à aucune option et serait silencieusement effacée
// au premier changement. On l'ajoute donc telle quelle à la liste plutôt que de
// la perdre.
function setSpecType(v){
  const sel = document.getElementById('s-spec');
  if(!sel) return;
  v = String(v || '').trim();
  if(v && ![...sel.options].some(o => o.value === v)){
    const opt = document.createElement('option');
    opt.value = v; opt.textContent = v + ' (dans la configuration)';
    sel.appendChild(opt);
  }
  sel.value = v;
  syncSpecRow();
  syncSpecDraftRow();
}
// Un type spéculatif à brouillon EXTERNE exige un fichier de draft à part (EAGLE-3,
// dFlash, dSpark, modèle séparé). MTP est intégré au modèle, les n-grammes n'ont pas
// de brouillon : eux n'ont pas de fichier à choisir.
function specNeedsDraft(v){
  v = String(v || '');
  return v.indexOf('draft-') === 0 && v !== 'draft-mtp';
}
// Ligne « modèle de draft » : visible seulement pour les types qui en réclament un.
function syncSpecDraftRow(){
  const sel = document.getElementById('s-spec');
  const row = document.getElementById('s-spec-draft-row');
  if(!sel || !row) return;
  row.style.display = specNeedsDraft(sel.value) ? '' : 'none';
}
// Modèle de draft courant : clé dédiée MODEL_DRAFT d'abord, sinon un --model-draft
// posé à la main dans EXTRA_ARGS (anciens presets).
function currentSpecDraftInTextarea(){
  return readEnvKey(document.getElementById('m-content').value, 'MODEL_DRAFT')
      || eaGetValued('--model-draft');
}
function onPickSpecDraft(){
  // On centralise dans la clé MODEL_DRAFT (résolue comme le modèle) et on retire un
  // éventuel --model-draft d'EXTRA_ARGS pour ne pas le passer deux fois.
  eaSetValued('--model-draft', '');
  cfgWriteKey('MODEL_DRAFT', document.getElementById('m-spec-draft').value);
}
// Peuple la liste des modèles de draft (mêmes .gguf que le modèle principal, hors
// projecteurs vision). Repli d'affichage si le fichier configuré est introuvable.
async function populateSpecDraft(){
  const sel = document.getElementById('m-spec-draft');
  if(!sel) return;
  const list = await jget('/api/models');
  const cur = currentSpecDraftInTextarea();
  let html = '<option value="">— aucun —</option>';
  let matched = false;
  for(const m of (list||[])){
    if(isMmprojName(m.name)) continue; // un projecteur n'est pas un modèle de draft
    const on = samePath(cur, m.value) || samePath(cur, m.path) ||
               samePath(baseName(cur), m.name) ? ' selected' : '';
    if(on) matched = true;
    html += '<option value="'+escHtml(m.value)+'"'+on+'>'+escHtml(m.name)+' ('+fmtSize(m.size)+')</option>';
  }
  if(cur && !matched){
    html += '<option value="'+escHtml(cur)+'" selected>'+escHtml(baseName(cur)||cur)+' (introuvable)</option>';
  }
  sel.innerHTML = html;
}
// Effort de réflexion : REASONING_EFFORT part dans la requête (chat_template_kwargs)
// mais n'a d'effet que si le moteur tourne avec --jinja. Comme éditer le preset
// impose déjà un switch (= redémarrage du moteur), autant ajouter --jinja tout de
// suite quand on choisit un effort. On ne le RETIRE pas en repassant sur « défaut » :
// il a pu être posé à la main pour les appels d'outils, et le laisser est sans
// risque.
function onEffort(val){
  cfgWriteKey('REASONING_EFFORT', val);
  if(val) eaToggleFlag('--jinja', true);
}
// Le nombre de jetons anticipés ne veut rien dire sans type : ligne masquée, et
// flag retiré pour ne pas laisser un réglage orphelin dans le preset.
function syncSpecRow(){
  const sel = document.getElementById('s-spec');
  const row = document.getElementById('s-spec-n-row');
  if(!sel || !row) return;
  const on = !!sel.value;
  row.style.display = on ? '' : 'none';
  return on;
}
function onSpecType(){
  const v = document.getElementById('s-spec').value;
  eaSetValued('--spec-type', v);
  if(!v){
    eaSetValued('--spec-draft-n-max', '');
    document.getElementById('s-spec-n').value = '';
  }
  // Un type sans brouillon externe (MTP intégré, n-grammes, aucun) ne doit pas
  // laisser traîner un modèle de draft orphelin qui serait quand même passé au moteur.
  if(!specNeedsDraft(v)){
    eaSetValued('--model-draft', '');
    cfgWriteKey('MODEL_DRAFT', '');
    const ds = document.getElementById('m-spec-draft'); if(ds) ds.value = '';
  }
  syncSpecRow();
  syncSpecDraftRow();
}

// Replie/déplie l'éditeur du fichier .env (config brute) dans le modal preset.
function toggleRaw(){
  const b = document.getElementById('m-raw-body');
  const c = document.getElementById('m-raw-caret');
  const show = b.style.display === 'none';
  b.style.display = show ? '' : 'none';
  if(c) c.classList.toggle('open', show);
}
const openPreset = (id)=>openItem('preset', id);
const openMem    = (n)=>openItem('mem', n);
// Fermer la modale n'annule PAS un téléchargement en cours (il vit côté
// serveur) : on arrête juste de l'interroger.
function closeModal(){ hideModal('modal'); document.getElementById('modal').classList.remove('loading'); stopDlPoll(); }
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
  // Le choix « supprimer aussi le .gguf » est posé DANS la confirmation : il n'a
  // de sens qu'au moment de supprimer, et il occupait le pied de l'éditeur en
  // permanence. Décoché à chaque ouverture — jamais de modèle effacé parce que
  // la case serait restée cochée d'une fois sur l'autre.
  const msg = 'Supprimer le ' + K.label.toLowerCase() + ' « ' + name + ' » ?'
    + (editingKind==='preset' ? '\n\nLe fichier .gguf du modèle est conservé, sauf si vous cochez ci-dessous (irréversible).' : '');
  const opts = {title:'Suppression', okText:'Supprimer', danger:true};
  if(editingKind==='preset') opts.check = 'supprimer aussi le fichier .gguf';
  if(!await askConfirm(msg, opts)) return;
  const delModel = editingKind==='preset' && askChecked();
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
// Download a .gguf from Hugging Face. Le téléchargement vit côté serveur : fermer
// la modale ne l'interrompt pas, on se rebranche dessus à la réouverture
// (attachDownload) et seul « Annuler » l'arrête vraiment.
let dlPoll = null, dlName = '';
function dlEls(){
  return {
    btn:  document.getElementById('m-hf-btn'),
    prog: document.getElementById('m-hf-progress'),
    bar:  document.getElementById('m-hf-bar'),
    cancel: document.getElementById('m-hf-cancel'),
  };
}
// Arrête le poll local sans toucher au téléchargement serveur.
function stopDlPoll(){ if(dlPoll){ clearInterval(dlPoll); dlPoll=null; } }
// Remet la zone de téléchargement à zéro (réouverture de la modale).
function resetDlUI(){
  const e = dlEls();
  stopDlPoll(); dlName='';
  e.prog.style.display='none'; e.bar.style.display='none';
  e.cancel.style.display='none'; e.btn.disabled=false;
}
// Remplit le sélecteur de destination avec les dossiers de modèles connus.
// L'espace libre est dans le libellé de l'option : c'est l'info utile au moment
// de choisir, et ça évite une ligne de plus dans une modale déjà chargée.
// Même source que « Dossiers de modèles » : ajouter un disque externe là-bas le
// rend aussitôt disponible ici.
let dlDirList = [];
async function populateDlDirs(){
  const sel = document.getElementById('m-hf-dir');
  if(!sel) return;
  let d = {};
  try{ d = await jget('/api/models/dirs'); }catch(_){ return; }
  dlDirList = d.dirs || [];
  const prev = sel.value;
  sel.innerHTML = '';
  for(const x of dlDirList){
    const o = document.createElement('option');
    o.value = x.path;
    o.textContent = x.path + (x.free >= 0 ? ' — ' + fmtSize(x.free) + ' libres' : '');
    sel.append(o);
  }
  if(prev && dlDirList.some(x => samePath(x.path, prev))) sel.value = prev;
}
async function startDownload(){
  const url = document.getElementById('m-hf-url').value.trim();
  if(!url){ toast('colle un lien .gguf'); return; }
  const dir = (document.getElementById('m-hf-dir')||{}).value || '';
  const e = dlEls();
  e.btn.disabled = true;
  e.prog.style.display = 'block';
  e.prog.textContent = 'vérification de la taille et de l’espace disque…';
  e.bar.style.display = 'block';
  e.bar.className = 'pe-bar indet';
  e.bar.firstElementChild.style.width = '';
  // Sonde d'abord : on connaît la taille exacte du fichier et l'espace restant
  // AVANT d'écrire quoi que ce soit, plutôt que d'échouer à 90 % du transfert.
  const p = await jpost('/api/models/download/probe', {url, dir});
  if(!p.ok || !p.enough){
    e.prog.innerHTML = '<span style="color:var(--err)">erreur : '+escHtml(p.error||'espace insuffisant')+'</span>';
    e.bar.style.display = 'none'; e.btn.disabled=false;
    await populateDlDirs();
    return;
  }
  // p.size couvre TOUTES les tranches : un modèle découpé annonce ses 45 Go, pas
  // les 15 Go du fichier dont le lien a été collé.
  const nparts = p.parts > 1 ? ' en '+p.parts+' fichiers' : '';
  e.prog.textContent = fmtSize(p.size)+nparts+' à télécharger — '+fmtSize(p.free)+' libres';
  const r = await jpost('/api/models/download', {url, dir});
  if(!r.ok){
    e.prog.innerHTML = '<span style="color:var(--err)">erreur : '+(r.error||'')+'</span>';
    e.bar.style.display = 'none'; e.btn.disabled=false; return;
  }
  watchDownload(r.filename);
}
// Annule le téléchargement en cours : le serveur coupe les connexions et
// supprime le fichier partiel.
async function cancelDownload(){
  if(!dlName) return;
  const e = dlEls();
  e.cancel.disabled = true;
  await jpost('/api/models/download/cancel', {filename: dlName});
  // L'état « annulé » est confirmé par le prochain tick de poll.
}
// Suit un téléchargement (nouveau ou déjà en cours) et pilote barre + texte.
function watchDownload(fname){
  const e = dlEls();
  dlName = fname;
  stopDlPoll();
  e.btn.disabled = true;
  e.prog.style.display = 'block';
  e.bar.style.display = 'block';
  e.cancel.style.display = 'inline';
  e.cancel.disabled = false;
  const stop = ()=>{ stopDlPoll(); dlName=''; e.btn.disabled=false; e.cancel.style.display='none'; };
  const tick = async ()=>{
    const list = await jget('/api/models/download/status');
    const st = (list||[]).find(d=>d.filename===fname);
    if(!st) return;
    if(st.canceled){
      e.prog.textContent = 'téléchargement annulé — '+fname;
      e.bar.style.display = 'none'; stop(); return;
    }
    if(st.error){
      e.prog.innerHTML = '<span style="color:var(--err)">erreur : '+st.error+'</span>';
      e.bar.style.display = 'none'; stop(); return;
    }
    if(st.finished){
      e.prog.innerHTML = '<span style="color:var(--ok)">✓ '+fname+' téléchargé ('+fmtSize(st.done)+')</span>';
      e.bar.className = 'pe-bar done'; e.bar.firstElementChild.style.width = '100%';
      stop();
      await Promise.all([populateModelPicker(), populateMmproj(), populateSpecDraft(), populateDlDirs()]);
      // Un projecteur (mmproj) se sélectionne dans le champ Vision, un modèle dans
      // le sélecteur de modèle — d'après le nom du fichier téléchargé. Le fichier
      // a pu atterrir hors du dossier ajean : l'option porte alors le chemin
      // complet, pas le simple nom de fichier.
      const pick = (id, cb)=>{
        const s = document.getElementById(id);
        const o = Array.from(s.options).find(o => samePath(baseName(o.value), fname));
        if(o){ s.value = o.value; cb(); }
      };
      if(isMmprojName(fname)) pick('m-mmproj', onPickMmproj);
      else pick('m-model', onPickModel);
      return;
    }
    const pct = st.total>0 ? st.done*100/st.total : 0;
    if(st.total>0){
      e.bar.className = 'pe-bar';
      e.bar.firstElementChild.style.width = Math.min(100, pct).toFixed(1)+'%';
    } else {
      e.bar.className = 'pe-bar indet';
    }
    const tot = st.total>0 ? ' / '+fmtSize(st.total)+' ('+Math.round(pct)+'%)' : '';
    let extra = '';
    if(st.speed>0){
      extra += ' — '+fmtSize(st.speed)+'/s';
      if(st.total>0){
        const eta = Math.max(0, Math.round((st.total-st.done)/st.speed));
        const m = Math.floor(eta/60), s = eta%60;
        extra += ' — reste '+(m>0 ? m+' min '+s+' s' : s+' s');
      }
    }
    // Une seule barre pour tout le modèle ; le compteur de tranches dit où on en
    // est, sans quoi un téléchargement en 3 fichiers semble se figer par paliers.
    const part = st.parts > 1 ? ' — fichier '+(st.part||1)+'/'+st.parts : '';
    e.prog.textContent = '↓ '+fmtSize(st.done)+tot+extra+part+' — '+fname;
  };
  dlPoll = setInterval(tick, 800);
  tick();
}
// À l'ouverture de la modale : se rebrancher sur un téléchargement encore en
// cours côté serveur (modale fermée entre-temps, page rechargée, autre appareil).
async function attachDownload(){
  const list = await jget('/api/models/download/status');
  const st = (list||[]).find(d=>!d.finished);
  if(st) watchDownload(st.filename);
}
// Smart autoscroll: follow the bottom while the user hasn't manually scrolled
// up. Re-stick when they scroll back near bottom themselves.
