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
// Dernière sélection PEINTE. La liste est redessinée à chaque rafraîchissement
// (et il y en a beaucoup : sondage de bascule, loadAll…) ; sans ce repère, les
// animations d'entrée (barre qui glisse, puce qui apparaît) repartaient à chaque
// redessin et la puce semblait clignoter en boucle. On ne les rejoue que quand la
// sélection CHANGE vraiment.
let paintedSel = '';
// Liste des modèles (refonte 0.19) : même rendu que l'ancienne liste des
// presets, une ligne par service, dans l'ordre choisi par l'utilisateur.
async function loadPresets(){ if(typeof loadServices === 'function') await loadServices(); }
function svcCtxLabel(n){
  if(!n) return '';
  if(n >= 1e6) return (Math.round(n/1e5)/10).toString().replace(/\.0$/, '') + 'M';
  return Math.round(n/1000) + 'K';
}
function paintPresets(){
  const p = (typeof SERVICES !== 'undefined' ? SERVICES : []).map(sv => ({
    id: sv.id, svc: sv, name: sv.name, active: sv.chat, quant: sv.quant || '',
    ctx: svcCtxLabel(sv.context), bench: sv.bench, vision: sv.vision,
    reasoning: sv.reasoning || '', external: sv.state === 'remote', model: sv.state === 'remote' ? sv.model : '',
  }));
  const act = p.find(x=>x.active);
  // Build via DOM (not string concat) so preset names can contain anything —
  // spaces, accents, quotes, < > & — without breaking markup or handlers.
  const sp = document.getElementById('status-preset');
  sp.textContent = act ? act.name : '';
  sp.title = act ? t('settings.presets.active_title') : '';
  const cont=document.getElementById('presets');
  cont.innerHTML='';
  if(!p.length){ cont.innerHTML='<span class="muted">'+tf('svc.none','Aucun modèle. Ajouter un service avec +.')+'</span>'; return; }
  // Signature de la sélection : quel modèle est celui du chat.
  const sel = act ? act.id : '';
  const moved = sel !== paintedSel; paintedSel = sel;
  p.forEach(x=>{
    const row=document.createElement('div');
    const pend = !!SVC_JOBS[x.svc.id];
    row.className='preset'+(x.active?' active':'')+(pend?' pending':'')+(moved?' sel-anim':'');
    // Réordonnable à la souris (SortableJS, init plus bas). data-id sert à relire
    // l'ordre après un déplacement.
    row.dataset.id = x.id;
    // Guard : un glissement ne doit pas être pris pour un clic qui bascule le preset.
    row.onclick=()=>{ if(presetJustDragged) return; useService(x.svc.id); };
    const info=document.createElement('div'); info.className='preset-info';
    const nm=document.createElement('div'); nm.className='preset-name';
    // Puce de l'actif : un ÉLÉMENT rond en CSS, pas le caractère « ● ». Le glyphe
    // est dessiné par la police du système — sur Windows il sortait plus petit et
    // plus bas que sur macOS. Un disque CSS a la même taille et la même assiette
    // partout.
    // Puce réservée à l'actif POUR DE BON : pendant la bascule, seule la barre
    // orange parle ; la puce apparaît quand la barre passe au blanc.
    if(x.active && !pend){ const d=document.createElement('i'); d.className='preset-dot'; nm.appendChild(d); }
    nm.appendChild(document.createTextNode(x.name)); nm.title=x.name;
    // Second row: quant tag + bench perf, so the title row stays full-width.
    const meta=document.createElement('div'); meta.className='preset-meta';
    if(pend){ const j=document.createElement('span'); j.className='ctag'; j.textContent=SVC_JOBS[x.svc.id]; meta.appendChild(j); }
    else if(x.svc.state==='failed'){ const j=document.createElement('span'); j.className='ctag'; j.textContent=tf('svc.state_failed','erreur'); j.title=x.svc.message||''; meta.appendChild(j); }
    // API distante : le nom du modèle distant à la place des tags quant/ctx.
    if(x.external){
      if(x.model){
        const mt=document.createElement('span'); mt.className='ctag';
        mt.title=t('settings.presets.external_model_title'); mt.textContent=x.model;
        meta.appendChild(mt);
      }
    } else {
      if(x.quant){
        const q=document.createElement('span'); q.className='qtag';
        q.textContent=x.quant; q.title=t('settings.presets.quant_title');
        meta.appendChild(q);
      }
    }
    // Contexte : toujours en bulle quand il est connu, même pour une API distante.
    if(x.ctx){
      const c=document.createElement('span'); c.className='ctag';
      c.title=t('settings.presets.context_size_title');
      c.textContent=x.ctx;
      meta.appendChild(c);
    }
    // Bench : mesuré sur un modèle local (le serveur n'en renvoie pas pour une
    // simple API distante).
    if(x.bench){
      const bt=document.createElement('span'); bt.className='btag';
      bt.title=t('settings.presets.bench_title');
      bt.textContent=x.bench.prefill.toFixed(0)+'-'+x.bench.decode.toFixed(0)+' t/s';
      meta.appendChild(bt);
    }
    // Pastille « capacités » à droite des autres tags : API externe (planète),
    // vision (œil), raisonnement (ampoule), regroupées comme un seul tag.
    const caps=[], capTitle=[];
    if(x.external){
      caps.push('<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="M2 12h20"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>');
      capTitle.push(t('settings.presets.external_title'));
    }
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
    edit.onclick=(e)=>{ e.stopPropagation(); openService(x.svc.id); };
    // Pas de poignée : la ligne entière est déplaçable, on attrape où on veut.
    row.appendChild(info); row.appendChild(edit);
    cont.appendChild(row);
  });
  initPresetSortable(cont);
}
// ─── Réordonnancement des modèles (SortableJS) ───────────────────────────────
// Glissement fluide (animation de poussée des voisins), ordre persisté côté
// serveur (/api/services/order). SortableJS est vendorisé dans l'UI (00-sortable).
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
    // Clone posé sur <body> : dans la modale (ancêtre transformé), sa position fixe
    // se calculait par rapport à la modale et il apparaissait loin du curseur.
    fallbackOnBody: true,
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
      // On re-rend la liste une fois le nouvel ordre persisté.
      jpost('/api/services/order', {ids}).then(()=>loadPresets()).catch(()=>{});
    },
  });
}
async function loadAgent(){
  // MEM_VIEW_PROJECT (si posé) scope les pages + le mode mémoire sur un autre projet.
  const s=await jget('/api/agent' + (MEM_VIEW_PROJECT ? ('?project='+encodeURIComponent(MEM_VIEW_PROJECT)) : ''));
  document.getElementById('compact-toggle').checked = (s.compact !== false);
  const pw=document.getElementById('prewarm-toggle'); if(pw) pw.checked = (s.prewarm !== false);
  const cm = document.getElementById('compact-max');
  if(cm){
    const v = String(s.compact_max || 0);
    if(![...cm.options].some(o=>o.value===v)) cm.add(new Option(Math.round(+v/1024)+'K', v));
    cm.value = v; cm.disabled = s.compact === false;
  }
  applyFastBtn();
  if(s.mem_mode){ setMemModeUI(s.mem_mode); }
  memPages = (s.pages || s.skills || []).slice().sort((a,b)=>a.name.localeCompare(b.name));
  memShown = MEM_PAGE;
  document.getElementById('mem-count').textContent = memPages.length ? String(memPages.length) : '';
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
  if(Array.isArray(s.versions)){
    const mine = s.versions.filter(v=>v.this).length;
    msg += ' · '+mine+' '+t('settings.backup.versions_this_machine');
    if(s.versions.length > mine) msg += ' · '+s.versions.length+' '+t('settings.backup.versions_total');
  }
  if(s.error) msg+='\n'+s.error;
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
// Libellé d'une sauvegarde : machine d'origine + date.
function backupLabel(v){
  const when = v.when ? new Date(v.when).toLocaleString() : v.id;
  const who = v.this ? t('settings.backup.this_machine')
            : (v.machineName || (v.machine ? v.machine.slice(0,8) : t('settings.backup.old_backup')));
  return who+' · '+when;
}
async function backupRestore(){
  let s; try{ s=await jget('/api/backup/status'); }catch(e){ await askAlert(t('settings.backup.relay_unreachable')); return; }
  const vers=(s.versions||[]);
  if(!vers.length){ await askAlert(t('settings.backup.none_available')); return; }
  // Plusieurs machines sur le compte : on choisit explicitement la sauvegarde.
  // Présélection : la plus récente de CETTE machine, sinon la plus récente.
  const def = vers.find(v=>v.this) || vers[0];
  const key = v => (v.machine||'')+'/'+v.id;
  if(!await askConfirm(t('settings.backup.restore_choose_msg'),{title:t('settings.backup.restore_title'),okText:t('settings.backup.restore_title'),danger:true,
      choices: vers.map(v=>({value:key(v), label:backupLabel(v)})), choice: key(def)})) return;
  const pick = vers.find(v=>key(v)===askChoice()) || def;
  // Boucle : une clé fausse redemande tout de suite.
  while(true){
    const secret=await askPrompt(t('settings.backup.restore_secret_prompt'),{title:t('settings.backup.restore_title'),placeholder:t('settings.backup.restore_secret_placeholder')});
    if(!secret) return; // annulé
    toast(t('settings.backup.restoring'));
    let r; try{ r=await jpost('/api/backup/restore',{id:pick.id,machine:pick.machine||'',secret}); }catch(e){ await askAlert(t('common.failed_prefix')+e); return; }
    if(r.ok){ await askAlert(t('settings.backup.restore_done_msg'),{title:t('settings.backup.restore_done_title')}); loadMem(); return; }
    await askAlert((r.error&&r.error.indexOf('incorrecte')>=0)?t('settings.wrong_key'):(t('common.failed_prefix')+(r.error||t('common.unknown'))),{title:t('settings.refused_title')});
  }
}
// Liste mémoire scalable : recherche + rendu plafonné (les milliers de pages ne
// déroulent plus une barre géante). memShown grimpe par paliers via « voir plus ».
let memPages=[], memShown=0; const MEM_PAGE=50;
// MEM_VIEW_PROJECT : slug d'un AUTRE projet dont on consulte la mémoire dans le hub
// sans avoir basculé dessus (bouton « Voir la mémoire » du menu ⋯ d'un projet).
// Vide = projet actif. Les appels mémoire (liste, lecture, mode, déplacement) y
// ajoutent ?project=/from= pour cibler ce projet côté serveur.
let MEM_VIEW_PROJECT='';
function renderMemList(){
  const q=(document.getElementById('mem-search').value||'').trim().toLowerCase();
  const list=document.getElementById('mem-list');
  list.textContent='';
  if(!memPages.length){ list.innerHTML='<div class="mm-empty"><svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/></svg><span>'+t('settings.memory.no_pages')+'</span></div>'; return; }
  const matches = q ? memPages.filter(x=>(x.name+' '+(x.desc||'')).toLowerCase().includes(q)) : memPages;
  if(!matches.length){ list.innerHTML='<div class="muted">'+t('settings.memory.no_results_before')+q.replace(/[<>&]/g,'')+t('settings.memory.no_results_after')+'</div>'; return; }
  const shown = matches.slice(0, memShown);
  const dbIco='<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/></svg>';
  shown.forEach(x=>{
    const row=document.createElement('div'); row.className='mem-item';
    row.onclick=()=>openMem(x.name);
    const ic=document.createElement('span'); ic.className='mem-item-ic'; ic.innerHTML=dbIco;
    const main=document.createElement('div'); main.className='mem-item-main';
    const nm=document.createElement('div'); nm.className='mem-item-name'; nm.textContent=x.name;
    main.appendChild(nm);
    if(x.desc){ const d=document.createElement('div'); d.className='mem-item-desc'; d.textContent=x.desc; main.appendChild(d); }
    // Menu ⋮ (éditer / déplacer), même gabarit que les conversations et trackers.
    const menu=document.createElement('button'); menu.className='sess-menu-btn';
    menu.innerHTML=(typeof projDotsSvg==='function'?projDotsSvg():'⋮'); menu.title=t('projects.options');
    menu.setAttribute('aria-label',t('projects.options'));
    menu.onclick=e=>{ e.stopPropagation(); openMemMenu(menu, x); };
    row.appendChild(ic); row.appendChild(main); row.appendChild(menu);
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
// Menu ⋮ d'une page mémoire : éditer / déplacer.
function openMemMenu(anchor, x){
  popMenu(anchor, [
    {icon:'pencil', label:t('settings.memory.edit_btn'), run:()=>openMem(x.name)},
    PROJECTS.length > 1 && {icon:'move', label:t('settings.memory.move_btn'), run:()=>moveMemUI(x.name, anchor)},
  ], {side:'below'});
}

// Déplacer une note mémoire du projet actif vers un autre projet (issue #55).
// Réutilise le sélecteur de projet du hub (pickProjectPop, 18-projects.js).
async function moveMemUI(name, anchor){
  if(typeof pickProjectPop!=='function'){ toast(t('settings.memory.move_unavailable')); return; }
  // Source = le projet actuellement consulté (vu ou actif). Le sélecteur exclut cette
  // source ; le déplacement part de `from` côté serveur.
  const from = MEM_VIEW_PROJECT || (typeof ACTIVE_PROJECT!=='undefined'?ACTIVE_PROJECT:'');
  pickProjectPop(anchor, from, async(slug)=>{
    let r; try{ r = await jpost('/api/projects/move-mem', {name, slug, from}); }catch(_){ toast(t('settings.memory.network_error')); return; }
    if(!r.ok){ toast(r.error || t('settings.memory.move_failed')); return; }
    toast(t('settings.memory.note_moved'));
    loadAgent();
  });
}
// alias : plusieurs appelants rafraîchissent juste la liste des pages mémoire
const loadMem = loadAgent;
// Pré-chauffage du prompt : réglage du serveur, commun à tous les appareils.
async function togglePrewarm(){
  const cb=document.getElementById('prewarm-toggle');
  const r=await jpost('/api/agent/prewarm',{on:cb.checked});
  if(!r || r.ok===false){ cb.checked=!cb.checked; toast((r&&r.error)||'erreur'); }
}
async function toggleCompact(){
  const on=document.getElementById('compact-toggle').checked;
  await jpost('/api/agent/compact',{on});
  const cm=document.getElementById('compact-max'); if(cm) cm.disabled=!on;
}
// Plafond du seuil de compactage (#142) : 0 = 75 % de la fenêtre du modèle.
async function setCompactMax(){
  const on=document.getElementById('compact-toggle').checked;
  const max=+document.getElementById('compact-max').value;
  const r=await jpost('/api/agent/compact',{on, max});
  if(r && r.ok===false) toast(r.error||'erreur');
}
// Mode mémoire (4 états, par projet) — indépendant du mode agent. La description
// de chaque mode vit désormais sous chaque option (voir .mem-opt-d dans le
// template), plus dans une boîte séparée.
// Coche la bonne option de la liste de modes (modal mémoire).
function setMemModeUI(m){
  const r=document.querySelector('input[name="mem-mode-seg"][value="'+m+'"]');
  if(r) r.checked=true;
}
// mode passé par le segment cliqué ; à défaut on lit le segment coché.
async function setMemMode(mode){
  if(!mode){ const r=document.querySelector('input[name="mem-mode-seg"]:checked'); mode=r?r.value:'always'; }
  const r=await jpost('/api/memory',{mode, project: MEM_VIEW_PROJECT||''});
  setMemModeUI(r.mode||mode);
}
// Accès internet : le moteur web intégré, rien à configurer, juste l'interrupteur.
let internetOn=false;
function renderInternet(s){
  internetOn = !!s.enabled;
  document.getElementById('internet-toggle').checked = internetOn;
  const st=document.getElementById('internet-status');
  st.textContent=t('settings.internet.builtin_engine_status');
}
async function loadInternet(){ renderInternet(await jget('/api/internet')); }
// Computer use : pilotage d'un navigateur de l'hôte (outils cu_*). La pastille ne
// signale que l'anomalie — activé mais aucun navigateur détecté sur la machine.
function renderComputer(s){
  const tg=document.getElementById('cu-toggle'); if(tg) tg.checked=!!s.enabled;
  if(s.enabled && !s.browser_ok) setBadge('cu-badge','warn','navigateur introuvable');
  else setBadge('cu-badge', null);
  const st=document.getElementById('cu-status'); if(!st) return;
  // Le statut DÉPEND de l'interrupteur : décoché, on ne prétend surtout pas que
  // c'est actif (sinon confusion). On indique juste si un navigateur est présent.
  if(!s.enabled){ st.textContent=s.browser_ok?'':t('settings.computer.no_browser_hint'); return; }
  if(!s.browser_ok){ st.textContent=t('settings.computer.no_browser'); return; }
  st.textContent=t('settings.computer.browser_ready')+(s.vision?'':(' · '+t('settings.computer.no_vision')));
}
async function loadComputer(){ renderComputer(await jget('/api/computer')); }
async function toggleComputer(){
  const on=document.getElementById('cu-toggle').checked;
  renderComputer(await jpost('/api/computer',{on}));
}
// --- Accès OpenAI (endpoint /v1 + clé API des complétions) -----------------
let OAI_KEY='', OAI_REVEAL=false;
async function copyText(txt, msg){
  if(!txt){ toast(t('settings.copy.nothing')); return; }
  try{ await navigator.clipboard.writeText(txt); }
  catch(_){ const ta=document.createElement('textarea'); ta.value=txt; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); ta.remove(); }
  toast(msg||t('settings.copy.copied'));
}
// Retour visuel d'un bouton copier : l'icône devient une coche un instant.
const COPIED_SVG='<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>';
function flashCopied(btn){
  if(!btn || btn._copied) return;
  btn._copied = btn.innerHTML; btn.innerHTML = COPIED_SVG; btn.classList.add('copied');
  setTimeout(()=>{ btn.innerHTML = btn._copied; btn._copied = null; btn.classList.remove('copied'); }, 1200);
}
function copyApiKey(){ copyText(OAI_KEY, OAI_KEY?t('settings.apikey.copied'):t('settings.apikey.none')); }
function renderApiKey(d){
  OAI_KEY = d.key || '';
  // Adresse, nom et clé : ceux du modèle du chat, annoncés par le serveur. Dans
  // le tunnel ajean.link, location.hostname serait le domaine du relais (faux) :
  // l'adresse affichée reste TOUJOURS celle que le serveur connaît.
  const url = d.url || ('http://'+(d.host || location.hostname)+':'+d.port+'/v1');
  document.getElementById('oai-url').value = url;
  document.getElementById('oai-model').value = d.model || 'ajean';
  // Modèle qui n'écoute que sur cette machine : l'adresse ne répond pas depuis
  // un autre ordinateur, et rien d'autre ne le dirait.
  const note = document.getElementById('api-local-note');
  if(note) note.style.display = /^https?:\/\/(127\.0\.0\.1|localhost|\[::1\])[:\/]/i.test(url) ? '' : 'none';
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
  document.getElementById('oai-key-copy').style.display = d.set ? '' : 'none';
}
async function loadApiKey(){ renderApiKey(await jget('/api/apikey')); }
// --- Export de la conversation ---------------------------------------------
// Le modal d'export riche (format/portée/contenu) a été retiré : l'export se
// fait par conversation depuis le hub Projets (téléchargement direct via
// downloadExport, juste en dessous).
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
function toggleKeyReveal(){ OAI_REVEAL=!OAI_REVEAL; const inp=document.getElementById('oai-key'); if(OAI_KEY) inp.value = OAI_REVEAL ? OAI_KEY : (OAI_KEY.slice(0,8)+'…'+OAI_KEY.slice(-4)); const eye=document.getElementById('oai-key-eye'); if(eye){ eye.title = OAI_REVEAL ? t('settings.apikey.hide') : t('settings.apikey.show'); eye.classList.toggle('on', OAI_REVEAL); } }
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
async function toggleInternet(){
  const on=document.getElementById('internet-toggle').checked;
  renderInternet(await jpost('/api/internet',{enabled:on}));
}
// Navigateur des liens externes cliqués dans l'interface. Réglage partagé entre
// appareils (/api/prefs) mais écrit à part de savePrefs() : celui-ci poste tout le
// jeu connu à chaque changement de thème, et écraserait donc, sur un appareil au
// localStorage vide, un choix fait depuis un autre appareil.
const LINK_BROWSERS=['auto','chrome','edge','brave','chromium'];
// Disponibilité réelle des navigateurs sur la machine : la liste propose les
// cinq, mais seuls ceux qui sont installés doivent être choisissables. Sans ça
// on offrait Brave à qui ne l'a pas — le choix ne cassait rien (le clic retombe
// sur window.open) mais il mentait sur ce qui est possible ici.
// « auto » n'est jamais grisé : il passe par le gestionnaire de l'OS, qui existe
// toujours et couvre les navigateurs absents de cette liste.
async function loadOpen(){
  try{
    const r=await jget('/api/open');
    const sel=document.getElementById('link-browser');
    if(!r || !r.ok || !r.available || !sel) return;
    for(const o of sel.options){
      o.disabled = o.value==='auto' ? false : r.available[o.value]===false;
    }
  }catch(e){}
}
function applyLinkBrowser(v){
  const val=LINK_BROWSERS.indexOf(v)>=0 ? v : 'auto';
  try{ localStorage.setItem('ajean-link-browser', val); }catch(e){}
  const sel=document.getElementById('link-browser');
  if(sel && document.activeElement!==sel) sel.value=val;
  return val;
}
async function setLinkBrowser(v){
  const val=applyLinkBrowser(v);
  await jpost('/api/prefs',{link_browser:val});
  toast(t('settings.link_browser_saved'));
}
async function loadAll(){
  // allSettled et pas all : un seul chargement en échec (accès distant coupé,
  // clé API absente…) ne doit pas empêcher la suite — et surtout pas laisser les
  // hauteurs réservées en place pour toujours.
  await Promise.allSettled([loadStatus(),loadTelemetry(),loadCfg(),loadPresets(),loadServices(),loadAgent(),loadInternet(),loadComputer(),loadMCP(),loadApiKey(),loadPrefs(),loadOpen(),loadRemote(),loadTasks(),loadSTT()]);
  releaseHeights(); // tout est en place : on rend la main et on mesure pour la prochaine fois
}
