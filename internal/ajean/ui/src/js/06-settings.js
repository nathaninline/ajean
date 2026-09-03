// --- Réserve de hauteur des blocs peuplés par le réseau ---------------------
// Ces blocs sont vides (ou réduits à « … ») tant que le serveur n'a pas répondu,
// puis prennent leur vraie taille : tout le menu sautait alors d'un cran, ce qui
// se voyait surtout sur les sections du bas (« Moteur »). On mémorise leur
// hauteur d'une session à l'autre et on la réserve au démarrage ; la réserve
// tombe dès que le vrai contenu est là (elle ne fait que TENIR la place, elle
// n'affiche jamais rien de faux).
const HRESERVE=['cfg','vram','ram-details','presets','internet-status','mcp-list'];
function reserveHeights(){
  HRESERVE.forEach(id=>{
    try{
      const h=parseInt(localStorage.getItem('ajean-h-'+id)||'0',10);
      const el=document.getElementById(id);
      if(el && h>0) el.style.minHeight=h+'px';
    }catch(e){}
  });
}
function releaseHeights(){
  HRESERVE.forEach(id=>{
    const el=document.getElementById(id); if(!el) return;
    el.style.minHeight='';
    try{ localStorage.setItem('ajean-h-'+id, String(el.offsetHeight||0)); }catch(e){}
  });
}
document.addEventListener('DOMContentLoaded', reserveHeights);
// Numéro (1-based) du preset vers lequel on bascule, tant que le serveur n'a pas
// fini de redémarrer le service. Basculer prend plusieurs secondes : sans ça, la
// liste restait FIGÉE sur l'ancien actif et on ne savait pas si le clic avait pris.
// Voir switchTo() dans 07-models.js.
let pendingPreset = 0;
// Dernière sélection PEINTE. La liste est redessinée à chaque rafraîchissement
// (et il y en a beaucoup : sondage de bascule, loadAll…) ; sans ce repère, les
// animations d'entrée (barre qui glisse, puce qui apparaît) repartaient à chaque
// redessin et la puce semblait clignoter en boucle. On ne les rejoue que quand la
// sélection CHANGE vraiment.
let paintedSel = '';
async function loadPresets(){
  const p=await jget('/api/presets');
  // Bascule terminée : le preset visé est devenu l'actif, on éteint l'attente.
  if(pendingPreset && p[pendingPreset-1] && p[pendingPreset-1].active) pendingPreset = 0;
  const act = p.find(x=>x.active);
  // Build via DOM (not string concat) so preset names can contain anything —
  // spaces, accents, quotes, < > & — without breaking markup or handlers.
  const sp = document.getElementById('status-preset');
  sp.textContent = act ? act.name : '';
  sp.title = act ? t('settings.presets.active_title') : '';
  const cont=document.getElementById('presets');
  cont.innerHTML='';
  if(!p.length){ cont.innerHTML='<span class="muted">'+t('settings.presets.none')+'</span>'; return; }
  // Signature de la sélection : quel preset est actif, et vers lequel on bascule.
  const sel = (act?act.id:'')+'|'+pendingPreset;
  const moved = sel !== paintedSel; paintedSel = sel;
  p.forEach((x,i)=>{
    const row=document.createElement('div');
    const pend = !x.active && pendingPreset===i+1;
    row.className='preset'+(x.active?' active':'')+(pend?' pending':'')+(moved?' sel-anim':'');
    // Réordonnable à la souris (SortableJS, init plus bas). data-id sert à relire
    // l'ordre après un déplacement.
    row.dataset.id = x.id;
    // Guard : un glissement ne doit pas être pris pour un clic qui bascule le preset.
    row.onclick=()=>{ if(presetJustDragged) return; switchTo(i+1, x.name); };
    const info=document.createElement('div'); info.className='preset-info';
    const nm=document.createElement('div'); nm.className='preset-name';
    // Puce de l'actif : un ÉLÉMENT rond en CSS, pas le caractère « ● ». Le glyphe
    // est dessiné par la police du système — sur Windows il sortait plus petit et
    // plus bas que sur macOS. Un disque CSS a la même taille et la même assiette
    // partout.
    // Puce réservée à l'actif POUR DE BON : pendant la bascule, seule la barre
    // orange parle ; la puce apparaît quand la barre passe au blanc.
    if(x.active){ const d=document.createElement('i'); d.className='preset-dot'; nm.appendChild(d); }
    nm.appendChild(document.createTextNode(x.name)); nm.title=x.name;
    // Second row: quant tag + bench perf, so the title row stays full-width.
    const meta=document.createElement('div'); meta.className='preset-meta';
    if(x.quant){
      const q=document.createElement('span'); q.className='qtag';
      q.textContent=x.quant; q.title=t('settings.presets.quant_title');
      meta.appendChild(q);
    }
    if(x.ctx){
      const c=document.createElement('span'); c.className='ctag';
      c.title=t('settings.presets.context_size_title');
      c.textContent=x.ctx;
      meta.appendChild(c);
    }
    if(x.bench){
      const bt=document.createElement('span'); bt.className='btag';
      bt.title=t('settings.presets.bench_title');
      bt.textContent=x.bench.prefill.toFixed(0)+'-'+x.bench.decode.toFixed(0)+' t/s';
      meta.appendChild(bt);
    }
    // Bulle « capacités » à droite des autres : icônes vision (œil) et raisonnement
    // (ampoule), regroupées dans une seule pastille comme les autres tags.
    const caps=[], capTitle=[];
    if(x.vision){
      caps.push('<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>');
      capTitle.push(t('settings.presets.vision_title'));
    }
    if(x.reasoning){
      caps.push('<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18h6"/><path d="M10 21h4"/><path d="M12 3a6 6 0 0 0-4 10.5c.6.6 1 1.4 1 2.5h6c0-1.1.4-1.9 1-2.5A6 6 0 0 0 12 3z"/></svg>');
      capTitle.push(t('settings.presets.reasoning_title')+(typeof x.reasoning==='string'?' ('+x.reasoning+')':''));
    }
    if(caps.length){
      const cap=document.createElement('span'); cap.className='captag';
      cap.innerHTML=caps.join('');
      cap.title=capTitle.join(' · ');
      meta.appendChild(cap);
    }
    info.appendChild(nm);
    if(meta.children.length) info.appendChild(meta);
    const edit=document.createElement('button');
    // Engrenage (et non plus un crayon) : la silhouette est ronde, donc elle se
    // lit comme centrée dans son coin, là où le crayon penché tirait de travers.
    edit.className='preset-edit'; edit.title=t('settings.presets.edit_title');
    edit.innerHTML='<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 11-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 11-2.83-2.83l.06-.06a1.65 1.65 0 00.33-1.82 1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 112.83-2.83l.06.06a1.65 1.65 0 001.82.33H9a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 112.83 2.83l-.06.06a1.65 1.65 0 00-.33 1.82V9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"/></svg>';
    edit.onclick=(e)=>{ e.stopPropagation(); openPreset(x.id); };
    // Pas de poignée : la ligne entière est déplaçable, on attrape où on veut.
    row.appendChild(info); row.appendChild(edit);
    cont.appendChild(row);
  });
  initPresetSortable(cont);
}
// ─── Réordonnancement des presets (SortableJS) ───────────────────────────────
// Glissement fluide (animation de poussée des voisins), ordre persisté côté
// serveur (/api/presets/order). SortableJS est vendorisé dans l'UI (00-sortable).
let presetJustDragged = false;
let presetSortable = null;
function initPresetSortable(cont){
  if(typeof Sortable==='undefined') return; // lib absente : on garde la liste simple
  // Une seule instance, attachée au conteneur (qui persiste entre les rendus).
  if(presetSortable) return;
  presetSortable = Sortable.create(cont, {
    animation: 160,
    easing: 'cubic-bezier(.2,.7,.3,1)',
    // forceFallback : Sortable gère lui-même le clone qui suit le curseur, au lieu
    // de l'image native du navigateur (rendue semi-transparente, non stylable).
    // Le clone (.preset-drag) reste donc PLEIN, comme la ligne d'origine.
    forceFallback: true,
    fallbackTolerance: 3,
    // Il faut MAINTENIR l'appui avant que le glissement démarre : sinon, sur mobile,
    // un simple défilement attrapait un preset. Le délai ne s'applique qu'au toucher
    // (souris immédiate), et un petit mouvement pendant le délai est toléré.
    delay: 220,
    delayOnTouchOnly: true,
    touchStartThreshold: 6,
    ghostClass: 'preset-ghost',   // l'emplacement cible laissé dans la liste
    chosenClass: 'preset-chosen', // l'élément saisi
    dragClass: 'preset-drag',     // le clone plein qui suit le curseur
    onStart(){ presetJustDragged = true; },
    onEnd(){
      // Un léger délai pour que le clic de fin de drag ne bascule pas le preset.
      setTimeout(()=>{ presetJustDragged = false; }, 60);
      const ids=[...cont.children].map(r=>r.dataset.id).filter(Boolean);
      jpost('/api/presets/order', {ids}).catch(()=>{});
    },
  });
}
// « Mode agent » = accès machine + skills réunis en un seul interrupteur.
// Quand il est actif, un « a » blanc apparaît en fondu devant « ajean » → « ajean ».
async function loadAgent(){
  const s=await jget('/api/agent');
  const on = s.enabled;
  document.getElementById('agent-toggle').checked = on;
  document.getElementById('compact-toggle').checked = (s.compact !== false);
  document.getElementById('machines-toggle').checked = !!s.machines;
  setBadge('agent-badge', on, on?t('settings.agent.badge_on'):t('settings.agent.badge_off'));
  document.getElementById('brand').classList.toggle('agent', on);
  setAgentGate(on);
  if(s.mem_mode){ document.getElementById('mem-mode').value = s.mem_mode; renderMemModeDesc(s.mem_mode); }
  memPages = (s.pages || s.skills || []).slice().sort((a,b)=>a.name.localeCompare(b.name));
  memShown = MEM_PAGE;
  document.getElementById('mem-count').textContent = memPages.length ? '('+memPages.length+')' : '';
  // Barre de recherche visible seulement si beaucoup de pages. Si elle est masquée, on
  // VIDE aussi son champ : sinon une requête tapée dans un projet fourni restait active
  // (invisible) après bascule vers un projet à peu de notes, filtrant tout → « aucun
  // résultat » sans que rien n'indique qu'un filtre est appliqué.
  const showMemSearch = memPages.length > MEM_PAGE;
  document.getElementById('mem-search-row').style.display = showMemSearch ? '' : 'none';
  if(!showMemSearch){ const ms=document.getElementById('mem-search'); if(ms) ms.value=''; }
  renderMemList();
  loadMemEnc();
}

// --- Chiffrement (mémoire + conversations) ----------------------------------
let memUnlockDone=false; // tentative de déverrouillage déjà faite ce chargement
async function loadMemEnc(){
  let h; try{ h=await jget('/api/mem/health'); }catch(e){ return; }
  const tog=document.getElementById('mem-enc-toggle'); if(!tog) return;
  // La case n'est cochée QUE si TOUT est réellement chiffré (h.fully). Un
  // chiffrement partiel/raté ou verrouillé-incomplet la laisse décochée : si elle
  // est cochée, tu es sûr que c'est vraiment chiffré.
  tog.checked=!!h.fully;
  // Aucun concept « verrouillé/déverrouillé » affiché : avoir accès à l'interface
  // = clé d'accès fournie = mémoire ouverte. Le toggle seul dit si c'est chiffré.
  // Résilience : si le drapeau MEM_ENCRYPTED a disparu (ex. effacé par un bug)
  // mais qu'un coffre existe encore (vault_copies>0), on propose QUAND MÊME le
  // déverrouillage au lieu de laisser la mémoire muette sans invite. Sinon l'IA
  // se retrouve sans mémoire et rien ne demande la clé (incident du 2026-08-25).
  const flagLost = !h.encrypted && (h.vault_copies|0) > 0;
  if(((h.encrypted && h.locked) || flagLost) && !memUnlockDone){
    memUnlockDone=true;
    // La clé de chiffrement = ta clé d'API (localStorage 'ajean.key'), qui ne vit
    // que côté client. Le serveur ne la détient jamais (juste son empreinte). Même
    // logique en local et sur ajean.link.
    const apiKey=localStorage.getItem('ajean.key')||'';
    if(apiKey){ const r=await jpost('/api/mem/unlock',{secret:apiKey}); if(r.ok){ loadMem(); return; } }
    // Migration d'un ancien coffre (créé avec un mot de passe distinct) : on l'ouvre
    // avec l'ancien secret retenu, puis on ajoute la clé d'API pour la suite.
    const legacy=localStorage.getItem('ajean.enckey')||'';
    if(legacy && legacy!==apiKey){
      const r=await jpost('/api/mem/unlock',{secret:legacy});
      if(r.ok){ if(apiKey) await jpost('/api/mem/addkey',{secret:apiKey}); localStorage.removeItem('ajean.enckey'); loadMem(); return; }
    }
    // Boucle : une clé fausse redemande tout de suite (plus besoin de rafraîchir).
    while(true){
      const s=await askPrompt(t('settings.memory.unlock_prompt'),{title:t('settings.memory.unlock_title'),placeholder:t('settings.memory.unlock_placeholder')});
      if(!s){ memUnlockDone=false; break; } // annulé : nouvelle tentative au prochain chargement
      const r=await jpost('/api/mem/unlock',{secret:s});
      if(r.ok){ if(apiKey && s!==apiKey) await jpost('/api/mem/addkey',{secret:apiKey}); loadMem(); return; }
      await askAlert(t('settings.wrong_key'),{title:t('settings.refused_title')});
    }
  }
  loadBackup();
}
async function toggleMemEncrypt(){
  const tog=document.getElementById('mem-enc-toggle');
  if(tog.checked){
    // Cas « déjà chiffré mais pas complet » (ex. conversations à migrer, ou
    // verrouillé) : on ne relance PAS un chiffrement, on complète en déverrouillant.
    let cur; try{ cur=await jget('/api/mem/health'); }catch(e){}
    if(cur && cur.encrypted){ memUnlockDone=false; await loadMemEnc(); return; }
    // La clé d'API (clé de pilotage) EST la clé de chiffrement. Elle ne vit que
    // sur cet appareil ; le serveur n'en a que l'empreinte → il ne peut pas ouvrir
    // le coffre seul. Une seule clé pour l'accès ET le chiffrement.
    const apiKey=localStorage.getItem('ajean.key')||'';
    if(!apiKey){ await askAlert(t('settings.memory.api_key_required_msg'),{title:t('settings.memory.api_key_required_title')}); tog.checked=false; return; }
    const ok=await askConfirm(t('settings.memory.encrypt_confirm_msg'),{title:t('settings.memory.encrypt_confirm_title'),okText:t('settings.continue_ok')});
    if(!ok){ tog.checked=false; return; }
    let r; try{ r=await jpost('/api/mem/encrypt',{password:apiKey}); }catch(e){ await askAlert(t('common.failed_prefix')+e); tog.checked=false; return; }
    if(!r.ok){ await askAlert(t('common.failed_prefix')+(r.error||t('common.unknown'))); tog.checked=false; loadMemEnc(); return; }
    memUnlockDone=true;
    await askAlert(t('settings.memory.recovery_key_msg_before')+r.recovery+t('settings.memory.recovery_key_msg_after'),{title:t('settings.memory.recovery_key_title')});
    toast(t('settings.encryption_on')); loadMem();
  } else {
    const ok=await askConfirm(t('settings.memory.decrypt_confirm_msg'),{title:t('settings.memory.decrypt_confirm_title'),okText:t('settings.memory.decrypt_ok_text'),danger:true});
    if(!ok){ tog.checked=true; return; }
    let r; try{ r=await jpost('/api/mem/decrypt',{}); }catch(e){ await askAlert(t('common.failed_prefix')+e); tog.checked=true; return; }
    if(!r.ok){ await askAlert(t('common.failed_prefix')+(r.error||t('common.unknown'))); tog.checked=true; loadMemEnc(); return; }
    localStorage.removeItem('ajean.enckey');
    toast(t('settings.encryption_off')); loadMem();
  }
}
// --- Sauvegarde ajean.link ---------------------------------------------------
async function loadBackup(){
  const block=document.getElementById('backup-block'); if(!block) return;
  let s; try{ s=await jget('/api/backup/status'); }catch(e){ block.style.display='none'; return; }
  if(!s.linked){ block.style.display='none'; return; } // réservé aux serveurs liés à ajean.link
  block.style.display='';
  document.getElementById('backup-auto-toggle').checked=!!s.auto;
  setBadge('backup-badge', !!s.auto, s.auto?t('settings.backup.auto_badge'):t('settings.backup.manual_badge'));
  const st=document.getElementById('backup-status');
  let msg='';
  if(s.last){ const d=new Date(s.last); msg=t('settings.backup.last_backup_prefix')+(isNaN(d)?s.last:d.toLocaleString()); }
  else msg=t('settings.backup.none_yet');
  if(Array.isArray(s.versions)) msg+=' · '+s.versions.length+' '+t('settings.backup.versions_on_relay');
  if(s.error) msg+='\n⚠️ '+s.error;
  st.textContent=msg; st.style.whiteSpace='pre-line';
}
async function toggleBackupAuto(){
  const on=document.getElementById('backup-auto-toggle').checked;
  await jpost('/api/backup/auto',{on});
  if(on) await askAlert(t('settings.backup.auto_enabled_msg'),{title:t('settings.backup.auto_enabled_title')});
  loadBackup();
}
async function backupNow(){
  // Aucun mot de passe : le paquet est chiffré avec la clé de ta mémoire (déjà
  // ouverte). La restauration se fera avec ta clé d'API.
  toast(t('settings.backup_in_progress'));
  let r; try{ r=await jpost('/api/backup/now',{}); }catch(e){ await askAlert(t('common.failed_prefix')+e); return; }
  if(!r.ok){ await askAlert(t('common.failed_prefix')+(r.error||t('common.unknown'))); return; }
  toast(t('settings.backup.sent')); loadBackup();
}
async function backupRestore(){
  let s; try{ s=await jget('/api/backup/status'); }catch(e){ await askAlert(t('settings.backup.relay_unreachable')); return; }
  const vers=(s.versions||[]);
  if(!vers.length){ await askAlert(t('settings.backup.none_available')); return; }
  const latest=vers[0];
  const when=latest.when? new Date(latest.when).toLocaleString():latest.id;
  if(!await askConfirm(t('settings.backup.restore_confirm_before')+when+t('settings.backup.restore_confirm_after'),{title:t('settings.backup.restore_title'),okText:t('settings.backup.restore_title'),danger:true})) return;
  // Boucle : une clé fausse redemande tout de suite.
  while(true){
    const secret=await askPrompt(t('settings.backup.restore_secret_prompt'),{title:t('settings.backup.restore_title'),placeholder:t('settings.backup.restore_secret_placeholder')});
    if(!secret) return; // annulé
    toast(t('settings.backup.restoring'));
    let r; try{ r=await jpost('/api/backup/restore',{id:latest.id,secret}); }catch(e){ await askAlert(t('common.failed_prefix')+e); return; }
    if(r.ok){ await askAlert(t('settings.backup.restore_done_msg'),{title:t('settings.backup.restore_done_title')}); loadMem(); return; }
    await askAlert((r.error&&r.error.indexOf('incorrecte')>=0)?t('settings.wrong_key'):(t('common.failed_prefix')+(r.error||t('common.unknown'))),{title:t('settings.refused_title')});
  }
}
// Mémoire + accès internet sont des sous-réglages du mode agent : sans agent, ni
// les outils mem_* ni les outils web ne sont fournis (voir globalCaps côté Go). On
// grise donc ces blocs quand l'agent est off pour que l'UI ne mente pas.
function setAgentGate(on){
  ['mem-block','net-block','mcp-block'].forEach(id=>{
    const el=document.getElementById(id); if(el) el.classList.toggle('gated', !on);
  });
  // « Paramètres » n'a plus de wrapper gaté (la notification y cohabite mais reste
  // toujours active) : on grise les lignes agent UNE PAR UNE via .agent-gated.
  document.querySelectorAll('.agent-gated').forEach(el=>el.classList.toggle('gated', !on));
}
// Repli/dépli de la liste des pages mémoire (fermée par défaut → gagne de la place).
function toggleMemPages(){
  const body=document.getElementById('mem-pages-body');
  const bar=document.getElementById('mem-pages-bar');
  const open=body.hasAttribute('hidden');
  if(open){ body.removeAttribute('hidden'); bar.classList.add('open'); }
  else { body.setAttribute('hidden',''); bar.classList.remove('open'); }
}
// Liste mémoire scalable : recherche + rendu plafonné (les milliers de pages ne
// déroulent plus une barre géante). memShown grimpe par paliers via « voir plus ».
let memPages=[], memShown=0; const MEM_PAGE=50;
function renderMemList(){
  const q=(document.getElementById('mem-search').value||'').trim().toLowerCase();
  const list=document.getElementById('mem-list');
  list.textContent='';
  if(!memPages.length){ list.innerHTML='<div class="muted">'+t('settings.memory.no_pages')+'</div>'; return; }
  const matches = q ? memPages.filter(x=>(x.name+' '+(x.desc||'')).toLowerCase().includes(q)) : memPages;
  if(!matches.length){ list.innerHTML='<div class="muted">'+t('settings.memory.no_results_before')+q.replace(/[<>&]/g,'')+t('settings.memory.no_results_after')+'</div>'; return; }
  const shown = matches.slice(0, memShown);
  shown.forEach(x=>{
    const row=document.createElement('div'); row.className='preset'; row.style.fontSize='12px';
    row.onclick=()=>openMem(x.name);
    const span=document.createElement('span');
    const b=document.createElement('b'); b.style.color='var(--text)'; b.textContent=x.name; span.appendChild(b);
    if(x.desc){ const d=document.createElement('span'); d.className='muted'; d.textContent=' — '+x.desc; span.appendChild(d); }
    const btn=document.createElement('button'); btn.textContent=t('settings.memory.edit_btn'); btn.style.cssText='margin:0;padding:2px 8px;font-size:11px';
    btn.onclick=e=>{ e.stopPropagation(); openMem(x.name); };
    row.appendChild(span); row.appendChild(btn);
    // Déplacer la note vers un autre projet (issue #55) — visible s'il existe au
    // moins un autre projet (PROJECTS vient de 18-projects.js).
    if(typeof PROJECTS!=='undefined' && PROJECTS.length>1){
      const mv=document.createElement('button'); mv.textContent=t('settings.memory.move_btn'); mv.style.cssText='margin:0 0 0 4px;padding:2px 8px;font-size:11px';
      mv.onclick=e=>{ e.stopPropagation(); moveMemUI(x.name, mv); };
      row.appendChild(mv);
    }
    list.appendChild(row);
  });
  if(matches.length > shown.length){
    const more=document.createElement('div'); more.className='mem-more';
    more.textContent=t('settings.memory.show_more_before')+(matches.length-shown.length)+t('settings.memory.show_more_after');
    more.style.cursor='pointer';
    more.onclick=()=>{ memShown+=MEM_PAGE; renderMemList(); };
    list.appendChild(more);
  }
}
// Déplacer une note mémoire du projet actif vers un autre projet (issue #55).
// Réutilise le sélecteur de projet du hub (pickProjectPop, 18-projects.js).
async function moveMemUI(name, anchor){
  if(typeof pickProjectPop!=='function'){ toast(t('settings.memory.move_unavailable')); return; }
  pickProjectPop(anchor, (typeof ACTIVE_PROJECT!=='undefined'?ACTIVE_PROJECT:''), async(slug)=>{
    let r; try{ r = await jpost('/api/projects/move-mem', {name, slug}); }catch(_){ toast(t('settings.memory.network_error')); return; }
    if(!r.ok){ toast(r.error || t('settings.memory.move_failed')); return; }
    toast(t('settings.memory.note_moved'));
    loadAgent();
  });
}
// alias : plusieurs appelants rafraîchissent juste la liste des pages mémoire
const loadMem = loadAgent;
async function toggleAgent(){
  const on=document.getElementById('agent-toggle').checked;
  if(on && !await askConfirm(t('settings.agent.enable_confirm_msg'), {title:t('settings.agent.enable_confirm_title'), okText:t('settings.agent.enable_ok'), danger:true})){ document.getElementById('agent-toggle').checked=false; return; }
  await jpost('/api/agent/toggle',{on});
  loadAgent();
}
async function toggleCompact(){
  const on=document.getElementById('compact-toggle').checked;
  await jpost('/api/agent/compact',{on});
}
// Gestion autonome des machines (postes) : donne à l'IA les outils machines_*
// et le briefing. Off par défaut, aucun effet quand décoché.
async function toggleMachines(){
  const on=document.getElementById('machines-toggle').checked;
  await jpost('/api/agent/machines',{on});
}
// Mode mémoire (3 états) — indépendant du mode agent.
const MEM_DESC={
  always:t('settings.memory.mode_always_desc'),
  ondemand:t('settings.memory.mode_ondemand_desc'),
  off:t('settings.memory.mode_off_desc')
};
function renderMemModeDesc(m){ const d=document.getElementById('mem-mode-desc'); if(d) d.textContent=MEM_DESC[m]||''; }
async function setMemMode(){
  const mode=document.getElementById('mem-mode').value;
  const r=await jpost('/api/memory',{mode});
  renderMemModeDesc(r.mode||mode);
}
// Accès internet : serveur Crawl4AI + drapeau. Actif ET fonctionnel = pastille verte.
let internetOn=false, webEngine='go';
function renderInternet(s){
  internetOn = !!s.enabled;
  webEngine = s.engine || 'go';
  document.getElementById('internet-toggle').checked = internetOn;
  const sel=document.getElementById('web-engine');
  if(sel && document.activeElement!==sel) sel.value = webEngine;
  // Les réglages Crawl4AI n'ont aucun sens avec le moteur intégré.
  const cf=document.getElementById('crawl-fields');
  if(cf) cf.style.display = (webEngine==='crawl4ai') ? '' : 'none';
  if(document.activeElement !== document.getElementById('crawl-url'))
    document.getElementById('crawl-url').value = s.url || '';
  // La pastille ne redit PAS ce que l'interrupteur montre déjà (actif/inactif) :
  // elle ne sert qu'à signaler l'anomalie — actif mais serveur injoignable, cas
  // où les outils web ne sont pas proposés au modèle.
  if(internetOn && !s.reachable) setBadge('internet-badge','warn',t('settings.internet.unreachable_badge'));
  else setBadge('internet-badge', null);
  // Clé du serveur Crawl4AI : le champ reste vide (la clé n'est jamais renvoyée),
  // on indique seulement si une clé est enregistrée.
  const ks=document.getElementById('crawl-key-state'), kc=document.getElementById('crawl-key-clear');
  if(ks){
    ks.textContent = s.key_set ? (t('settings.internet.key_registered_prefix')+(s.key_hint||'')) : t('settings.internet.no_key_open_server');
    if(kc) kc.style.display = s.key_set ? '' : 'none';
    const ki=document.getElementById('crawl-key');
    if(ki && document.activeElement!==ki) ki.value='';
    if(ki) ki.placeholder = s.key_set ? t('settings.internet.key_placeholder_replace') : t('settings.internet.key_placeholder_new');
  }
  const st=document.getElementById('internet-status');
  if(webEngine!=='crawl4ai'){ st.innerHTML='<span style="color:var(--accent)">✓</span> '+t('settings.internet.builtin_engine_status'); }
  else if(!s.url){ st.textContent=t('settings.internet.server_not_configured'); st.style.color=''; }
  else if(s.reachable){ st.innerHTML='<span style="color:var(--accent)">✓</span> '+t('settings.internet.server_reachable'); }
  else { st.innerHTML='⚠ '+t('settings.internet.server_unreachable'); }
}
async function loadInternet(){ renderInternet(await jget('/api/internet')); }
// --- Accès OpenAI (endpoint /v1 + clé API des complétions) -----------------
let OAI_KEY='', OAI_REVEAL=false;
async function copyText(txt, msg){
  if(!txt){ toast(t('settings.copy.nothing')); return; }
  try{ await navigator.clipboard.writeText(txt); }
  catch(_){ const ta=document.createElement('textarea'); ta.value=txt; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); ta.remove(); }
  toast(msg||t('settings.copy.copied'));
}
function copyApiKey(){ copyText(OAI_KEY, OAI_KEY?t('settings.apikey.copied'):t('settings.apikey.none')); }
function renderApiKey(d){
  OAI_KEY = d.key || '';
  // URL de l'endpoint : llama-server tourne sur d.port (≠ port de l'UI web).
  // On prend l'hôte annoncé par le serveur (IP LAN détectée côté Go) : dans le
  // tunnel ajean.link, location.hostname serait le domaine du relais (faux) —
  // l'accès OpenAI reste TOUJOURS l'adresse locale de la machine.
  const host = d.host || location.hostname;
  document.getElementById('oai-url').value = 'http://'+host+':'+d.port+'/v1';
  // Endpoint PUBLIC (ajean.link) : affiché seulement si l'accès public est activé.
  const tg = document.getElementById('oai-public-toggle');
  if(tg) tg.checked = !!d.oai_public;
  const pubWrap = document.getElementById('oai-public-wrap');
  if(d.oai_public && d.machine){
    document.getElementById('oai-public-url').value = 'https://'+d.machine+'.oai.ajean.link/v1';
    pubWrap.style.display = '';
  } else {
    pubWrap.style.display = 'none';
  }
  const inp=document.getElementById('oai-key');
  if(!d.set){ inp.value=t('settings.apikey.none_open_server'); inp.style.opacity=.6; }
  else { inp.style.opacity=1; inp.value = OAI_REVEAL ? OAI_KEY : d.masked; }
  document.getElementById('oai-key-eye').style.display = d.set ? '' : 'none';
}
async function loadApiKey(){ renderApiKey(await jget('/api/apikey')); }
// --- Export de la conversation ---------------------------------------------
// xTurnsTotal : nombre d'échanges du fil, borne haute du curseur. Vient de
// /api/chat/state ; 0 = conversation vide, et la fenêtre n'a alors rien à régler.
let xTurnsTotal = 0;
async function openExportModal(){
  showModal('export-modal');
  try{
    const st = await jget('/api/chat/state');
    xTurnsTotal = (st && st.turns) || 0;
  }catch(e){ xTurnsTotal = 0; }
  // Rien à exporter : on remplace les réglages par un message, plutôt que de
  // proposer de tailler un fichier qui serait vide de toute façon.
  const vide = xTurnsTotal === 0;
  document.getElementById('x-empty').style.display = vide ? '' : 'none';
  document.getElementById('x-body').style.display = vide ? 'none' : '';
  document.getElementById('x-go').style.display = vide ? 'none' : '';
  if(vide) return;
  const el = document.getElementById('x-turns');
  el.max = String(xTurnsTotal);
  el.value = el.max;               // par défaut : tout le fil
  el.disabled = xTurnsTotal < 2;   // un seul échange : rien à trancher
  onExportFormat();
}
function closeExportModal(){ hideModal('export-modal'); }
// Le format ne change QUE le contenant : les options de contenu sont les mêmes
// des deux côtés. Il n'y a donc plus rien à montrer ou cacher ici, seulement le
// résumé à rafraîchir.
function onExportFormat(){ onExportPreview(); }
function exportFormat(){
  const r = document.querySelector('input[name=x-fmt]:checked');
  return r ? r.value : 'md';
}
// Construit la requête d'export à partir des cases. On n'envoie QUE ce qui
// s'écarte du défaut : l'URL reste lisible, et un export complet redevient le
// simple /api/chat/export.
function exportQuery(){
  const p = new URLSearchParams();
  if(exportFormat() === 'json') p.set('format','json');
  const on = id => document.getElementById(id).checked;
  if(!on('x-reasoning')) p.set('reasoning','0');
  if(!on('x-tools')) p.set('tools','0');
  if(!on('x-results')) p.set('results','0');
  const t = exportTurns();
  if(t > 0) p.set('turns', String(t));
  const q = p.toString();
  return '/api/chat/export' + (q ? '?'+q : '');
}
// Valeur de portée à envoyer : 0 = tout le fil. La butée DROITE du curseur vaut
// « toute la conversation », donc on n'envoie rien plutôt qu'un nombre qui se
// périmerait à l'échange suivant.
function exportTurns(){
  const v = parseInt(document.getElementById('x-turns').value, 10) || 0;
  if(!xTurnsTotal || v >= xTurnsTotal) return 0;
  return v;
}
function onExportPreview(){
  // Sortie d'outil sans bulle d'outil n'a aucun sens : la case suit.
  const tools = document.getElementById('x-tools');
  const results = document.getElementById('x-results');
  results.disabled = !tools.checked;
  if(!tools.checked) results.checked = false;
  paintExportRange();
  const off = [];
  if(!document.getElementById('x-reasoning').checked) off.push(t('settings.export.reasonings'));
  if(!tools.checked) off.push(t('settings.export.tools'));
  else if(!results.checked) off.push(t('settings.export.tool_outputs'));
  document.getElementById('x-note').textContent = off.length
    ? t('settings.export.without_prefix') + off.join(' '+t('settings.export.nor')+' ') + '.'
    : '';
}
// Remplit la piste du curseur et son libellé. Purement local : appelé à chaque
// pixel de glissement, il ne doit RIEN demander au serveur.
function paintExportRange(){
  const el = document.getElementById('x-turns');
  const n = parseInt(el.value, 10) || 0;
  const max = Math.max(1, xTurnsTotal);
  // La pastille ne parcourt pas toute la largeur : elle va de THUMB/2 à
  // largeur - THUMB/2. On coupe le remplissage dans SON repère, sinon la piste
  // colorée déborde à côté d'elle aux extrémités (même calcul que la barre GPU).
  const frac = max > 1 ? (n - 1) / (max - 1) : 1;
  const fill = document.getElementById('x-fill');
  if(fill) fill.style.width = 'calc(' + (frac*100).toFixed(2) + '% - ' + ((frac - .5) * GPU_THUMB).toFixed(2) + 'px)';
  const scope = document.getElementById('x-scope');
  if(!scope) return;
  scope.textContent = n >= xTurnsTotal
    ? t('settings.export.whole_conversation') + xTurnsTotal + t('settings.export.exchange_unit') + (xTurnsTotal>1?'s':'') + ')'
    : n + t('settings.export.last_word') + (n>1?'s':'') + t('settings.export.exchange_unit') + (n>1?'s':'') + t('settings.export.out_of') + xTurnsTotal;
}
async function runExport(){
  const btn = document.getElementById('x-go');
  btn.disabled = true;
  try{ await downloadExport(exportQuery()); closeExportModal(); }
  finally{ btn.disabled = false; }
}
// On passe par jfetch (et pas par un simple <a href>) pour deux raisons : la clé
// de pilotage voyage dans un en-tête Authorization, qu'un lien ne porterait pas,
// et le chemin de base change derrière le tunnel (/u/<id>).
async function downloadExport(url){
  toast(t('settings.export.preparing'));
  try{
    const r = await jfetch(url);
    if(!r.ok){ toast(t('settings.error_prefix') + 'HTTP ' + r.status); return; }
    // Derrière app.ajean.link, le proxy chiffré réemballe TOUTE réponse en JSON :
    // un export Markdown revenait donc comme une chaîne JSON entre guillemets,
    // échappements compris. On la déballe. En local, rien à faire — le type de
    // contenu n'est pas du JSON, et un export JSON est déjà à sa place.
    let text = await r.text();
    if((r.headers.get('Content-Type')||'').includes('json')){
      try{ const v = JSON.parse(text); if(typeof v === 'string') text = v; }catch(_){}
    }
    const blob = new Blob([text]);
    // Nom du fichier : celui proposé par le serveur (horodaté), à défaut un nom
    // déduit du format DEMANDÉ — le type renvoyé ne dit plus rien derrière le
    // tunnel, où tout arrive en application/json.
    const cd = r.headers.get('Content-Disposition') || '';
    const m = cd.match(/filename="([^"]+)"/);
    const name = m ? m[1] : 'ajean-conversation.' + (url.includes('format=json') ? 'json' : 'md');
    // ⚠️ blobURL, surtout pas `url` : ce nom est déjà celui du paramètre, et le
    // redéclarer ici mettrait la ligne `jfetch(url)` ci-dessus dans la zone morte
    // du const — l'export échouerait avant même de partir.
    const blobURL = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = blobURL; a.download = name;
    document.body.appendChild(a); a.click(); a.remove();
    // Révocation différée : Safari annule le téléchargement si l'URL disparaît
    // dans la foulée du clic.
    setTimeout(()=>URL.revokeObjectURL(blobURL), 10000);
    toast(t('settings.export.exported_prefix') + name);
  }catch(e){ toast(t('settings.error_prefix') + e.message); }
}
// --- Écoute réseau du moteur (HOST + pare-feu) ------------------------------
// Le retour d'utilisateur qui a motivé ce réglage : « à part le chat dans le
// navigateur, impossible d'utiliser ton URL dans les logiciels en local ». Le
// moteur écoutait sur 127.0.0.1 et rien ne le disait nulle part.
function renderNetwork(st){
  if(!st) return;
  const cb = document.getElementById('lan-toggle');
  if(cb) cb.checked = !!st.exposed;
  const warn = document.getElementById('lan-warn');
  if(!warn) return;
  if(!st.exposed){
    warn.style.display = '';
    warn.innerHTML = '<div class="muted" style="margin:0">'+t('settings.network.local_only_warning')+'</div>';
    return;
  }
  // Exposé mais bloqué par le pare-feu : le cas le plus déroutant (« ça écoute
  // partout » et pourtant rien ne passe). On donne la commande à coller.
  if(st.hint){
    warn.style.display = '';
    // --err et non --warn : la palette est volontairement monochrome et --warn y
    // est un gris, illisible comme alerte. Ici il y a une vraie action à faire.
    warn.innerHTML = '<div style="margin:0;color:var(--err)">⚠ '
      + escHtml(st.hint).replace(/\n/g,'<br>') + '</div>';
    return;
  }
  warn.style.display = 'none';
}
async function loadNetwork(){
  try{ const r = await jget('/api/network'); renderNetwork(r && r.status); }catch(e){}
}
async function toggleLAN(){
  const cb = document.getElementById('lan-toggle');
  const on = cb.checked;
  const r = await jpost('/api/network', {exposed:on});
  if(!r || !r.ok){ cb.checked = !on; toast(t('settings.error_prefix') + ((r&&r.error)||'')); return; }
  renderNetwork(r.status);
  // llama-server ne lit --host qu'au lancement : sans redémarrage, l'interrupteur
  // affiche un état que le moteur en cours ne respecte pas encore.
  if(await askConfirm(t('settings.network.restart_confirm_msg'),
                      {title: on ? t('settings.network.open_title') : t('settings.network.close_title'), okText:t('settings.network.restart_ok')})){
    await act('restart'); // même chemin que les boutons du panneau Moteur
  }
  loadApiKey();
}
function toggleKeyReveal(){ OAI_REVEAL=!OAI_REVEAL; const inp=document.getElementById('oai-key'); if(OAI_KEY) inp.value = OAI_REVEAL ? OAI_KEY : (OAI_KEY.slice(0,8)+'…'+OAI_KEY.slice(-4)); const eye=document.getElementById('oai-key-eye'); if(eye) eye.textContent = OAI_REVEAL ? t('settings.apikey.hide') : t('settings.apikey.show'); }
async function apiKeyAction(action){
  if(action==='clear' && !await askConfirm(t('settings.apikey.remove_confirm_msg'), {title:t('settings.apikey.remove_confirm_title'), okText:t('settings.apikey.remove_ok')})) return;
  if(action==='generate' && OAI_KEY && !await askConfirm(t('settings.apikey.regenerate_confirm_msg'), {title:t('settings.apikey.regenerate_confirm_title'), okText:t('settings.apikey.regenerate_ok')})) return;
  OAI_REVEAL=(action==='generate'); // révèle la clé fraîche pour qu'on puisse la copier
  toast(t('settings.applying'));
  renderApiKey(await jpost('/api/apikey', {action}));
}
async function toggleOAIPublic(){
  const cb = document.getElementById('oai-public-toggle');
  const on = cb.checked;
  // L'accès OpenAI public passe par ajean.link (<machine>.oai.ajean.link) : il exige
  // que l'accès distant soit activé sur ce serveur. Sinon, on annule et on explique.
  if(on){
    let linked = false;
    try{ const s = await jget('/api/link/status'); linked = !!(s && s.linked); }catch(e){}
    if(!linked){
      cb.checked = false;
      await askAlert(t('settings.oai.public_needs_remote_msg'), {title:t('settings.oai.public_needs_remote_title')});
      return;
    }
  }
  await jpost('/api/oai/public', {enabled:on});
  toast(on ? t('settings.oai.public_on') : t('settings.oai.public_off'));
  loadApiKey();
}
async function apiKeySet(){
  const k = await askPrompt(t('settings.apikey.set_prompt_msg'), {title:t('settings.apikey.set_prompt_title'), placeholder:'sk-…'});
  if(!k || !k.trim()) return;
  OAI_REVEAL=true; toast(t('settings.applying'));
  renderApiKey(await jpost('/api/apikey', {action:'set', key:k.trim()}));
}
async function toggleInternet(){
  const on=document.getElementById('internet-toggle').checked;
  const url=document.getElementById('crawl-url').value.trim();
  // Crawl4AI sans URL de serveur = rien à joindre. Le moteur intégré, lui, n'a
  // besoin d'aucun réglage : on active directement.
  if(on && webEngine==='crawl4ai' && !url){ toast(t('settings.internet.need_url')); document.getElementById('internet-toggle').checked=false; return; }
  renderInternet(await jpost('/api/internet',{enabled:on, url}));
}
async function saveWebEngine(){
  const engine=document.getElementById('web-engine').value;
  renderInternet(await jpost('/api/internet',{engine}));
  toast(engine==='crawl4ai' ? t('settings.internet.engine_crawl4ai_selected') : t('settings.internet.engine_builtin_selected'));
}
async function saveCrawlUrl(){
  const url=document.getElementById('crawl-url').value.trim();
  renderInternet(await jpost('/api/internet',{url}));
  toast(url ? t('settings.internet.server_saved') : t('settings.internet.server_removed'));
}
async function saveCrawlKey(){
  const el=document.getElementById('crawl-key'), key=el.value.trim();
  if(!key){ toast(t('settings.internet.key_missing')); return; }
  renderInternet(await jpost('/api/internet',{key}));
  toast(t('settings.internet.key_saved'));
}
async function clearCrawlKey(){
  if(!await askConfirm(t('settings.internet.remove_key_confirm'))) return;
  document.getElementById('crawl-key').value='';
  renderInternet(await jpost('/api/internet',{key:''}));
  toast(t('settings.internet.key_removed'));
}
async function loadAll(){
  // allSettled et pas all : un seul chargement en échec (accès distant coupé,
  // clé API absente…) ne doit pas empêcher la suite — et surtout pas laisser les
  // hauteurs réservées en place pour toujours.
  await Promise.allSettled([loadStatus(),loadVram(),loadRam(),loadCfg(),loadPresets(),loadAgent(),loadInternet(),loadMCP(),loadNode(),loadApiKey(),loadNetwork(),loadPrefs(),loadLlamacpp(),loadRemote(),loadTasks()]);
  releaseHeights(); // tout est en place : on rend la main et on mesure pour la prochaine fois
}
async function act(a){ toast(a+'…'); await jpost('/api/'+a); setTimeout(loadAll,1500); }
