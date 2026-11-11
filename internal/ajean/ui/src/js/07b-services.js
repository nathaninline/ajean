// 07b-services.js — section « Modèles » : les services système (services.go).
// Clic sur une ligne : le chat utilise ce modèle (démarré si besoin, garde-fou
// côté serveur). Bouton rond : marche / arrêt. Crayon : édition.

let SERVICES = [];
const SVC_JOBS = {};      // id → dernière ligne de progression d'un démarrage
let svcEditing = null;    // service ouvert dans la modale (null = nouveau)
let svcPollTimer = null;

function tf(key, fr){ const v = t(key); return v === key ? fr : v; }

const SVC_STATE = {
  active:     ['svc.state_active', 'en marche'],
  activating: ['svc.state_activating', 'chargement'],
  inactive:   ['svc.state_inactive', 'arrêté'],
  failed:     ['svc.state_failed', 'erreur'],
  remote:     ['svc.state_remote', 'distant'],
  unknown:    ['svc.state_unknown', 'inconnu'],
};

async function loadServices(){
  let list;
  try{ list = await jget('/api/services'); }catch(e){ return; }
  if(!Array.isArray(list)) return;
  SERVICES = list;
  renderServices();
}

function renderServices(){
  // La liste est celle des presets (06-settings.js, paintPresets).
  if(typeof paintPresets === 'function') paintPresets();
}

// Démarrage suivi par /api/services/progress (le chargement peut durer).
async function startService(id, use, force, replace){
  if(SVC_JOBS[id]) return;
  SVC_JOBS[id] = tf('svc.starting', 'démarrage…');
  renderServices();
  let q = '/api/services/start?id=' + encodeURIComponent(id) + (use ? '&use=1' : '') + (force ? '&force=1' : '') + (replace ? '&replace=1' : '');
  const r = await jpost(q).catch(e => ({ok:false, error:String(e)}));
  if(!r.ok){ delete SVC_JOBS[id]; toast(r.error || t('chat.error')); renderServices(); return; }
  for(;;){
    await new Promise(res => setTimeout(res, 1200));
    let p;
    try{ p = await jget('/api/services/progress?id=' + encodeURIComponent(id)); }catch(e){ continue; }
    if(p.lines && p.lines.length){ SVC_JOBS[id] = p.lines[p.lines.length-1]; renderServices(); }
    if(p.done){
      delete SVC_JOBS[id];
      await loadServices();
      if(p.conflict){
        // Bascule : le port est tenu par un autre modèle géré.
        const c = p.conflict;
        const q = tf('svc.conflict_q', 'Le port {port} est utilisé par {name}. Arrêter {name} et démarrer celui-ci ?').replaceAll('{port}', c.port).replaceAll('{name}', c.conflict);
        if(await askConfirm(q)) startService(id, use, force, true);
      } else if(p.failure){
        const f = p.failure;
        let msg = f.failed + (f.detail ? '\n\n' + f.detail : '');
        if(f.restored) msg += '\n\n' + tf('svc.restored', '{name} a été remis en service.').replace('{name}', f.restored);
        askAlert(msg);
      } else if(p.error){
        const memo = /mémoire insuffisante/.test(p.error);
        const msg = p.error + (memo ? '\n\n' + tf('svc.force_q', 'Démarrer quand même ? Le chien de garde RAM reste actif.') : '');
        if(memo){
          if(await askConfirm(msg)) startService(id, use, true);
        } else {
          askAlert(p.error);
        }
      } else if(use){
        toast(tf('svc.chat_now', 'Le chat utilise ') + (SERVICES.find(s => s.id === id) || {name:id}).name);
        if(typeof loadStatus === 'function') loadStatus();
        if(typeof loadCfg === 'function') loadCfg();
      }
      return;
    }
  }
}

function useService(id){
  const s = SERVICES.find(x => x.id === id);
  if(s && s.chat && s.ready) return;
  startService(id, true);
}

async function stopService(id){
  const r = await jpost('/api/services/stop?id=' + encodeURIComponent(id)).catch(e => ({ok:false, error:String(e)}));
  if(!r.ok) toast(r.error || t('chat.error'));
  loadServices();
}

// --- Modale d'édition ------------------------------------------------------

function svcKind(k){
  document.querySelectorAll('input[name=svc-kind]').forEach(i => i.checked = i.value === k);
  document.getElementById('svc-local-body').style.display = k === 'local' ? '' : 'none';
  document.getElementById('svc-remote-body').style.display = k === 'remote' ? '' : 'none';
  document.querySelectorAll('#svc-modal .svc-local').forEach(e => e.style.display = k === 'remote' ? 'none' : '');
}

function svcVal(id, v){ const e = document.getElementById(id); if(e.type === 'checkbox') e.checked = !!v; else e.value = v == null ? '' : v; }

async function openService(id){
  let s = null;
  if(id){
    try{ s = (await jget('/api/services')).find(x => x.id === id) || null; }catch(e){}
  }
  svcEditing = s;
  const remote = !!(s && !s.exec && !s.command);
  document.getElementById('svc-title').textContent = s ? s.name : tf('svc.new', 'Nouveau modèle');
  svcVal('svc-name', s && s.name);
  const form = (s && s.form) || {};
  svcVal('svc-cmd', svcCmdPretty(svcCmdMask(form.command || '')));
  svcVal('svc-workdir', form.workdir || '');
  svcVal('svc-user', form.user || '');
  svcVal('svc-restart', s ? form.restart !== false : true);
  svcVal('svc-autostart', s && s.autostart);
  svcVal('svc-env', s && s.env ? Object.entries(s.env).map(([k, v]) => k + '=' + v).join('\n') : '');
  document.querySelectorAll('#svc-modal .svc-exist-only').forEach(e => e.style.display = s ? '' : 'none');
  // Un service local reste sur cette machine.
  document.getElementById('svc-kind-seg').style.display = s && !remote ? 'none' : '';
  const u = document.getElementById('svc-url');
  svcVal('svc-url', s && !remote ? s.url : '');
  u.dataset.manual = '';
  svcVal('svc-model', s && !remote ? s.model : '');
  svcVal('svc-key', '');
  svcVal('svc-url-remote', remote ? s.url : '');
  svcVal('svc-model-remote', remote ? s.model : '');
  svcVal('svc-key-remote', '');
  document.querySelectorAll('#svc-modal .svc-key-note').forEach(e => e.textContent = tf('svc.key_hint', 'facultative ; vide = inchangée'));
  svcVal('svc-ctx', s && s.context ? s.context : '');
  svcVal('svc-vision', s && s.vision);
  document.getElementById('svc-test-status').textContent = '';
  document.getElementById('svc-del').style.display = s ? '' : 'none';
  // Bench : mesure le modèle du chat, seulement quand il répond (comme avant : preset actif).
  document.getElementById('svc-bench').style.display = s && s.chat && s.ready ? 'inline-flex' : 'none';
  document.getElementById('svc-stop').style.display = s && !remote && (s.state === 'active' || s.state === 'activating') ? '' : 'none';
  document.querySelector('#svc-modal .svc-adv').open = false;
  svcKind(remote ? 'remote' : 'local');
  document.getElementById('svc-mlog').textContent = '';
  document.getElementById('svc-log-group').style.display = s && !remote ? '' : 'none';
  showModal('svc-modal');
  svcCmdFit();
  if(s && !remote) loadServiceLog();
}

async function stopFromModal(){
  if(!svcEditing) return;
  await stopService(svcEditing.id);
  closeService();
}

function closeService(){ hideModal('svc-modal'); }

// Benchmark du modèle du chat : même fenêtre qu'avant (runBenchUI, 07-models.js).
function benchService(){ openBenchModal(); }
// Commande affichée en entier : une option par ligne, champ à la hauteur du
// texte. Elle est remise sur une seule ligne à l'enregistrement.
function svcCmdPretty(cmd){
  const toks = String(cmd).match(/(?:[^\s'"]+|'[^']*'|"(?:\\.|[^"\\])*")+/g) || [];
  let out = '';
  toks.forEach((t, i) => { out += i === 0 ? t : (/^--?[A-Za-z]/.test(t) ? '\n  ' : ' ') + t; });
  return out;
}
function svcCmdFit(){
  const e = document.getElementById('svc-cmd');
  if(!e) return;
  e.style.height = 'auto';
  e.style.height = (e.scrollHeight + 2) + 'px';
}
function svcCmdFlat(v){ return String(v).trim().replace(/\s*\n\s*/g, ' '); }
// La clé API n'apparaît pas en clair dans la commande affichée : elle est
// remplacée par SVC_KEY_MASK, puis remise à l'enregistrement.
const SVC_KEY_MASK = '••••••••';
const SVC_KEY_RE = /(--api-key(?:=|\s+))('[^']*'|"[^"]*"|\S+)/g;
function svcCmdMask(cmd){ return String(cmd).replace(SVC_KEY_RE, (m, flag) => flag + SVC_KEY_MASK); }
function svcCmdRead(){
  const cmd = svcCmdFlat(document.getElementById('svc-cmd').value);
  if(!cmd.includes(SVC_KEY_MASK)) return cmd;
  const keys = [];
  String((svcEditing && svcEditing.form && svcEditing.form.command) || '').replace(SVC_KEY_RE, (m, flag, k) => { keys.push(k); return m; });
  let i = 0;
  return cmd.replace(SVC_KEY_RE, (m, flag, k) => k === SVC_KEY_MASK && i < keys.length ? flag + keys[i++] : m);
}
function svcFromForm(){
  const kind = (document.querySelector('input[name=svc-kind]:checked') || {}).value || 'local';
  const g = id => document.getElementById(id).value.trim();
  const name = g('svc-name');
  const id = svcEditing ? svcEditing.id : (name.toLowerCase().normalize('NFD').replace(/[\u0300-\u036f]/g, '').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 40) || 'modele');
  const s = { id, name, context: parseInt(g('svc-ctx')) || 0, vision: document.getElementById('svc-vision').checked };
  if(svcEditing && svcEditing.preset) s.preset = svcEditing.preset;
  // Échantillonnage, prompt système et garde-fou ne se règlent plus ici : on
  // garde les valeurs existantes. Le garde-fou se cale seul sur le pic mesuré.
  const ed = svcEditing || {};
  if(ed.chat_params) s.chat_params = ed.chat_params;
  s.need = ed.need || {};
  s.ready_timeout_s = ed.ready_timeout_s || 0;
  s.exclusive = svcEditing ? !!ed.exclusive : true;
  if(kind === 'remote'){
    s.url = g('svc-url-remote'); s.model = g('svc-model-remote'); s.api_key = g('svc-key-remote');
    return s;
  }
  s.url = g('svc-url'); s.model = g('svc-model'); s.api_key = g('svc-key');
  if(svcEditing && svcEditing.exec && !svcEditing.command){
    s.exec = svcEditing.exec; s.args = svcEditing.args; s.workdir = svcEditing.workdir;
  } else {
    s.command = svcCmdRead();
    s.workdir = g('svc-workdir');
    s.user = g('svc-user');
    s.no_restart = !document.getElementById('svc-restart').checked;
  }
  const env = {};
  document.getElementById('svc-env').value.split('\n').forEach(l => { const i = l.indexOf('='); if(i > 0) env[l.slice(0, i).trim()] = l.slice(i + 1).trim(); });
  if(Object.keys(env).length) s.env = env;
  return s;
}

function svcFormFields(){
  const g = id => document.getElementById(id).value.trim();
  return { command: svcCmdRead(), workdir: g('svc-workdir'), user: g('svc-user'),
    description: g('svc-name'), restart: document.getElementById('svc-restart').checked };
}

async function svcSetAutostart(on){
  if(!svcEditing) return;
  const r = await jpost('/api/services/autostart?id=' + encodeURIComponent(svcEditing.id) + '&on=' + (on ? 1 : 0)).catch(e => ({ok:false, error:String(e)}));
  if(!r.ok){ askAlert(r.error || t('chat.error')); document.getElementById('svc-autostart').checked = !on; }
}

async function saveService(){
  const kind = (document.querySelector('input[name=svc-kind]:checked') || {}).value || 'local';
  const s = svcFromForm();
  const r = await jpost('/api/services', s).catch(e => ({ok:false, error:String(e)}));
  if(!r.ok){ askAlert(r.error || t('chat.error')); return; }
  // Ligne de commande modifiée : l'unité est réécrite (service relancé s'il tournait).
  if(svcEditing && kind === 'local' && (svcEditing.command || svcEditing.exec)){
    const f = svcFormFields();
    const old = svcEditing.form || {};
    if(f.command !== svcCmdFlat(old.command || '') || f.workdir !== (old.workdir || '') || f.user !== (old.user || '') || f.restart !== old.restart){
      const rf = await jpost('/api/services/form?id=' + encodeURIComponent(s.id), f).catch(e => ({ok:false, error:String(e)}));
      if(!rf.ok){ askAlert(rf.error || t('chat.error')); return; }
    }
  }
  // Modèle du chat modifié : on rebascule pour appliquer réglages et prompt.
  if(svcEditing && svcEditing.chat) await jpost('/api/services/use?id=' + encodeURIComponent(s.id)).catch(() => {});
  if(svcEditing && svcEditing.chat && typeof loadCfg === 'function') loadCfg();
  closeService();
  toast(tf('svc.saved', 'Modèle enregistré'));
  loadServices();
}

async function deleteService(){
  if(!svcEditing) return;
  if(!await askConfirm(tf('svc.delete_q', 'Désinstaller ce modèle ? Il sera arrêté, et son fichier de service mis de côté.'))) return;
  const r = await jfetch('/api/services?id=' + encodeURIComponent(svcEditing.id), {method:'DELETE'}).then(x => x.json()).catch(e => ({ok:false, error:String(e)}));
  if(!r.ok){ askAlert(r.error || t('chat.error')); return; }
  closeService();
  loadServices();
}

async function testService(){
  const el = document.getElementById('svc-test-status');
  el.textContent = tf('svc.testing', 'test…');
  const r = await jpost('/api/services/test', svcFromForm()).catch(e => ({ok:false, error:String(e)}));
  el.textContent = r.ok ? tf('svc.test_ok', 'répond, modèle : ') + r.model : r.error;
}

async function loadServiceLog(){
  if(!svcEditing) return;
  const el = document.getElementById('svc-mlog');
  try{
    const r = await jfetch('/api/services/logs?id=' + encodeURIComponent(svcEditing.id) + '&n=200');
    el.textContent = (await r.text()).trim() || tf('svc.log_empty', '(journal vide)');
    el.scrollTop = el.scrollHeight;
  }catch(e){ el.textContent = String(e); }
}

// Rafraîchissement : rapide pendant un chargement, lent sinon.
function svcPoll(){
  clearTimeout(svcPollTimer);
  svcPollTimer = setTimeout(async () => {
    if(!document.hidden) await loadServices();
    svcPoll();
  }, Object.keys(SVC_JOBS).length ? 2000 : 8000);
}
svcPoll();

// Rapport de conversion des presets (refonte 0.19) : affiché une seule fois.
async function svcShowConvertReport(){
  let rep;
  try{ rep = await jget('/api/services/convert'); }catch(e){ return; }
  if(!rep || rep.seen || !rep.results || !rep.results.length) return;
  const ok = rep.results.filter(r => r.status === 'ok' || r.status === 'exists');
  const bad = rep.results.filter(r => r.status === 'error');
  let msg = tf('svc.convert_intro', 'Les presets sont devenus des modèles (services système). Les fichiers de presets n\'ont pas été modifiés.') + '\n\n';
  msg += ok.map(r => '• ' + r.name + (r.note ? '\n   ' + r.note : '')).join('\n');
  if(bad.length) msg += '\n\n' + tf('svc.convert_failed', 'Non repris :') + '\n' + bad.map(r => '• ' + r.name + ' : ' + r.detail).join('\n');
  await askAlert(msg);
  jpost('/api/services/convert?seen=1').catch(() => {});
  loadServices();
}
setTimeout(svcShowConvertReport, 1500);

