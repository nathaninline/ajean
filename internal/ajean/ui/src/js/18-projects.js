// 18-projects.js — les PROJETS. Un projet cloisonne une mémoire (pages .md +
// index MEMORY.md) ET ses sessions de chat. Le bouton dossier du composeur ouvre
// ce modal : sélectionner un projet bascule dessus (nouvelle session vierge,
// l'ancienne est archivée dans son projet), créer / renommer / supprimer aussi.
//
// Après une bascule, le serveur remet la conversation à zéro (nouvelle session) :
// le flux SSE reçoit un {reset} et l'UI se nettoie toute seule, comme un « clear
// chat ». On rafraîchit juste la liste mémoire des réglages et le libellé du bouton.

let PROJECTS = [], ACTIVE_PROJECT = '';

function openProjectHub(){ showModal('project-modal'); loadProjects(); }
function closeProjectModal(){ hideModal('project-modal'); }

// Met à jour le petit libellé du projet actif à côté de l'icône dossier.
function setProjectBtnName(name){
  const el = document.getElementById('project-btn-name');
  if(el) el.textContent = name || '';
}

// Récupère la liste + l'actif ; rend le modal s'il est ouvert et rafraîchit le bouton.
async function loadProjects(){
  let r; try{ r = await jget('/api/projects'); }catch(_){ r = null; }
  if(!r || !r.ok){ const box=document.getElementById('project-list'); if(box) box.innerHTML='<span class="muted" style="font-size:12px">erreur de chargement</span>'; return; }
  PROJECTS = r.projects || [];
  ACTIVE_PROJECT = r.active || '';
  const act = PROJECTS.find(p=>p.slug===ACTIVE_PROJECT);
  setProjectBtnName(act ? act.name : '');
  renderProjectList();
  loadProjectSessions();
}

// Dossier DUOTONE (corps rempli en fondu + tracé net) : rendu plus « produit »
// qu'un simple contour. Prend la couleur courante (accent sur la tuile active).
function projFolderSvg(sz){
  const s = sz || 34;
  return '<svg viewBox="0 0 24 24" width="'+s+'" height="'+s+'" fill="none" aria-hidden="true">'
    + '<path d="M3 8.6A2.6 2.6 0 0 1 5.6 6h3.5a1.4 1.4 0 0 1 1 .43L14.4 8.9h4A2.6 2.6 0 0 1 21 11.5V17a2.6 2.6 0 0 1-2.6 2.6H5.6A2.6 2.6 0 0 1 3 17z" fill="currentColor" opacity=".15"/>'
    + '<path d="M3 9.4A2.4 2.4 0 0 1 5.4 7h3.4a1.2 1.2 0 0 1 .85.35L11.3 9h6.3A2.4 2.4 0 0 1 20 11.4V17a2.4 2.4 0 0 1-2.4 2.4H5.4A2.4 2.4 0 0 1 3 17z" stroke="currentColor" stroke-width="1.5"/>'
    + '</svg>';
}
function projDotsSvg(){
  // Points VERTICAUX (⋮), plus discrets dans un coin.
  return '<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true"><circle cx="12" cy="5" r="1.7"/><circle cx="12" cy="12" r="1.7"/><circle cx="12" cy="19" r="1.7"/></svg>';
}
// Une carte-dossier de projet, dans une GRILLE. Cliquer bascule dessus ; le bouton
// « ⋯ » ouvre un petit menu (renommer / supprimer) — pas d'icônes brutes entassées.
function projectTile(p){
  const active = p.slug === ACTIVE_PROJECT;
  const tile = document.createElement('div'); tile.className = 'proj-tile' + (active?' active':'');
  tile.tabIndex = 0; tile.title = active ? 'Projet actif' : 'Basculer sur ce projet';
  if(!active){
    tile.onclick = ()=>switchProjectUI(p.slug);
    tile.onkeydown = (e)=>{ if((e.key==='Enter'||e.key===' ') && e.target===tile){ e.preventDefault(); switchProjectUI(p.slug); } };
  }
  if(active){ const badge = document.createElement('span'); badge.className='proj-badge'; badge.textContent='actif'; tile.appendChild(badge); }
  // Bouton menu ⋯
  const menu = document.createElement('button');
  menu.className='proj-menu-btn'; menu.innerHTML=projDotsSvg(); menu.title='Options du projet'; menu.setAttribute('aria-label','Options du projet');
  menu.onclick=(e)=>{ e.stopPropagation(); openProjMenu(menu, p); };
  tile.appendChild(menu);

  const icon = document.createElement('div'); icon.className = 'proj-ticon'; icon.innerHTML = projFolderSvg(36);
  const name = document.createElement('div'); name.className = 'proj-tname'; name.textContent = p.name || p.slug;
  tile.appendChild(icon); tile.appendChild(name);
  return tile;
}

// Petit menu contextuel d'un projet (renommer / supprimer), ancré au bouton ⋯.
let _projPop = null;
function _projOutside(e){
  // Ignore un clic sur un bouton ⋯ (sinon fermer+rouvrir) ou dans le menu.
  if(_projPop && (_projPop.contains(e.target) || (e.target.closest && e.target.closest('.proj-menu-btn,.sess-menu-btn')))) return;
  closeProjMenu();
}
function closeProjMenu(){ if(_projPop){ _projPop.remove(); _projPop=null; document.removeEventListener('click', _projOutside, true); document.removeEventListener('scroll', closeProjMenu, true); } }
function openProjMenu(anchor, p){
  closeProjMenu();
  const pop = document.createElement('div'); pop.className='pop-menu';
  const item = (icon, label, cls, fn)=>{ const b=document.createElement('button'); if(cls) b.className=cls; b.innerHTML=sessIconSvg(icon)+'<span>'+label+'</span>'; b.onclick=(e)=>{ e.stopPropagation(); closeProjMenu(); fn(); }; return b; };
  pop.appendChild(item('pencil', 'Renommer', '', ()=>renameProjectUI(p.slug, p.name)));
  pop.appendChild(item('doc', 'Décrire', '', ()=>describeProjectUI(p.slug, p.desc||'')));
  if(PROJECTS.length > 1) pop.appendChild(item('trash', 'Supprimer', 'danger', ()=>deleteProjectUI(p.slug, p.name)));
  document.body.appendChild(pop);
  // Positionne sous le bouton, calé à droite, en restant dans l'écran.
  const r = anchor.getBoundingClientRect();
  const pw = pop.offsetWidth, ph = pop.offsetHeight;
  let left = Math.min(r.right - pw, window.innerWidth - pw - 8);
  left = Math.max(8, left);
  let top = r.bottom + 6;
  if(top + ph > window.innerHeight - 8) top = r.top - ph - 6;
  pop.style.left = left+'px'; pop.style.top = top+'px';
  _projPop = pop;
  // Ferme au prochain clic ailleurs / défilement (capture pour attraper tôt).
  setTimeout(()=>{ document.addEventListener('click', _projOutside, true); document.addEventListener('scroll', closeProjMenu, true); }, 0);
}

function renderProjectList(){
  const box = document.getElementById('project-list'); if(!box) return;
  const cnt = document.getElementById('proj-count'); if(cnt) cnt.textContent = PROJECTS.length || '';
  box.className = 'proj-grid';
  box.innerHTML = '';
  if(!PROJECTS.length){ box.innerHTML='<span class="muted" style="font-size:12px">aucun projet</span>'; return; }
  PROJECTS.forEach(p=>box.appendChild(projectTile(p)));
}

// Basculer sur un projet : nouvelle session vierge côté serveur, la mémoire suit.
// Le modal RESTE ouvert : on met à jour la liste + les sessions du nouveau projet,
// pour pouvoir enchaîner (ouvrir une session, en créer une…). Le fil derrière est
// déjà remis à zéro (le flux SSE reçoit un reset).
async function switchProjectUI(slug){
  if(slug === ACTIVE_PROJECT) return;
  let r; try{ r = await jpost('/api/projects/switch', {slug}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'bascule impossible'); return; }
  ACTIVE_PROJECT = r.active || slug;
  // On relit la liste depuis le serveur (source de vérité) : le libellé du bouton et
  // la sélection active se mettent à jour même si le projet vient d'être créé et
  // n'était pas encore dans PROJECTS — sans avoir à rafraîchir la page.
  await loadProjects();
  const p = PROJECTS.find(x=>x.slug===ACTIVE_PROJECT);
  toast('projet : ' + (p ? p.name : ACTIVE_PROJECT));
  // Rafraîchit la liste des pages mémoire des réglages (elle est projet-scopée). On
  // vide d'abord le filtre de recherche : une requête laissée d'un projet fourni
  // filtrerait les notes du nouveau projet (jusqu'à tout masquer) sans qu'on le voie.
  const ms=document.getElementById('mem-search'); if(ms) ms.value='';
  if(typeof loadAgent === 'function') loadAgent();
}

// ===== Sessions du projet actif (dans le hub) ==============================
// Réutilise les mêmes endpoints /api/chat/history* que l'ancien modal, mais rend
// dans #project-sessions et rafraîchit ici. Ouvrir une session ferme le hub.
async function loadProjectSessions(){
  const box = document.getElementById('project-sessions'); if(!box) return;
  if(!box.children.length) box.innerHTML = '<span class="muted" style="font-size:12px">chargement…</span>';
  let list = [], active = '';
  try{ const r = await jget('/api/chat/history'); list = (r && r.conversations) || []; active = (r && r.active) || ''; }
  catch(_){ box.innerHTML = '<span class="muted" style="font-size:12px">erreur de chargement</span>'; return; }
  box.innerHTML = '';
  if(!list.length){ box.innerHTML = '<span class="muted" style="font-size:12px">aucune conversation dans ce projet. Commence à discuter, elle apparaîtra ici.</span>'; return; }
  const favs = list.filter(c=>c.fav), others = list.filter(c=>!c.fav);
  const section = (label)=>{ const h=document.createElement('div'); h.className='sess-head'; h.style.paddingLeft='8px'; h.textContent=label; box.appendChild(h); };
  if(favs.length){ section('Favoris'); favs.forEach(c=>box.appendChild(projSessionRow(c, c.id===active))); }
  if(others.length){ if(favs.length) section('Récentes'); others.forEach(c=>box.appendChild(projSessionRow(c, c.id===active))); }
}

function projSessionRow(c, active){
  const row = document.createElement('div'); row.className = 'sess-row' + (active?' active':'');
  if(!active){ row.tabIndex = 0; row.title = 'Ouvrir cette conversation';
    row.onclick = ()=>openProjSession(c.id);
    row.onkeydown = (e)=>{ if((e.key==='Enter'||e.key===' ') && e.target===row){ e.preventDefault(); openProjSession(c.id); } };
  }
  const info = document.createElement('div'); info.className = 'sess-info';
  const name = document.createElement('div'); name.className = 'sess-name'; name.textContent = c.title || 'Conversation';
  const meta = document.createElement('div'); meta.className = 'sess-meta';
  const n = c.turns || 0;
  meta.textContent = fmtHistDate(c.saved_at) + ' · ' + n + ' message' + (n>1?'s':'');
  info.appendChild(name); info.appendChild(meta);
  row.appendChild(info);
  // Menu ⋮ (Favori / Renommer / Supprimer) — comme les projets, plus de crayon/poubelle.
  const menu = document.createElement('button');
  menu.className='sess-menu-btn'; menu.innerHTML=projDotsSvg(); menu.title='Options'; menu.setAttribute('aria-label','Options');
  menu.onclick=(e)=>{ e.stopPropagation(); openSessMenu(menu, c, active); };
  row.appendChild(menu);
  return row;
}

// Menu contextuel d'une conversation (favori / renommer / déplacer / supprimer).
function openSessMenu(anchor, c, active){
  closeProjMenu();
  const pop = document.createElement('div'); pop.className='pop-menu';
  const item = (icon, label, cls, fn)=>{ const b=document.createElement('button'); if(cls) b.className=cls; b.innerHTML=sessIconSvg(icon)+'<span>'+label+'</span>'; b.onclick=(e)=>{ e.stopPropagation(); closeProjMenu(); fn(); }; return b; };
  pop.appendChild(item('star', c.fav?'Retirer des favoris':'Mettre en favori', '', ()=>favProjSession(c.id, !c.fav)));
  pop.appendChild(item('pencil', 'Renommer', '', ()=>renameProjSession(c.id, c.title)));
  // Déplacer vers un autre projet (issue #55) — désactivé pour la conversation en cours.
  if(PROJECTS.length > 1 && !active) pop.appendChild(item('move', 'Déplacer vers…', '', ()=>moveSessionUI(c.id, anchor)));
  // Exporter CETTE conversation (archivée), sans avoir à l'ouvrir : ?id=<session>.
  const exp = document.createElement('button');
  exp.innerHTML = '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12M8 11l4 4 4-4M4 19h16"/></svg><span>Exporter</span>';
  exp.onclick = (e)=>{ e.stopPropagation(); closeProjMenu(); if(typeof downloadExport==='function') downloadExport('/api/chat/export?id='+encodeURIComponent(c.id)); };
  pop.appendChild(exp);
  pop.appendChild(item('trash', 'Supprimer', 'danger', ()=>deleteProjSession(c.id, c.title)));
  document.body.appendChild(pop);
  const r = anchor.getBoundingClientRect();
  const pw = pop.offsetWidth, ph = pop.offsetHeight;
  let left = Math.max(8, Math.min(r.right - pw, window.innerWidth - pw - 8));
  let top = r.bottom + 6; if(top + ph > window.innerHeight - 8) top = r.top - ph - 6;
  pop.style.left = left+'px'; pop.style.top = top+'px';
  _projPop = pop;
  setTimeout(()=>{ document.addEventListener('click', _projOutside, true); document.addEventListener('scroll', closeProjMenu, true); }, 0);
}

async function openProjSession(id){
  let r; try{ r = await jpost('/api/chat/history/restore', {id}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'impossible d\'ouvrir'); return; }
  closeProjectModal();
  toast('conversation ouverte');
}
async function favProjSession(id, fav){
  let r; try{ r = await jpost('/api/chat/history/fav', {id, fav}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'impossible'); return; }
  loadProjectSessions();
}
async function renameProjSession(id, current){
  const name = await askPrompt('Nom de la conversation (laisser vide pour le nom automatique) :', {title:'Renommer la conversation', okText:'Enregistrer', default: current||'', placeholder:'ex. Config serveur GPU'});
  if(name===null) return;
  let r; try{ r = await jpost('/api/chat/history/rename', {id, title:name}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'renommage impossible'); return; }
  loadProjectSessions();
}
async function deleteProjSession(id, title){
  if(!await askConfirm('Supprimer définitivement « ' + (title || 'cette conversation') + ' » ? Cette action est irréversible.', {title:'Supprimer la conversation', okText:'Supprimer', danger:true})) return;
  let r; try{ r = await jpost('/api/chat/history/delete', {id}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'suppression impossible'); return; }
  loadProjectSessions();
}
// Nouvelle conversation vierge dans le projet actif (archive la courante).
async function newSessionUI(){
  let r; try{ r = await jpost('/api/chat/reset', {}); }catch(_){ toast('erreur réseau'); return; }
  if(!r || !r.ok){ toast('impossible'); return; }
  closeProjectModal();
  toast('nouvelle conversation');
}
// Vide les conversations du projet actif sauf les favoris et celle en cours.
async function clearProjectSessions(){
  if(!await askConfirm('Supprimer toutes les conversations de ce projet, sauf les favoris et celle en cours ? Cette action est irréversible.', {title:'Vider les conversations', okText:'Vider', danger:true})) return;
  let r; try{ r = await jpost('/api/chat/history/clear', {}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'suppression impossible'); return; }
  toast((r.deleted||0) + ' conversation' + ((r.deleted>1)?'s':'') + ' supprimée' + ((r.deleted>1)?'s':''));
  loadProjectSessions();
}

async function createProjectUI(){
  // Création via une petite fenêtre (comme le renommage), pas une barre inline.
  const name = await askPrompt('Nom du projet :', {title:'Nouveau projet', okText:'Créer', placeholder:'nom du projet'});
  if(name===null) return;               // annulé
  if(!name.trim()){ toast('nom vide'); return; }
  let r; try{ r = await jpost('/api/projects/create', {name}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'création impossible'); return; }
  // On bascule directement sur le nouveau projet (on le crée pour y travailler).
  await switchProjectUI(r.slug);
}

async function renameProjectUI(slug, current){
  const name = await askPrompt('Nouveau nom du projet :', {title:'Renommer le projet', okText:'Enregistrer', default: current||'', placeholder:'ex. Site vitrine'});
  if(name===null) return;
  if(!name.trim()){ toast('nom vide'); return; }
  let r; try{ r = await jpost('/api/projects/rename', {slug, name}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'renommage impossible'); return; }
  loadProjects();
}

// Décrire un projet : la description est fournie à l'IA en tête de chaque nouvelle
// conversation du projet (elle sait alors à quoi sert le projet, sans qu'on ait à
// le lui réexpliquer). Vide = efface la description.
async function describeProjectUI(slug, current){
  const desc = await askPrompt(
    'Décris ce projet : à quoi il sert, le contexte, les contraintes. Ce texte est donné à l\'IA au début de chaque conversation du projet.',
    {title:'Décrire le projet', okText:'Enregistrer', multiline:true, default: current||'', placeholder:'ex. Site vitrine d\'un client, stack Next.js, ton pro et sobre, deadline fin octobre…'});
  if(desc===null) return;
  let r; try{ r = await jpost('/api/projects/describe', {slug, desc}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'enregistrement impossible'); return; }
  toast(desc.trim() ? 'description enregistrée' : 'description effacée');
  loadProjects();
}

// Petit sélecteur de projet (pop-menu ancré au bouton), pour choisir une DESTINATION.
// Exclut excludeSlug (le projet source). Appelle onPick(slug) au choix.
function pickProjectPop(anchor, excludeSlug, onPick){
  closeProjMenu();
  const pop = document.createElement('div'); pop.className='pop-menu';
  const dests = PROJECTS.filter(p=>p.slug!==excludeSlug);
  if(!dests.length){ const b=document.createElement('button'); b.disabled=true; b.innerHTML='<span class="muted">aucun autre projet</span>'; pop.appendChild(b); }
  dests.forEach(p=>{
    const b=document.createElement('button');
    b.innerHTML = projFolderSvg(16) + '<span>' + (p.name||p.slug) + '</span>';
    b.onclick=(e)=>{ e.stopPropagation(); closeProjMenu(); onPick(p.slug); };
    pop.appendChild(b);
  });
  document.body.appendChild(pop);
  const r = anchor.getBoundingClientRect();
  const pw = pop.offsetWidth, ph = pop.offsetHeight;
  let left = Math.max(8, Math.min(r.right - pw, window.innerWidth - pw - 8));
  let top = r.bottom + 6; if(top + ph > window.innerHeight - 8) top = r.top - ph - 6;
  pop.style.left = left+'px'; pop.style.top = top+'px';
  _projPop = pop;
  setTimeout(()=>{ document.addEventListener('click', _projOutside, true); document.addEventListener('scroll', closeProjMenu, true); }, 0);
}

// Déplacer une conversation vers un autre projet (issue #55). La liste des sessions
// affichées appartient au projet ACTIF → destination = tout projet sauf l'actif.
async function moveSessionUI(id, anchor){
  pickProjectPop(anchor || document.body, ACTIVE_PROJECT, async(slug)=>{
    let r; try{ r = await jpost('/api/projects/move-session', {id, slug}); }catch(_){ toast('erreur réseau'); return; }
    if(!r.ok){ toast(r.error || 'déplacement impossible'); return; }
    toast('conversation déplacée vers ' + projName(slug));
    loadProjectSessions();
  });
}

// Nom d'affichage d'un projet depuis son slug (repli sur le slug).
function projName(slug){ const p = PROJECTS.find(x=>x.slug===slug); return p ? (p.name||p.slug) : slug; }

async function deleteProjectUI(slug, name){
  if(!await askConfirm('Supprimer le projet « ' + (name||slug) + ' » ? Sa mémoire ET toutes ses sessions seront définitivement effacées.', {title:'Supprimer le projet', okText:'Supprimer', danger:true})) return;
  let r; try{ r = await jpost('/api/projects/delete', {slug}); }catch(_){ toast('erreur réseau'); return; }
  if(!r.ok){ toast(r.error || 'suppression impossible'); return; }
  ACTIVE_PROJECT = r.active || ACTIVE_PROJECT;
  toast('projet supprimé');
  loadProjects();
  if(typeof loadAgent === 'function') loadAgent();
}

// ===== Menu « + » du composeur (machines + fichiers) =======================
let _plusPop = null;
// Ferme SAUF si le clic est sur le bouton + lui-même (sinon re-cliquer fermerait
// puis rouvrirait aussitôt) ou à l'intérieur du menu.
function _plusOutside(e){
  const btn = document.getElementById('plus-btn');
  if(_plusPop && (_plusPop.contains(e.target) || (btn && btn.contains(e.target)))) return;
  closePlusMenu();
}
function closePlusMenu(){
  if(_plusPop){ _plusPop.remove(); _plusPop=null;
    document.removeEventListener('click', _plusOutside, true);
    const b=document.getElementById('plus-btn'); if(b) b.classList.remove('open');
  }
}
function togglePlusMenu(e){
  if(e){ e.stopPropagation(); e.preventDefault(); }
  if(_plusPop){ closePlusMenu(); return; }
  const anchor = document.getElementById('plus-btn'); if(!anchor) return;
  const pop = document.createElement('div'); pop.className='pop-menu';
  const item = (svg, label, fn)=>{ const b=document.createElement('button'); b.innerHTML=svg+'<span>'+label+'</span>'; b.onclick=(ev)=>{ ev.stopPropagation(); closePlusMenu(); fn(); }; return b; };
  const icFile = '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21.4 11.05 12.25 20.2a5.5 5.5 0 0 1-7.78-7.78l9.19-9.19a3.67 3.67 0 1 1 5.18 5.18l-9.2 9.2a1.83 1.83 0 1 1-2.59-2.6l8.49-8.48"/></svg>';
  const icMachine = '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/></svg>';
  const icCompact = '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 9l4-4 4 4M20 15l-4 4-4-4M8 5v6M16 19v-6"/></svg>';
  pop.appendChild(item(icFile, 'Joindre un fichier', ()=>{ const inp=document.getElementById('attach-input'); if(inp) inp.click(); }));
  pop.appendChild(item(icMachine, 'Postes distants', ()=>{ if(typeof openNodeHub==='function') openNodeHub(); }));
  const icTracker = '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 19V5M4 19h16M8 16l3-4 3 2 4-6"/></svg>';
  pop.appendChild(item(icTracker, 'Trackers', ()=>{ if(typeof openTrackerHub==='function') openTrackerHub(); }));
  // « Compacter le contexte » : uniquement quand le contexte dépasse 50%.
  if(typeof COMPACT_AVAILABLE!=='undefined' && COMPACT_AVAILABLE){
    pop.appendChild(item(icCompact, 'Compacter le contexte', ()=>{ if(typeof compactContext==='function') compactContext(); }));
  }
  // « Passation » : l'IA résume la conversation (Objectif / Problématique /
  // Fichiers importants / Ce qui a raté / Prochaine étape) et pose le document
  // comme page mémoire datée. Ne vide rien, n'injecte rien — la reprise est une
  // demande explicite de l'utilisateur dans la session d'après.
  const icPassation = '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12M7 10l5 5 5-5M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2"/></svg>';
  pop.appendChild(item(icPassation, 'Passation (reprendre plus tard)', ()=>{ if(typeof makePassation==='function') makePassation(); }));
  document.body.appendChild(pop);
  // Positionne AU-DESSUS du bouton (le composeur est en bas de l'écran), calé à gauche.
  // Écart plus généreux pour ne pas coller à la zone de saisie.
  const r = anchor.getBoundingClientRect();
  const pw = pop.offsetWidth, ph = pop.offsetHeight;
  let left = Math.max(8, Math.min(r.left, window.innerWidth - pw - 8));
  let top = r.top - ph - 14;
  if(top < 8) top = r.bottom + 14;
  pop.style.left = left+'px'; pop.style.top = top+'px';
  _plusPop = pop; anchor.classList.add('open');
  // Pas d'écoute du SCROLL : pendant la génération le chat défile tout seul, ce qui
  // fermait le menu aussitôt (« ça saute »). Le clic-dehors suffit.
  setTimeout(()=>{ document.addEventListener('click', _plusOutside, true); }, 0);
}

// Au chargement, on peuple le libellé du bouton (sans ouvrir le modal).
document.addEventListener('DOMContentLoaded', ()=>{ loadProjects(); });
