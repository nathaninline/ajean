// 19-tracker.js — les TRACKERS côté UI (3ᵉ type de mémoire : données datées qui
// s'accumulent). Le modal liste les trackers du projet actif ; chaque tracker porte un
// menu ⋮ (déplacer vers un projet / supprimer), comme les conversations. Ouvrir un
// tracker affiche sa frise par année (année en cours dépliée) avec ajout / édition /
// suppression de points. Le « quand » est un sélecteur date + heure FACULTATIVE :
// sans heure, le point n'a pas d'heure (pas de faux « minuit »).

let TRACKER_CUR = null;  // {slug, name} du tracker ouvert, ou null en vue liste
let TRACKER_EDIT = null; // point en cours d'édition (mode inline), ou null
let TRACKER_WHEN_AUTO = null; // {date,time} préremplis par le reset (pour détecter « inchangé »)

function openTrackerHub(){ showModal('tracker-modal'); trackerBack(); if(typeof loadProjects==='function' && (typeof PROJECTS==='undefined' || !PROJECTS.length)) loadProjects(); }
function closeTrackerModal(){ hideModal('tracker-modal'); }

// Vue LISTE (referme le détail).
function trackerBack(){
  TRACKER_CUR = null; trackerClearForm();
  const lv=document.getElementById('tracker-list-view'), dv=document.getElementById('tracker-detail-view');
  if(lv) lv.hidden=false; if(dv) dv.hidden=true;
  loadTrackers();
}

async function loadTrackers(){
  const box=document.getElementById('tracker-list'); if(!box) return;
  let r; try{ r=await jget('/api/tracker'); }catch(_){ box.innerHTML='<span class="muted" style="font-size:12px">'+t('tracker.load_error')+'</span>'; return; }
  const list=(r&&r.trackers)||[];
  const cnt=document.getElementById('tracker-count'); if(cnt) cnt.textContent = list.length ? (list.length+' '+(list.length>1?t('tracker.count_plural'):t('tracker.count_singular'))) : '';
  box.innerHTML='';
  if(!list.length){ box.innerHTML='<span class="muted" style="font-size:12px">'+t('tracker.empty_list')+'</span>'; return; }
  list.forEach(s=>box.appendChild(trackerRow(s)));
}

function trackerRow(s){
  const card=document.createElement('div'); card.className='tracker-card'; card.tabIndex=0; card.title=t('tracker.open_this');
  card.onclick=()=>openTrackerDetail(s.slug, s.name);
  card.onkeydown=(e)=>{ if((e.key==='Enter'||e.key===' ')&&e.target===card){ e.preventDefault(); openTrackerDetail(s.slug, s.name); } };
  const main=document.createElement('div'); main.className='tracker-card-main';
  const name=document.createElement('div'); name.className='tracker-card-name'; name.textContent=s.name;
  const sub=document.createElement('div'); sub.className='tracker-card-sub';
  sub.textContent = s.count ? (s.count+' '+(s.count>1?t('tracker.point_plural'):t('tracker.point_singular'))+' · '+t('tracker.updated')+' '+(s.last||'')) : t('tracker.empty_single');
  main.appendChild(name); main.appendChild(sub);
  if(s.latest){ const val=document.createElement('div'); val.className='tracker-card-val'; val.textContent=s.latest; main.appendChild(val); }
  card.appendChild(main);
  const menu=document.createElement('button'); menu.className='sess-menu-btn'; menu.innerHTML=projDotsSvg(); menu.title=t('tracker.options');
  menu.onclick=(e)=>{ e.stopPropagation(); openTrackerMenu(menu, s); };
  card.appendChild(menu);
  return card;
}

// Menu ⋮ d'un tracker : déplacer vers un projet / supprimer. Réutilise l'infra pop du
// hub projets (closeProjMenu / _projOutside).
function openTrackerMenu(anchor, s){
  closeProjMenu();
  const pop=document.createElement('div'); pop.className='pop-menu';
  const item=(icon,label,cls,fn)=>{ const b=document.createElement('button'); if(cls) b.className=cls; b.innerHTML=sessIconSvg(icon)+'<span>'+label+'</span>'; b.onclick=(e)=>{ e.stopPropagation(); closeProjMenu(); fn(); }; return b; };
  pop.appendChild(item('pencil',t('tracker.rename'),'',()=>trackerRename(s)));
  if(typeof PROJECTS!=='undefined' && PROJECTS.length>1) pop.appendChild(item('move',t('tracker.move_to'),'',()=>trackerMove(s, anchor)));
  pop.appendChild(item('trash',t('tracker.delete'),'danger',()=>trackerDelete(s)));
  document.body.appendChild(pop);
  const r=anchor.getBoundingClientRect(); const pw=pop.offsetWidth, ph=pop.offsetHeight;
  let left=Math.max(8, Math.min(r.right-pw, window.innerWidth-pw-8));
  let top=r.bottom+6; if(top+ph>window.innerHeight-8) top=r.top-ph-6;
  pop.style.left=left+'px'; pop.style.top=top+'px';
  _projPop=pop;
  setTimeout(()=>{ document.addEventListener('click', _projOutside, true); document.addEventListener('scroll', closeProjMenu, true); }, 0);
}

async function trackerRename(s){
  const name=await askPrompt(t('tracker.rename_prompt'), {title:t('tracker.rename_title'), okText:t('tracker.rename'), placeholder:t('tracker.new_name_placeholder'), default:s.name});
  if(name===null) return; const nn=name.trim(); if(!nn){ toast(t('tracker.empty_name')); return; }
  if(nn===s.name) return;
  let r; try{ r=await jpost('/api/tracker/rename', {slug:s.slug, name:nn}); }catch(_){ toast(t('tracker.network_error')); return; }
  if(!r.ok){ toast(r.error||t('tracker.rename_failed')); return; }
  toast(t('tracker.renamed'));
  // Le slug a pu changer (dérivé du nom) : on repart de la liste plutôt que de garder
  // l'ancien slug ouvert. Si le détail du tracker renommé était ouvert, on le rouvre.
  const newSlug=(nn.toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'')||'tracker');
  if(TRACKER_CUR && TRACKER_CUR.slug===s.slug) openTrackerDetail(newSlug, nn); else loadTrackers();
}

function trackerMove(s, anchor){
  if(typeof pickProjectPop!=='function') return;
  pickProjectPop(anchor||document.body, (typeof ACTIVE_PROJECT!=='undefined'?ACTIVE_PROJECT:''), async(slug)=>{
    let r; try{ r=await jpost('/api/tracker/move', {slug:s.slug, toSlug:slug}); }catch(_){ toast(t('tracker.network_error')); return; }
    if(!r.ok){ toast(r.error||t('tracker.move_failed')); return; }
    toast(t('tracker.moved')); loadTrackers();
  });
}

async function trackerDelete(s){
  if(!await askConfirm(t('tracker.delete_confirm_prefix')+s.name+t('tracker.delete_confirm_suffix'), {title:t('tracker.delete_title'), okText:t('tracker.delete'), danger:true})) return;
  let r; try{ r=await jpost('/api/tracker/delete', {slug:s.slug}); }catch(_){ toast(t('tracker.network_error')); return; }
  if(!r.ok){ toast(r.error||t('tracker.delete_failed')); return; }
  toast(t('tracker.deleted'));
  if(TRACKER_CUR && TRACKER_CUR.slug===s.slug) trackerBack(); else loadTrackers();
}

// Créer un tracker = poser son premier point.
async function newTrackerUI(){
  const name=await askPrompt(t('tracker.new_name_prompt'), {title:t('tracker.new_title'), okText:t('tracker.new_next'), placeholder:t('tracker.new_name_placeholder')});
  if(name===null) return; if(!name.trim()){ toast(t('tracker.empty_name')); return; }
  const text=await askPrompt(t('tracker.new_point_prompt'), {title:t('tracker.new_title_named_prefix')+name.trim(), okText:t('tracker.new_create'), placeholder:t('tracker.new_point_placeholder')});
  if(text===null) return; if(!text.trim()){ toast(t('tracker.empty_value')); return; }
  let r; try{ r=await jpost('/api/tracker/add', {name:name.trim(), when:'', text:text.trim()}); }catch(_){ toast(t('tracker.network_error')); return; }
  if(!r.ok){ toast(r.error||t('tracker.create_failed')); return; }
  toast(t('tracker.created'));
  openTrackerDetail((name.trim().toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'')||'tracker'), name.trim());
}

async function openTrackerDetail(slug, name){
  TRACKER_CUR={slug, name}; trackerClearForm();
  const lv=document.getElementById('tracker-list-view'), dv=document.getElementById('tracker-detail-view');
  if(lv) lv.hidden=true; if(dv) dv.hidden=false;
  await renderTrackerEvents();
}

async function renderTrackerEvents(){
  if(!TRACKER_CUR) return;
  const hero=document.getElementById('tracker-hero');
  const box=document.getElementById('tracker-events'); if(!box) return;
  box.innerHTML='<span class="muted" style="font-size:12px">'+t('tracker.loading')+'</span>';
  let r; try{ r=await jget('/api/tracker/events?slug='+encodeURIComponent(TRACKER_CUR.slug)); }catch(_){ box.innerHTML='<span class="muted" style="font-size:12px">'+t('tracker.error')+'</span>'; return; }
  if(!r.ok){ box.innerHTML='<span class="muted" style="font-size:12px">'+(r.error||t('tracker.error'))+'</span>'; return; }
  TRACKER_CUR.name = r.name || TRACKER_CUR.name;
  const evs=(r.events||[]).slice().sort((a,b)=>b.ts-a.ts); // plus récent d'abord
  // Héros compact : nom + étendue seulement. Le dernier point n'est PAS répété ici :
  // la frise ci-dessous l'affiche en tête (plus récent d'abord).
  if(hero){
    hero.innerHTML='';
    const nm=document.createElement('div'); nm.className='tracker-hero-name'; nm.textContent=r.name;
    const sub=document.createElement('div'); sub.className='tracker-hero-sub';
    sub.textContent = evs.length ? (evs.length+' '+(evs.length>1?t('tracker.point_plural'):t('tracker.point_singular'))+' · '+t('tracker.since')+' '+evs[evs.length-1].when.slice(0,10)) : t('tracker.no_points');
    hero.appendChild(nm); hero.appendChild(sub);
  }
  box.innerHTML='';
  if(!evs.length){ box.innerHTML='<span class="muted" style="font-size:12px">'+t('tracker.no_points_hint')+'</span>'; return; }
  const curYear=new Date().getFullYear().toString();
  const byYear={}; evs.forEach(e=>{ const y=e.when.slice(0,4); (byYear[y]=byYear[y]||[]).push(e); });
  Object.keys(byYear).sort((a,b)=>b.localeCompare(a)).forEach(y=>{
    const yEvs=byYear[y];
    const yh=document.createElement('div'); yh.className='tracker-yhead';
    const wrap=document.createElement('div'); wrap.hidden=(y!==curYear);
    const setLbl=()=>{ yh.innerHTML=''; const tri=document.createElement('span'); tri.textContent=(wrap.hidden?'▸':'▾')+' '+y; const c=document.createElement('span'); c.className='cnt'; c.textContent=yEvs.length+' '+t('tracker.pt_abbrev'); yh.appendChild(tri); yh.appendChild(c); };
    setLbl();
    yh.onclick=()=>{ wrap.hidden=!wrap.hidden; setLbl(); };
    box.appendChild(yh);
    yEvs.forEach(e=>wrap.appendChild(trackerEventRow(e)));
    box.appendChild(wrap);
  });
}

function trackerEventRow(e){
  const row=document.createElement('div'); row.className='tracker-pt';
  const val=document.createElement('div'); val.className='tracker-pt-val'; val.textContent=e.text;
  const date=document.createElement('div'); date.className='tracker-pt-date';
  const wp=(e.when||'').split(' '); const dd=document.createElement('span'); dd.textContent=wp[0]||'';
  date.appendChild(dd);
  if(wp[1]){ const tt=document.createElement('span'); tt.className='tracker-pt-time'; tt.textContent=wp[1]; date.appendChild(tt); }
  const acts=document.createElement('div'); acts.className='tracker-pt-acts';
  const ed=document.createElement('button'); ed.title=t('tracker.edit'); ed.innerHTML=sessIconSvg('pencil');
  ed.onclick=(ev)=>{ ev.stopPropagation(); openTrackerPtModal(e); };
  const rm=document.createElement('button'); rm.className='danger'; rm.title=t('tracker.delete'); rm.innerHTML=sessIconSvg('trash');
  rm.onclick=(ev)=>{ ev.stopPropagation(); deleteTrackerPoint(e); };
  acts.appendChild(ed); acts.appendChild(rm);
  row.appendChild(val); row.appendChild(date); row.appendChild(acts);
  return row;
}

// Construit la valeur `when` à partir des sélecteurs date + heure : vide = maintenant,
// date seule = pas d'heure, date + heure = les deux.
function trackerWhenValue(){
  const d=(document.getElementById('tracker-date')||{}).value||'';
  const tv=(document.getElementById('tracker-time')||{}).value||'';
  // Ajout, champs inchangés depuis le préremplissage auto → « maintenant » (le serveur
  // horodate à l'instant réel de soumission, pas à l'ouverture du formulaire).
  if(!TRACKER_EDIT && TRACKER_WHEN_AUTO && d===TRACKER_WHEN_AUTO.date && tv===TRACKER_WHEN_AUTO.time) return '';
  if(!d.trim()) return '';
  return tv.trim() ? (d.trim()+' '+tv.trim()) : d.trim();
}
// Réinitialise le formulaire d'ajout : note vide + Date/Heure préremplies à
// l'instant présent (jamais de champ vide, qui s'affiche en boîte noire sur iOS).
function trackerClearForm(){
  TRACKER_EDIT=null;
  const tx=document.getElementById('tracker-text'); if(tx) tx.value='';
  const n=new Date(), p=x=>String(x).padStart(2,'0');
  const d=document.getElementById('tracker-date'); if(d) d.value=n.getFullYear()+'-'+p(n.getMonth()+1)+'-'+p(n.getDate());
  const tm=document.getElementById('tracker-time'); if(tm) tm.value=p(n.getHours())+':'+p(n.getMinutes());
  TRACKER_WHEN_AUTO={date:d?d.value:'', time:tm?tm.value:''};
  const btn=document.getElementById('tracker-add-btn'); if(btn) btn.textContent=t('tracker.add');
}

// Ouvre le modal d'ajout (e absent) ou d'édition (e = point). Le formulaire vit
// désormais dans sa propre modale, empilée par-dessus le hub des trackers.
function openTrackerPtModal(e){
  if(!TRACKER_CUR) return;
  if(e) editTrackerPoint(e); else trackerClearForm();
  const ttl=document.getElementById('tracker-pt-title'); if(ttl) ttl.textContent = e ? t('tracker.edit_point_title') : t('tracker.add_point_title');
  showModal('tracker-pt-modal');
  const tx=document.getElementById('tracker-text'); if(tx){ tx.focus(); }
}
function closeTrackerPtModal(){ hideModal('tracker-pt-modal'); trackerClearForm(); }

async function trackerAddPoint(){
  if(!TRACKER_CUR) return;
  const tx=document.getElementById('tracker-text');
  const text=(tx&&tx.value||'').trim(); if(!text){ toast(t('tracker.empty_value')); if(tx) tx.focus(); return; }
  const when=trackerWhenValue();
  let r;
  try{
    if(TRACKER_EDIT) r=await jpost('/api/tracker/edit', {slug:TRACKER_CUR.slug, id:TRACKER_EDIT.id, text, when});
    else r=await jpost('/api/tracker/add', {name:TRACKER_CUR.name, when, text});
  }catch(_){ toast(t('tracker.network_error')); return; }
  if(!r.ok){ toast(r.error||t('tracker.add_failed')); return; }
  closeTrackerPtModal();
  renderTrackerEvents();
}

// Préremplit le formulaire (partagé ajout/édition, vit dans tracker-pt-modal) avec le
// point visé et bascule le bouton sur « Enregistrer ».
function editTrackerPoint(e){
  TRACKER_EDIT=e;
  const parts=(e.when||'').split(' ');
  const set=(id,v)=>{ const el=document.getElementById(id); if(el) el.value=v||''; };
  set('tracker-text', e.text); set('tracker-date', parts[0]||''); set('tracker-time', parts[1]||'');
  const btn=document.getElementById('tracker-add-btn'); if(btn) btn.textContent=t('tracker.save');
  const tx=document.getElementById('tracker-text'); if(tx) tx.focus();
}

async function deleteTrackerPoint(e){
  if(!await askConfirm(t('tracker.delete_point_confirm_prefix')+e.when+' — '+e.text, {title:t('tracker.delete_point_title'), okText:t('tracker.delete'), danger:true})) return;
  let r; try{ r=await jpost('/api/tracker/delete', {slug:TRACKER_CUR.slug, id:e.id}); }catch(_){ toast(t('tracker.network_error')); return; }
  if(!r.ok){ toast(r.error||t('tracker.delete_point_failed')); return; }
  if(TRACKER_EDIT && TRACKER_EDIT.id===e.id) trackerClearForm();
  renderTrackerEvents();
}
