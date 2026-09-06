// ─── Tâches planifiées ───────────────────────────────────────────────────────
// L'IA exécute une consigne toute seule sur une fréquence réglable (intervalle
// simple ou cron). Chaque tâche tourne ISOLÉE de la conversation : elle livre son
// résultat via les propres outils de l'IA (mail via MCP, shell…). L'interrupteur
// maître (« suspendre toutes les tâches ») permet de tout figer d'un coup pour
// discuter tranquille. Réutilise le gabarit visuel des serveurs MCP (mcp-row).
let tasksList = [];
let taskEditing = null; // id en cours d'édition ('' = nouvelle, null = fermé)
let TASK_RUNNING = '';  // id de la tâche en cours d'exécution ('' = aucune)
let TASK_PRESETS = [];  // presets disponibles (pour le sélecteur de la modale)
let TASK_PROJECTS = []; // projets disponibles (pour le sélecteur « mémoire du projet »)
let TASK_MEM_ON = true; // état mémoire global (défaut d'une nouvelle tâche)
let TASK_WEB_ON = true; // état web global (défaut d'une nouvelle tâche)
let TASK_SCRIPTS = []; // scripts durables disponibles (pour une tâche « script seul »)
let tasksPollTimer = null;

async function loadTasks(){ renderTasks(await jget('/api/tasks')); }

// Tant qu'une tâche tourne, on rafraîchit la liste toutes les 2 s (l'exécution est
// isolée du fil : sans sondage, on ne verrait ni son démarrage ni sa fin). On
// arrête le sondage dès qu'aucune tâche ne tourne — pas de poll permanent.
function ensureTasksPoll(){
  if(TASK_RUNNING && !tasksPollTimer){
    tasksPollTimer = setInterval(loadTasks, 2000);
  } else if(!TASK_RUNNING && tasksPollTimer){
    clearInterval(tasksPollTimer); tasksPollTimer = null;
  }
}

function renderTasks(r){
  tasksList = (r && r.tasks) || [];
  const paused = !!(r && r.paused);
  const agentOn = !!(r && r.agent);
  TASK_PRESETS = (r && r.presets) || [];
  TASK_MEM_ON = !r || r.mem_on !== false;
  TASK_WEB_ON = !r || r.web_on !== false;
  TASK_SCRIPTS = (r && r.scripts) || [];
  TASK_PROJECTS = (r && r.projects) || [];
  TASK_RUNNING = (r && r.running_id) || '';
  ensureTasksPoll();
  const pc = document.getElementById('tasks-pause-toggle');
  if(pc) pc.checked = paused;
  // Avertissement : sans mode agent, une tâche n'a aucun outil pour agir.
  const warn = document.getElementById('tasks-agent-warn');
  if(warn) warn.style.display = (tasksList.length && !agentOn) ? '' : 'none';
  // Pastille : « en cours » l'emporte, puis l'interrupteur maître (tout suspendu),
  // sinon le nombre de tâches actives.
  const nActive = tasksList.filter(t=>t.enabled).length;
  if(TASK_RUNNING) setBadge('tasks-badge', 'warn', t('tasks.status_running'));
  else if(paused && tasksList.length) setBadge('tasks-badge', 'warn', t('tasks.status_paused'));
  else setBadge('tasks-badge', nActive ? true : null, nActive ? nActive+' '+t('tasks.active_label')+(nActive>1?'s':'') : '');

  const list = document.getElementById('tasks-list');
  list.textContent = '';
  if(!tasksList.length){
    list.innerHTML = '<div class="muted" style="font-size:12px">'+t('tasks.empty')+'</div>';
    return;
  }
  tasksList.forEach(tk=>{
    const running = (tk.id === TASK_RUNNING);
    const row = document.createElement('div');
    row.className = 'mcp-row'+(tk.enabled?'':' off')+(running?' task-running':'');

    const dot = document.createElement('span');
    dot.className = 'mcp-dot';
    if(running){
      // Point animé pendant l'exécution.
      dot.classList.add('mcp-dot-ok','task-dot-run');
    } else if(!tk.enabled){ dot.classList.add('mcp-dot-off'); }
    else if(tk.last_run && !tk.last_ok){ dot.classList.add('mcp-dot-err'); }
    else if(tk.last_run && tk.last_ok){ dot.classList.add('mcp-dot-ok'); }
    else { dot.classList.add('mcp-dot-off'); }
    dot.title = running ? t('tasks.dot_running')
      : (!tk.enabled ? t('tasks.dot_disabled')
      : (tk.last_run ? (tk.last_ok ? t('tasks.dot_last_ok') : t('tasks.dot_last_err')) : t('tasks.dot_never')));

    const info = document.createElement('div');
    info.className = 'mcp-info';
    const nm = document.createElement('div'); nm.className = 'mcp-name';
    nm.textContent = tk.name; nm.title = tk.name;
    const meta = document.createElement('div'); meta.className = 'mcp-meta';
    if(running){
      const rn = document.createElement('span'); rn.className = 'task-run-badge';
      rn.textContent = t('tasks.running_badge'); meta.appendChild(rn);
    } else {
      const fr = document.createElement('span'); fr.className = 'mcp-tag';
      fr.textContent = scheduleLabel(tk.schedule); meta.appendChild(fr);
      if(tk.kind === 'script'){
        const sc = document.createElement('span'); sc.className = 'mcp-tag';
        sc.textContent = t('tasks.script_tag'); sc.title = t('tasks.script_title_prefix')+(tk.script||'');
        meta.appendChild(sc);
      }
      // (Le nom du modèle/preset n'est PLUS affiché dans la liste : on garde juste la
      // planification, plus lisible. Le preset reste réglable dans l'édition.)
      if(tk.enabled && tk.next_run){
        const nx = document.createElement('span'); nx.className = 'mcp-tools';
        nx.textContent = '→ '+fmtWhen(tk.next_run);
        nx.title = t('tasks.next_run_title_prefix')+fmtWhen(tk.next_run);
        meta.appendChild(nx);
      }
      if(tk.last_run && !tk.last_ok && tk.last_error){
        const er = document.createElement('span'); er.className = 'mcp-err';
        er.textContent = t('tasks.failed'); er.title = tk.last_error;
        meta.appendChild(er);
      }
    }
    info.appendChild(nm); info.appendChild(meta);

    row.appendChild(dot); row.appendChild(info);

    if(running){
      // Bouton arrêter : /api/tasks/stop annule le bon contexte selon le type —
      // conv.Stop pour une tâche IA, le registre des scripts pour une tâche script.
      const st = document.createElement('button');
      st.className = 'task-stop-btn'; st.textContent = t('tasks.stop_btn');
      st.onclick = (e)=>{ e.stopPropagation(); stopRunningTask(tk.id); };
      row.appendChild(st);
    } else {
      const sw = document.createElement('label'); sw.className = 'switch mcp-switch';
      sw.onclick = (e)=>e.stopPropagation();
      const cb = document.createElement('input'); cb.type = 'checkbox'; cb.checked = !!tk.enabled;
      cb.onchange = ()=>toggleTask(tk.id, cb.checked);
      const sl = document.createElement('span'); sl.className = 'slider';
      sw.appendChild(cb); sw.appendChild(sl);
      // Pas de bouton « éditer » : cliquer la ligne ouvre déjà la tâche en édition.
      row.appendChild(sw);
    }
    row.onclick = ()=> running ? null : openTask(tk.id);
    list.appendChild(row);
  });
}

async function stopRunningTask(id){
  await jpost('/api/tasks/stop',{id: id||TASK_RUNNING||''}).catch(()=>{});
  toast(t('tasks.stopping_toast'));
  setTimeout(loadTasks, 800);
}

// fmtWhen met en forme un epoch ms dans le FUSEAU du navigateur (jj/mm HH:MM) —
// le serveur envoie l'horodatage brut, sinon il serait figé en UTC (décalage).
function fmtWhen(ms){
  if(!ms) return '';
  const d = new Date(ms);
  const p = n => String(n).padStart(2,'0');
  return p(d.getDate())+'/'+p(d.getMonth()+1)+' '+p(d.getHours())+':'+p(d.getMinutes());
}

// fmtDur met une durée en ms sous forme lisible (« 8 s », « 2 min 5 s »).
function fmtDur(ms){
  const s = ms/1000;
  if(s < 60) return (s<10 ? s.toFixed(1) : Math.round(s))+' '+t('tasks.unit_seconds');
  const m = Math.floor(s/60), r = Math.round(s%60);
  return m+' '+t('tasks.unit_minutes')+(r ? ' '+r+' '+t('tasks.unit_seconds') : '');
}

// scheduleLabel rend une fréquence lisible pour la pastille de la liste.
function scheduleLabel(s){
  s = (s||'').trim();
  const m = /^@every\s+(.+)$/i.exec(s);
  if(m){
    const d = m[1].trim();
    const mm = /^(\d+)(m|h|d)(?:@(\d{1,2}:\d{2}))?$/.exec(d);
    if(mm){
      let n = parseInt(mm[1], 10), u = mm[2]; const tm = mm[3];
      // Anciennes tâches « jours » stockées en heures (multiple de 24).
      if(u === 'h' && n % 24 === 0 && n >= 24){ n = n/24; u = 'd'; }
      const ul = {m:t('tasks.unit_minutes'), h:t('tasks.unit_hour'), d:t('tasks.unit_day')}[u] || u;
      let lbl = t('tasks.every_prefix')+n+' '+ul+((u==='d'&&n>1)?'s':'');
      if(u==='d' && tm) lbl += t('tasks.at_time')+tm;
      return lbl;
    }
    return t('tasks.every_prefix')+d;
  }
  return t('tasks.cron_prefix')+s;
}

async function toggleTask(id, on){
  await jpost('/api/tasks/toggle', {id, on});
  await loadTasks();
  toast(on ? t('tasks.toggle_on') : t('tasks.toggle_off'));
}

async function toggleTasksPause(){
  const on = document.getElementById('tasks-pause-toggle').checked;
  await jpost('/api/tasks/pause', {on});
  toast(on ? t('tasks.pause_on') : t('tasks.pause_off'));
  loadTasks();
}

// Ouvre la modale d'ajout (id='') ou d'édition (id existant).
function openTask(id){
  taskEditing = id || '';
  const tk = id ? tasksList.find(x=>x.id===id) : null;
  document.getElementById('task-modal-title').textContent = tk ? t('tasks.edit_prefix')+tk.name+t('tasks.edit_suffix') : t('tasks.new_title');
  document.getElementById('task-name').value = tk ? tk.name : '';
  document.getElementById('task-prompt').value = tk ? tk.prompt : '';
  document.getElementById('task-enabled').checked = tk ? !!tk.enabled : true;
  // Accès mémoire/web : sur une tâche existante on lit son réglage (no_mem/no_web,
  // stockés en négation) ; sur une nouvelle on suit l'état global de la machine.
  document.getElementById('task-mem').checked = tk ? !tk.no_mem : TASK_MEM_ON;
  document.getElementById('task-web').checked = tk ? !tk.no_web : TASK_WEB_ON;
  fillPresetSelect(tk ? (tk.preset||'') : '');
  fillProjectSelect(tk ? (tk.project||'') : '');
  // Type de tâche (IA ou script) + sélecteur de script.
  fillScriptSelect(tk ? (tk.script||'') : '');
  setTaskKind(tk && tk.kind === 'script' ? 'script' : 'agent');

  // Décompose le schedule en intervalle (par défaut) ou cron. Forme intervalle :
  // « @every N(m|h|d)[@HH:MM] » (l'heure n'existe que pour les jours).
  const sched = tk ? (tk.schedule||'') : '@every 2h';
  const m = /^@every\s+(\d+)(m|h|d)(?:@(\d{1,2}:\d{2}))?\s*$/i.exec(sched);
  document.getElementById('task-time').value = '09:00';
  if(m || !tk){
    setTaskFreqMode('interval');
    document.getElementById('task-interval-n').value = m ? m[1] : '2';
    document.getElementById('task-interval-unit').value = m ? m[2] : 'h';
    if(m && m[3]){ const tm = m[3]; document.getElementById('task-time').value = tm.length===4 ? '0'+tm : tm; }
    document.getElementById('task-cron').value = '';
  } else {
    setTaskFreqMode('cron');
    document.getElementById('task-cron').value = sched;
  }
  taskUnitUI();

  document.getElementById('task-modal-status').textContent = '';
  renderTaskResult(tk);
  document.getElementById('task-del').style.display = tk ? '' : 'none';
  document.getElementById('task-run').style.display = tk ? '' : 'none';
  showModal('task-modal');
}

// renderTaskResult remplit le panneau « Dernier résultat » avec le compte-rendu
// COMPLET (défilable), l'état (ok / échec / en cours) et l'horodatage. Masqué pour
// une tâche neuve ou jamais exécutée.
function renderTaskResult(tk){
  const block = document.getElementById('task-result-block');
  const box = document.getElementById('task-result');
  const when = document.getElementById('task-result-when');
  box.className = 'task-report';
  if(!tk || (!tk.last_run && tk.id !== TASK_RUNNING)){ block.style.display = 'none'; return; }
  block.style.display = '';
  if(tk.id === TASK_RUNNING){
    when.textContent = t('tasks.result_running');
    box.classList.add('report-run');
    box.textContent = t('tasks.result_running_text');
    return;
  }
  const parts = [];
  if(tk.last_run) parts.push(new Date(tk.last_run).toLocaleString());
  if(tk.last_dur_ms) parts.push(t('tasks.duration_prefix')+fmtDur(tk.last_dur_ms));
  when.textContent = parts.length ? '· '+parts.join('  ·  ') : '';
  if(!tk.last_ok){
    box.classList.add('report-err');
    box.textContent = t('tasks.result_failed_prefix')+(tk.last_error||t('tasks.result_unknown_error'));
    return;
  }
  const rep = (tk.last_report||'').trim();
  if(rep){ box.classList.add('md-report'); box.innerHTML = md(rep); }
  else { box.classList.add('report-empty'); box.textContent = t('tasks.result_empty'); }
}

function closeTask(){ hideModal('task-modal'); taskEditing = null; }

// fillPresetSelect peuple le sélecteur de preset. Première option = « preset
// actif » (vide) : la tâche utilise le modèle en cours au moment de l'exécution.
function fillPresetSelect(selected){
  const sel = document.getElementById('task-preset');
  sel.textContent = '';
  const opt0 = document.createElement('option'); opt0.value = ''; opt0.textContent = t('tasks.preset_active');
  sel.appendChild(opt0);
  TASK_PRESETS.forEach(p=>{
    const o = document.createElement('option'); o.value = p.id;
    o.textContent = p.name + (p.active ? t('tasks.preset_active_suffix') : '');
    sel.appendChild(o);
  });
  sel.value = selected || '';
}

// fillProjectSelect peuple le sélecteur de projet (mémoire de la tâche). La
// sélection est OBLIGATOIRE : la tâche lit/écrit dans la mémoire de ce projet. Pour
// une tâche existante on garde son projet ; pour une nouvelle on présélectionne le
// projet actif (ACTIVE_PROJECT, défini par 18-projects.js), sinon le premier.
function fillProjectSelect(selected){
  const sel = document.getElementById('task-project');
  if(!sel) return;
  sel.textContent = '';
  TASK_PROJECTS.forEach(p=>{
    const o = document.createElement('option'); o.value = p.slug; o.textContent = p.name;
    sel.appendChild(o);
  });
  const fallback = (typeof ACTIVE_PROJECT !== 'undefined' && ACTIVE_PROJECT) ||
    (TASK_PROJECTS[0] && TASK_PROJECTS[0].slug) || '';
  sel.value = selected || fallback;
  // Si le projet enregistré n'existe plus (supprimé), on retombe sur le fallback.
  if(!sel.value && sel.options.length) sel.selectedIndex = 0;
}

// setTaskKind bascule le formulaire entre tâche IA (consigne + preset + accès) et
// tâche script (sélecteur de script). Masque les groupes sans objet.
function setTaskKind(kind){
  const r = document.querySelector('input[name="task-kind"][value="'+kind+'"]');
  if(r) r.checked = true;
  const isScript = kind === 'script';
  document.getElementById('task-prompt-group').style.display = isScript ? 'none' : '';
  document.getElementById('task-preset-group').style.display = isScript ? 'none' : '';
  document.getElementById('task-access-group').style.display = isScript ? 'none' : '';
  document.getElementById('task-script-group').style.display = isScript ? '' : 'none';
}

// fillScriptSelect peuple le sélecteur de script. Vide s'il n'y a aucun script.
function fillScriptSelect(selected){
  const sel = document.getElementById('task-script');
  sel.textContent = '';
  if(!TASK_SCRIPTS.length){
    const o = document.createElement('option'); o.value = '';
    o.textContent = t('tasks.no_script'); sel.appendChild(o);
    return;
  }
  TASK_SCRIPTS.forEach(s=>{
    const o = document.createElement('option'); o.value = s.name; o.textContent = s.name;
    sel.appendChild(o);
  });
  sel.value = selected || TASK_SCRIPTS[0].name;
}

function setTaskFreqMode(mode){
  const r = document.querySelector('input[name="task-freq-mode"][value="'+mode+'"]');
  if(r) r.checked = true;
  taskFreqUI(mode);
}
function taskFreqUI(mode){
  document.getElementById('task-interval-fields').style.display = mode==='interval' ? '' : 'none';
  document.getElementById('task-cron-fields').style.display = mode==='cron' ? '' : 'none';
  taskUnitUI();
}
// taskUnitUI : la ligne « À (heure) » n'apparaît que pour un intervalle en JOURS
// (« tous les N jours à HH:MM »). Sans intérêt pour des minutes/heures.
function taskUnitUI(){
  const mode = (document.querySelector('input[name="task-freq-mode"]:checked')||{}).value;
  const unit = document.getElementById('task-interval-unit').value;
  document.getElementById('task-time-row').style.display = (mode==='interval' && unit==='d') ? '' : 'none';
}

// buildSchedule sérialise l'état du formulaire de fréquence en chaîne serveur.
function buildSchedule(){
  const mode = (document.querySelector('input[name="task-freq-mode"]:checked')||{}).value || 'interval';
  if(mode==='cron') return document.getElementById('task-cron').value.trim();
  const n = parseInt(document.getElementById('task-interval-n').value, 10);
  const u = document.getElementById('task-interval-unit').value;
  if(!n || n < 1) return '';
  // Jours : on ancre à une heure de la journée (« @every Nd@HH:MM »).
  if(u === 'd'){
    const tm = document.getElementById('task-time').value || '09:00';
    return '@every '+n+'d@'+tm;
  }
  return '@every '+n+u;
}

async function saveTask(){
  const name = document.getElementById('task-name').value.trim();
  const prompt = document.getElementById('task-prompt').value.trim();
  const schedule = buildSchedule();
  const enabled = document.getElementById('task-enabled').checked;
  const preset = document.getElementById('task-preset').value;
  const tz = (Intl.DateTimeFormat().resolvedOptions().timeZone) || '';
  const no_mem = !document.getElementById('task-mem').checked;
  const no_web = !document.getElementById('task-web').checked;
  const kind = (document.querySelector('input[name="task-kind"]:checked')||{}).value || 'agent';
  const script = document.getElementById('task-script').value;
  const project = (document.getElementById('task-project')||{}).value || '';
  const st = document.getElementById('task-modal-status');
  if(!name){ st.textContent = t('tasks.name_required'); st.style.color = 'var(--err)'; return; }
  if(kind === 'script'){
    if(!script){ st.textContent = t('tasks.script_required'); st.style.color = 'var(--err)'; return; }
  } else if(!prompt){ st.textContent = t('tasks.prompt_required'); st.style.color = 'var(--err)'; return; }
  if(!schedule){ st.textContent = t('tasks.schedule_invalid'); st.style.color = 'var(--err)'; return; }
  const r = await jpost('/api/tasks/save', {id: taskEditing||'', name, prompt, schedule, enabled, preset, project, tz, no_mem, no_web, kind, script});
  if(!r || !r.ok){ st.textContent = (r && r.error) || t('tasks.failed'); st.style.color = 'var(--err)'; return; }
  closeTask();
  await loadTasks();
  toast(t('tasks.saved_toast'));
}

async function deleteTaskUI(){
  if(!taskEditing) return;
  const tk = tasksList.find(x=>x.id===taskEditing);
  if(!await askConfirm(t('tasks.delete_confirm_prefix')+(tk?tk.name:'')+t('tasks.delete_confirm_suffix'), {title:t('tasks.delete_title'), okText:t('tasks.delete_title'), danger:true})) return;
  await jpost('/api/tasks/delete', {id: taskEditing});
  closeTask();
  await loadTasks();
  toast(t('tasks.deleted_toast'));
}

async function runTaskNow(){
  if(!taskEditing) return;
  const st = document.getElementById('task-modal-status');
  const r = await jpost('/api/tasks/run', {id: taskEditing});
  if(r && r.ok){
    st.textContent = t('tasks.run_started');
    st.style.color = '';
    closeTask();
    // Rafraîchit tout de suite : loadTasks détecte running_id et lance le sondage
    // qui suivra l'exécution jusqu'à la fin.
    setTimeout(loadTasks, 400);
  } else {
    st.textContent = (r && r.error) || t('tasks.run_failed');
    st.style.color = 'var(--err)';
  }
}
