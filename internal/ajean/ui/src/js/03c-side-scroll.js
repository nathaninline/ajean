// 03c-side-scroll.js — barre de défilement flottante du menu latéral.
//
// La barre native du menu est masquée (CSS) : elle prenait 10 px de largeur,
// ou les réservait pour rien quand il n'y avait rien à faire défiler. Celle-ci
// flotte sur le bord droit du menu, ne prend aucune place, n'apparaît que
// pendant le défilement, puis s'efface. On peut l'attraper pour faire défiler.
// SIDE_THUMB_QUIET_UNTIL : la barre reste cachée jusqu'à cet instant (voir showSideView).
let SIDE_THUMB_QUIET_UNTIL = 0;
(function(){
  let side, thumb, hideT = 0, drag = null, hover = false;
  function place(show){
    if(!side || !thumb) return;
    const r = side.getBoundingClientRect();
    const sh = side.scrollHeight, ch = side.clientHeight;
    if(sh <= ch + 1 || r.width < 40){ thumb.classList.remove('on'); return; }
    const pad = 4, track = ch - pad * 2;
    const h = Math.max(32, track * ch / sh);
    const top = pad + (track - h) * (side.scrollTop / (sh - ch));
    thumb.style.height = h + 'px';
    thumb.style.top = (r.top + top) + 'px';
    thumb.style.left = (r.right - 12) + 'px';
    // Bascule menu / historique : le retour en haut n'est pas un défilement de
    // l'utilisateur, la barre ne doit pas s'afficher pour ça.
    if(Date.now() < SIDE_THUMB_QUIET_UNTIL && !drag){ thumb.classList.remove('on'); return; }
    if(show || hover || drag){
      thumb.classList.add('on');
      clearTimeout(hideT);
      if(!hover && !drag) hideT = setTimeout(()=>thumb.classList.remove('on'), 900);
    }
  }
  function init(){
    side = document.getElementById('side'); thumb = document.getElementById('side-thumb');
    if(!side || !thumb) return;
    side.addEventListener('scroll', ()=>place(true), {passive:true});
    // Pas d'affichage au simple survol du menu : la barre n'apparaît que quand
    // on défile (et reste tant que la souris est dessus ou qu'on la tire).
    thumb.addEventListener('mouseenter', ()=>{ hover = true; place(true); });
    thumb.addEventListener('mouseleave', ()=>{ hover = false; place(true); });
    window.addEventListener('resize', ()=>place(false));
    // Le contenu change de hauteur (sections qui s'ouvrent, bascule historique).
    new ResizeObserver(()=>place(false)).observe(side);
    const views = document.getElementById('side-views'); if(views) new ResizeObserver(()=>place(false)).observe(views);
    thumb.addEventListener('pointerdown', (e)=>{
      e.preventDefault();
      drag = {y: e.clientY, top: side.scrollTop};
      thumb.classList.add('drag'); thumb.setPointerCapture(e.pointerId);
    });
    thumb.addEventListener('pointermove', (e)=>{
      if(!drag) return;
      const ch = side.clientHeight, sh = side.scrollHeight;
      const track = ch - 8, h = Math.max(32, track * ch / sh);
      side.scrollTop = drag.top + (e.clientY - drag.y) * (sh - ch) / Math.max(1, track - h);
    });
    const end = ()=>{ if(!drag) return; drag = null; thumb.classList.remove('drag'); place(true); };
    thumb.addEventListener('pointerup', end); thumb.addEventListener('pointercancel', end);
    place(false);
  }
  if(document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init); else init();
})();
// Largeur de la barre latérale : poignée #side-grip (comme la 1.0). Les événements
// du pointeur sont capturés par la poignée pendant le glissement ; double-clic =
// largeur d'origine ; au clavier, flèches gauche et droite.
(()=>{
  const KEY='ajean-side-w';
  const setW=(w,save)=>{
    w=Math.round(Math.max(240,Math.min(w,innerWidth*.45)));
    document.documentElement.style.setProperty('--side-w',w+'px');
    if(save!==false){ try{ localStorage.setItem(KEY,String(w)); }catch(_){} }
  };
  try{ const w=+localStorage.getItem(KEY); if(w) setW(w,false); }catch(_){}
  const init=()=>{
    const grip=document.getElementById('side-grip'), side=document.getElementById('side');
    if(!grip||!side) return;
    grip.onpointerdown=e=>{ if(e.button) return; e.preventDefault(); grip.setPointerCapture(e.pointerId); grip.classList.add('drag'); document.body.classList.add('resizing'); };
    grip.onpointermove=e=>{ if(grip.classList.contains('drag')) setW(e.clientX-side.getBoundingClientRect().left); };
    grip.onpointerup=grip.onpointercancel=()=>{ grip.classList.remove('drag'); document.body.classList.remove('resizing'); };
    grip.ondblclick=()=>{ document.documentElement.style.removeProperty('--side-w'); try{ localStorage.removeItem(KEY); }catch(_){} };
    grip.onkeydown=e=>{ const d=e.key==='ArrowLeft'?-16:e.key==='ArrowRight'?16:0; if(d){ e.preventDefault(); setW(side.offsetWidth+d); } };
  };
  if(document.readyState==='loading') document.addEventListener('DOMContentLoaded',init); else init();
})();
