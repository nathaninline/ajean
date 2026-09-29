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
