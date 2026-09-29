// 03b-select.js — listes déroulantes maison, partout dans l'app.
//
// Les <select> restent dans le HTML (valeur, onchange, mise en page et style
// fermé inchangés) : on intercepte seulement leur OUVERTURE pour afficher le menu
// commun (03f-menu.js) au lieu de la liste native. Choisir une option pose la
// valeur et déclenche « change » comme un vrai choix. Délégation : vaut aussi
// pour les listes créées plus tard.
let CSEL = null; // { sel, items, idx } de la liste ouverte

function cselOpen(sel){
  const menu = document.getElementById('csel-menu'); if(!menu) return;
  menu.innerHTML = '';
  const items = [];
  const add = (o)=>{
    const b = document.createElement('button'); b.type = 'button';
    b.textContent = o.textContent; b.disabled = o.disabled; b.classList.toggle('on', o.selected);
    b.onclick = (e)=>{ e.stopPropagation(); cselPick(sel, o); };
    menu.appendChild(b); items.push({b, o});
  };
  for(const c of sel.children){
    if(c.tagName === 'OPTGROUP'){
      menu.appendChild(Object.assign(document.createElement('div'), {className:'menu-head', textContent:c.label}));
      for(const o of c.children) if(o.tagName === 'OPTION' && !o.hidden) add(o);
    } else if(c.tagName === 'OPTION' && !c.hidden) add(c);
  }
  if(!items.length) return;
  openMenu(menu, sel, {side:'below', minW:150, maxH:360, onClose:()=>{ CSEL = null; }});
  const idx = items.findIndex(x=>x.o.selected);
  if(idx >= 0) items[idx].b.scrollIntoView({block:'nearest'});
  CSEL = {sel, items, idx};
}

function cselPick(sel, o){
  closeMenu();
  if(o.disabled) return;
  if(!o.selected){
    o.selected = true;
    sel.dispatchEvent(new Event('input', {bubbles:true}));
    sel.dispatchEvent(new Event('change', {bubbles:true}));
  }
  try{ sel.focus({preventScroll:true}); }catch(_){}
}

const cselOk = (s)=> s && s.tagName === 'SELECT' && !s.multiple && !s.disabled && !(s.size > 1) && s.offsetParent !== null;
function cselToggle(e){
  const sel = e.target.closest && e.target.closest('select');
  if(!cselOk(sel)) return;
  e.preventDefault(); // pas de liste native (roue iOS comprise)
  if(menuOpenFor(sel)) closeMenu(); else cselOpen(sel);
}
document.addEventListener('mousedown', (e)=>{ if(e.button === 0) cselToggle(e); }, true);
document.addEventListener('touchend', cselToggle, {capture:true, passive:false});

// Clavier : Entrée / Espace / Alt+↓ / F4 ouvrent ; dans la liste ↑ ↓ Entrée, Tab ferme.
document.addEventListener('keydown', (e)=>{
  if(CSEL){
    const {items} = CSEL; let i = CSEL.idx;
    const move = (d)=>{
      do{ i = (i + d + items.length) % items.length; }while(items[i].o.disabled && i !== CSEL.idx);
      items.forEach((x, k)=>x.b.classList.toggle('kbd', k === i));
      items[i].b.scrollIntoView({block:'nearest'}); CSEL.idx = i;
    };
    if(e.key === 'ArrowDown'){ e.preventDefault(); move(1); }
    else if(e.key === 'ArrowUp'){ e.preventDefault(); move(-1); }
    else if(e.key === 'Enter' || e.key === ' '){ e.preventDefault(); if(i >= 0) cselPick(CSEL.sel, items[i].o); }
    else if(e.key === 'Tab') closeMenu();
    return;
  }
  const sel = document.activeElement;
  if(cselOk(sel) && (e.key === 'Enter' || e.key === ' ' || e.key === 'F4' || (e.altKey && e.key === 'ArrowDown'))){
    e.preventDefault(); cselOpen(sel);
  }
}, true);
