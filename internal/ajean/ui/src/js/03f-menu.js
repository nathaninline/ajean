// 03f-menu.js — UN seul système de menus flottants pour toute l'app.
//
// Menus du composeur (modes, projets, réflexion, +), listes déroulantes, menus ⋮
// des lignes (historique, projets, mémoire, trackers) : tous passent par ici.
// Un seul menu ouvert à la fois ; il se place sous/au-dessus de son ancre, reste
// dans l'écran, s'ouvre et se ferme en fondu (classes .menu / .open, styles.css),
// et se referme au clic ailleurs, à Échap, au défilement et au redimensionnement.
//
//   openMenu(el, anchor, opts)   ouvre un menu existant (élément du HTML)
//   toggleMenu(el, anchor, opts) idem, ou le referme s'il est déjà ouvert sur anchor
//   popMenu(anchor, items, opts) construit un menu éphémère à partir d'une liste
//   placeMenu(el, anchor, opts)  le recale (contenu qui change pendant qu'il est ouvert)
//   closeMenu(now)               ferme le menu ouvert
//
// opts : side 'above' | 'below' (préférence, bascule s'il manque de place),
//        align 'left' | 'right', gap (px), maxH (px), minW (px, au moins la largeur
//        de l'ancre), keepOnScroll, onClose.
let MENU = null;

function placeMenu(el, anchor, o = {}, rect){
  const r = rect || anchor.getBoundingClientRect();
  const gap = o.gap ?? 6, H = innerHeight, W = innerWidth;
  el.style.display = 'flex'; el.style.visibility = 'hidden'; el.style.maxHeight = '';
  if(o.minW) el.style.minWidth = Math.max(o.minW, r.width) + 'px';
  const need = Math.min(el.scrollHeight, 240);
  const below = H - r.bottom - gap - 8, above = r.top - gap - 8;
  const up = o.side === 'above' ? !(above < need && below > above) : (below < need && above > below);
  el.style.maxHeight = Math.max(120, Math.min(o.maxH || 420, up ? above : below)) + 'px';
  const mw = el.offsetWidth, mh = el.offsetHeight;
  const left = Math.max(8, Math.min(o.align === 'right' ? r.right - mw : r.left, W - mw - 8));
  el.style.left = left + 'px';
  el.style.top = (up ? r.top - gap - mh : r.bottom + gap) + 'px';
  el.style.transformOrigin = (up ? 'bottom ' : 'top ') + (o.align === 'right' ? 'right' : 'left');
  el.style.visibility = '';
}

function openMenu(el, anchor, o = {}){
  const rect = anchor.getBoundingClientRect(); // avant closeMenu : l'ancre peut vivre dans le menu fermé
  // Sous-menu (opts.sub) : ouvert depuis un menu déjà ouvert (⋯ d'une ligne de la
  // liste des projets), il se pose PAR-DESSUS sans refermer son parent.
  const parent = (o.sub && MENU && MENU.el.contains(anchor)) ? MENU : null;
  if(!parent) closeMenu(true);
  clearTimeout(el._t);
  el.classList.add('menu');
  placeMenu(el, anchor, o, rect);
  void el.offsetWidth;
  el.classList.add('open'); anchor.classList.add('open');
  MENU = {el, anchor, o, parent};
  return el;
}

// all : referme aussi les menus parents (action choisie dans un sous-menu).
function closeMenu(now, all){
  if(!MENU) return;
  const {el, anchor, o, parent} = MENU; MENU = parent || null;
  el.classList.remove('open'); anchor.classList.remove('open');
  const done = ()=>{ if(el.classList.contains('open')) return; if(o.dyn) el.remove(); else el.style.display = 'none'; };
  if(now) done(); else el._t = setTimeout(done, 180);
  if(o.onClose) o.onClose();
  if(all && MENU) closeMenu(now, true);
}

const menuOpenFor = (anchor)=> !!MENU && MENU.anchor === anchor;

function toggleMenu(el, anchor, o){
  if(menuOpenFor(anchor)){ closeMenu(); return false; }
  openMenu(el, anchor, o); return true;
}

// items : { icon (nom sessIconSvg ou SVG brut), label, run, danger, on, disabled } ou '-' (séparateur).
function popMenu(anchor, items, o = {}){
  if(menuOpenFor(anchor)){ closeMenu(); return null; }
  const el = document.createElement('div');
  el.style.display = 'none';
  for(const it of items){
    if(!it) continue;
    if(it === '-'){ el.appendChild(Object.assign(document.createElement('div'), {className:'menu-sep'})); continue; }
    const b = document.createElement('button'); b.type = 'button';
    b.classList.toggle('danger', !!it.danger); b.classList.toggle('on', !!it.on); b.disabled = !!it.disabled;
    b.innerHTML = (it.icon ? (it.icon[0] === '<' ? it.icon : sessIconSvg(it.icon)) : '') + '<span></span>';
    b.lastChild.textContent = it.label;
    b.onclick = (e)=>{ e.stopPropagation(); closeMenu(false, true); if(it.run) it.run(); };
    el.appendChild(b);
  }
  document.body.appendChild(el);
  return openMenu(el, anchor, {align:'right', ...o, dyn:true});
}

document.addEventListener('pointerdown', (e)=>{
  // Clic hors du sous-menu : il se ferme ; hors du parent aussi : tout se ferme.
  while(MENU && !MENU.el.contains(e.target) && !MENU.anchor.contains(e.target)) closeMenu();
}, true);
document.addEventListener('keydown', (e)=>{ if(e.key === 'Escape' && MENU){ e.stopPropagation(); closeMenu(); } }, true);

// ----- Bulles d'aide (« ? ») ------------------------------------------------
// Comme la 1.0 : UNE bulle pour toute la page (#help-tip), posée au-dessus des
// fenêtres. Elle s'ouvre au survol (vraie souris) et au focus clavier ; un clic ou
// un toucher l'épingle jusqu'au clic suivant, à un clic ailleurs ou à Échap. Elle
// se pose sous la pastille, au-dessus si la place manque, sans sortir de l'écran
// ni de la fenêtre qui la contient. Le texte vient du .tip caché de la pastille.
let HTIP=null; // {anchor, pinned}
function helpTipEl(){
  let el=document.getElementById('help-tip');
  if(!el){ el=document.createElement('div'); el.id='help-tip'; el.setAttribute('role','tooltip'); document.body.appendChild(el); }
  return el;
}
function closeHelps(){
  if(!HTIP) return;
  HTIP.anchor.classList.remove('open'); HTIP=null;
  helpTipEl().classList.remove('show');
}
function openHelp(anchor, pinned){
  if(HTIP && HTIP.anchor===anchor){ HTIP.pinned=HTIP.pinned||pinned; return; }
  closeHelps();
  const src=anchor.querySelector('.tip'); if(!src) return;
  const el=helpTipEl(), r=anchor.getBoundingClientRect(), gap=6;
  const card=anchor.closest('.modal-card,.side'), box=card ? card.getBoundingClientRect() : {left:0,right:innerWidth};
  el.innerHTML=src.innerHTML;
  const left=r.left+r.width/2-el.offsetWidth/2, below=innerHeight-r.bottom-gap-8;
  el.style.left=Math.max(8, box.left+8, Math.min(left, innerWidth-8-el.offsetWidth, box.right-8-el.offsetWidth))+'px';
  el.style.top=(below<el.offsetHeight && r.top>below ? r.top-gap-el.offsetHeight : r.bottom+gap)+'px';
  anchor.classList.add('open'); el.classList.add('show');
  HTIP={anchor, pinned};
}
const HOVER=window.matchMedia && matchMedia('(hover:hover)').matches;
document.addEventListener('mouseover', e=>{ if(!HOVER) return; const h=e.target.closest && e.target.closest('.help'); if(h) openHelp(h, false); });
document.addEventListener('mouseout', e=>{ if(HTIP && !HTIP.pinned && e.target===HTIP.anchor && !HTIP.anchor.contains(e.relatedTarget)) closeHelps(); });
document.addEventListener('focusin', e=>{ if(e.target.matches && e.target.matches('.help:focus-visible')) openHelp(e.target, false); });
document.addEventListener('focusout', e=>{ if(HTIP && e.target===HTIP.anchor && !HTIP.pinned) closeHelps(); });
// En capture : les pastilles ont un onclick qui arrête la propagation.
document.addEventListener('click', e=>{
  const h=e.target.closest && e.target.closest('.help');
  if(h){ e.preventDefault(); (HTIP && HTIP.anchor===h && HTIP.pinned) ? closeHelps() : openHelp(h, true); return; }
  if(HTIP && !helpTipEl().contains(e.target)) closeHelps();
}, true);
document.addEventListener('keydown', e=>{ if(e.key==='Escape' && HTIP){ e.stopPropagation(); closeHelps(); } }, true);
document.addEventListener('scroll', e=>{ if(HTIP && e.target.contains && e.target.contains(HTIP.anchor)) closeHelps(); }, true);
addEventListener('resize', ()=>closeHelps());
// Seul un défilement qui DÉPLACE l'ancre ferme le menu (page entière ou conteneur
// de l'ancre). Le fil du chat défile tout seul pendant la génération : avant,
// n'importe quel défilement refermait les menus ouverts ailleurs (issue #113).
document.addEventListener('scroll', (e)=>{
  if(!MENU || MENU.o.keepOnScroll || MENU.el.contains(e.target)) return;
  const t = e.target;
  if(t === document || t === document.documentElement || t === document.body || (t.contains && t.contains(MENU.anchor))) closeMenu(false, true);
}, true);
addEventListener('resize', ()=>closeMenu(true, true));
