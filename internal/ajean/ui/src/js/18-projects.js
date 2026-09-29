// 18-projects.js — les PROJETS. Un projet cloisonne une mémoire (pages .md +
// index MEMORY.md), ses trackers et ses conversations. On les choisit dans la
// liste rapide de la bulle projet du composeur (plus de fenêtre dédiée) ; chaque
// projet y a son menu ⋮ (renommer, options, voir la mémoire, supprimer).
//
// Après une bascule, le serveur remet la conversation à zéro (nouvelle session) :
// le flux SSE reçoit un {reset} et l'UI se nettoie toute seule, comme un « clear
// chat ». On rafraîchit juste la mémoire des réglages et le libellé de la bulle.

let PROJECTS = [], ACTIVE_PROJECT = '';
// LIVE_GENERATING : une génération tourne-t-elle sur la conversation VIVE du
// serveur ? Pendant ce temps on ne bascule pas de projet (ça couperait la réponse).
let LIVE_GENERATING = false;

// Met à jour le libellé du projet actif dans la bulle du composeur. Changement de
// nom animé (sortie vers le haut, largeur qui glisse, entrée par le bas) ;
// premier affichage et mouvement réduit : direct.
function setProjectBtnName(name){
  const el = document.getElementById('project-btn-name'), b = document.getElementById('project-btn');
  if(!el) return;
  name = name || '';
  if(el.textContent === name) return;
  if(!el.textContent || !b || !b.offsetWidth || matchMedia('(prefers-reduced-motion: reduce)').matches){ el.textContent = name; return; }
  clearTimeout(el._t);
  const w0 = b.offsetWidth;
  el.classList.add('out');
  el._t = setTimeout(()=>{
    el.textContent = name;
    b.style.width = '';
    const w1 = b.offsetWidth;
    b.style.width = w0 + 'px'; void b.offsetWidth; b.style.width = w1 + 'px';
    el.classList.remove('out'); el.classList.add('in'); void el.offsetWidth;
    el.classList.remove('in');
    setTimeout(()=>{ b.style.width = ''; }, 300);
  }, 140);
}

// Récupère la liste des projets + l'actif, et l'état de génération du serveur.
async function loadProjects(){
  let s = null, r = null;
  try{ [s, r] = await Promise.all([ jget('/api/chat/state').catch(()=>null), jget('/api/projects').catch(()=>null) ]); }catch(_){}
  LIVE_GENERATING = !!(s && s.generating);
  if(!r || !r.ok) return;
  PROJECTS = r.projects || [];
  ACTIVE_PROJECT = r.active || '';
  const act = PROJECTS.find(p=>p.slug===ACTIVE_PROJECT);
  setProjectBtnName(act ? act.name : '');
  refreshProjMenu();
}

// Dossier DUOTONE (corps rempli en fondu + tracé net), à la couleur courante.
function projFolderSvg(sz){
  const s = sz || 34;
  return '<svg viewBox="0 0 24 24" width="'+s+'" height="'+s+'" fill="none" aria-hidden="true">'
    + '<path d="M3 8.6A2.6 2.6 0 0 1 5.6 6h3.5a1.4 1.4 0 0 1 1 .43L14.4 8.9h4A2.6 2.6 0 0 1 21 11.5V17a2.6 2.6 0 0 1-2.6 2.6H5.6A2.6 2.6 0 0 1 3 17z" fill="currentColor" opacity=".15"/>'
    + '<path d="M3 9.4A2.4 2.4 0 0 1 5.4 7h3.4a1.2 1.2 0 0 1 .85.35L11.3 9h6.3A2.4 2.4 0 0 1 20 11.4V17a2.4 2.4 0 0 1-2.4 2.4H5.4A2.4 2.4 0 0 1 3 17z" stroke="currentColor" stroke-width="1.5"/>'
    + '</svg>';
}
// Points verticaux (⋮) des menus de ligne.
function projDotsSvg(){
  return '<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true"><circle cx="12" cy="5" r="1.7"/><circle cx="12" cy="12" r="1.7"/><circle cx="12" cy="19" r="1.7"/></svg>';
}

// Menu ⋮ d'un projet (renommer, options, voir la mémoire, supprimer).
// Compat : closeProjMenu est appelé un peu partout ; c'est le menu commun.
const closeProjMenu = ()=>closeMenu();
function openProjMenu(anchor, p){
  popMenu(anchor, [
    {icon:'pencil', label:t('projects.rename'), run:()=>renameProjectUI(p.slug, p.name)},
    {icon:'doc', label:t('projects.options'), run:()=>optionsProjectUI(p)},            // description fournie à l'IA
    {icon:'mem', label:t('projects.view_memory'), run:()=>openMemHub(p.slug, p.name)},  // sans basculer dessus
    PROJECTS.length > 1 && {icon:'trash', label:t('projects.delete'), danger:true, run:()=>deleteProjectUI(p.slug, p.name)},
  ], {side:'below'});
}

// Basculer sur un projet : nouvelle session vierge côté serveur, la mémoire suit.
// Bascule visuelle instantanée (libellé de la bulle), la requête part ensuite ;
// en cas d'échec on revient au projet précédent.
async function switchProjectUI(slug){
  if(slug === ACTIVE_PROJECT) return;
  QUIET_LOADER_UNTIL = Date.now() + 4000; // pas de logo de chargement pendant la bascule
  const prev = ACTIVE_PROJECT;
  const label = (s)=>{ const p = PROJECTS.find(x=>x.slug===s); if(p) setProjectBtnName(p.name); };
  ACTIVE_PROJECT = slug; label(slug);
  let r; try{ r = await jpost('/api/projects/switch', {slug}); }
  catch(_){ ACTIVE_PROJECT = prev; label(prev); toast(t('projects.network_error')); return; }
  if(!r.ok){ ACTIVE_PROJECT = prev; label(prev); toast(r.error || t('projects.switch_failed')); return; }
  ACTIVE_PROJECT = r.active || slug;
  // La mémoire des réglages est propre au projet : on vide son filtre (une requête
  // laissée d'un autre projet masquerait les notes du nouveau) et on la recharge.
  const ms=document.getElementById('mem-search'); if(ms) ms.value='';
  loadAgent();
  if(!PROJECTS.some(p=>p.slug===slug)) await loadProjects(); // projet tout juste créé
  toast(t('projects.switched_toast_prefix') + projName(ACTIVE_PROJECT));
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

// Options d'un projet : la description, fournie à l'IA en tête de chaque nouvelle
// conversation. Vide = efface la description.
async function optionsProjectUI(p){
  const slug = p.slug;
  const desc = await askPrompt(
    t('projects.describe_prompt'),
    {title:t('projects.options_title'), okText:t('projects.save_btn'), multiline:true,
     default: p.desc||'', placeholder:t('projects.describe_placeholder')});
  if(desc===null) return;                 // annulé : on ne touche à rien
  let r; try{ r = await jpost('/api/projects/describe', {slug, desc}); }catch(_){ toast(t('projects.network_error')); return; }
  if(!r.ok){ toast(r.error || t('projects.save_failed')); return; }
  toast(desc.trim() ? t('projects.desc_saved') : t('projects.desc_cleared'));
  loadProjects();
}

// Petit sélecteur de projet (menu commun ancré au bouton), pour choisir une DESTINATION.
// Exclut excludeSlug (le projet source). Appelle onPick(slug) au choix.
function pickProjectPop(anchor, excludeSlug, onPick){
  const dests = PROJECTS.filter(p=>p.slug!==excludeSlug);
  popMenu(anchor, dests.length
    ? dests.map(p=>({icon:projFolderSvg(16), label:p.name||p.slug, run:()=>onPick(p.slug)}))
    : [{label:t('projects.no_other_project'), disabled:true}], {side:'below'});
}

// Déplacer une conversation vers un autre projet (issue #55), depuis le menu ⋮ de
// l'historique. Destination = tout projet sauf celui de la conversation.
function moveSessionUI(c, anchor){
  pickProjectPop(anchor, c.project || HIST_DEFAULT_PROJ, async(slug)=>{
    let r; try{ r = await jpost('/api/projects/move-session', {id:c.id, slug}); }catch(_){ toast(t('projects.network_error')); return; }
    if(!r.ok){ toast(r.error || t('projects.move_failed')); return; }
    toast(t('projects.moved_toast_prefix') + projName(slug));
    loadHistory();
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
  await loadProjects();
  if(typeof loadAgent === 'function') loadAgent();
}

// ===== Menu « + » du composeur : fichier, nouvelle conversation, compactage =====
const PLUS_IC = {
  file:'<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21.4 11.05 12.25 20.2a5.5 5.5 0 0 1-7.78-7.78l9.19-9.19a3.67 3.67 0 1 1 5.18 5.18l-9.2 9.2a1.83 1.83 0 1 1-2.59-2.6l8.49-8.48"/></svg>',
  chat:'<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/></svg>',
  compact:'<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 9l4-4 4 4M20 15l-4 4-4-4M8 5v6M16 19v-6"/></svg>',
};
const closePlusMenu = ()=>closeMenu();
function togglePlusMenu(e){
  if(e){ e.stopPropagation(); e.preventDefault(); }
  popMenu(document.getElementById('plus-btn'), [
    {icon:PLUS_IC.file, label:t('projects.attach_file'), run:()=>document.getElementById('attach-input').click()},
    {icon:PLUS_IC.chat, label:t('chat.new_chat_btn'), run:newChatFromTop},
    COMPACT_AVAILABLE && {icon:PLUS_IC.compact, label:t('projects.compact_context'), run:compactContext}, // contexte ≥ 50 %
  ], {side:'above', align:'left', gap:14, keepOnScroll:true}); // le chat défile pendant la génération
}

// Mémoire du projet : modal ouvert depuis le menu +. Le contenu (mode + pages)
// vit toujours sous les mêmes IDs que l'ancien bloc du menu de gauche, donc
// loadAgent()/renderMemList() le remplissent sans changement.
// openMemHub() = mémoire du projet ACTIF. openMemHub(slug, name) = consulter la
// mémoire d'un AUTRE projet sans basculer dessus (menu ⋯ d'un projet) : on pose le
// contexte MEM_VIEW_PROJECT (lu par loadAgent / openMem / setMemMode / moveMemUI) et
// on affiche le nom du projet consulté dans l'en-tête du modal.
function openMemHub(slug, name){
  const other = slug && (typeof ACTIVE_PROJECT==='undefined' || slug!==ACTIVE_PROJECT);
  if(typeof MEM_VIEW_PROJECT!=='undefined') MEM_VIEW_PROJECT = other ? slug : '';
  const tag=document.getElementById('mem-proj');
  if(tag) tag.textContent = other ? (name || slug) : '';
  if(typeof showModal==='function') showModal('mem-modal');
  if(typeof loadAgent==='function') loadAgent(); // resynchronise mode + liste des pages (scopé projet)
}
function closeMemHub(){
  if(typeof MEM_VIEW_PROJECT!=='undefined') MEM_VIEW_PROJECT='';
  const tag=document.getElementById('mem-proj'); if(tag) tag.textContent='';
  if(typeof hideModal==='function') hideModal('mem-modal');
}

// Au chargement, on peuple le libellé du bouton (sans ouvrir le modal).
document.addEventListener('DOMContentLoaded', ()=>{ loadProjects(); });

// ===== Liste rapide (bulle projet du composeur) ============================
// Petit menu comme celui des modes : les projets, l'actif coché, un clic bascule.
// Pendant une génération on ne bascule pas (ça couperait la réponse) : le clic
// ouvre le hub en lecture seule sur ce projet, comme dans le hub lui-même.
const PM_FOLDER='<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>';
const PM_PLUS='<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>';
function renderProjMenu(){
  const menu=document.getElementById('proj-menu'); if(!menu) return;
  menu.innerHTML='';
  menu.appendChild(Object.assign(document.createElement('div'), {className:'menu-head', textContent:t('projects.title')}));
  // Seule la liste défile (en-tête et « Nouveau projet » restent visibles).
  const list=document.createElement('div'); list.className='pm-list'; menu.appendChild(list);
  PROJECTS.forEach(p=>{
    // Une ligne = le projet (clic = bascule) + un ⋮ avec ses options (renommer,
    // options, voir la mémoire, supprimer), comme l'ancienne fenêtre des projets.
    const row=document.createElement('div'); row.className='pm-row';
    const b=document.createElement('button');
    b.innerHTML=PM_FOLDER+'<span></span>';
    b.lastChild.textContent=p.name||p.slug;
    if(p.slug===ACTIVE_PROJECT) b.classList.add('on');
    b.onclick=()=>pickProjFromMenu(p.slug);
    const more=document.createElement('button'); more.className='pm-more';
    more.innerHTML=projDotsSvg(); more.title=t('chat.session.actions'); more.setAttribute('aria-label', more.title);
    more.onclick=(e)=>{ e.stopPropagation(); openProjMenu(more, p); };
    row.appendChild(b); row.appendChild(more);
    list.appendChild(row);
  });
  menu.appendChild(Object.assign(document.createElement('div'), {className:'menu-sep'}));
  const add=document.createElement('button'); add.className='pm-add';
  add.innerHTML=PM_PLUS+'<span></span>'; add.lastChild.textContent=t('projects.new_project'); add.onclick=()=>{ closeMenu(); createProjectUI(); };
  menu.appendChild(add);
}
const closeProjQuickMenu = ()=>closeMenu();
const PM_OPTS = {side:'above', maxH:440};
async function toggleProjMenu(ev){
  ev.stopPropagation();
  const menu=document.getElementById('proj-menu'), b=document.getElementById('project-btn');
  renderProjMenu();
  if(!toggleMenu(menu, b, PM_OPTS)) return;
  // Liste fraîche (projets créés ailleurs, génération en cours) sans bloquer l'ouverture.
  let s=null, r=null;
  try{ [s, r] = await Promise.all([ jget('/api/chat/state').catch(()=>null), jget('/api/projects').catch(()=>null) ]); }catch(_){}
  LIVE_GENERATING = !!(s && s.generating);
  if(r && r.ok){ PROJECTS=r.projects||[]; ACTIVE_PROJECT=r.active||''; }
  refreshProjMenu();
}
// Liste rapide ouverte (création, renommage…) : on la reconstruit et la recale.
function refreshProjMenu(){
  const b=document.getElementById('project-btn');
  if(menuOpenFor(b)){ renderProjMenu(); placeMenu(document.getElementById('proj-menu'), b, PM_OPTS); }
}
async function pickProjFromMenu(slug){
  closeMenu();
  if(slug===ACTIVE_PROJECT) return;
  if(LIVE_GENERATING){ toast(t('projects.busy_switch')); return; }
  await switchProjectUI(slug);
}
