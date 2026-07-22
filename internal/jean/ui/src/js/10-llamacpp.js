// Backend llama.cpp : statut, vérification des maj, install / update / rebuild
// en job asynchrone côté serveur (logs + progression pollés sur /api/llamacpp/job).
let lcState = null, lcPoll = null, lcLogNext = 0;

function lcBadge(html){ document.getElementById('lc-badge').innerHTML = html; }

async function loadLlamacpp(){
  let s;
  try{ s = await jget('/api/llamacpp'); }catch(_){ return; }
  lcState = s;
  const st = document.getElementById('lc-status');
  const parts = [];
  if(!s.installed){
    parts.push('<span style="color:var(--dim)">llama.cpp n\'est pas encore installé sur cette machine.</span>');
    parts.push('<span style="color:var(--dim)">L\'installation clone le dépôt, détecte le GPU et compile <code>llama-server</code> automatiquement.</span>');
    lcBadge('<span class="tag" style="opacity:.6">absent</span>');
  } else {
    const esc = t=>String(t||'').replace(/[<>&]/g,c=>({'<':'&lt;','>':'&gt;','&':'&amp;'}[c]));
    parts.push('commit <b style="color:var(--text)">'+esc(s.commit)+'</b>'+(s.branch?' <span style="opacity:.7">('+esc(s.branch)+')</span>':''));
    if(s.commit_date) parts.push('<span style="opacity:.8">'+esc(String(s.commit_date).slice(0,16))+'</span> — '+esc(s.commit_msg));
    if(s.bin){
      parts.push('binaire : <span style="color:var(--ok)">✓ compilé</span>'+(s.in_use?' · <span style="color:var(--accent)">utilisé par la config</span>':' · <span style="opacity:.7">non utilisé par la config (BIN pointe ailleurs)</span>'));
    } else {
      parts.push('binaire : <span style="color:var(--err)">absent (pas encore compilé)</span>');
    }
    if(s.behind > 0) lcBadge('<span class="tag" style="color:var(--accent);border-color:var(--accent)">maj dispo</span>');
    else if(s.bin) lcBadge('<span class="tag ok">ok</span>');
    else lcBadge('<span class="tag err">à compiler</span>');
  }
  if(s.plan){
    const p = s.plan;
    parts.push('accélérateur détecté : <b style="color:var(--text)">'+p.backend.toUpperCase()+'</b>'+(p.arch?' <span style="opacity:.7">(arch '+p.arch+')</span>':'')+' · '+p.jobs+' jobs');
  }
  // Mode actif : binaires officiels précompilés vs build local.
  const pb = s.prebuilt || {};
  if(pb.in_use){
    parts.push('mode : <b style="color:var(--accent)">⚡ binaires précompilés officiels</b>'+(pb.tag?' <span style="opacity:.7">('+pb.tag+')</span>':''));
    lcBadge('<span class="tag ok">précompilé</span>');
  } else if(s.installed && s.in_use){
    parts.push('mode : <b style="color:var(--text)">🔧 compilé depuis les sources</b>'+(pb.tag?' <span style="opacity:.6">(précompilé '+pb.tag+' aussi installé)</span>':''));
  }
  st.innerHTML = parts.join('<br>');
  document.getElementById('lc-install').style.display = s.installed ? 'none' : '';
  document.getElementById('lc-check').style.display = (s.installed || pb.in_use) ? '' : 'none';
  document.getElementById('lc-update').style.display = (s.installed && !pb.in_use && (s.behind>0 || !s.bin)) ? '' : 'none';
  document.getElementById('lc-rebuild').style.display = (s.installed && !pb.in_use) ? '' : 'none';
  document.getElementById('lc-prebuilt').textContent = pb.in_use ? '⚡ maj binaires précompilés' : '⚡ binaires précompilés';
  // Job en cours (page rechargée pendant un build) → on raccroche le polling.
  if(s.job && s.job.exists && s.job.running && !lcPoll){
    document.getElementById('lc-details').open = true;
    lcStartPolling();
  }
}

async function lcCheck(){
  const btn = document.getElementById('lc-check');
  const msg = document.getElementById('lc-check-msg');
  btn.disabled = true; btn.textContent = '⏳ vérification…';
  // Mode précompilé : on compare à la dernière release officielle, pas au git.
  if(lcState && lcState.prebuilt && lcState.prebuilt.in_use){
    try{
      const r = await jpost('/api/llamacpp/prebuilt/check');
      if(!r.ok){ msg.innerHTML = '<span style="color:var(--err)">'+(r.error||'erreur')+'</span>'; return; }
      if(r.update){
        msg.innerHTML = 'nouvelle release officielle : <b style="color:var(--accent)">'+r.latest+'</b> (installée : '+(r.current||'aucune')+')<br><span style="opacity:.8">'+String(r.variant||'').replace(/[<>&]/g,'')+' · ~'+r.size_mb+' Mo</span>';
        lcBadge('<span class="tag" style="color:var(--accent);border-color:var(--accent)">maj dispo</span>');
      } else {
        msg.innerHTML = '<span style="color:var(--ok)">✓ à jour</span> ('+r.current+')';
      }
    } finally { btn.disabled = false; btn.textContent = '↥ vérifier les maj'; }
    return;
  }
  try{
    const r = await jpost('/api/llamacpp/check');
    if(!r.ok){ msg.innerHTML = '<span style="color:var(--err)">'+(r.error||'erreur')+'</span>'; return; }
    if(r.behind > 0){
      msg.innerHTML = '<b style="color:var(--accent)">'+r.behind+' commit(s) de retard</b> sur origin/'+r.branch+'<br>'+
        'dernier distant : <b>'+r.remote+'</b> — '+String(r.remote_msg||'').replace(/[<>&]/g,'');
      document.getElementById('lc-update').style.display = '';
      lcBadge('<span class="tag" style="color:var(--accent);border-color:var(--accent)">maj dispo</span>');
    } else if(!r.has_binary){
      msg.innerHTML = 'sources à jour mais <b>binaire absent</b> — lance une recompilation.';
    } else {
      msg.innerHTML = '<span style="color:var(--ok)">✓ à jour</span> ('+r.local+')';
    }
  } finally { btn.disabled = false; btn.textContent = '↥ vérifier les maj'; }
}

async function lcInstall(){
  if(!await askConfirm('Cloner llama.cpp puis compiler llama-server pour cette machine (détection GPU automatique). La compilation peut prendre de longues minutes (surtout en CUDA).', {title:'Installer llama.cpp ?', okText:'Installer'})) return;
  const r = await jpost('/api/llamacpp/install', {});
  if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
  lcStartPolling();
}

async function lcPrebuilt(){
  const inUse = lcState && lcState.prebuilt && lcState.prebuilt.in_use;
  const msg = inUse
    ? 'Télécharger la dernière release officielle précompilée de llama.cpp (~2 min, aucune compilation). Le service sera arrêté pendant le remplacement puis redémarré.'
    : 'Télécharger les binaires OFFICIELS précompilés de llama.cpp au lieu de compiler (~2 min). Le variant est choisi automatiquement (CUDA / Vulkan / Metal / CPU) et BIN pointera dessus. Le build local, s\'il existe, reste sur le disque — tu peux y revenir via l\'éditeur de preset. Note : builds génériques (pas de tuning natif) ; sous Linux il n\'existe pas de build CUDA officiel (variant Vulkan).';
  if(!await askConfirm(msg, {title: inUse ? 'Mettre à jour les binaires précompilés ?' : 'Passer aux binaires précompilés ?', okText: inUse ? 'Mettre à jour' : 'Télécharger'})) return;
  const r = await jpost('/api/llamacpp/prebuilt', {});
  if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
  lcStartPolling();
}

async function lcUpdate(clean){
  const msg = clean
    ? 'Recompiler llama.cpp from scratch (build/ supprimé). Le service sera arrêté pendant le build puis redémarré.'
    : 'Mettre à jour llama.cpp (git pull) et recompiler. Le service sera arrêté pendant le build puis redémarré.';
  if(!await askConfirm(msg, {title: clean ? 'Recompiler llama.cpp ?' : 'Mettre à jour llama.cpp ?', okText: clean ? 'Recompiler' : 'Mettre à jour'})) return;
  const r = await jpost('/api/llamacpp/update', {clean: !!clean});
  if(!r.ok){ toast('erreur : '+(r.error||'')); return; }
  lcStartPolling();
}

function lcSetButtons(disabled){
  ['lc-install','lc-check','lc-update','lc-rebuild','lc-prebuilt'].forEach(id=>{
    const b=document.getElementById(id); if(b) b.disabled = disabled;
  });
}

function lcStartPolling(){
  const jobBox = document.getElementById('lc-job');
  jobBox.style.display = '';
  document.getElementById('lc-log').textContent = '';
  lcLogNext = 0;
  lcSetButtons(true);
  lcBadge('<span class="tag" style="color:var(--accent);border-color:var(--accent)"><span class="lc-spin">⏳</span> build</span>');
  if(lcPoll) clearInterval(lcPoll);
  lcPoll = setInterval(lcPollJob, 1000);
  lcPollJob();
}

async function lcPollJob(){
  let j;
  try{ j = await jget('/api/llamacpp/job?from='+lcLogNext); }catch(_){ return; }
  if(!j.exists) return;
  const phaseEl = document.getElementById('lc-job-phase');
  // Logs incrémentaux (offset absolu — le serveur tronque le début si besoin).
  if(j.lines && j.lines.length){
    const pre = document.getElementById('lc-log');
    const stick = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 20;
    pre.textContent += j.lines.join('\n') + '\n';
    if(stick) pre.scrollTop = pre.scrollHeight;
  }
  if(typeof j.next === 'number') lcLogNext = j.next;
  if(j.running){
    phaseEl.innerHTML = '<span class="lc-spin">⏳</span> <span>'+String(j.phase||'…').replace(/[<>&]/g,'')+'</span>';
    return;
  }
  // Terminé.
  clearInterval(lcPoll); lcPoll = null;
  lcSetButtons(false);
  if(j.error){
    phaseEl.innerHTML = '<span style="color:var(--err)">✗ '+String(j.error).replace(/[<>&]/g,'')+'</span>';
    // Ouvre les logs pour que l'erreur réelle soit visible tout de suite.
    const pre = document.getElementById('lc-log');
    if(pre.hasAttribute('hidden')) lcToggleLog();
    pre.scrollTop = pre.scrollHeight;
    toast('build llama.cpp en échec');
  } else {
    phaseEl.innerHTML = '<span style="color:var(--ok)">✓ '+String(j.phase||'terminé').replace(/[<>&]/g,'')+'</span>';
    toast('llama.cpp : '+(j.phase||'terminé'));
  }
  loadAll(); // rafraîchit tout (config, statut service… et ce panneau)
}

function lcToggleLog(){
  const pre = document.getElementById('lc-log');
  const bar = document.querySelector('.lc-logbar');
  if(pre.hasAttribute('hidden')){ pre.removeAttribute('hidden'); bar.classList.add('open'); pre.scrollTop = pre.scrollHeight; }
  else { pre.setAttribute('hidden',''); bar.classList.remove('open'); }
}
