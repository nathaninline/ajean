// ===== Fichiers de Jean (gestionnaire de fichiers du mode Jean) ===============
// Le dossier de travail de Jean, parcouru, rempli et vidé depuis l'app : ce que
// l'utilisateur y dépose, ce que Jean y fabrique. Jean lit tout en fond (texte,
// PDF, Word, ce que montrent les photos) et retrouve un fichier par son contenu
// (jean_files.go). Repris du gestionnaire de fichiers de Sillage.
//
// Tout passe par jfetch, donc aussi par le tunnel chiffré d'app.ajean.link :
// listes et actions par /api/jean/files*, envoi par morceaux par /api/chat/upload
// (champ dest), vignettes et téléchargements par /api/chat/file (« jean-workspace/… »).
// Souris : un clic sélectionne (Ctrl ajoute, Maj une plage), un double-clic ouvre,
// clic droit = actions. Doigt : un toucher ouvre, un appui long fait glisser vers
// un dossier, ⋯ = actions.
const JF = {dir:'', entries:[], sel:new Set(), anchor:null, sort:'name', desc:false, grid:false,
  search:null, count:'', thumbs:new Map(), io:null, open:null, viewURL:null, busy:false};
const jfFine = matchMedia('(hover: hover) and (pointer: fine)');
(()=>{ try{ const p=JSON.parse(localStorage.getItem('jf-prefs')||'{}'); JF.sort=p.sort||'name'; JF.desc=!!p.desc; JF.grid=!!p.grid; }catch(_){} })();
function jfSavePrefs(){ try{ localStorage.setItem('jf-prefs', JSON.stringify({sort:JF.sort, desc:JF.desc, grid:JF.grid})); }catch(_){} }
const jfEl = (id)=>document.getElementById(id);
const jfRel = (name)=>(JF.dir ? JF.dir+'/' : '')+name;
const jfUpDir = (dir)=>dir.split('/').slice(0,-1).join('/');
const jfWS = (rel)=>'jean-workspace/'+rel;
const jfIsOpen = ()=>{ const m=jfEl('jean-files-modal'); return !!m && m.classList.contains('show'); };
const JF_TEXT = /\.(txt|md|markdown|log|csv|tsv|json|jsonl|yaml|yml|toml|ini|cfg|conf|env|xml|html|htm|css|js|mjs|ts|tsx|jsx|py|go|rs|java|kt|c|h|cpp|hpp|cs|rb|php|sh|bash|zsh|ps1|bat|sql|lua|swift|r|tex|srt|vtt)$/i;
const JF_VIDEO = /\.(mp4|webm|mov|m4v)$/i, JF_AUDIO = /\.(mp3|m4a|wav|ogg|oga|flac|aac)$/i;
const JF_IC = {
  dir:'<svg viewBox="0 0 24 24" fill="currentColor"><path d="M3 6.5A2.5 2.5 0 0 1 5.5 4h4l2 2.2h7A2.5 2.5 0 0 1 21 8.7v8.8A2.5 2.5 0 0 1 18.5 20h-13A2.5 2.5 0 0 1 3 17.5z"/></svg>',
  more:'<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor"><circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/></svg>',
  grid:'<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.9"><rect x="4" y="4" width="7" height="7" rx="1.5"/><rect x="13" y="4" width="7" height="7" rx="1.5"/><rect x="4" y="13" width="7" height="7" rx="1.5"/><rect x="13" y="13" width="7" height="7" rx="1.5"/></svg>',
  list:'<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round"><path d="M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01"/></svg>',
  open:'<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/></svg>',
  dl:'<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12M8 11l4 4 4-4M4 19h16"/></svg>',
  ask:'<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>',
  ren:'<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/></svg>',
  del:'<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/></svg>',
  sel:'<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="4"/><path d="M8.5 12l2.5 2.5 4.5-5"/></svg>',
};
const jfHuman = (b)=>fmtSize(b);
const jfDate = (ms)=>{ try{ return new Date(ms).toLocaleString([], {dateStyle:'medium', timeStyle:'short'}); }catch(_){ return ''; } };

async function jfPost(url, body){
  const r=await jfetch(url, {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body||{})});
  const j=await r.json().catch(()=>({}));
  if(!r.ok || !j.ok) throw new Error(j.error || ('HTTP '+r.status));
  return j;
}
const jfFail = (e)=>toast(t('jean.files_error')+((e&&e.message)||''));

function openJeanFiles(){
  if(typeof closeMenu==='function') closeMenu(true, true);
  showModal('jean-files-modal');
  jfList(JF.dir);
}
function closeJeanFiles(){
  jfCloseViewer();
  jfClearSel();
  hideModal('jean-files-modal');
}

async function jfList(dir){
  if(dir!==JF.dir){ JF.sel.clear(); JF.anchor=null; jfEl('jf-filter').value=''; JF.search=null; }
  JF.dir=dir;
  let d;
  try{ d=await jfPost('/api/jean/files', {dir}); }
  catch(e){
    if(dir){ JF.dir=''; return jfList(''); } // dossier disparu entre-temps : retour à la racine
    JF.entries=[]; jfCrumbs(); jfRender(e.message); return;
  }
  JF.entries=d.entries||[];
  JF.count = d.meaning ? t('jean.files_count').replace('{n}', d.indexed).replace('{t}', d.total) : t('jean.files_count_off');
  const names=new Set(JF.entries.map(e=>e.name));
  for(const n of [...JF.sel]) if(!names.has(n)) JF.sel.delete(n);
  jfCrumbs();
  jfRender();
}

// Le chemin : chaque dossier est un bouton pour y revenir (et une cible de dépôt).
function jfCrumbs(){
  const nav=jfEl('jf-crumbs'); nav.textContent='';
  const parts=JF.dir ? JF.dir.split('/') : [];
  [[t('jean.files_root'), ''], ...parts.map((p,i)=>[p, parts.slice(0,i+1).join('/')])].forEach(([label, to], i, all)=>{
    if(i){ const s=document.createElement('i'); s.textContent='›'; nav.appendChild(s); }
    const b=document.createElement('button'); b.type='button'; b.textContent=label;
    if(i===all.length-1) b.setAttribute('aria-current','page');
    b.onclick=()=>{ if(to!==JF.dir) jfList(to); };
    b.dataset.rel=to; b.dataset.drop='1';
    nav.appendChild(b);
  });
  nav.scrollLeft=nav.scrollWidth;
  jfEl('jf-back').hidden=!JF.dir;
}

// Les entrées montrées : filtrées par le nom, triées (dossiers d'abord).
function jfShown(){
  const q=jfEl('jf-filter').value.trim().toLowerCase();
  const k=JF.sort, m=JF.desc ? -1 : 1;
  return (JF.entries||[]).filter(e=>!q || e.name.toLowerCase().includes(q)).sort((a,b)=>{
    if(a.dir!==b.dir) return a.dir ? -1 : 1;
    const c = k==='name' ? a.name.localeCompare(b.name, undefined, {numeric:true, sensitivity:'base'}) : ((a[k]-b[k]) || a.name.localeCompare(b.name));
    return c*m;
  });
}

function jfThumbObserver(){
  if(JF.io || typeof IntersectionObserver!=='function') return JF.io;
  JF.io=new IntersectionObserver((ents)=>{
    for(const en of ents){
      if(!en.isIntersecting) continue;
      JF.io.unobserve(en.target);
      jfLoadThumb(en.target);
    }
  }, {root:jfEl('jf-list'), rootMargin:'300px'});
  return JF.io;
}
// Vignette d'une image : calculée par le serveur (~560 px), gardée pour les
// prochains passages dans ce dossier.
function jfLoadThumb(img){
  const key=img.dataset.key, path=img.dataset.path;
  const ready=JF.thumbs.get(key);
  if(ready){ img.src=ready; return; }
  getWorkspaceThumb(path).then(b=>{
    const u=URL.createObjectURL(b);
    JF.thumbs.set(key, u);
    if(JF.thumbs.size>400){ const [k0,u0]=JF.thumbs.entries().next().value; JF.thumbs.delete(k0); try{ URL.revokeObjectURL(u0); }catch(_){} }
    if(img.isConnected) img.src=u;
  }).catch(()=>{ const box=img.parentNode; if(box){ img.remove(); box.innerHTML=fileIconSvg(img.dataset.name||''); } });
}

function jfRender(err){
  const list=jfEl('jf-list');
  list.classList.toggle('grid', JF.grid);
  jfEl('jean-files-modal').classList.toggle('jf-gridview', JF.grid);
  const vb=jfEl('jf-view'); vb.innerHTML=JF.grid ? JF_IC.list : JF_IC.grid;
  vb.title=vb.ariaLabel=JF.grid ? t('jean.files_view_list') : t('jean.files_view_grid');
  document.querySelectorAll('#jf-cols button').forEach(b=>{ b.classList.toggle('on', b.dataset.sort===JF.sort); b.classList.toggle('desc', JF.desc); });
  jfEl('jf-count').textContent=JF.busy ? JF.busy : (JF.count||'');
  list.textContent='';
  if(JF.search){ jfRenderSearch(list); jfSelBar(); return; }
  const entries=err ? [] : jfShown();
  if(!entries.length){
    const em=document.createElement('div'); em.className='jf-empty';
    em.textContent = err || (jfEl('jf-filter').value.trim() ? t('jean.files_no_match') : t('jean.files_empty')+(jfFine.matches ? ' '+t('jean.files_empty_drop') : ''));
    list.appendChild(em);
  }
  const io=jfThumbObserver();
  for(const e of entries){
    const rel=jfRel(e.name);
    const row=document.createElement('div'); row.className='jf-row';
    row.innerHTML='<button type="button" class="jf-main"><span class="jf-ficon"></span><span class="jf-name"><b></b><span></span></span><span class="jf-size"></span><span class="jf-date"></span></button><button type="button" class="jf-more"></button>';
    row.dataset.name=e.name; row.dataset.rel=rel;
    if(e.dir){ row.dataset.dir='1'; row.dataset.drop='1'; }
    if(JF.sel.has(e.name)) row.classList.add('sel');
    const more=row.querySelector('.jf-more');
    more.innerHTML=JF_IC.more; more.title=more.ariaLabel=t('jean.files_actions');
    more.addEventListener('pointerdown', ev=>ev.stopPropagation()); // pas un début de glisser
    more.onclick=(ev)=>{ ev.stopPropagation(); jfMenu(more, e); };
    const icon=row.querySelector('.jf-ficon');
    if(!e.dir && isImageName(e.name)){
      const img=document.createElement('img');
      img.className='jf-img'; img.alt=e.name; img.draggable=false;
      img.dataset.path=jfWS(rel); img.dataset.name=e.name; img.dataset.key=rel+'|'+e.mod;
      icon.appendChild(img);
      if(io) io.observe(img); else jfLoadThumb(img);
    } else icon.innerHTML = e.dir ? JF_IC.dir : fileIconSvg(e.name);
    if(e.dir) icon.classList.add('dir');
    row.querySelector('b').textContent=e.name;
    row.querySelector('.jf-name span').textContent = e.dir ? t('jean.files_folder') : jfHuman(e.size)+' · '+new Date(e.mod).toLocaleDateString();
    row.querySelector('.jf-size').textContent = e.dir ? '' : jfHuman(e.size);
    row.querySelector('.jf-date').textContent = jfDate(e.mod);
    const main=row.querySelector('.jf-main');
    main.title=e.name;
    const open=()=>jfOpenEntry(e, row);
    main.onclick=(ev)=>{
      if(JF_DRAG.suppress){ JF_DRAG.suppress=false; return; } // fin d'un glisser, pas un toucher
      if(!jfFine.matches && !JF.sel.size) return open();
      jfSelect(e.name, ev);
    };
    main.ondblclick=open;
    main.onkeydown=(ev)=>{ if(ev.key==='Enter'){ ev.preventDefault(); open(); } };
    row.addEventListener('contextmenu', ev=>{
      if(!jfFine.matches) return;
      ev.preventDefault();
      if(!JF.sel.has(e.name)) jfSelect(e.name, {});
      jfMenuAt(ev.clientX, ev.clientY, e);
    });
    jfDraggable(row, rel, e.name);
    list.appendChild(row);
  }
  if(entries.length>1 && !jfFine.matches && jfHintLeft()){
    const h=document.createElement('div'); h.className='jf-hint'; h.textContent=t('jean.files_hint_move'); list.appendChild(h);
  }
  jfSelBar();
}
// L'astuce du glisser est montrée trois fois au plus, puis plus jamais une fois utilisée.
function jfHintLeft(){ try{ if(localStorage.getItem('jf-moved')==='1') return false; const n=+localStorage.getItem('jf-hint')||0; localStorage.setItem('jf-hint', n+1); return n<3; }catch(_){ return false; } }

// Ouvrir : un dossier s'affiche, une image passe dans la visionneuse (les autres
// images du dossier à portée de flèche), le reste dans l'aperçu.
function jfOpenEntry(e, row){
  if(e.dir){ jfList(jfRel(e.name)); return; }
  const img=row && row.querySelector('img.jf-img');
  if(img && typeof openLightbox==='function'){
    if(img.naturalWidth){ openLightbox(img); return; }
    img.addEventListener('load', ()=>openLightbox(img), {once:true});
    jfLoadThumb(img);
    return;
  }
  jfView(e);
}

// ---- sélection, comme dans un gestionnaire de fichiers de bureau ----
function jfSelect(name, ev){
  const names=jfShown().map(e=>e.name);
  if(ev.shiftKey && JF.anchor!==null && names.includes(JF.anchor)){
    const [a,b]=[names.indexOf(JF.anchor), names.indexOf(name)].sort((x,y)=>x-y);
    if(!ev.ctrlKey && !ev.metaKey) JF.sel.clear();
    names.slice(a, b+1).forEach(n=>JF.sel.add(n));
  } else if(ev.ctrlKey || ev.metaKey || (!jfFine.matches && JF.sel.size) || ev.toggle){
    JF.sel.has(name) ? JF.sel.delete(name) : JF.sel.add(name);
    JF.anchor=name;
  } else { JF.sel.clear(); JF.sel.add(name); JF.anchor=name; }
  document.querySelectorAll('#jf-list .jf-row').forEach(r=>r.classList.toggle('sel', JF.sel.has(r.dataset.name)));
  jfSelBar();
}
function jfClearSel(){
  JF.sel.clear(); JF.anchor=null;
  document.querySelectorAll('#jf-list .jf-row.sel').forEach(r=>r.classList.remove('sel'));
  jfSelBar();
}
const jfSelected = ()=>(JF.entries||[]).filter(e=>JF.sel.has(e.name));
function jfSelBar(){
  const sel=jfSelected(), n=sel.length, bar=jfEl('jf-sel');
  bar.classList.toggle('show', n>0 && !JF.search);
  jfEl('jf-seln').textContent = n===1 ? t('jean.files_sel_one') : t('jean.files_sel_many').replace('{n}', n);
  bar.querySelector('[data-bulk="rename"]').hidden = n!==1;
  bar.querySelector('[data-bulk="dl"]').hidden = !sel.some(e=>!e.dir);
  bar.querySelector('[data-bulk="ask"]').hidden = !sel.some(e=>!e.dir);
}
// Télécharger : chaque fichier à son tour (un dossier ne se télécharge pas d'un bloc).
async function jfDownload(entries){
  for(const e of entries.filter(x=>!x.dir)) await downloadWorkspaceFile(jfWS(jfRel(e.name)), e.name, null);
}
async function jfBulk(act){
  const sel=jfSelected();
  if(!sel.length && act!=='none') return;
  switch(act){
    case 'none': jfClearSel(); break;
    case 'dl': jfDownload(sel); break;
    case 'ask': jfAsk(sel); break;
    case 'rename': if(await jfRename(jfRel(sel[0].name), sel[0].name, sel[0].dir)!==null){ jfClearSel(); jfList(JF.dir); } break;
    case 'delete': if(await jfRemove(sel)){ jfClearSel(); jfList(JF.dir); } break;
  }
}

// Les actions d'une ligne (⋯), ou de la sélection quand elle en compte plusieurs.
function jfMenuItems(e){
  const many=JF.sel.size>1 && JF.sel.has(e.name);
  if(many) return [
    {icon:JF_IC.dl, label:t('jean.files_download'), run:()=>jfBulk('dl')},
    {icon:JF_IC.ask, label:t('jean.files_ask'), run:()=>jfBulk('ask')},
    '-',
    {icon:JF_IC.del, label:t('jean.files_delete_n').replace('{n}', JF.sel.size), danger:true, run:()=>jfBulk('delete')},
  ];
  const rel=jfRel(e.name);
  return [
    {icon:JF_IC.open, label:e.dir ? t('jean.files_open') : t('jean.files_preview'), run:()=>jfOpenEntry(e, [...document.querySelectorAll('#jf-list .jf-row')].find(r=>r.dataset.name===e.name))},
    !e.dir && {icon:JF_IC.dl, label:t('jean.files_download'), run:()=>downloadWorkspaceFile(jfWS(rel), e.name, null)},
    !e.dir && {icon:JF_IC.ask, label:t('jean.files_ask'), run:()=>jfAsk([e])},
    !jfFine.matches && {icon:JF_IC.sel, label:t('jean.files_select'), run:()=>jfSelect(e.name, {toggle:true})},
    '-',
    {icon:JF_IC.ren, label:t('jean.files_rename'), run:async()=>{ if(await jfRename(rel, e.name, e.dir)!==null) jfList(JF.dir); }},
    {icon:JF_IC.del, label:t('jean.files_delete'), danger:true, run:async()=>{ if(await jfRemove([e])) jfList(JF.dir); }},
  ];
}
function jfMenu(anchor, e){ popMenu(anchor, jfMenuItems(e), {align:'right', minW:200}); }
// Clic droit : le menu là où est le pointeur (une ancre invisible d'un pixel).
function jfMenuAt(x, y, e){
  const a=document.createElement('span');
  a.style.cssText='position:fixed;width:1px;height:1px;left:'+x+'px;top:'+y+'px;pointer-events:none';
  document.body.appendChild(a);
  popMenu(a, jfMenuItems(e), {align:'left', minW:200, gap:2, onClose:()=>setTimeout(()=>a.remove(), 300)});
}

// remove : supprime, une fois confirmé (en rouge, et dit définitif).
async function jfRemove(entries){
  if(!entries.length) return false;
  const dirs=entries.some(e=>e.dir);
  const title = entries.length===1 ? t('jean.files_del_one').replace('{name}', entries[0].name) : t('jean.files_del_many').replace('{n}', entries.length);
  const ok=await askConfirm(dirs ? t('jean.files_del_dir_msg') : t('jean.files_del_msg'), {title, okText:t('jean.files_delete'), danger:true});
  if(!ok) return false;
  try{ await jfPost('/api/jean/files/delete', {paths:entries.map(e=>jfRel(e.name))}); toast(t('jean.files_deleted')); return true; }
  catch(e){ jfFail(e); return true; } // une partie a pu partir : on rafraîchit quand même
}
// rename : l'ancien nom prêt à changer ; l'extension reste si on n'en tape pas.
// Renvoie le nom gardé par le serveur, ou null.
async function jfRename(rel, name, dir){
  const dot = dir ? -1 : name.lastIndexOf('.');
  const base = dot>0 ? name.slice(0, dot) : name;
  const typed=await askPrompt('', {title:t('jean.files_rename'), default:base, okText:t('jean.files_rename')});
  if(typed===null || !typed.trim() || typed===base || typed===name) return null;
  try{ const r=await jfPost('/api/jean/files/rename', {path:rel, name:typed}); toast(t('jean.files_renamed')); return r.name; }
  catch(e){ jfFail(e); return null; }
}
async function jfMkdir(){
  const name=await askPrompt('', {title:t('jean.files_mkdir'), placeholder:t('jean.files_mkdir_ph'), okText:t('jean.files_create')});
  if(!name || !name.trim()) return;
  try{ await jfPost('/api/jean/files/mkdir', {dir:JF.dir, name}); }catch(e){ jfFail(e); }
  jfList(JF.dir);
}

// « Demander à Jean » : le fichier part avec le prochain message, sans être
// recopié (il reste dans son dossier) ; la question reste à écrire.
function jfAsk(entries){
  const files=entries.filter(e=>!e.dir);
  if(!files.length) return;
  for(const e of files){
    const path=jfWS(jfRel(e.name));
    if(ATTACH.some(a=>a.path===path)) continue;
    const rec={id:++ATTACH_SEQ, name:e.name, size:e.size, file:null, path, state:'ok'};
    ATTACH.push(rec);
    if(isImageName(e.name)) getWorkspaceThumb(path).then(b=>{ rec.thumb=URL.createObjectURL(b); renderAttach(); }).catch(()=>{});
  }
  renderAttach();
  closeJeanFiles();
  // Dans la scène, les pièces jointes ne se voient pas : on passe au fil complet,
  // où le composeur les montre.
  const b=document.body;
  if(b.classList.contains('jean-mode') && !b.classList.contains('jean-history')){ const h=document.querySelector('.jst-hist'); if(h) h.click(); }
  setTimeout(()=>{ const i=jfEl('input'); if(i) i.focus(); }, 380);
  toast(t('jean.files_attached'));
}

// ---- envoi ------------------------------------------------------------------------
// Par morceaux de 8 Mo comme les pièces jointes du chat (sendChunk), rangés dans
// le dossier affiché. Un fichier du même nom et du même contenu n'est pas recopié.
async function jfUpload(files){
  files=[...(files||[])].filter(f=>{
    if(!f.size){ toast(t('attach.quote_open')+f.name+t('attach.quote_close')+t('attach.empty_middle')); return false; }
    if(f.size>ATTACH_MAX){ toast(t('attach.quote_open')+f.name+t('attach.quote_close')+t('attach.too_big_middle')+fmtSize(f.size)+t('attach.too_big_max')+fmtSize(ATTACH_MAX)); return false; }
    return true;
  });
  if(!files.length) return;
  const dir=JF.dir, total=files.reduce((s,f)=>s+f.size, 0);
  let sent=0, failed=0;
  const show=()=>{ JF.busy=t('jean.files_uploading').replace('{p}', Math.round(sent*100/Math.max(1,total))); const c=jfEl('jf-count'); if(c) c.textContent=JF.busy; };
  show();
  for(const f of files){
    try{
      let id='', off=0;
      do{
        const end=Math.min(off+ATTACH_CHUNK, f.size);
        const data=await readAsBase64(f.slice(off, end));
        const last=end>=f.size;
        const payload={name:f.name, data, id, more:!last, size:(id?0:f.size)};
        if(!id) payload.dest=dir;
        const j=await sendChunk(payload);
        if(j.id) id=j.id;
        sent+=end-off; off=end; show();
      } while(off<f.size);
    }catch(e){ failed++; toast(t('attach.quote_open')+f.name+t('attach.quote_close')+' : '+((e&&e.message)||t('attach.failed'))); }
  }
  JF.busy=false;
  if(!failed) toast(files.length===1 ? t('jean.files_added_one') : t('jean.files_added_many').replace('{n}', files.length));
  if(JF.dir===dir) jfList(dir); else jfRender();
}

// ---- recherche dans le contenu ------------------------------------------------------
// Entrée dans le champ : les fichiers qui parlent de ce qu'on a tapé, par le sens
// (ce que Jean y a lu), où qu'ils soient dans le dossier.
async function jfSearch(q){
  q=String(q||'').trim();
  if(!q){ JF.search=null; jfRender(); return; }
  JF.search={q, hits:null, by:''};
  jfRender();
  try{
    const r=await jfPost('/api/jean/files/search', {q});
    if(!JF.search || JF.search.q!==q) return;
    JF.search.hits=r.hits||[]; JF.search.by=r.by||'';
  }catch(e){ if(JF.search && JF.search.q===q){ JF.search.hits=[]; JF.search.err=e.message; } }
  jfRender();
}
function jfRenderSearch(list){
  const s=JF.search;
  const head=document.createElement('div'); head.className='jf-shead';
  head.innerHTML='<span></span><button type="button" class="jf-ic" aria-label="×"><svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg></button>';
  head.firstChild.textContent = t('jean.files_search_title')+(s.by==='name' ? ' '+t('jean.files_search_by_name') : '');
  head.lastChild.onclick=()=>{ jfEl('jf-filter').value=''; jfSearch(''); };
  list.appendChild(head);
  if(!s.hits){ const w=document.createElement('div'); w.className='jf-empty'; w.textContent=t('jean.files_searching'); list.appendChild(w); return; }
  if(!s.hits.length){ const w=document.createElement('div'); w.className='jf-empty'; w.textContent=s.err || t('jean.files_search_none'); list.appendChild(w); return; }
  for(const h of s.hits){
    const row=document.createElement('button'); row.type='button'; row.className='jf-hit';
    row.innerHTML='<span class="jf-ficon"></span><span class="jf-name"><b></b><span class="jf-hit-dir"></span><span class="jf-hit-p"></span></span>';
    const name=h.path.split('/').pop(), dir=jfUpDir(h.path);
    row.querySelector('.jf-ficon').innerHTML=fileIconSvg(name);
    row.querySelector('b').textContent=name;
    row.querySelector('.jf-hit-dir').textContent=dir ? dir+'/' : t('jean.files_root');
    row.querySelector('.jf-hit-p').textContent=h.passage||'';
    row.onclick=()=>jfReveal(h.path);
    list.appendChild(row);
  }
}
// Ouvre le dossier d'un fichier trouvé, puis le fichier lui-même.
async function jfReveal(rel){
  const dir=jfUpDir(rel), name=rel.split('/').pop();
  jfEl('jf-filter').value=''; JF.search=null;
  await jfList(dir);
  const e=(JF.entries||[]).find(x=>x.name===name);
  if(!e) return;
  JF.sel.clear(); JF.sel.add(name); JF.anchor=name;
  const row=[...document.querySelectorAll('#jf-list .jf-row')].find(r=>r.dataset.name===name);
  if(row){ row.classList.add('sel'); row.scrollIntoView({block:'nearest'}); }
  jfSelBar();
  jfOpenEntry(e, row);
}

// ---- aperçu (texte, Markdown, PDF, vidéo, son) ----------------------------------
// Le fichier est lu comme données : un .html s'affiche en source, jamais rendu
// (il s'exécuterait dans l'origine de l'app).
async function jfView(e){
  const rel=jfRel(e.name), path=jfWS(rel);
  const v=jfEl('jf-viewer'), body=jfEl('jf-vbody');
  jfRevokeView();
  JF.open={rel, name:e.name, entry:e};
  const open=JF.open;
  jfEl('jf-vname').textContent=e.name;
  jfEl('jf-vmeta').textContent=jfHuman(e.size)+' · '+jfDate(e.mod);
  body.className='jf-vbody'; body.textContent='';
  v.classList.add('show');
  const kind = /\.pdf$/i.test(e.name) ? 'pdf' : JF_TEXT.test(e.name) && e.size<=(1<<20) ? 'text'
    : JF_VIDEO.test(e.name) && e.size<=(300<<20) ? 'video' : JF_AUDIO.test(e.name) && e.size<=(200<<20) ? 'audio' : '';
  if(!kind){ jfNoPreview(body, e); return; }
  const wait=document.createElement('div'); wait.className='jf-empty'; wait.textContent=t('jean.files_loading'); body.appendChild(wait);
  let blob;
  try{ blob=await getWorkspaceBlob(path); }
  catch(err){ if(JF.open===open){ wait.textContent=t('jean.files_error')+err.message; } return; }
  if(JF.open!==open) return; // fermé ou autre fichier ouvert entre-temps
  body.textContent='';
  if(kind==='text'){
    const text=await blob.text();
    if(JF.open!==open) return;
    if(/\.(md|markdown)$/i.test(e.name)){
      const d=document.createElement('div'); d.className='jf-md body'; d.innerHTML=md(text); body.appendChild(d);
      return;
    }
    const lines=text.replace(/\n$/, '').split('\n');
    const pre=document.createElement('pre'); pre.className='jf-code';
    pre.style.setProperty('--nw', String(lines.length).length+'ch');
    lines.forEach((l, i)=>{
      const n=document.createElement('span'); n.className='n'; n.textContent=i+1;
      pre.appendChild(n); pre.appendChild(document.createTextNode(l+'\n'));
    });
    body.appendChild(pre);
    return;
  }
  const type = kind==='pdf' ? 'application/pdf' : (blob.type && blob.type!=='application/octet-stream' ? blob.type : '');
  JF.viewURL=URL.createObjectURL(type ? new Blob([blob], {type}) : blob);
  if(kind==='pdf'){
    body.classList.add('pdf');
    const f=document.createElement('iframe'); f.className='jf-pdf'; f.title=e.name; f.src=JF.viewURL; body.appendChild(f);
  } else {
    const m=document.createElement(kind); m.controls=true; m.src=JF.viewURL; m.className='jf-media'; m.setAttribute('playsinline', '');
    body.appendChild(m);
  }
}
function jfNoPreview(body, e){
  body.innerHTML='<div class="jf-nopv"><span class="jf-ficon big"></span><div></div></div>';
  body.querySelector('.jf-ficon').innerHTML=fileIconSvg(e.name);
  body.querySelector('.jf-nopv div').textContent=t('jean.files_no_preview');
}
function jfRevokeView(){ if(JF.viewURL){ const u=JF.viewURL; JF.viewURL=null; setTimeout(()=>{ try{ URL.revokeObjectURL(u); }catch(_){} }, 1000); } }
function jfCloseViewer(){
  const v=jfEl('jf-viewer'); if(!v) return;
  v.classList.remove('show');
  JF.open=null; jfRevokeView();
  setTimeout(()=>{ if(!v.classList.contains('show')) jfEl('jf-vbody').textContent=''; }, 200);
}
async function jfViewerAct(act){
  const o=JF.open; if(!o) return;
  if(act==='dl') downloadWorkspaceFile(jfWS(o.rel), o.name, null);
  else if(act==='ask') jfAsk([o.entry]);
  else if(act==='rename'){
    const name=await jfRename(o.rel, o.name, false);
    if(name===null) return;
    o.rel=jfRel(name); o.name=name; o.entry={...o.entry, name};
    jfEl('jf-vname').textContent=name;
    jfList(JF.dir);
  } else if(act==='delete'){
    if(await jfRemove([o.entry])){ jfCloseViewer(); jfList(JF.dir); }
  }
}

// ---- ranger par glisser-déposer ------------------------------------------------------
// Une ligne se prend par un appui long (un appui court à la souris), suit le doigt,
// et se dépose sur un dossier, sur le chemin, ou sur ‹ pour le dossier parent.
const JF_DRAG={item:null, ghost:null, target:null, timer:0, x:0, y:0, suppress:false};
document.addEventListener('touchmove', e=>{ if(JF_DRAG.item) e.preventDefault(); }, {passive:false}); // pas de défilement pendant un glisser
function jfDropTarget(x, y){
  JF_DRAG.ghost.style.display='none';
  const el=document.elementFromPoint(x, y);
  JF_DRAG.ghost.style.display='';
  if(!el) return null;
  const row=el.closest('#jf-list .jf-row[data-dir]');
  if(row && row.dataset.rel!==JF_DRAG.item.rel && !JF.sel.has(row.dataset.name)) return {el:row, to:row.dataset.rel};
  if(el.closest('#jf-back') && JF.dir) return {el:jfEl('jf-back'), to:jfUpDir(JF.dir)};
  const crumb=el.closest('#jf-crumbs button');
  if(crumb && crumb.dataset.rel!==JF.dir) return {el:crumb, to:crumb.dataset.rel};
  return null;
}
function jfEndDrag(drop){
  clearTimeout(JF_DRAG.timer);
  if(!JF_DRAG.item) return;
  const {item, target}=JF_DRAG;
  JF_DRAG.ghost.remove();
  item.row.classList.remove('lifted');
  if(target) target.el.classList.remove('drop-here');
  JF_DRAG.item=JF_DRAG.ghost=JF_DRAG.target=null;
  if(drop && target){
    // La sélection entière voyage quand la ligne prise en fait partie.
    const paths = JF.sel.has(item.name) && JF.sel.size>1 ? jfSelected().map(e=>jfRel(e.name)) : [item.rel];
    jfPost('/api/jean/files/move', {paths, to:target.to})
      .then(()=>{ try{ localStorage.setItem('jf-moved', '1'); }catch(_){} toast(t('jean.files_moved')); }, jfFail)
      .finally(()=>{ jfClearSel(); jfList(JF.dir); });
  }
}
function jfDraggable(row, rel, name){
  row.addEventListener('pointerdown', e=>{
    if(e.button>0) return;
    JF_DRAG.x=e.clientX; JF_DRAG.y=e.clientY;
    const pid=e.pointerId;
    clearTimeout(JF_DRAG.timer);
    JF_DRAG.timer=setTimeout(()=>{
      JF_DRAG.item={row, rel, name};
      try{ row.setPointerCapture(pid); }catch(_){}
      const g=JF_DRAG.ghost=document.createElement('div');
      g.className='jf-ghost';
      g.textContent = JF.sel.has(name) && JF.sel.size>1 ? t('jean.files_sel_many').replace('{n}', JF.sel.size) : name;
      document.body.appendChild(g);
      g.style.transform='translate('+(JF_DRAG.x+12)+'px,'+(JF_DRAG.y-24)+'px)';
      row.classList.add('lifted');
      if(navigator.vibrate) navigator.vibrate(12);
    }, e.pointerType==='mouse' ? 220 : 400);
  });
  row.addEventListener('pointermove', e=>{
    if(!JF_DRAG.item){ // avant la prise, un mouvement est un défilement
      if(Math.hypot(e.clientX-JF_DRAG.x, e.clientY-JF_DRAG.y)>8) clearTimeout(JF_DRAG.timer);
      return;
    }
    if(JF_DRAG.item.row!==row) return;
    e.preventDefault();
    JF_DRAG.ghost.style.transform='translate('+(e.clientX+12)+'px,'+(e.clientY-24)+'px)';
    const tg=jfDropTarget(e.clientX, e.clientY);
    if((tg&&tg.el)!==(JF_DRAG.target&&JF_DRAG.target.el)){
      if(JF_DRAG.target) JF_DRAG.target.el.classList.remove('drop-here');
      if(tg) tg.el.classList.add('drop-here');
      JF_DRAG.target=tg;
    }
  });
  row.addEventListener('pointerup', ()=>{
    clearTimeout(JF_DRAG.timer);
    if(JF_DRAG.item && JF_DRAG.item.row===row){ JF_DRAG.suppress=true; jfEndDrag(true); setTimeout(()=>{ JF_DRAG.suppress=false; }, 350); }
  });
  row.addEventListener('pointercancel', ()=>jfEndDrag(false));
  row.addEventListener('contextmenu', e=>{ if(!jfFine.matches) e.preventDefault(); }); // l'appui long ne doit pas ouvrir le menu du système
}

// ---- branchements ---------------------------------------------------------------------
document.addEventListener('DOMContentLoaded', ()=>{
  const m=jfEl('jean-files-modal'); if(!m) return;
  jfEl('jf-filter').addEventListener('input', ()=>{ if(JF.search) JF.search=null; jfRender(); });
  jfEl('jf-filter').addEventListener('keydown', e=>{
    if(e.key==='Enter'){ e.preventDefault(); jfSearch(e.target.value); }
    else if(e.key==='Escape' && e.target.value){ e.preventDefault(); e.stopPropagation(); e.target.value=''; JF.search=null; jfRender(); }
  });
  jfEl('jf-view').onclick=()=>{ JF.grid=!JF.grid; jfSavePrefs(); jfRender(); };
  document.querySelectorAll('#jf-cols button').forEach(b=>b.onclick=()=>{
    if(JF.sort===b.dataset.sort) JF.desc=!JF.desc; else { JF.sort=b.dataset.sort; JF.desc=b.dataset.sort!=='name'; }
    jfSavePrefs(); jfRender();
  });
  jfEl('jf-sel').addEventListener('click', e=>{ const b=e.target.closest('[data-bulk]'); if(b) jfBulk(b.dataset.bulk); });
  jfEl('jf-list').addEventListener('click', e=>{ if(e.target===jfEl('jf-list') && jfFine.matches) jfClearSel(); });
  // Fichiers glissés depuis l'ordinateur : envoyés dans le dossier affiché.
  const card=m.querySelector('.jf-card');
  card.addEventListener('dragover', e=>{ if(e.dataTransfer && [...e.dataTransfer.types].includes('Files')){ e.preventDefault(); e.stopPropagation(); card.classList.add('dropping'); } });
  card.addEventListener('dragleave', e=>{ if(!card.contains(e.relatedTarget)) card.classList.remove('dropping'); });
  card.addEventListener('drop', e=>{
    if(!e.dataTransfer || !e.dataTransfer.files.length) return;
    e.preventDefault(); e.stopPropagation();
    card.classList.remove('dropping');
    jfUpload(e.dataTransfer.files);
  });
});
// Le clavier, quand la fenêtre est au premier plan : flèches, Entrée ouvre, Retour
// arrière remonte, Suppr supprime, F2 renomme, Ctrl+A tout, Ctrl+F filtre, Échap ferme.
document.addEventListener('keydown', e=>{
  if(!jfIsOpen()) return;
  if((typeof LB!=='undefined' && LB && LB.open) || (jfEl('ask-modal') && jfEl('ask-modal').classList.contains('show')) || (typeof MENU!=='undefined' && MENU)) return;
  if(e.key==='Escape'){
    e.preventDefault();
    if(jfEl('jf-viewer').classList.contains('show')) jfCloseViewer();
    else if(JF.sel.size) jfClearSel();
    else closeJeanFiles();
    return;
  }
  if(jfEl('jf-viewer').classList.contains('show')) return;
  if(e.target.closest && e.target.closest('input, textarea, select')) return;
  const names=jfShown().map(x=>x.name);
  const at=JF.anchor!==null ? names.indexOf(JF.anchor) : -1;
  const go=(i)=>{
    if(!names.length) return;
    i=Math.max(0, Math.min(names.length-1, i));
    jfSelect(names[i], {shiftKey:e.shiftKey});
    if(!e.shiftKey) JF.anchor=names[i];
    const row=[...document.querySelectorAll('#jf-list .jf-row')].find(r=>r.dataset.name===names[i]);
    if(row){ row.scrollIntoView({block:'nearest'}); row.querySelector('.jf-main').focus({preventScroll:true}); }
  };
  const cols=JF.grid ? Math.max(1, Math.round(jfEl('jf-list').clientWidth/140)) : 1;
  switch(e.key){
    case 'ArrowDown': e.preventDefault(); go(at+cols); break;
    case 'ArrowUp': e.preventDefault(); go(at<0 ? 0 : at-cols); break;
    case 'ArrowRight': if(JF.grid){ e.preventDefault(); go(at+1); } break;
    case 'ArrowLeft': if(JF.grid){ e.preventDefault(); go(at-1); } break;
    case 'Enter': { const s=jfSelected(); if(s.length===1){ e.preventDefault(); jfOpenEntry(s[0], [...document.querySelectorAll('#jf-list .jf-row')].find(r=>r.dataset.name===s[0].name)); } break; }
    case 'Delete': if(JF.sel.size){ e.preventDefault(); jfBulk('delete'); } break;
    case 'F2': if(JF.sel.size===1){ e.preventDefault(); jfBulk('rename'); } break;
    case 'Backspace': if(JF.dir){ e.preventDefault(); jfList(jfUpDir(JF.dir)); } break;
    case 'a': if(e.ctrlKey||e.metaKey){ e.preventDefault(); names.forEach(n=>JF.sel.add(n)); jfRender(); } break;
    case 'f': if(e.ctrlKey||e.metaKey){ e.preventDefault(); jfEl('jf-filter').focus(); } break;
  }
});
