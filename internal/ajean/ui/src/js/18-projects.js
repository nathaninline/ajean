// 18-projects.js — les PROJETS. Un projet cloisonne une mémoire (pages .md +
// index MEMORY.md) ET ses sessions de chat. Le bouton dossier du composeur ouvre
// ce modal : sélectionner un projet bascule dessus (nouvelle session vierge,
// l'ancienne est archivée dans son projet), créer / renommer / supprimer aussi.
//
// Après une bascule, le serveur remet la conversation à zéro (nouvelle session) :
// le flux SSE reçoit un {reset} et l'UI se nettoie toute seule, comme un « clear
// chat ». On rafraîchit juste la liste mémoire des réglages et le libellé du bouton.

let PROJECTS = [], ACTIVE_PROJECT = '';
// BROWSE_PROJECT : projet PARCOURU en lecture seule pendant qu'une génération
// tourne (on regarde ailleurs sans quitter ni couper la conversation en cours).
// Vide = on regarde le projet actif. Toujours vide hors génération (on bascule
// vraiment dans ce cas).
let BROWSE_PROJECT = '';
// LIVE_GENERATING : une génération tourne-t-elle sur la conversation VIVE du
// serveur ? C'est ça (et non `busy`, qui suit la conversation AFFICHÉE) qui pilote
// le mode lecture seule du hub : sinon, une fois qu'on lit une archive, `busy`
// retombe à false et le hub croirait à tort que plus rien ne génère.
let LIVE_GENERATING = false;

// Le projet dont on affiche les conversations dans le hub (parcouru si on lit
// ailleurs pendant une génération, sinon le projet actif).
function viewedProject(){ return BROWSE_PROJECT || ACTIVE_PROJECT; }

function openProjectHub(){
  // On NE réinitialise PAS BROWSE_PROJECT ici : pendant une génération, le projet
  // qu'on parcourt en lecture seule doit rester sélectionné même si on rouvre le
  // hub. loadProjects le remet à zéro dès qu'aucune génération ne tourne.
  showModal('project-modal');
  showSessionsLoading(); // spinner tout de suite : pas de flash de l'ancienne liste avant le chargement
  loadProjects();
}
function closeProjectModal(){ hideModal('project-modal'); }

// Met à jour le petit libellé du projet actif à côté de l'icône dossier.
function setProjectBtnName(name){
  const el = document.getElementById('project-btn-name');
  if(el) el.textContent = name || '';
}

// Récupère la liste + l'actif ; rend le modal s'il est ouvert et rafraîchit le bouton.
async function loadProjects(){
  // En parallèle : l'état réel du serveur (une génération tourne-t-elle sur la
  // conversation vive ? → pilote le mode lecture seule) ET la liste des projets.
  let s = null, r = null;
  try{ [s, r] = await Promise.all([ jget('/api/chat/state').catch(()=>null), jget('/api/projects').catch(()=>null) ]); }catch(_){}
  LIVE_GENERATING = !!(s && s.generating);
  if(!r || !r.ok){ const box=document.getElementById('project-list'); if(box) box.innerHTML='<span class="muted" style="font-size:12px">'+t('projects.load_error')+'</span>'; return; }
  PROJECTS = r.projects || [];
  ACTIVE_PROJECT = r.active || '';
  // Parcours en lecture seule (BROWSE_PROJECT) : n'a de sens que pendant une
  // génération et pour un projet qui existe encore. Sinon on retombe sur l'actif.
  if(!LIVE_GENERATING || !PROJECTS.some(p=>p.slug===BROWSE_PROJECT)) BROWSE_PROJECT = '';
  const act = PROJECTS.find(p=>p.slug===ACTIVE_PROJECT);
  setProjectBtnName(act ? act.name : '');
  renderProjectList();
  loadProjectSessions();
}

// browseProject : regarde un AUTRE projet en lecture seule pendant une génération
// (ne bascule pas, ne coupe rien). Cliquer le projet actif revient à ses sessions.
function browseProject(slug){
  BROWSE_PROJECT = (slug === ACTIVE_PROJECT) ? '' : slug;
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
  const viewed = p.slug === viewedProject();  // tuile mise en avant (projet regardé)
  const genHere = LIVE_GENERATING && p.slug === ACTIVE_PROJECT; // génération en cours dans ce projet
  const tile = document.createElement('div'); tile.className = 'proj-tile' + (viewed?' active':'');
  tile.tabIndex = 0;
  tile.title = genHere ? t('projects.generating_here') : (viewed ? t('projects.active_tile_title') : t('projects.switch_tile_title'));
  // Pendant une génération : clic = LECTURE SEULE (parcourir), jamais de bascule (on
  // ne coupe pas le tour en cours). Hors génération : clic = bascule réelle.
  const act = LIVE_GENERATING ? (()=>browseProject(p.slug)) : (p.slug!==ACTIVE_PROJECT ? (()=>switchProjectUI(p.slug)) : null);
  if(act){
    tile.onclick = act;
    tile.onkeydown = (e)=>{ if((e.key==='Enter'||e.key===' ') && e.target===tile){ e.preventDefault(); act(); } };
  }
  // Pastille « projet regardé » en haut à gauche — sauf si un spinner « en cours »
  // y prend déjà la place (projet qui génère), pour ne pas empiler les deux.
  if(viewed && !genHere){ const badge = document.createElement('span'); badge.className='proj-badge'; badge.textContent=t('projects.active_badge'); tile.appendChild(badge); }
  // Marqueur « génération en cours ici » : petit spinner en haut à GAUCHE (le coin
  // haut-droit est pris par le menu ⋯).
  if(genHere){ const g=document.createElement('span'); g.className='proj-gen'; g.innerHTML='<span class="spinner"></span>'; g.title=t('projects.generating_here'); tile.appendChild(g); }
  // Bouton menu ⋯
  const menu = document.createElement('button');
  menu.className='proj-menu-btn'; menu.innerHTML=projDotsSvg(); menu.title=t('projects.options_project_title'); menu.setAttribute('aria-label',t('projects.options_project_title'));
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
  pop.appendChild(item('pencil', t('projects.rename'), '', ()=>renameProjectUI(p.slug, p.name)));
  pop.appendChild(item('doc', t('projects.describe'), '', ()=>describeProjectUI(p.slug, p.desc||'')));
  if(PROJECTS.length > 1) pop.appendChild(item('trash', t('projects.delete'), 'danger', ()=>deleteProjectUI(p.slug, p.name)));
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
  if(!PROJECTS.length){ box.innerHTML='<span class="muted" style="font-size:12px">'+t('projects.none')+'</span>'; return; }
  PROJECTS.forEach(p=>box.appendChild(projectTile(p)));
}

// Basculer sur un projet : nouvelle session vierge côté serveur, la mémoire suit.
// Le modal RESTE ouvert : on met à jour la liste + les sessions du nouveau projet,
// pour pouvoir enchaîner (ouvrir une session, en créer une…). Le fil derrière est
// déjà remis à zéro (le flux SSE reçoit un reset).
async function switchProjectUI(slug){
  if(slug === ACTIVE_PROJECT) return;
  const known = PROJECTS.some(p=>p.slug===slug);
  const prev = ACTIVE_PROJECT;
  const revert = ()=>{
    ACTIVE_PROJECT = prev; renderProjectList();
    const pp = PROJECTS.find(x=>x.slug===prev); if(pp) setProjectBtnName(pp.name);
    loadProjectSessions();
  };
  // Bascule visuelle INSTANTANÉE au clic (accent + libellé + spinner des sessions),
  // sans attendre le serveur : on connaît déjà la liste des projets côté client, seul
  // l'actif change. La bascule serveur part en arrière-plan ; en cas d'échec on
  // revient à l'état précédent. (Projet inconnu, ex. tout juste créé : on garde le
  // flux serveur-d'abord, il faut relire la liste pour afficher la nouvelle tuile.)
  if(known){
    ACTIVE_PROJECT = slug;
    renderProjectList();
    const p = PROJECTS.find(x=>x.slug===slug); if(p) setProjectBtnName(p.name);
    showSessionsLoading(); // vide l'ancienne liste tout de suite, spinner pendant la bascule
  }
  let r; try{ r = await jpost('/api/projects/switch', {slug}); }
  catch(_){ if(known) revert(); toast(t('projects.network_error')); return; }
  if(!r.ok){ if(known) revert(); toast(r.error || t('projects.switch_failed')); return; }
  ACTIVE_PROJECT = r.active || slug;
  // Rafraîchit la liste des pages mémoire des réglages (elle est projet-scopée). On
  // vide d'abord le filtre de recherche : une requête laissée d'un projet fourni
  // filtrerait les notes du nouveau projet (jusqu'à tout masquer) sans qu'on le voie.
  const ms=document.getElementById('mem-search'); if(ms) ms.value='';
  if(typeof loadAgent === 'function') loadAgent();
  // Serveur à jour : on charge les sessions du nouveau projet. Grille déjà à jour si
  // le projet était connu ; sinon on relit toute la liste pour afficher sa tuile.
  if(known) loadProjectSessions();
  else await loadProjects();
  const pn = PROJECTS.find(x=>x.slug===ACTIVE_PROJECT);
  toast(t('projects.switched_toast_prefix') + (pn ? pn.name : ACTIVE_PROJECT));
}

// ===== Sessions du projet actif (dans le hub) ==============================
// Réutilise les mêmes endpoints /api/chat/history* que l'ancien modal, mais rend
// dans #project-sessions et rafraîchit ici. Ouvrir une session ferme le hub.
// showSessionsLoading pose le spinner rond dans la zone conversations (même rond
// que le chargement du modal presets) en FIGEANT la hauteur courante : sinon la
// zone se rétracte à la taille du spinner puis se redéploie quand la liste arrive
// (la carte « saute »). Idempotent : si le spinner/verrou de hauteur sont déjà en
// place (bascule optimiste → loadProjectSessions enchaîne), on ne re-mesure pas
// (sinon on figerait la hauteur du spinner, pas celle de l'ancienne liste).
function showSessionsLoading(){
  const box = document.getElementById('project-sessions'); if(!box) return;
  if(!box.style.minHeight){ const h = box.offsetHeight; if(h > 0) box.style.minHeight = h + 'px'; }
  box.classList.remove('ready');
  if(!box.querySelector('.proj-sess-load')) box.innerHTML = '<div class="proj-sess-load"><span class="spinner"></span></div>';
}

async function loadProjectSessions(){
  const box = document.getElementById('project-sessions'); if(!box) return;
  showSessionsLoading();
  // Projet parcouru : le projet actif, ou un AUTRE en lecture seule (?project=)
  // pendant une génération — sans jamais basculer.
  const foreign = !!BROWSE_PROJECT && BROWSE_PROJECT !== ACTIVE_PROJECT;
  const url = foreign ? '/api/chat/history?project='+encodeURIComponent(BROWSE_PROJECT) : '/api/chat/history';
  let list = [], active = '';
  try{ const r = await jget(url); list = (r && r.conversations) || []; active = (r && r.active) || ''; }
  catch(_){ box.style.minHeight = ''; box.innerHTML = '<span class="muted" style="font-size:12px">'+t('projects.load_error')+'</span>'; return; }
  box.style.minHeight = '';
  box.innerHTML = '';
  if(!list.length){ box.innerHTML = '<span class="muted" style="font-size:12px">'+t('projects.no_sessions')+'</span>'; box.classList.add('ready'); return; }
  const favs = list.filter(c=>c.fav), others = list.filter(c=>!c.fav);
  const section = (label)=>{ const h=document.createElement('div'); h.className='sess-head'; h.style.paddingLeft='8px'; h.textContent=label; box.appendChild(h); };
  const row = (c)=>projSessionRow(c, c.id===active && !foreign, foreign);
  if(favs.length){ section(t('projects.favorites')); favs.forEach(c=>box.appendChild(row(c))); }
  if(others.length){ if(favs.length) section(t('projects.recent')); others.forEach(c=>box.appendChild(row(c))); }
  box.classList.add('ready');
}

function projSessionRow(c, active, foreign){
  const genHere = LIVE_GENERATING && active; // la conversation vive, en cours de génération
  const unseenHere = active && !genHere && (typeof PENDING_LIVE!=='undefined' && PENDING_LIVE); // réponse finie non lue
  const row = document.createElement('div'); row.className = 'sess-row' + (active?' active':'');
  // Clic : la conversation VIVE (active) ramène toujours au direct ; une AUTRE
  // conversation pendant une génération s'ouvre en LECTURE SEULE (plein chat) ;
  // sinon ouverture/restauration normale.
  let onOpen = null;
  if(active) onOpen = ()=>{ if(typeof exitReading==='function') exitReading(); closeProjectModal(); };
  else if(LIVE_GENERATING) onOpen = ()=>{ closeProjectModal(); if(typeof readConversation==='function') readConversation(c.id, c.title); };
  else onOpen = ()=>openProjSession(c.id);
  row.tabIndex = 0;
  row.title = active ? t('projects.back_to_live') : (LIVE_GENERATING ? t('projects.reading_readonly') : t('projects.open_session_title'));
  row.onclick = onOpen;
  row.onkeydown = (e)=>{ if((e.key==='Enter'||e.key===' ') && e.target===row){ e.preventDefault(); onOpen(); } };
  const info = document.createElement('div'); info.className = 'sess-info';
  const name = document.createElement('div'); name.className = 'sess-name'; name.textContent = c.title || t('projects.conversation_fallback');
  const meta = document.createElement('div'); meta.className = 'sess-meta';
  const n = c.turns || 0;
  meta.textContent = fmtHistDate(c.saved_at) + ' · ' + n + ' ' + t('projects.message_label') + (n>1?'s':'');
  // Marqueur « en cours » (spinner) sur la conversation qui génère ; sinon, si une
  // réponse y a été générée sans être vue, un point de notif accent.
  if(genHere){ const g=document.createElement('span'); g.className='sess-gen'; g.innerHTML='<span class="spinner"></span><span>'+t('projects.generating_here')+'</span>'; meta.appendChild(g); }
  else if(unseenHere){ const d=document.createElement('span'); d.className='sess-unseen'; d.title=t('projects.unseen_response'); meta.appendChild(d); }
  info.appendChild(name); info.appendChild(meta);
  row.appendChild(info);
  // Menu ⋮ (Favori / Renommer / Supprimer) — comme les projets, plus de crayon/poubelle.
  const menu = document.createElement('button');
  menu.className='sess-menu-btn'; menu.innerHTML=projDotsSvg(); menu.title=t('projects.options'); menu.setAttribute('aria-label',t('projects.options'));
  menu.onclick=(e)=>{ e.stopPropagation(); openSessMenu(menu, c, active); };
  row.appendChild(menu);
  return row;
}

// Menu contextuel d'une conversation (favori / renommer / déplacer / supprimer).
function openSessMenu(anchor, c, active){
  closeProjMenu();
  const pop = document.createElement('div'); pop.className='pop-menu';
  const item = (icon, label, cls, fn)=>{ const b=document.createElement('button'); if(cls) b.className=cls; b.innerHTML=sessIconSvg(icon)+'<span>'+label+'</span>'; b.onclick=(e)=>{ e.stopPropagation(); closeProjMenu(); fn(); }; return b; };
  pop.appendChild(item('star', c.fav?t('projects.unfavorite'):t('projects.favorite'), '', ()=>favProjSession(c.id, !c.fav)));
  pop.appendChild(item('pencil', t('projects.rename'), '', ()=>renameProjSession(c.id, c.title)));
  // Déplacer vers un autre projet (issue #55) — désactivé pour la conversation en cours.
  if(PROJECTS.length > 1 && !active) pop.appendChild(item('move', t('projects.move_to'), '', ()=>moveSessionUI(c.id, anchor)));
  // Exporter CETTE conversation (archivée), sans avoir à l'ouvrir : ?id=<session>.
  const exp = document.createElement('button');
  exp.innerHTML = '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12M8 11l4 4 4-4M4 19h16"/></svg><span>'+t('projects.export')+'</span>';
  exp.onclick = (e)=>{ e.stopPropagation(); closeProjMenu(); if(typeof downloadExport==='function') downloadExport('/api/chat/export?id='+encodeURIComponent(c.id)); };
  pop.appendChild(exp);
  pop.appendChild(item('trash', t('projects.delete'), 'danger', ()=>deleteProjSession(c.id, c.title)));
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
  let r; try{ r = await jpost('/api/chat/history/restore', {id}); }catch(_){ toast(t('projects.network_error')); return; }
  if(!r.ok){ toast(r.error || t('projects.open_failed')); return; }
  closeProjectModal();
  toast(t('projects.session_opened'));
}

async function favProjSession(id, fav){
  let r; try{ r = await jpost('/api/chat/history/fav', {id, fav}); }catch(_){ toast(t('projects.network_error')); return; }
  if(!r.ok){ toast(r.error || t('projects.impossible')); return; }
  loadProjectSessions();
}
async function renameProjSession(id, current){
  const name = await askPrompt(t('projects.rename_session_prompt'), {title:t('projects.rename_session_title'), okText:t('projects.save_btn'), default: current||'', placeholder:t('projects.rename_session_placeholder')});
  if(name===null) return;
  let r; try{ r = await jpost('/api/chat/history/rename', {id, title:name}); }catch(_){ toast(t('projects.network_error')); return; }
  if(!r.ok){ toast(r.error || t('projects.rename_failed')); return; }
  loadProjectSessions();
}
async function deleteProjSession(id, title){
  if(!await askConfirm(t('projects.delete_session_prefix') + (title || t('projects.conversation_fallback_lower')) + t('projects.delete_session_suffix'), {title:t('projects.delete_session_title'), okText:t('projects.delete'), danger:true})) return;
  let r; try{ r = await jpost('/api/chat/history/delete', {id}); }catch(_){ toast(t('projects.network_error')); return; }
  if(!r.ok){ toast(r.error || t('projects.delete_failed')); return; }
  loadProjectSessions();
}
// Nouvelle conversation vierge dans le projet actif (archive la courante).
async function newSessionUI(){
  let r; try{ r = await jpost('/api/chat/reset', {}); }catch(_){ toast(t('projects.network_error')); return; }
  if(!r || !r.ok){ toast(t('projects.impossible')); return; }
  closeProjectModal();
  toast(t('projects.new_session_toast'));
}
// Vide les conversations du projet actif sauf les favoris et celle en cours.
async function clearProjectSessions(){
  if(!await askConfirm(t('projects.clear_confirm'), {title:t('projects.clear_title'), okText:t('projects.clear_btn'), danger:true})) return;
  let r; try{ r = await jpost('/api/chat/history/clear', {}); }catch(_){ toast(t('projects.network_error')); return; }
  if(!r.ok){ toast(r.error || t('projects.delete_failed')); return; }
  toast((r.deleted||0) + ' ' + t('projects.conversation_word') + ((r.deleted>1)?'s':'') + ' ' + t('projects.deleted_word') + ((r.deleted>1)?'s':''));
  loadProjectSessions();
}

async function createProjectUI(){
  // Création via une petite fenêtre (comme le renommage), pas une barre inline.
  const name = await askPrompt(t('projects.name_prompt'), {title:t('projects.new_project_modal_title'), okText:t('projects.create_btn'), placeholder:t('projects.name_placeholder')});
  if(name===null) return;               // annulé
  if(!name.trim()){ toast(t('projects.name_empty')); return; }
  let r; try{ r = await jpost('/api/projects/create', {name}); }catch(_){ toast(t('projects.network_error')); return; }
  if(!r.ok){ toast(r.error || t('projects.create_failed')); return; }
  // On bascule directement sur le nouveau projet (on le crée pour y travailler).
  await switchProjectUI(r.slug);
}

async function renameProjectUI(slug, current){
  const name = await askPrompt(t('projects.rename_prompt'), {title:t('projects.rename_title'), okText:t('projects.save_btn'), default: current||'', placeholder:t('projects.rename_placeholder')});
  if(name===null) return;
  if(!name.trim()){ toast(t('projects.name_empty')); return; }
  let r; try{ r = await jpost('/api/projects/rename', {slug, name}); }catch(_){ toast(t('projects.network_error')); return; }
  if(!r.ok){ toast(r.error || t('projects.rename_failed')); return; }
  loadProjects();
}

// Décrire un projet : la description est fournie à l'IA en tête de chaque nouvelle
// conversation du projet (elle sait alors à quoi sert le projet, sans qu'on ait à
// le lui réexpliquer). Vide = efface la description.
async function describeProjectUI(slug, current){
  const desc = await askPrompt(
    t('projects.describe_prompt'),
    {title:t('projects.describe_title'), okText:t('projects.save_btn'), multiline:true, default: current||'', placeholder:t('projects.describe_placeholder')});
  if(desc===null) return;
  let r; try{ r = await jpost('/api/projects/describe', {slug, desc}); }catch(_){ toast(t('projects.network_error')); return; }
  if(!r.ok){ toast(r.error || t('projects.save_failed')); return; }
  toast(desc.trim() ? t('projects.desc_saved') : t('projects.desc_cleared'));
  loadProjects();
}

// Petit sélecteur de projet (pop-menu ancré au bouton), pour choisir une DESTINATION.
// Exclut excludeSlug (le projet source). Appelle onPick(slug) au choix.
function pickProjectPop(anchor, excludeSlug, onPick){
  closeProjMenu();
  const pop = document.createElement('div'); pop.className='pop-menu';
  const dests = PROJECTS.filter(p=>p.slug!==excludeSlug);
  if(!dests.length){ const b=document.createElement('button'); b.disabled=true; b.innerHTML='<span class="muted">'+t('projects.no_other_project')+'</span>'; pop.appendChild(b); }
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
    let r; try{ r = await jpost('/api/projects/move-session', {id, slug}); }catch(_){ toast(t('projects.network_error')); return; }
    if(!r.ok){ toast(r.error || t('projects.move_failed')); return; }
    toast(t('projects.moved_toast_prefix') + projName(slug));
    loadProjectSessions();
  });
}

// Nom d'affichage d'un projet depuis son slug (repli sur le slug).
function projName(slug){ const p = PROJECTS.find(x=>x.slug===slug); return p ? (p.name||p.slug) : slug; }

async function deleteProjectUI(slug, name){
  if(!await askConfirm(t('projects.delete_project_prefix') + (name||slug) + t('projects.delete_project_suffix'), {title:t('projects.delete_project_title'), okText:t('projects.delete'), danger:true})) return;
  let r; try{ r = await jpost('/api/projects/delete', {slug}); }catch(_){ toast(t('projects.network_error')); return; }
  if(!r.ok){ toast(r.error || t('projects.delete_failed')); return; }
  ACTIVE_PROJECT = r.active || ACTIVE_PROJECT;
  toast(t('projects.deleted_toast'));
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
  pop.appendChild(item(icFile, t('projects.attach_file'), ()=>{ const inp=document.getElementById('attach-input'); if(inp) inp.click(); }));
  pop.appendChild(item(icMachine, t('projects.remote_hosts'), ()=>{ if(typeof openNodeHub==='function') openNodeHub(); }));
  const icTracker = '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 19V5M4 19h16M8 16l3-4 3 2 4-6"/></svg>';
  pop.appendChild(item(icTracker, t('projects.trackers'), ()=>{ if(typeof openTrackerHub==='function') openTrackerHub(); }));
  // « Compacter le contexte » : uniquement quand le contexte dépasse 50%.
  if(typeof COMPACT_AVAILABLE!=='undefined' && COMPACT_AVAILABLE){
    pop.appendChild(item(icCompact, t('projects.compact_context'), ()=>{ if(typeof compactContext==='function') compactContext(); }));
  }
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
