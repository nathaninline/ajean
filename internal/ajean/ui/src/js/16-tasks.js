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
let TASK_MEM_ON = true; // état mémoire global (défaut d'une nouvelle tâche)
let TASK_WEB_ON = true; // état web global (défaut d'une nouvelle tâche)
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
  TASK_RUNNING = (r && r.running_id) || '';
  ensureTasksPoll();
  const pc = document.getElementById('tasks-pause-toggle');
  if(pc) pc.checked = paused;
  // Avertissement : sans mode agent, une tâche n'a aucun outil pour agir.
  const warn = document.getElementById('tasks-agent-warn');
  if(warn) warn.style.display = (tasksList.length && !agentOn) ? '' : 'none';
  // Pastille : « en cours » l'emporte, sinon le nombre de tâches actives.
  const nActive = tasksList.filter(t=>t.enabled).length;
  if(TASK_RUNNING) setBadge('tasks-badge', 'warn', 'en cours');
  else setBadge('tasks-badge', nActive ? true : null, nActive ? nActive+' active'+(nActive>1?'s':'') : '');

  const list = document.getElementById('tasks-list');
  list.textContent = '';
  if(!tasksList.length){
    list.innerHTML = '<div class="muted" style="font-size:12px">(aucune tâche)</div>';
    return;
  }
  tasksList.forEach(t=>{
    const running = (t.id === TASK_RUNNING);
    const row = document.createElement('div');
    row.className = 'mcp-row'+(t.enabled?'':' off')+(running?' task-running':'');

    const dot = document.createElement('span');
    dot.className = 'mcp-dot';
    if(running){
      // Point animé pendant l'exécution.
      dot.classList.add('mcp-dot-ok','task-dot-run');
    } else if(!t.enabled){ dot.classList.add('mcp-dot-off'); }
    else if(t.last_run && !t.last_ok){ dot.classList.add('mcp-dot-err'); }
    else if(t.last_run && t.last_ok){ dot.classList.add('mcp-dot-ok'); }
    else { dot.classList.add('mcp-dot-off'); }
    dot.title = running ? 'en cours d’exécution'
      : (!t.enabled ? 'désactivée'
      : (t.last_run ? (t.last_ok ? 'dernier passage : ok' : 'dernier passage : échec') : 'jamais exécutée'));

    const info = document.createElement('div');
    info.className = 'mcp-info';
    const nm = document.createElement('div'); nm.className = 'mcp-name';
    nm.textContent = t.name; nm.title = t.name;
    const meta = document.createElement('div'); meta.className = 'mcp-meta';
    if(running){
      const rn = document.createElement('span'); rn.className = 'task-run-badge';
      rn.textContent = 'en cours…'; meta.appendChild(rn);
    } else {
      const fr = document.createElement('span'); fr.className = 'mcp-tag';
      fr.textContent = scheduleLabel(t.schedule); meta.appendChild(fr);
      if(t.preset){
        const pr = document.createElement('span'); pr.className = 'mcp-tag';
        const pn = (TASK_PRESETS.find(p=>p.id===t.preset)||{}).name || t.preset;
        pr.textContent = pn; pr.title = 'preset : '+pn; meta.appendChild(pr);
      }
      if(t.enabled && t.next_run){
        const nx = document.createElement('span'); nx.className = 'mcp-tools';
        nx.textContent = 'prochaine : '+fmtWhen(t.next_run);
        meta.appendChild(nx);
      }
      if(t.last_run && !t.last_ok && t.last_error){
        const er = document.createElement('span'); er.className = 'mcp-err';
        er.textContent = 'échec'; er.title = t.last_error;
        meta.appendChild(er);
      }
    }
    info.appendChild(nm); info.appendChild(meta);

    row.appendChild(dot); row.appendChild(info);

    if(running){
      // Bouton arrêter : passe par /api/chat/stop (conv.Stop annule le contexte de
      // la tâche, cf. RunAutonomous).
      const st = document.createElement('button');
      st.className = 'task-stop-btn'; st.textContent = 'arrêter';
      st.onclick = (e)=>{ e.stopPropagation(); stopRunningTask(); };
      row.appendChild(st);
    } else {
      const sw = document.createElement('label'); sw.className = 'switch mcp-switch';
      sw.onclick = (e)=>e.stopPropagation();
      const cb = document.createElement('input'); cb.type = 'checkbox'; cb.checked = !!t.enabled;
      cb.onchange = ()=>toggleTask(t.id, cb.checked);
      const sl = document.createElement('span'); sl.className = 'slider';
      sw.appendChild(cb); sw.appendChild(sl);
      const edit = document.createElement('button');
      edit.className = 'mcp-edit'; edit.title = 'éditer'; edit.textContent = '✎';
      edit.onclick = (e)=>{ e.stopPropagation(); openTask(t.id); };
      row.appendChild(sw); row.appendChild(edit);
    }
    row.onclick = ()=> running ? null : openTask(t.id);
    list.appendChild(row);
  });
}

async function stopRunningTask(){
  await jfetch('/api/chat/stop',{method:'POST'}).catch(()=>{});
  toast('arrêt de la tâche…');
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
  if(s < 60) return (s<10 ? s.toFixed(1) : Math.round(s))+' s';
  const m = Math.floor(s/60), r = Math.round(s%60);
  return m+' min'+(r ? ' '+r+' s' : '');
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
      const ul = {m:'min', h:'h', d:'jour'}[u] || u;
      let lbl = 'toutes les '+n+' '+ul+((u==='d'&&n>1)?'s':'');
      if(u==='d' && tm) lbl += ' à '+tm;
      return lbl;
    }
    return 'toutes les '+d;
  }
  return 'cron : '+s;
}

async function toggleTask(id, on){
  await jpost('/api/tasks/toggle', {id, on});
  await loadTasks();
  toast(on ? 'tâche activée' : 'tâche désactivée');
}

async function toggleTasksPause(){
  const on = document.getElementById('tasks-pause-toggle').checked;
  await jpost('/api/tasks/pause', {on});
  toast(on ? 'tâches suspendues' : 'tâches réactivées');
  loadTasks();
}

// Ouvre la modale d'ajout (id='') ou d'édition (id existant).
function openTask(id){
  taskEditing = id || '';
  const t = id ? tasksList.find(x=>x.id===id) : null;
  document.getElementById('task-modal-title').textContent = t ? 'Éditer « '+t.name+' »' : 'Nouvelle tâche';
  document.getElementById('task-name').value = t ? t.name : '';
  document.getElementById('task-prompt').value = t ? t.prompt : '';
  document.getElementById('task-enabled').checked = t ? !!t.enabled : true;
  // Accès mémoire/web : sur une tâche existante on lit son réglage (no_mem/no_web,
  // stockés en négation) ; sur une nouvelle on suit l'état global de la machine.
  document.getElementById('task-mem').checked = t ? !t.no_mem : TASK_MEM_ON;
  document.getElementById('task-web').checked = t ? !t.no_web : TASK_WEB_ON;
  fillPresetSelect(t ? (t.preset||'') : '');

  // Décompose le schedule en intervalle (par défaut) ou cron. Forme intervalle :
  // « @every N(m|h|d)[@HH:MM] » (l'heure n'existe que pour les jours).
  const sched = t ? (t.schedule||'') : '@every 2h';
  const m = /^@every\s+(\d+)(m|h|d)(?:@(\d{1,2}:\d{2}))?\s*$/i.exec(sched);
  document.getElementById('task-time').value = '09:00';
  if(m || !t){
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
  renderTaskResult(t);
  document.getElementById('task-del').style.display = t ? '' : 'none';
  document.getElementById('task-run').style.display = t ? '' : 'none';
  showModal('task-modal');
}

// renderTaskResult remplit le panneau « Dernier résultat » avec le compte-rendu
// COMPLET (défilable), l'état (ok / échec / en cours) et l'horodatage. Masqué pour
// une tâche neuve ou jamais exécutée.
function renderTaskResult(t){
  const block = document.getElementById('task-result-block');
  const box = document.getElementById('task-result');
  const when = document.getElementById('task-result-when');
  box.className = 'task-report';
  if(!t || (!t.last_run && t.id !== TASK_RUNNING)){ block.style.display = 'none'; return; }
  block.style.display = '';
  if(t.id === TASK_RUNNING){
    when.textContent = '(en cours)';
    box.classList.add('report-run');
    box.textContent = 'exécution en cours…';
    return;
  }
  const parts = [];
  if(t.last_run) parts.push(new Date(t.last_run).toLocaleString());
  if(t.last_dur_ms) parts.push('en '+fmtDur(t.last_dur_ms));
  when.textContent = parts.length ? '· '+parts.join('  ·  ') : '';
  if(!t.last_ok){
    box.classList.add('report-err');
    box.textContent = 'Échec : '+(t.last_error||'erreur inconnue');
    return;
  }
  const rep = (t.last_report||'').trim();
  if(rep){ box.classList.add('md-report'); box.innerHTML = md(rep); }
  else { box.classList.add('report-empty'); box.textContent = '(terminée sans texte de compte-rendu — l\'action a été faite via les outils)'; }
}

function closeTask(){ hideModal('task-modal'); taskEditing = null; }

// fillPresetSelect peuple le sélecteur de preset. Première option = « preset
// actif » (vide) : la tâche utilise le modèle en cours au moment de l'exécution.
function fillPresetSelect(selected){
  const sel = document.getElementById('task-preset');
  sel.textContent = '';
  const opt0 = document.createElement('option'); opt0.value = ''; opt0.textContent = 'preset actif';
  sel.appendChild(opt0);
  TASK_PRESETS.forEach(p=>{
    const o = document.createElement('option'); o.value = p.id;
    o.textContent = p.name + (p.active ? ' (actif)' : '');
    sel.appendChild(o);
  });
  sel.value = selected || '';
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
  const st = document.getElementById('task-modal-status');
  if(!name || !prompt){ st.textContent = 'nom et consigne obligatoires'; st.style.color = 'var(--err)'; return; }
  if(!schedule){ st.textContent = 'fréquence invalide'; st.style.color = 'var(--err)'; return; }
  const r = await jpost('/api/tasks/save', {id: taskEditing||'', name, prompt, schedule, enabled, preset, tz, no_mem, no_web});
  if(!r || !r.ok){ st.textContent = (r && r.error) || 'échec'; st.style.color = 'var(--err)'; return; }
  closeTask();
  await loadTasks();
  toast('tâche enregistrée');
}

async function deleteTaskUI(){
  if(!taskEditing) return;
  const t = tasksList.find(x=>x.id===taskEditing);
  if(!await askConfirm('Supprimer la tâche « '+(t?t.name:'')+' » ?', {title:'Supprimer', okText:'Supprimer', danger:true})) return;
  await jpost('/api/tasks/delete', {id: taskEditing});
  closeTask();
  await loadTasks();
  toast('tâche supprimée');
}

async function runTaskNow(){
  if(!taskEditing) return;
  const st = document.getElementById('task-modal-status');
  const r = await jpost('/api/tasks/run', {id: taskEditing});
  if(r && r.ok){
    st.textContent = 'lancée, suis son avancement dans la liste';
    st.style.color = '';
    closeTask();
    // Rafraîchit tout de suite : loadTasks détecte running_id et lance le sondage
    // qui suivra l'exécution jusqu'à la fin.
    setTimeout(loadTasks, 400);
  } else {
    st.textContent = (r && r.error) || 'impossible de lancer maintenant';
    st.style.color = 'var(--err)';
  }
}
