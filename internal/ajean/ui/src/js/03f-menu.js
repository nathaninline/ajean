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
  closeMenu(true);
  clearTimeout(el._t);
  el.classList.add('menu');
  placeMenu(el, anchor, o, rect);
  void el.offsetWidth;
  el.classList.add('open'); anchor.classList.add('open');
  MENU = {el, anchor, o};
  return el;
}

function closeMenu(now){
  if(!MENU) return;
  const {el, anchor, o} = MENU; MENU = null;
  el.classList.remove('open'); anchor.classList.remove('open');
  const done = ()=>{ if(el.classList.contains('open')) return; if(o.dyn) el.remove(); else el.style.display = 'none'; };
  if(now) done(); else el._t = setTimeout(done, 180);
  if(o.onClose) o.onClose();
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
    b.onclick = (e)=>{ e.stopPropagation(); closeMenu(); if(it.run) it.run(); };
    el.appendChild(b);
  }
  document.body.appendChild(el);
  return openMenu(el, anchor, {align:'right', ...o, dyn:true});
}

document.addEventListener('pointerdown', (e)=>{
  if(MENU && !MENU.el.contains(e.target) && !MENU.anchor.contains(e.target)) closeMenu();
}, true);
document.addEventListener('keydown', (e)=>{ if(e.key === 'Escape' && MENU){ e.stopPropagation(); closeMenu(); } }, true);
document.addEventListener('scroll', (e)=>{
  if(MENU && !MENU.o.keepOnScroll && !MENU.el.contains(e.target)) closeMenu();
}, true);
addEventListener('resize', ()=>closeMenu(true));
