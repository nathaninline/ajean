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
  let r; try{ r=await jget('/api/tracker'); }catch(_){ box.innerHTML='<span class="muted" style="font-size:12px">erreur de chargement</span>'; return; }
  const list=(r&&r.trackers)||[];
  const cnt=document.getElementById('tracker-count'); if(cnt) cnt.textContent = list.length ? (list.length+' tracker'+(list.length>1?'s':'')) : '';
  box.innerHTML='';
  if(!list.length){ box.innerHTML='<span class="muted" style="font-size:12px">aucun tracker. Crée-en un avec « + tracker » (ex. abonnés, poids, revenus).</span>'; return; }
  list.forEach(s=>box.appendChild(trackerRow(s)));
}

function trackerRow(s){
  const card=document.createElement('div'); card.className='tracker-card'; card.tabIndex=0; card.title='Ouvrir ce tracker';
  card.onclick=()=>openTrackerDetail(s.slug, s.name);
  card.onkeydown=(e)=>{ if((e.key==='Enter'||e.key===' ')&&e.target===card){ e.preventDefault(); openTrackerDetail(s.slug, s.name); } };
  const main=document.createElement('div'); main.className='tracker-card-main';
  const name=document.createElement('div'); name.className='tracker-card-name'; name.textContent=s.name;
  const sub=document.createElement('div'); sub.className='tracker-card-sub';
  sub.textContent = s.count ? (s.count+' point'+(s.count>1?'s':'')+' · maj '+(s.last||'')) : 'vide';
  main.appendChild(name); main.appendChild(sub);
  if(s.latest){ const val=document.createElement('div'); val.className='tracker-card-val'; val.textContent=s.latest; main.appendChild(val); }
  card.appendChild(main);
  const menu=document.createElement('button'); menu.className='sess-menu-btn'; menu.innerHTML=projDotsSvg(); menu.title='Options';
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
  if(typeof PROJECTS!=='undefined' && PROJECTS.length>1) pop.appendChild(item('move','Déplacer vers…','',()=>trackerMove(s, anchor)));
  pop.appendChild(item('trash','Supprimer','danger',()=>trackerDelete(s)));
  document.body.appendChild(pop);
  const r=anchor.getBoundingClientRect(); const pw=pop.offsetWidth, ph=pop.offsetHeight;
  let left=Math.max(8, Math.min(r.right-pw, window.innerWidth-pw-8));
  let top=r.bottom+6; if(top+ph>window.innerHeight-8) top=r.top-ph-6;
  pop.style.left=left+'px'; pop.style.top=top+'px';
  _projPop=pop;
  setTimeout(()=>{ document.addEventListener('click', _projOutside, true); document.addEventListener('scroll', closeProjMenu, true); }, 0);
}

function trackerMove(s, anchor){
  if(typeof pickProjectPop!=='function') return;
  pickProjectPop(anchor||document.body, (typeof ACTIVE_PROJECT!=='undefined'?ACTIVE_PROJECT:''), async(slug)=>{
    let r; try{ r=await jpost('/api/tracker/move', {slug:s.slug, toSlug:slug}); }catch(_){ toast('erreur réseau'); return; }
    if(!r.ok){ toast(r.error||'déplacement impossible'); return; }
    toast('tracker déplacé'); loadTrackers();
  });
}

async function trackerDelete(s){
  if(!await askConfirm('Supprimer le tracker « '+s.name+' » et tous ses points ? Cette action est irréversible.', {title:'Supprimer le tracker', okText:'Supprimer', danger:true})) return;
  let r; try{ r=await jpost('/api/tracker/delete', {slug:s.slug}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error||'suppression impossible'); return; }
  toast('tracker supprimé');
  if(TRACKER_CUR && TRACKER_CUR.slug===s.slug) trackerBack(); else loadTrackers();
}

// Créer un tracker = poser son premier point.
async function newTrackerUI(){
  const name=await askPrompt('Nom du tracker (ex. abonnés, poids, revenus) :', {title:'Nouveau tracker', okText:'Suivant', placeholder:'nom du tracker'});
  if(name===null) return; if(!name.trim()){ toast('nom vide'); return; }
  const text=await askPrompt('Premier point (valeur ou note) :', {title:'Nouveau tracker — '+name.trim(), okText:'Créer', placeholder:'ex. abonnés : 4210'});
  if(text===null) return; if(!text.trim()){ toast('valeur vide'); return; }
  let r; try{ r=await jpost('/api/tracker/add', {name:name.trim(), when:'', text:text.trim()}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error||'création impossible'); return; }
  toast('tracker créé');
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
  box.innerHTML='<span class="muted" style="font-size:12px">chargement…</span>';
  let r; try{ r=await jget('/api/tracker/events?slug='+encodeURIComponent(TRACKER_CUR.slug)); }catch(_){ box.innerHTML='<span class="muted" style="font-size:12px">erreur</span>'; return; }
  if(!r.ok){ box.innerHTML='<span class="muted" style="font-size:12px">'+(r.error||'erreur')+'</span>'; return; }
  TRACKER_CUR.name = r.name || TRACKER_CUR.name;
  const evs=(r.events||[]).slice().sort((a,b)=>b.ts-a.ts); // plus récent d'abord
  // Héros compact : nom + étendue seulement. Le dernier point n'est PAS répété ici :
  // la frise ci-dessous l'affiche en tête (plus récent d'abord).
  if(hero){
    hero.innerHTML='';
    const nm=document.createElement('div'); nm.className='tracker-hero-name'; nm.textContent=r.name;
    const sub=document.createElement('div'); sub.className='tracker-hero-sub';
    sub.textContent = evs.length ? (evs.length+' point'+(evs.length>1?'s':'')+' · depuis '+evs[evs.length-1].when.slice(0,10)) : 'aucun point';
    hero.appendChild(nm); hero.appendChild(sub);
  }
  box.innerHTML='';
  if(!evs.length){ box.innerHTML='<span class="muted" style="font-size:12px">aucun point. Ajoute-en un ci-dessus.</span>'; return; }
  const curYear=new Date().getFullYear().toString();
  const byYear={}; evs.forEach(e=>{ const y=e.when.slice(0,4); (byYear[y]=byYear[y]||[]).push(e); });
  Object.keys(byYear).sort((a,b)=>b.localeCompare(a)).forEach(y=>{
    const yEvs=byYear[y];
    const yh=document.createElement('div'); yh.className='tracker-yhead';
    const wrap=document.createElement('div'); wrap.hidden=(y!==curYear);
    const setLbl=()=>{ yh.innerHTML=''; const tri=document.createElement('span'); tri.textContent=(wrap.hidden?'▸':'▾')+' '+y; const c=document.createElement('span'); c.className='cnt'; c.textContent=yEvs.length+' pt'; yh.appendChild(tri); yh.appendChild(c); };
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
  const ed=document.createElement('button'); ed.title='Modifier'; ed.innerHTML=sessIconSvg('pencil');
  ed.onclick=(ev)=>{ ev.stopPropagation(); editTrackerPoint(e); };
  const rm=document.createElement('button'); rm.className='danger'; rm.title='Supprimer'; rm.innerHTML=sessIconSvg('trash');
  rm.onclick=(ev)=>{ ev.stopPropagation(); deleteTrackerPoint(e); };
  acts.appendChild(ed); acts.appendChild(rm);
  row.appendChild(val); row.appendChild(date); row.appendChild(acts);
  return row;
}

// Construit la valeur `when` à partir des sélecteurs date + heure : vide = maintenant,
// date seule = pas d'heure, date + heure = les deux.
function trackerWhenValue(){
  const d=(document.getElementById('tracker-date')||{}).value||'';
  const t=(document.getElementById('tracker-time')||{}).value||'';
  // Ajout, champs inchangés depuis le préremplissage auto → « maintenant » (le serveur
  // horodate à l'instant réel de soumission, pas à l'ouverture du formulaire).
  if(!TRACKER_EDIT && TRACKER_WHEN_AUTO && d===TRACKER_WHEN_AUTO.date && t===TRACKER_WHEN_AUTO.time) return '';
  if(!d.trim()) return '';
  return t.trim() ? (d.trim()+' '+t.trim()) : d.trim();
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
  const btn=document.getElementById('tracker-add-btn'); if(btn) btn.textContent='Ajouter';
}

async function trackerAddPoint(){
  if(!TRACKER_CUR) return;
  const tx=document.getElementById('tracker-text');
  const text=(tx&&tx.value||'').trim(); if(!text){ toast('valeur vide'); if(tx) tx.focus(); return; }
  const when=trackerWhenValue();
  let r;
  try{
    if(TRACKER_EDIT) r=await jpost('/api/tracker/edit', {slug:TRACKER_CUR.slug, id:TRACKER_EDIT.id, text, when});
    else r=await jpost('/api/tracker/add', {name:TRACKER_CUR.name, when, text});
  }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error||'échec'); return; }
  trackerClearForm();
  renderTrackerEvents();
}

// Édition INLINE : préremplit le formulaire d'ajout (le bouton devient « Enregistrer »).
function editTrackerPoint(e){
  TRACKER_EDIT=e;
  const parts=(e.when||'').split(' ');
  const set=(id,v)=>{ const el=document.getElementById(id); if(el) el.value=v||''; };
  set('tracker-text', e.text); set('tracker-date', parts[0]||''); set('tracker-time', parts[1]||'');
  const btn=document.getElementById('tracker-add-btn'); if(btn) btn.textContent='Enregistrer';
  const tx=document.getElementById('tracker-text'); if(tx) tx.focus();
}

async function deleteTrackerPoint(e){
  if(!await askConfirm('Supprimer ce point ?\n\n'+e.when+' — '+e.text, {title:'Supprimer le point', okText:'Supprimer', danger:true})) return;
  let r; try{ r=await jpost('/api/tracker/delete', {slug:TRACKER_CUR.slug, id:e.id}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error||'suppression impossible'); return; }
  if(TRACKER_EDIT && TRACKER_EDIT.id===e.id) trackerClearForm();
  renderTrackerEvents();
}
